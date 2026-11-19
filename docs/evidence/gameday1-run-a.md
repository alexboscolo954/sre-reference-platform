# GameDay 1, run A: CNPG primary kill (evidence)

Recorded 2026-11-19 on the local kind cluster, all times UTC. Raw recordings (k6 log, per-second recorder output, Hubble flows) were not kept; the excerpts below are copied from them unchanged.

## Timeline

| UTC | Observed |
|---|---|
| 22:18:27 | k6 starts, 20 req/s for 10m |
| 22:20:27 | PodChaos applied; the `shop-db-rw` endpoint (shop-db-1) goes terminating, then empty |
| 22:20:28 | CNPG phase "Failing over", target primary pending |
| 22:20:29 | First k6 errors: 2x `500 unexpected EOF`, then `500 failed to connect ... shop-db-rw:5432 connection refused` |
| 22:20:29-49 | Checkout liveness probe fails 3 times on both pods (`/healthz` returns 503) |
| 22:20:34 | CNPG target primary shop-db-2 |
| 22:20:47 | The only Prometheus sample of a checkout 500 counter: xdbhl = 183. The flgwt 500 series was never scraped |
| 22:20:49 | Both checkout containers killed by liveness. The restarted containers exit at once on `connection refused` to shop-db-rw, then back off |
| 22:20:49 - 22:21:01 | k6 gets `503 no healthy upstream` (228) and `503 ... remote connection failure` (12) from Envoy: no ready checkout endpoint |
| 22:20:51 | currentPrimary shop-db-2; `shop-db-rw` points to 10.244.1.13 |
| 22:21:00 | Checkout containers start; Ready at 22:21:01 |
| 22:21:02 | CNPG 3/3 ready, healthy; shop-db-1 back as a replica |
| 22:28:28 | k6 ends |

## Numbers

- k6: 647 of 12001 requests failed (5.39%): 407 x 500 and 240 x 503, all between 22:20:29 and 22:21:01 (32s).
- Storefront counters read once per second by the recorder agree exactly: 500 = 205 + 202, 503 = 118 + 122.
- Client-side error ratio over the 5 minutes around the fault: about 647 / 6000 = 10.8%. This is not the checkout SLI as defined: the 240 Envoy 503s never reached checkout.
- Checkout availability SLI (`slo:sli_error:ratio_rate5m`): 0 for the whole run. `CheckoutAvailabilityBudgetBurn` did not fire. The only firing alerts were Watchdog and NodeClockNotSynchronising.
- Prometheus `increase()` over the run on storefront: 500 = 213, 503 = 0. The 5xx series appear only after the first error, so the first scraped sample is already 50 to 160 and the step up from zero is not counted.
- Checkout restarts during the run: per pod, 1 liveness kill and 1 exit on start. The other 3 restarts per pod happened at cluster bootstrap (22:03), before this run.
- Scrape interval: 30s.

## Excerpts

### CNPG cluster status (recorder, consecutive duplicates removed)
```
22:18:27 Cluster in healthy state primary=shop-db-1 target=shop-db-1 ready=3
22:20:28 Cluster in healthy state primary=shop-db-1 target=shop-db-1 ready=2
22:20:28 Failing over primary=shop-db-1 target=shop-db-1 ready=2
22:20:28 Failing over primary=shop-db-1 target=pending ready=2
22:20:34 Failing over primary=shop-db-1 target=shop-db-2 ready=2
22:20:51 Failing over primary=shop-db-2 target=shop-db-2 ready=2
22:20:51 Waiting for the instances to become active primary=shop-db-2 target=shop-db-2 ready=2
22:21:02 Waiting for the instances to become active primary=shop-db-2 target=shop-db-2 ready=3
22:21:02 Cluster in healthy state primary=shop-db-2 target=shop-db-2 ready=3
```

### shop-db-rw endpoints
```
22:18:27 10.244.2.247 ready=true serving=true term=false
22:20:27 10.244.2.247 ready=false serving=true term=true
22:20:27 
22:20:51 10.244.1.13 ready=true serving=true term=false
```

### k6 non-200 responses by body (local time is UTC-3)
```
   2  unexpected EOF               T19:20:29 T19:20:29 
 405  connection refused           T19:20:29 T19:20:49 
 228  no healthy upstream          T19:20:49 T19:21:01 
  12  remote connection failure    T19:20:49 T19:20:50 
```

### Liveness kills and the crashed restart
```
2026-11-19T22:20:49Z checkout-55597b8f88-flgwt Container checkout failed liveness probe, will be restarted
2026-11-19T22:20:49Z checkout-55597b8f88-xdbhl Container checkout failed liveness probe, will be restarted
2026-11-19T22:20:49.694133722Z failed to connect to `user=app database=app`: 10.96.206.62:5432 (shop-db-rw.shop): dial error: dial tcp 10.96.206.62:5432: connect: connection refused
2026-11-19T22:20:49.695091097Z failed to connect to `user=app database=app`: 10.96.206.62:5432 (shop-db-rw.shop): dial error: dial tcp 10.96.206.62:5432: connect: connection refused
```

### 5xx samples Prometheus actually holds (30s scrape)
```
checkout-55597b8f88-xdbhl POST /checkout 500: 22:20:47=183
storefront-669fd989dd-lf9vg POST /buy 500: 22:20:34=50 22:21:04=205 22:21:34=205 ... (flat after)
storefront-669fd989dd-tmrzx POST /buy 500: 22:20:45=160 22:21:15=202 22:21:45=202 ... (flat after)
storefront-669fd989dd-lf9vg POST /buy 503: 22:21:04=118 22:21:34=118 22:22:04=118 ... (flat after)
storefront-669fd989dd-tmrzx POST /buy 503: 22:21:15=122 22:21:45=122 22:22:15=122 ... (flat after)
```
