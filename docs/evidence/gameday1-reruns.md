# GameDay 1, re-runs with the fixes (evidence)

Recorded 2026-11-19 and 2026-11-20 on the local kind cluster, all times UTC, with the same load and fault as the first runs ([run A](gameday1-run-a.md), [run B](gameday1-run-b.md)). Raw recordings were not kept; the excerpts below are copied from them unchanged. k6 log times are local (UTC-3).

| Re-run | Images | Fixes deployed |
|---|---|---|
| A2 | checkout and storefront `sha-4c53c78` | Gateway SLI, 5xx series at zero, `/livez` liveness, start-up retry, Valkey optional |
| B2 | same | same |
| B3 | checkout `sha-44b32a1`, storefront `sha-4c53c78` | B2 plus a 200ms bound on the Valkey `INCR` (added after B2) |

Alerts were read from Prometheus `ALERTS` and from the Alertmanager API (`/api/v2/alerts`). The ticket `CheckoutAvailabilityBudgetBurn` fired in A2 (startsAt 23:40:35) and stayed active through B2 and B3, so in those two runs only the page is new evidence.

## A2: kill the CNPG primary

Before: primary `shop-db-2` on `sre-platform-worker`, checkout `gp4s2` (worker2) and `shbkz` (worker).

| UTC | Observed |
|---|---|
| 23:38:01 | k6 starts, 20 req/s for 10m |
| 23:40:01 | PodChaos kills `shop-db-2` |
| 23:40:02 | CNPG `Failing over`; `shop-db-rw` has no endpoint |
| 23:40:03 | First k6 error |
| 23:40:24 | `gp4s2` unready (readiness pings Postgres); `shbkz` stays Ready |
| 23:40:25 | `shop-db-1` (worker2) primary, `shop-db-rw` endpoint back; last k6 error |
| 23:40:34 | `gp4s2` Ready |
| 23:40:35 | CNPG 3/3. Alertmanager startsAt for the page and the ticket, receiver `slack` |
| 23:41:00 | First Prometheus `ALERTS` sample firing (page, ticket) |
| 23:48:02 | k6 ends |

- k6: 450 of 12001 failed (3.74%), 23:40:03 - 23:40:25 (22s), the time `shop-db-rw` had no endpoint. Run A: 647 over 32s.
- No Envoy 503s. No checkout restarts (both pods `restartCount` 0). Run A: 240 Envoy 503s, both pods restarted.
- Availability SLI (Gateway), 5m window: peak 0.1341 at 23:41:00.

### k6 non-200 responses by body
```
    2 20:40:03..20:40:03  500 unexpected EOF\n
  448 20:40:03..20:40:25  500 failed to connect to `user=app database=app`:\n\tIP (shop-db-rw.shop): dial error: dial tcp IP:
```

### Endpoints and CNPG status (recorder)
```
23:40:01 shop-db-rw 10.244.1.13 ready=false serving=true term=true
23:40:02 shop-db-rw
23:40:24 checkout 10.244.2.24 ready=false serving=false term=false | 10.244.1.238 ready=true serving=true term=false
23:40:25 shop-db-rw 10.244.2.73 ready=true serving=true term=false
23:40:34 checkout 10.244.2.24 ready=true serving=true term=false | 10.244.1.238 ready=true serving=true term=false

23:40:02 Failing over primary=shop-db-2 target=pending ready=2
23:40:08 Failing over primary=shop-db-2 target=shop-db-1 ready=2
23:40:25 Failing over primary=shop-db-1 target=shop-db-1 ready=2
23:40:35 Cluster in healthy state primary=shop-db-1 target=shop-db-1 ready=3
```

## Setting up B2 and B3

Both re-create run B: Valkey, a checkout, a storefront and the replica `shop-db-1` on `sre-platform-worker2`; primary `shop-db-2` on `sre-platform-worker`; Prometheus on `worker3`, Alertmanager on `worker`.

- The primary was moved with a CNPG switchover (`status.targetPrimary` patch, the `kubectl cnpg promote` mechanism): 8s to `Cluster in healthy state`, no checkout restarts, no load running.
- The first attempt cordoned `worker` and `worker3` to push Valkey onto worker2. CNPG then switched the primary away from the cordoned node within seconds, event `SwitchingOver: Current primary is running on unschedulable node sre-platform-worker, switching over from shop-db-2 to shop-db-1`. The switchover was repeated without cordoning.
- For B3, only `worker3` was cordoned while the storefront and Valkey pods were deleted until they landed on worker2.

