# GameDay 2, run A: Valkey kill (evidence)

Recorded 2026-11-20 on a fresh local kind cluster (`make up` at about 16:30), all times UTC. Checkout and storefront `sha-44b32a1` / `sha-4c53c78` (GameDay 0001 fixes in place). Raw recordings were not kept; the excerpts below are copied from them unchanged.

Placement: worker: checkout, storefront, valkey, shop-db-2 (replica). worker2: shop-db-3 (replica). worker3: checkout, storefront, shop-db-1 (primary).

## Timeline

| UTC | Observed |
|---|---|
| 16:49:57 | k6 starts, 20 req/s for 10m. First load on this cluster |
| 16:50:30 | First scrape with traffic: the Gateway's storefront `upstream_rq_completed` goes 0 to 565; the `upstream_rq_xx` class 2 series appears for the first time, at 565 |
| 16:51:00 | Availability SLI 5m and 1h both 1.0 |
| 16:51:30 - 16:51:45 | `CheckoutAvailabilityBudgetBurn` firing (page), no errors anywhere |
| 16:52:57 | PodChaos applied; `valkey-84dc8b6984-hwmvj` killed, `valkey` endpoint terminating, then empty. Replacement `valkey-84dc8b6984-sz5hn` scheduled on the same node |
| 16:52:57.541 | First failed `INCR` (`EOF`) |
| 16:52:58 | `sz5hn` Running and Ready, `valkey` endpoint 10.244.2.237 ready |
| 16:52:59.108 | Last failed `INCR` (`connection refused`) |
| 16:59:57 | k6 ends |
| about 17:02:23 | `CheckoutAvailabilityBudgetBurn` ticket active in Alertmanager, receiver `slack` |

## Numbers

- k6: 0 of 12000 requests failed. `http_req_duration` max 459.55ms, p95 18.5ms.
- Checkout requests over 300ms during the run (Prometheus, `POST /checkout` histogram): 0 of about 12393.
- Failed `INCR`: 32 in 1.6s (16:52:57.541 - 16:52:59.108), 7 `EOF` and 25 `connection refused` to the `valkey` ClusterIP. None failed an order.
- Valkey gap: the endpoint was empty for under a second; the last failure came about 1s after the recorder saw the new endpoint Ready (1s resolution).
- Orders: Postgres 0 before, 12000 after. Postgres kept growing at about 20/s through the kill (3597 at 16:52:57, 3638 at 16:52:59). Valkey `orders_total` 8365 after, on the new pod: 3635 below Postgres, about 3603 counted on the killed pod plus the 32 failed `INCR`s.
- Checkout: both pods Ready throughout (checkout endpoints never changed), 0 restarts.
- CNPG: unchanged, 3 ready, primary shop-db-1.
- Alerts: `CheckoutAvailabilityBudgetBurn` page firing at 16:51:30 and 16:51:45, 87s before the fault; ticket active in Alertmanager from about 17:02:23. Neither is from the fault (see below). `CheckoutLatencyBudgetBurn` never pending. Whether the page reached Slack was not checked.
- Scrape interval: 30s.

## Excerpts

### Gateway storefront cluster counters (Prometheus, summed over Envoys)
```
completed  16:48:00=0 ... 16:50:15=0 16:50:30=565 16:50:45=565 16:51:00=1165 16:51:15=1165 16:51:30=1765
xx class 2 16:50:30=565 16:50:45=565 16:51:00=1165 16:51:15=1165 16:51:30=1765
```
`completed` exists at 0 from Envoy start; the class 2 series is created by the first 2xx. `rate()` counts the 0 to 565 step in `completed` and cannot see it in class 2, so the SLI's `completed - xx{2,3,4}` reads those 565 requests as errors.

### Availability SLI (`slo:sli_error:ratio_rate5m` and `ratio_rate1h`, requests-availability)
```
5m 16:51:00=1 16:51:30=-0.1556 16:52:00=-0.069 16:52:30=-0.0263 16:53:00=-0.0009 16:53:30=0.0159 16:54:00=0.0279 16:54:30=0.0369 16:55:00=-0.0006 16:55:30=0
1h 16:51:00=1 16:51:30=-0.239 16:52:00=-0.1475 16:52:30=-0.1029 16:53:00=-0.0767 16:53:30=-0.0596 16:54:00=-0.0477 16:54:30=-0.0389 16:55:00=-0.0322 16:55:30=-0.027 16:56:00=-0.0228 16:56:30=-0.0194 16:57:00=-0.0166 16:57:30=-0.0143 16:58:00=-0.0123
```
The 5m window settles at 0 once it no longer covers 16:50:30. The 1h window stays negative until it passes 16:50:30, at about 17:51.

### Valkey pod and endpoint (recorder)
```
16:52:57 valkey-84dc8b6984-hwmvj deleting=true ready=True
16:52:57 valkey-84dc8b6984-sz5hn Pending node=sre-platform-worker
16:52:58 valkey-84dc8b6984-sz5hn Running ready=True ip=10.244.2.237
eps 16:52:57 ["10.244.2.220 ready=false serving=true term=true"]
eps 16:52:57 []
eps 16:52:58 ["10.244.2.237 ready=false serving=false term=false"]
eps 16:52:58 ["10.244.2.237 ready=true serving=true term=false"]
```

### Checkout `orders_total not incremented` (count, first, last, error)
```
checkout-55f57bb559-c8kkk
4 16:52:57.541 16:52:58.290  EOF
11 16:52:57.738 16:52:58.938  dial tcp 10.96.6.235:6379: connect: connection refused
checkout-55f57bb559-zjq85
3 16:52:57.641 16:52:57.839  EOF
14 16:52:57.891 16:52:59.108  dial tcp 10.96.6.235:6379: connect: connection refused
```

### Events (shop)
```
16:52:57 Applied  valkey-kill  Successfully apply chaos for shop/valkey-84dc8b6984-hwmvj
16:52:57 Killing  valkey-84dc8b6984-hwmvj  Stopping container valkey
16:52:58 Unhealthy  valkey-84dc8b6984-sz5hn  Readiness probe failed: dial tcp 10.244.2.237:6379: connect: connection refused
```
