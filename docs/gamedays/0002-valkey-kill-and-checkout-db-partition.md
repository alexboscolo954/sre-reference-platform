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
