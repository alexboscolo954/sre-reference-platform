# GameDay 0002: Valkey kill and checkout-to-Postgres partition

Status: hypotheses written 2026-11-20, before any run, and left unchanged. Everything from "Observations" on was written afterwards, from [`docs/evidence/gameday2-*.md`](../evidence/).

## Steady state

- Load: `make load`, k6 constant arrival rate of 20 req/s to storefront `POST /buy` for 10m, `preAllocatedVUs` 20, `maxVUs` 100. Storefront calls checkout `POST /checkout` through the GAMMA route, with a 5s client timeout and no retry.
- Each checkout request does one `INSERT ... RETURNING id` on Postgres (pgxpool, no retry, no timeout of its own, so bounded only by the request context) and one `INCR orders_total` on Valkey, bounded to 200ms. A failed `INSERT` returns 500; a failed `INCR` is logged and the order still returns 200, without `orders_total` (GameDay 0001, fix 4).
- Probes on checkout: liveness `/livez` (process only), readiness `/healthz` (`orders` table created, then `db.Ping`). Defaults: period 10s, timeout 1s, failure threshold 3. Valkey has a TCP readiness probe, one replica, no persistence.
- SLOs: availability 99.9% measured at the Gateway (Envoy, storefront route); latency 99% under 300ms measured in checkout. Each `*BudgetBurn` alert pages at 14.4x over 5m and 1h: 1.44% failed requests for availability, 14.4% slow requests for latency.
- `shop-db`: 3 instances, one per worker. Checkout reaches the primary through `shop-db-rw`.
- Placement is recorded before each run in the evidence. Neither scenario depends on it: A kills a pod, B cuts traffic from every checkout pod to every `shop-db` pod.

## Scenario A: kill Valkey

Method: under load, apply `chaos/valkey-kill.yaml` (PodChaos, `pod-kill`, grace period 0, selects `app=valkey`). Record until 3 minutes after the new Valkey pod is Ready.

Hypothesis:

1. The Deployment creates a new Valkey pod at once, and it is Ready within 20s of the kill.
2. No order fails: k6 sees no non-200 from the kill until the end of load.
3. Each `INCR` during the gap fails within 200ms (connection refused while the `valkey` Service has no endpoint, or the 200ms bound), so no checkout request takes over 300ms because of it.
4. Checkout logs one `orders_total not incremented` warning per order in the gap, about 20 per second, and none after the first request that follows the new pod's endpoint.
5. `orders_total` restarts from 0 on the new pod and ends below the number of orders in Postgres by roughly the orders before the kill plus the ones in the gap.
6. Checkout stays Ready with 0 restarts, and no `Checkout*` alert goes pending.

## Scenario B: partition checkout from Postgres

Method: under load, at least 5 minutes after scenario A, apply `chaos/checkout-db-partition.yaml` (NetworkChaos, `partition`, direction `to`, from `app=checkout` to `cnpg.io/cluster=shop-db`, 2 minutes). Record until 3 minutes after it ends.

