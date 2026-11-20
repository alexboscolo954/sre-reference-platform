# GameDay 0001: CNPG primary kill and worker node loss

Status: hypotheses written 2026-11-19, before either run, and left unchanged. Both runs done the same day; everything from "Observations" on was written afterwards, from the evidence in [`docs/evidence/gameday1-run-a.md`](../evidence/gameday1-run-a.md) and [`docs/evidence/gameday1-run-b.md`](../evidence/gameday1-run-b.md). Fixes were deployed and both scenarios re-run on 2026-11-19 and 2026-11-20: [`docs/evidence/gameday1-reruns.md`](../evidence/gameday1-reruns.md).

## Steady state

- Load: `make load`, k6 constant arrival rate of 20 req/s to storefront `POST /buy` for 10m, which calls checkout `POST /checkout`.
- Each checkout request does one `INSERT ... RETURNING id` on Postgres (pgxpool, no retry) and one `INCR` on Valkey. Either failing returns 500.
- `/healthz` pings both Postgres and Valkey and backs both the readiness and the liveness probe (default period 10s, timeout 1s, failure threshold 3).
- SLOs: checkout availability 99.9% (5xx over all requests, measured by checkout itself), latency 99.9% under 300ms. `CheckoutAvailabilityBudgetBurn` pages at 14.4x over 5m and 1h, or 6x over 30m and 6h.
- Placement at the time of writing (it will change after scenario A):

  | Node | Pods |
  |---|---|
  | sre-platform-worker | shop-db-2 (replica) |
  | sre-platform-worker2 | shop-db-1 (primary), checkout, storefront, valkey |
  | sre-platform-worker3 | shop-db-3 (replica), checkout, storefront |

- `shop-db` runs 3 instances with required anti-affinity on 3 workers, so there is no spare node.
- PDBs: only the two CNPG creates (`shop-db`, `shop-db-primary`). Neither applies here: a pod kill and a stopped node are not evictions.

## Scenario A: kill the CNPG primary

Method: under load, apply `chaos/cnpg-primary-kill.yaml` (PodChaos, `pod-kill`, grace period 0, selects `cnpg.io/instanceRole=primary`).

Hypothesis:

1. CNPG promotes one of the two replicas and moves `shop-db-rw` to it within 30s of the kill. The killed pod comes back as a replica.
2. Checkout returns 500 for every request that hits a pooled connection to the old primary, until pgxpool drops those connections. At 20 req/s and up to 30s, that is up to about 600 failed requests.
3. `/healthz` fails on both checkout pods while there is no primary. If that lasts longer than about 30s, the liveness probe restarts them. If it is shorter, both pods only go unready for a while.
4. While both checkout pods are unready, Cilium's Envoy on the storefront-to-checkout hop (the GAMMA route) answers 503 itself. Those requests never reach checkout, so the availability SLI does not count them and k6 sees more failures than the SLI does.
5. The 5m error ratio passes 1.44% and `CheckoutAvailabilityBudgetBurn` (page) fires, because on a fresh cluster the 1h window holds only a few minutes of data.

## Scenario B: lose a worker node

Method: under load, after scenario A has settled, `docker stop` the worker that hosts the CNPG primary at that moment. Record at least 6 minutes, then `docker start` it.

Hypothesis:

1. The node goes NotReady after about 40s. Until then, pods on it look healthy to Kubernetes and requests sent to them time out.
2. When the node goes NotReady, its pods are marked unready and drop out of the endpoints. Checkout keeps serving from the pod on another node.
3. CNPG fails over to a replica once it sees the primary unready, so about 40s plus the failover time from scenario A.
4. The lost CNPG instance stays Pending: the two remaining workers already hold an instance each, and its volume is on the stopped node. `shop-db` stays at 2 of 3 until the node returns.
5. Other pods on the node are replaced only after the 300s `not-ready` toleration. If Valkey (1 replica) is on that node, every checkout request fails, and both checkout pods are unready, from about 40s until Valkey is running again elsewhere, for 5 minutes or more. That would be the largest outage of the day.
6. The page alert fires. Burn is much higher than in scenario A if Valkey was on the node.

## What gets recorded

- k6 summary and the bodies of non-200 responses.
- `hack/recorder.sh` once per second, extended for this run with CNPG instance roles and node status.
- CNPG cluster status and events, Alertmanager alerts, Grafana burn-rate panels.

Abort: if the cluster has not recovered 10 minutes after a fault ends, stop load and record the state as it is.

## Observations

Alerts were read from Prometheus (`ALERTS`, pending and firing), not from Alertmanager or Slack, and no Grafana screenshots were taken: nothing reached the alerting path to show.

### Impact

