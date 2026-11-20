# GameDay 2, re-runs A2, B2, B3 (evidence)

Recorded 2026-11-20 on the local kind cluster, all times UTC (k6 prints UTC-3; converted here). Same load (`make load`, 20 req/s for 10m), same manifests and timing as runs A and B: fault applied 180s after load start. Raw recordings were not kept; the excerpts below are copied from them unchanged.

| Run | Cluster | Checkout image | Fixes in place |
|---|---|---|---|
| A2 | fresh (`make down && make up`), first load on it | `sha-f958148` | 1 (delta SLI), 2 (`INSERT` bounded to 1s) |
| B2 | same as A2, after A2 | `sha-f958148` | 1, 2 |
| B3 | fresh, first load on it | `sha-f682409` | 1, 2, 3 (latency SLI on 2xx only) |

Storefront stayed at `sha-4c53c78` throughout.

## A2: Valkey kill on a fresh cluster

Placement: worker: storefront, shop-db-2. worker2: checkout, storefront, valkey, shop-db-3. worker3: checkout, shop-db-1 (primary).

| UTC | Observed |
|---|---|
| 19:04:41 | k6 starts |
| 19:04:45 | Gateway storefront `completed` 0 to 7; the class 2 series appears at 7 (same start-up shape as run A) |
| 19:07:41 | PodChaos applied; `valkey` endpoint empty |
| 19:07:42 | New Valkey pod Ready, endpoint 10.244.2.88 ready |
| 19:14:41 | k6 ends |

- k6: 0 of 12001 failed, `http_req_duration` max 362.35ms. Checkout requests over 300ms (Prometheus, 11m): 0.
- Availability SLI 5m and 1h: 0 at every point from 19:05:30 to 19:16:00. No `Checkout*` alert pending or firing. Run A, same shape: 1.0 at the first point, page at 16:51:30.
- Failed `INCR`: 18 in 0.9s (19:07:41.920 - 19:07:42.834), none failing an order. This time one hit the 200ms bound:

```
checkout-684f7dfdd6-rgfcq
3 19:07:42.526 19:07:42.834  dial tcp 10.96.250.23:6379: connect: connection refused
4 19:07:41.920 19:07:42.669  EOF
checkout-684f7dfdd6-s746r
3 19:07:42.426 19:07:42.572  dial tcp 10.96.250.23:6379: connect: connection refused
3 19:07:42.217 19:07:42.377  dial tcp 10.96.250.23:6379: i/o timeout
4 19:07:41.969 19:07:42.620  EOF
1 19:07:42.377 19:07:42.377  context deadline exceeded
```

- Orders: Postgres 12001, Valkey `orders_total` 8381. Checkout 0 restarts.

### Gateway storefront counters at load start
```
completed  19:04:30=0 19:04:45=7 19:05:00=7 19:05:15=607 19:05:30=607 19:05:45=1207
xx class 2 19:04:45=7 19:05:00=7 19:05:15=607 19:05:30=607 19:05:45=1207
```

## B2: partition with the `INSERT` bounded to 1s

| UTC | Observed |
|---|---|
| 19:17:24 | k6 starts |
| 19:20:24 | NetworkChaos applied |
| 19:20:26 | First k6 error: `500 context deadline exceeded` |
| 19:20:54 / 19:20:56 | checkout pods not ready (30s, 32s) |
| 19:20:56 | First `503 no healthy upstream` |
| 19:20:57 | Last 500 |
| 19:21:03 | `CheckoutAvailabilityBudgetBurn` page and ticket active in Alertmanager, receiver `slack` |
| 19:21:16 | `CheckoutLatencyBudgetBurn` ticket active |
| 19:22:32 / 19:22:33 | checkout pods ready; last k6 error 19:22:32 |
| 19:27:24 | k6 ends |

- k6: 2548 of 11999 failed (21.23%): 619 x 500 (`context deadline exceeded`, 2 of them `timeout: context deadline exceeded`) and 1929 x 503. `vus_max` 21, 1 dropped iteration, `http_req_duration` max 1.03s.
- Orders: Postgres +9451, k6 200s 9451.
- Checkout over 10m: 643 x 500, about 652 requests over 300ms.
- Latency SLI 5m peaked at 0.1785 (19:23 - 19:25), over the 0.144 page threshold; the 1h window peaked at 0.0394, so the page did not fire. The ticket did.
- Availability SLI 5m peaked at 0.4247, 1h at 0.1389. Checkout 0 restarts, CNPG 3 ready throughout.

```
latency 5m 19:20:30=0 19:21:00=0.0621 19:21:30=0.1463 19:22:00=0.1463 19:22:30=0.1563 19:23:00=0.1784 19:23:30=0.1785 ... 19:25:00=0.1783 19:25:30=0.1089 19:26:00=0
```

## B3: partition, latency SLI on successful requests

Placement: worker: checkout, storefront, valkey, shop-db-3. worker2: storefront, shop-db-2. worker3: checkout, shop-db-1 (primary).

| UTC | Observed |
|---|---|
| 19:44:13 | k6 starts, first load on this cluster |
| 19:47:14 | NetworkChaos applied |
| 19:47:15 | First k6 error: `500 context deadline exceeded` |
| 19:47:38 / 19:47:39 | checkout pods not ready (24s, 25s) |
| 19:47:39 | First 503 |
| 19:47:41 | Last 500 |
| 19:48:07 | `CheckoutAvailabilityBudgetBurn` page and ticket active in Alertmanager, receiver `slack` |
| 19:49:15 / 19:49:20 | checkout pods ready; last k6 error 19:49:15 |
| 19:54:14 | k6 ends |

- k6: 2418 of 11999 failed (20.15%): 507 x 500 and 1911 x 503. `vus_max` 21, 1 dropped iteration, max 1.02s.
- Orders: Postgres 9581 (fresh cluster), k6 200s 9581.
- Checkout duration histogram over 11m, by code: 200 = 9141, all under 300ms; 500 = 387 (the 500 series is created by the first 500, so `increase()` misses its first step; k6 saw 507).
- Latency SLI 5m and 1h: 0 at every point. No `CheckoutLatencyBudgetBurn` pending or firing.
- Availability SLI 5m 0 until 19:48:00, peak 0.4031; 1h peak 0.3789. No availability alert before the fault.
- Checkout 0 restarts, CNPG 3 ready throughout.

### Checkout endpoints (recorder, changes only)
```
19:44:09 10.244.1.109 ready=true | 10.244.2.229 ready=true
19:47:38 10.244.1.109 ready=false | 10.244.2.229 ready=true
19:47:39 10.244.1.109 ready=false | 10.244.2.229 ready=false
19:49:15 10.244.1.109 ready=true | 10.244.2.229 ready=false
19:49:20 10.244.1.109 ready=true | 10.244.2.229 ready=true
```
