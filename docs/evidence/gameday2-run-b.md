# GameDay 2, run B: checkout-to-Postgres partition (evidence)

Recorded 2026-11-20 on the same cluster as run A, all times UTC (k6 prints local time, UTC-3; converted here). Started at 17:52, after the 1h availability window no longer covered run A's start-up artifact (see run A). Raw recordings were not kept; the excerpts below are copied from them unchanged.

Placement: unchanged from run A, except Valkey is now `valkey-84dc8b6984-sz5hn` (worker). Primary shop-db-1 (worker3).

## Fault check without load (17:06)

A copy of `chaos/checkout-db-partition.yaml` named `partition-check`, duration 45s, applied with no load. The plan was a `psql` connection from a checkout pod; the checkout image has no shell, so readiness was watched instead.

```
17:06:44 applied
17:07:04 c8kkk=true  zjq85=true
17:07:09 c8kkk=false zjq85=true
17:07:14 c8kkk=false zjq85=false
17:07:29 c8kkk=true  zjq85=false
17:07:35 c8kkk=true  zjq85=true
17:07:20 c8kkk Readiness probe failed: Get "http://10.244.3.253:8080/healthz": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
17:07:22 zjq85 Readiness probe failed: Get "http://10.244.2.215:8080/healthz": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
```

## Timeline

| UTC | Observed |
|---|---|
| 17:52:14 | k6 starts, 20 req/s for 10m |
| 17:55:14 | NetworkChaos applied to both checkout pods and all three shop-db pods. Postgres order count stops at 15597 |
| 17:55:19 | First k6 error: `502 Post "http://checkout/checkout": context deadline exceeded (Client.Timeout exceeded while awaiting headers)` |
| 17:55:40 | checkout `c8kkk` (10.244.3.253) not ready, 26s after the fault |
| 17:55:42 | checkout `zjq85` (10.244.2.215) not ready; no ready checkout endpoint. First `503 no healthy upstream` |
| 17:55:48 | Last 502 |
| 17:56:18 | `CheckoutLatencyBudgetBurn` ticket active in Alertmanager |
| 17:56:23 | `CheckoutAvailabilityBudgetBurn` page active in Alertmanager, receiver `slack` (Prometheus `ALERTS` firing from 17:56:30) |
| 17:57:14 | Chaos ends (`TimeUp`), rules removed |
| 17:57:16 | `c8kkk` ready; Postgres order count moves again (15601). Last k6 error |
| 17:57:21 | `zjq85` ready |
| 18:02:14 | k6 ends |

## Numbers

- k6: 2358 of 11921 requests failed (19.78%): 494 x 502 (17:55:19 - 17:55:48) and 1864 x `503 no healthy upstream` (17:55:42 - 17:57:16). 80 dropped iterations; VUs reached the maximum of 100. `http_req_duration` max 5.02s.
- Error window: 17:55:14 - 17:57:16, 2m02s.
- Orders: Postgres 12000 before, 21563 after (+9563). k6 200s: 11921 - 2358 = 9563. No order was committed and answered as an error. Postgres was at 15597 from 17:55:14 to 17:57:15.
- Valkey `orders_total` 8365 before, 17928 after (+9563).
- Checkout over 17:52:30 - 18:02:30 (Prometheus `increase`, 10m): about 9941 requests, 9421 under 300ms, 513 x 500. No 502 or 503 recorded by checkout.
- Checkout restarts: 0. CNPG: primary shop-db-1, 3 ready throughout; the recorder saw no status change. No shop-db restarts.
- Availability SLI 5m peaked at 0.4432 (17:58 - 18:00), 1h at 0.2749. Latency SLI 5m peaked at 0.143 (17:58), against a page threshold of 0.144 (14.4 x 1%); 1h at 0.0736.
- Alerts (Prometheus `ALERTS`, besides Watchdog and NodeClockNotSynchronising): availability page firing 17:56:30 to the end of the query (18:06); latency ticket firing 17:56:30 - 18:06; `KubePodNotReady` pending 17:56:15 - 17:58:00, never firing. The availability ticket was firing from before the run (17:02, after run A; no request failed between run A's start and this run's fault).

## Excerpts

### Checkout endpoints (recorder, changes only)
```
17:52:09 checkout 10.244.3.253 ready=true | 10.244.2.215 ready=true
17:55:40 checkout 10.244.3.253 ready=false | 10.244.2.215 ready=true
17:55:42 checkout 10.244.3.253 ready=false | 10.244.2.215 ready=false
17:57:16 checkout 10.244.3.253 ready=true | 10.244.2.215 ready=false
17:57:21 checkout 10.244.3.253 ready=true | 10.244.2.215 ready=true
```
`checkout-stable` and `checkout-canary` changed at the same seconds.

### Postgres order count (recorder, about 2 reads per second)
```
17:55:13 15590 17:55:14 15597 17:55:14 15597 17:55:15 15597 ... 17:57:15 15597 17:57:16 15601 17:57:16 15609 17:57:17 15617
```

### Availability and latency SLI
```
availability 5m 17:55:30=0 17:56:00=0.0226 17:56:30=0.1716 17:57:00=0.2722 17:57:30=0.3656 17:58:00=0.4432 ... 18:00:30=0.4165 18:01:00=0.2987 18:01:30=0.1876 18:02:00=0.0765 18:02:30=0
latency 5m      17:55:30=0 17:56:00=0.0775 17:56:30=0.1208 17:57:00=0.1211 17:57:30=0.1365 17:58:00=0.143 ... 18:00:00=0.1423 18:00:30=0.053 18:01:00=0
```

### Events (shop)
```
17:55:14 Applied  checkout-db-partition  Successfully apply chaos for shop/shop-db-1   (also shop-db-2, shop-db-3, both checkout pods)
17:57:07 Unhealthy  checkout-55f57bb559-c8kkk  Readiness probe failed: Get "http://10.244.3.253:8080/healthz": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
17:57:12 Unhealthy  checkout-55f57bb559-zjq85  Readiness probe failed: Get "http://10.244.2.215:8080/healthz": context deadline exceeded (Client.Timeout exceeded while awaiting headers)
17:57:14 TimeUp  checkout-db-partition  Time up according to the duration
17:57:14 Recovered  checkout-db-partition  Successfully recover chaos for shop/checkout-55f57bb559-c8kkk   (and the other four pods)
```