| Run | Fault | Failed requests (k6) | Error window | Alerts |
|---|---|---|---|---|
| A | primary `shop-db-1` killed 22:20:27 | 647 of 12001 (5.39%) | 22:20:29 - 22:21:01 (32s) | none |
| B | `sre-platform-worker2` stopped 22:37:30 | 7507 of 14341 (52.34%) | 22:37:30 - 22:43:49 (6m19s), nearly every request failing | none |

Neither `CheckoutAvailabilityBudgetBurn` nor `CheckoutLatencyBudgetBurn` went pending in either run. The only firing alerts were Watchdog and NodeClockNotSynchronising, both firing since before run A (22:05 and 22:15).

### Scenario A against the hypothesis

| # | Hypothesis | Result | What was observed |
|---|---|---|---|
| A1 | Replica promoted within 30s, killed pod back as replica | Held | `shop-db-rw` had no endpoint 22:20:27 - 22:20:51. shop-db-2 primary after 24s, CNPG 3/3 after 35s |
| A2 | 500s from pooled connections to the old primary | Wrong mechanism | 2 errors were in-flight connections (`unexpected EOF`). The other 405 x 500 were new connections refused by `shop-db-rw` while it had no endpoint |
| A3 | Liveness restarts checkout if the outage passes ~30s | Held, and worse | Liveness failed 3 times from 22:20:29 and killed both pods at 22:20:49. The new containers exited at once (`connection refused` to shop-db-rw at start) and came back at 22:21:00 |
| A4 | Envoy 503s while checkout is unready, invisible to the SLI | Held | 240 x 503 from Envoy 22:20:49 - 22:21:01, none counted by checkout |
| A5 | Page fires | Wrong | The checkout availability SLI stayed at 0. Prometheus holds one 500 sample (183) from one pod; the other pod's 500 counter was never scraped before liveness killed it |

### Scenario B against the hypothesis

Target: `sre-platform-worker2`, chosen after scenario A because it ran Valkey, a checkout, a storefront and the replica `shop-db-1`. The primary was on another node, so CNPG failover was not part of this run.

| # | Hypothesis | Result | What was observed |
|---|---|---|---|
| B1 | NotReady after ~40s, requests to its pods time out until then | Held, 55s | `Ready=Unknown` at 22:38:25. Until then: 524 x Envoy `connection timeout` on the Gateway-to-storefront hop (the dead storefront), 439 x storefront 502 after its 5s client timeout to the dead checkout |
| B2 | Its pods drop out of the endpoints, checkout keeps serving from the other node | Wrong | The checkout pod on worker3 went unready at 22:37:52, before the node did, because `/healthz` pings Valkey. Liveness killed it at 22:38:00, then it crash-looped because checkout exits at start when Valkey is unreachable. From 22:38:25 no checkout endpoint was ready: 6459 x `503 no healthy upstream` |
| B3 | CNPG fails over | Not exercised | Primary not on worker2. CNPG kept counting the unreachable `shop-db-1` as ready until 22:43:27 |
| B4 | Lost CNPG instance stays Pending | Wrong detail | No replacement pod was created while the node was down. `shop-db-1` stayed unready, then was stuck terminating, and was recreated on worker2 when it returned (Ready 22:44:57) |
| B5 | If Valkey is on the node, every request fails for 5+ minutes | Held | Valkey replaced only at the 300s eviction (22:43:26), Ready 22:43:32. Last error 22:43:49, 43s before the node came back. `orders_total` restarted from 0 on the new pod (4438 vs 18284 orders in Postgres) |
| B6 | Page fires, burn higher than A | Wrong | Prometheus holds one checkout 500 sample (92, 22:37:54) from the pod liveness killed 6s later. Checkout recorded no requests 22:39 - 22:44, because Envoy rejected them first. Availability SLI 0 until 22:42, no data 22:43 - 22:44; latency 1h window peaked at 0.007, under the 0.0144 threshold |

## Contributing factors

Only failures that happened in the two runs.

1. **The SLO alerts measure checkout from inside checkout.** Requests Envoy rejects never reach it (240 in A, 6459 in B), and a container killed by liveness takes its unscraped counts with it: both runs left a single 500 sample, which `rate()` cannot use. Storefront saw more but not all: in B the 524 timeouts to the dead storefront were counted by nobody. The 5xx series only appear after the first error, so even the storefront counters lose the step up from zero in `increase()` (213 of 407 storefront 500s counted in A). Two outages, 32s and 6m19s, produced no page.
2. **`/healthz` checks Postgres and Valkey and backs the liveness probe.** A dependency outage became a checkout restart in both runs. In B it took the healthy checkout on worker3 out of service because of a pod on another node.
3. **Checkout exits at start when a dependency is down.** Restarted containers exited again (A: once per pod, before the new primary; B: a crash loop until the new Valkey), and with back-off and probe timing checkout was Ready only 10s (A, 22:20:51 to 22:21:01) and 16s (B, 22:43:32 to 22:43:48) after the dependency was back.
4. **Every checkout request needs Valkey, which has one replica.** One lost pod failed every request in B. Its replacement waited for the default 300s `unreachable` toleration.