Before the run, a short check without load confirms the fault takes effect (a `psql` connection from a checkout pod's network namespace to `shop-db-rw` hangs while it is applied). If it does not, the evidence records why and what replaced it, and these hypotheses stay as written.

Hypothesis:

1. The partition drops pooled connections too, not only new ones: from the first second, no `INSERT` gets an answer.
2. Until checkout goes unready, each request hangs until storefront's 5s timeout and storefront answers 502. k6 needs about 100 VUs for that and reports dropped iterations.
3. Readiness fails by timeout (`db.Ping` waits on the partition, the probe gives up after 1s). Both checkout pods leave the endpoints 20 to 30s after the partition starts, and liveness restarts nothing.
4. While both are unready, Envoy on the GAMMA route answers `503 no healthy upstream` at once, and storefront passes the 503 through. That lasts until the partition ends.
5. Within one probe period (10s) of the partition ending, both checkout pods are Ready again and every request succeeds. pgxpool needs no restart: connections cancelled during the partition are closed and new ones are dialled.
6. Total impact is every request for about 2m10s, roughly 2,600 failed requests: about 500 502s, then 503s.
7. At most a few orders are committed in Postgres but answered as errors (only `INSERT`s in flight at the moment the partition starts). Checked by comparing the Postgres order count over the run with k6's 200s.
8. CNPG sees nothing: no failover, `shop-db` stays at 3 ready instances, and replication is unaffected.
9. `CheckoutAvailabilityBudgetBurn` pages. `CheckoutLatencyBudgetBurn` does not page: checkout records the requests that hung for 5s before it went unready, about 500, but against the fast requests in the 5m window they stay under 14.4%.

## What gets recorded

- k6 summary and the bodies of non-200 responses.
- `hack/recorder.sh`, once per second (endpoints now include `valkey`).
- A timeline file with the time of every action (load start, fault, fault end, load end).
- Postgres `SELECT count(*) FROM orders` once per second (recorder, on the primary at start), and Valkey `GET orders_total` before and after each run.
- After the run: Prometheus (`ALERTS`, SLI recording rules), Alertmanager `/api/v2/alerts`, events, checkout logs (`orders_total not incremented`), previous container logs if any restarted.

Abort: if the system has not recovered 5 minutes after a fault ends, stop load and record the state as it is.

## Observations

Both runs on one fresh cluster, 2026-11-20 ([run A](../evidence/gameday2-run-a.md), [run B](../evidence/gameday2-run-b.md)). Run B started at 17:52 rather than 5 minutes after A, so that the 1h availability window no longer held run A's start-up artifact (contributing factor 1). Alerts were read from Prometheus `ALERTS` and Alertmanager `/api/v2/alerts`.

### Impact

| Run | Fault | Failed requests (k6) | Error window | Alerts |
|---|---|---|---|---|
| A | Valkey pod killed 16:52:57 | 0 of 12000 | none | availability page 16:51:30 and ticket from 17:02, both before or unrelated to the fault |
| B | checkout cut off from shop-db 17:55:14 - 17:57:14 | 2358 of 11921 (19.78%) | 17:55:14 - 17:57:16 (2m02s) | availability page (Alertmanager 17:56:23, receiver `slack`), latency ticket |

### Scenario A against the hypothesis

| # | Hypothesis | Result | What was observed |
|---|---|---|---|
| A1 | New Valkey pod Ready within 20s | Held | Replacement on the same node, Ready at 16:52:58, about 1s after the kill |
| A2 | No order fails | Held | 0 of 12000 failed |
| A3 | Each `INCR` fails within 200ms, no request over 300ms | Held | 7 `EOF`, 25 `connection refused` to the `valkey` ClusterIP. 0 checkout requests over 300ms |
| A4 | One warning per order in the gap, none after the new endpoint | Wrong detail | 32 warnings in 1.6s (about 20/s), the last about 1s after the recorder saw the new endpoint Ready |
| A5 | `orders_total` restarts from 0, ends below Postgres by the orders before the kill plus the gap | Held | 8365 against 12000: about 3603 before the kill plus 32 |
| A6 | Checkout stays Ready, no `Checkout*` alert pending | Wrong | Checkout Ready throughout, 0 restarts. But `CheckoutAvailabilityBudgetBurn` paged at 16:51:30, 87s before the kill, with no failed request, and its ticket has been active since 17:02 |

### Scenario B against the hypothesis

| # | Hypothesis | Result | What was observed |
|---|---|---|---|
| B1 | Pooled connections are cut too, no `INSERT` answered from the first second | Held | Postgres order count stopped at 15597 at 17:55:14 and moved again at 17:57:16 |
| B2 | Requests hang until storefront's 5s timeout, 502, dropped iterations | Held | 494 x `502 ... Client.Timeout exceeded`, 17:55:19 - 17:55:48. 100 VUs, 80 dropped iterations |
| B3 | Readiness fails by timeout, both pods out 20 to 30s in, no restarts | Held | Not ready at 17:55:40 and 17:55:42 (26s, 28s), `context deadline exceeded`, 0 restarts |
| B4 | Envoy `503 no healthy upstream` until the partition ends | Held | 1864 x 503, 17:55:42 - 17:57:16 |
| B5 | Ready within 10s of the end, pgxpool recovers | Held | Ready at 17:57:16 and 17:57:21 (2s and 7s after `TimeUp`), orders resumed at 17:57:16 |
| B6 | About 2m10s of failures, about 2600, about 500 of them 502 | Held | 2m02s, 2358, of which 494 were 502 |
| B7 | At most a few orders committed but answered as errors | Held | None: Postgres +9563, k6 200s 9563 |
| B8 | CNPG sees nothing | Held | Primary shop-db-1, 3 ready throughout, no restarts |
| B9 | Availability pages, latency does not | Held, narrowly | Page at 17:56:23. Latency 5m SLI peaked at 0.143 against a page threshold of 0.144, and the latency ticket fired, which was not predicted |

## Contributing factors

Only failures that happened in the two runs.

1. **The Gateway availability SLI pages when traffic starts on an idle Envoy** (run A). Envoy's `upstream_rq_completed` exists at 0 from start-up; the `upstream_rq_xx` class 2 series is created by the first 2xx response. `rate()` sees the step from 0 in `completed` but not in class 2, so the first scrape interval of successes counts as errors: both windows read 1.0 at 16:51:00 and the page fired with no failed request. The ticket stayed active because the 6h window still holds it. The same `rate()` mismatch then pushed the 5m SLI between -0.16 and +0.037 until the window passed the series start. This is GameDay 0001's fix 1: written to count a first 5xx, it miscounts a first 2xx.
2. **Checkout's `INSERT` has no timeout of its own** (run B). Until readiness removed both pods (26 to 28s), every request waited for storefront's 5s client timeout: 494 failed slowly instead of at once, k6 needed all 100 VUs and dropped 80 iterations, and checkout recorded about 520 requests over 300ms. That brought the latency 5m SLI to 0.143, 0.001 under paging, and fired the latency ticket for an outage that was about availability.

Run B's two-phase shape (slow 502s, then fast 503s) is also the price of readiness at the default period: the 26 to 28s before the pods left the endpoints is three probe failures at 10s.

## Fixes and re-runs

Pending: fixes and re-runs not done yet.

### What is left

## Not explained

- Chaos Mesh reported the partition as applied to the three shop-db pods as well as to checkout, with direction `to`. Replication was not affected (3 ready throughout), and what it installed there was not read.