## B2: lose worker2

| UTC | Observed |
|---|---|
| 01:17:29 | k6 starts, 20 req/s for 12m |
| 01:19:29 | `docker stop sre-platform-worker2` (returns 01:19:30) |
| 01:19:29 | 2 Envoy resets (`connection termination`, `remote connection failure`) |
| 01:19:30 | First Valkey `INCR` failure on the surviving checkout `shbkz`, logged, order answered 200 |
| 01:19:35 - 01:20:24 | 470x Envoy `503 connection timeout` (Gateway to the dead storefront), 265x storefront `502` after its 5s timeout |
| 01:20:05 | Alertmanager startsAt for the page, receiver `slack` (36s after the stop) |
| 01:20:19 | worker2 `Ready=Unknown` (50s). Its checkout leaves the endpoints; `shbkz` Ready throughout. CNPG ready 3 -> 2, no failover |
| 01:20:24 | Last k6 error |
| 01:21:30 - 01:22:30 | `CheckoutLatencyBudgetBurn` ticket firing |
| 01:25:19 | Taint eviction; new checkout Ready 01:25:36; Valkey replaced on `worker` |
| 01:25:27 | Last `INCR` failure |
| 01:26:30 | `docker start`; node Ready 01:26:32; CNPG 3/3 01:27:10 |
| 01:29:30 | k6 ends |

- k6: 737 of 14350 failed (5.13%), 01:19:29 - 01:20:24 (55s). Run B: 7507 over 6m19s.
- No checkout restarts, no checkout 500s.
- `INCR` failures on `shbkz`: 6164 `connection refused` (01:19:30 - 01:25:27), 208 `operation was canceled` and 34 `context canceled` (01:19:35 - 01:20:20). In the 242 canceled requests the `INSERT` had committed, then the `INCR` dial outlasted storefront's 5s client timeout, and storefront answered 502 for an order that is in Postgres.
- `shbkz` latency, 3m to 01:22: about 293 requests over 300ms, 164 between 2.5s and 5s. p99 (1m) 4.83s at 01:21. Latency SLI 5m peak 0.0809, 1h 0.0848.
- Availability SLI (Gateway), 5m window: 0.0646 at 01:20:00, peak 0.197 at 01:20:30, 0 from 01:25:30.
- After the run: Valkey `orders_total` 4845, Postgres `orders` 43482 rows.

### k6 non-200 responses by body
```
    1 22:19:29..22:19:29  503 upstream connect error or disconnect/reset before headers. reset reason: connection termination
    1 22:19:29..22:19:29  503 upstream connect error or disconnect/reset before headers. reset reason: remote connection fail
  470 22:19:35..22:20:21  503 upstream connect error or disconnect/reset before headers. reset reason: connection timeout
  112 22:19:35..22:20:24  502 Post \"http://checkout/checkout\": context deadline exceeded (Client.Timeout exceeded while awa
   11 22:19:35..22:20:24  502 Post \"http://checkout/checkout\": context deadline exceeded\n
  142 22:19:35..22:20:24  502 Post \"http://checkout/checkout\": net/http: request canceled (Client.Timeout exceeded while aw
```

### checkout endpoints
```
01:17:30 10.244.2.24 ready=true serving=true term=false | 10.244.1.238 ready=true serving=true term=false
01:20:19 10.244.2.24 ready=false serving=false term=false | 10.244.1.238 ready=true serving=true term=false
01:25:19 10.244.2.24 ready=false serving=false term=true | 10.244.1.238 ready=true serving=true term=false
01:25:36 10.244.2.24 ready=false serving=false term=true | 10.244.1.238 ready=true serving=true term=false | 10.244.3.138 ready=true serving=true term=false
01:26:33 10.244.1.238 ready=true serving=true term=false | 10.244.3.138 ready=true serving=true term=false
```

### Valkey `INCR` errors on `shbkz` (Valkey address shown as VK)
```
6164 01:19:30-01:25:27  dial tcp VK: connect: connection refused
 208 01:19:35-01:20:20  dial tcp VK: operation was canceled
  34 01:19:35-01:20:20  context canceled
```

### `shbkz` request duration buckets, increase over 3m to 01:22 (rounded)
```
0.025 2063  0.05 2068  0.1 2075  0.2 2093  0.3 2098  0.5 2107  1.0 2142  2.5 2226  5.0 2387  +Inf 2390
```