PDBs would not have changed either run: a pod kill and a stopped node are not evictions. Retries in storefront would not have covered a 24s failover or a 6-minute Valkey outage.

## Fixes and re-runs

Each fix maps to a factor above and was checked by re-running the scenario that showed it, same load and same fault ([evidence](../evidence/gameday1-reruns.md)). A2 and B2 ran checkout and storefront `sha-4c53c78`; B3 added fix 5.

| # | Fix | Factor | Check | Result |
|---|---|---|---|---|
| 1 | Measure availability at the Gateway (Cilium Envoy, north-south), the only hop every request crosses (`PodMonitor` `cilium-envoy`, SLI on `envoy_cluster_upstream_rq_*` for the storefront route). Create the app's 5xx label values at zero on start so the per-service series stay usable | 1 | Re-run A and B: the page fires | Held. Alertmanager had the page 32s after the first error in A2 (23:40:35), 36s after the stop in B2 and 46s in B3, receiver `slack`. 5m SLI peaks: 0.134 (A2), 0.197 (B2), 0.154 (B3) |
| 2 | Split probes: liveness on `/livez` (process only), readiness on `/healthz` (Postgres) | 2 | Re-run A and B: no checkout restarts | Held. 0 restarts in A2, B2, B3. In A2 one pod was unready for 10s, the other stayed Ready; in B2 and B3 the surviving pod stayed Ready |
| 3 | Create the `orders` table in a background retry instead of exiting; readiness fails until it exists | 3 | Re-run A: checkout Ready within one probe period of the new primary | Not exercised: no checkout restarted in A2, so no start-up met a missing primary. Checked locally: with no database the process stays up, `/livez` 200, `/healthz` 503 |
| 4 | Make Valkey optional: a failed `INCR` is logged and the order still answers 200; readiness and start-up stop requiring Valkey | 4 | Re-run B: errors stop when the worker2 checkout leaves the endpoints, not at the 300s eviction | Held. B2: last error 01:20:24, 5s after `Ready=Unknown`; 6406 `INCR` failures over the 6 minutes without Valkey, none failing an order |
| 5 | Bound the `INCR` to 200ms | 5 | Re-run B: no order answered 502 after it was saved; no latency burn | Held in B3: no `INCR` canceled by the caller (B2: 242), no request over 300ms (B2: about 293), `CheckoutLatencyBudgetBurn` did not fire (B2: ticket for 1m) |

| Run | Failed requests (k6) | Error window | Page |
|---|---|---|---|
| A | 647 of 12001 (5.39%) | 32s | none |
| A2 | 450 of 12001 (3.74%) | 22s, the time `shop-db-rw` had no endpoint | yes |
| B | 7507 of 14341 (52.34%) | 6m19s | none |
| B2 | 737 of 14350 (5.13%) | 55s | yes |
| B3 | 643 of 14360 (4.47%) | 50s | yes |

### Found in the re-runs

5. **The Valkey `INCR` had no timeout of its own** (B2). Until worker2 went NotReady, the Valkey Service still pointed at the dead pod, and each `INCR` waited on the dial until storefront's 5s client timeout cancelled the request. 242 orders were committed in Postgres and answered 502; the slow requests also fired the latency ticket. Fixed by fix 5.
6. **Cordoning the node of the CNPG primary moves the primary.** While setting up B2, `kubectl cordon` on `sre-platform-worker` made CNPG switch over within seconds (`Current primary is running on unschedulable node`). Expected CNPG behaviour, but it means a cordon for maintenance is a write outage of a few seconds.

### What is left

- In B2 and B3 every remaining error (643 in B3) falls in the 45-50s between the stop and `Ready=Unknown`: Envoy timeouts to the dead storefront and storefront 5s timeouts to the dead checkout. Shorter node failure detection, or retries and outlier detection on those hops, would cut it. Not changed here.
- In A2 every remaining error is a request during the 22s without a primary. Storefront retries would have to span that window.
- Valkey `orders_total` restarts from 0 at every Valkey replacement (4845 against 43482 orders after B2).

## Not explained

- Why the latency SLI 5m window reached 0.55 at 22:42 in B, while checkout recorded almost no requests.
- CNPG reported 3 ready instances for 5 minutes with one instance on an unreachable node.
- Node loss with the primary on the node, and loss of the node running Alertmanager, were not run.