## Valkey INCR bound, local check

Checkout run locally against Postgres in Docker, `VALKEY_ADDR` set to an unroutable address (10.255.255.1:6379):

| Build | `POST /checkout` | `INCR` error |
|---|---|---|
| before (`sha-4c53c78` source) | 200 after 4.01s | `dial tcp 10.255.255.1:6379: connect: network is unreachable` |
| after (200ms bound) | 200 after 0.204 - 0.206s, 3 of 3 | `context deadline exceeded` |

## B3: lose worker2 with the INCR bound

| UTC | Observed |
|---|---|
| 02:01:19 | k6 starts, 20 req/s for 12m |
| 02:03:19 | `docker stop sre-platform-worker2` (returns 02:03:20) |
| 02:03:19 - 20 | 4x Envoy `remote connection failure` |
| 02:03:25 - 02:04:09 | 447x Envoy `503 connection timeout`, 192x storefront `502` after its 5s timeout |
| 02:04:04 | worker2 `Ready=Unknown` (45s). Its checkout leaves the endpoints; `s48dp` Ready throughout |
| 02:04:05 | Alertmanager startsAt for the page, receiver `slack` (46s after the stop) |
| 02:04:09 | Last k6 error |
| 02:09:04 | Taint eviction; new checkout Ready 02:09:12; Valkey replaced on `worker` |
| 02:09:05 | Last `INCR` failure |
| 02:10:20 | `docker start`; node Ready 02:10:22 |
| 02:13:20 | k6 ends |

- k6: 643 of 14360 failed (4.47%), 02:03:19 - 02:04:09 (50s).
- No checkout restarts, no checkout 500s.
- `INCR` failures on `s48dp`: none canceled by the caller (B2: 242). Errors were the 200ms bound, dial timeouts and, once the node was NotReady, `connection refused`.
- `s48dp`: no request over 300ms (B2: about 293). Latency SLI 5m 0 throughout; `CheckoutLatencyBudgetBurn` did not fire. The 1h window was still falling from B2 (0.0175 to 0.009).
- Availability SLI (Gateway), 5m window: 0.1314 at 02:04, peak 0.1544 at 02:05, 0 from 02:09.

### k6 non-200 responses by body
```
    4 23:03:19..23:03:20  503 upstream connect error or disconnect/reset before headers. reset reason: remote connection fail
  447 23:03:25..23:04:09  503 upstream connect error or disconnect/reset before headers. reset reason: connection timeout
   82 23:03:25..23:04:07  502 Post \"http://checkout/checkout\": context deadline exceeded (Client.Timeout exceeded while awa
  100 23:03:25..23:04:09  502 Post \"http://checkout/checkout\": net/http: request canceled (Client.Timeout exceeded while aw
   10 23:03:26..23:04:08  502 Post \"http://checkout/checkout\": context deadline exceeded\n
```

### checkout endpoints
```
02:01:20 10.244.2.69 ready=true serving=true term=false | 10.244.1.234 ready=true serving=true term=false
02:04:04 10.244.2.69 ready=false serving=false term=false | 10.244.1.234 ready=true serving=true term=false
02:09:04 10.244.2.69 ready=false serving=false term=true | 10.244.1.234 ready=true serving=true term=false
02:09:12 10.244.2.69 ready=false serving=false term=true | 10.244.1.234 ready=true serving=true term=false | 10.244.3.218 ready=true serving=true term=false
02:10:23 10.244.1.234 ready=true serving=true term=false | 10.244.3.218 ready=true serving=true term=false
```

### Valkey `INCR` errors on `s48dp` (Valkey address shown as VK)
```
6013 02:04:04-02:09:05  dial tcp VK: connect: connection refused
 159 02:03:20-02:04:04  context deadline exceeded
  49 02:03:22-02:04:02  dial tcp VK: i/o timeout
   3 02:03:20-02:03:20  EOF
   1 02:03:19-02:03:19  read tcp 10.244.1.234:57976->VK: read: connection reset by peer
```

### `s48dp` request duration buckets, increase over 3m to 02:06 (rounded)
```
0.025 2584  0.05 2584  0.1 2594  0.2 2608  0.3 2819  0.5 2819  1.0 2819  2.5 2819  5.0 2819  +Inf 2819
```
