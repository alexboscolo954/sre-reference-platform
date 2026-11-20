# GameDay 1, run B: worker node loss (evidence)

Recorded 2026-11-19 on the local kind cluster, all times UTC. `docker stop sre-platform-worker2` under load, `docker start` 7 minutes later. Raw recordings (k6 log, per-second recorder output, Hubble flows) were not kept; the excerpts below are copied from them unchanged.

Before the stop, worker2 ran valkey (its only replica), checkout `xdbhl`, storefront `lf9vg` and `shop-db-1` (replica). The primary `shop-db-2` was on `sre-platform-worker`, Prometheus on `worker3`, Alertmanager on `worker`.

## Timeline

| UTC | Observed |
|---|---|
| 22:35:30 | k6 starts, 20 req/s for 12m |
| 22:37:30 | `docker stop sre-platform-worker2` (returns 22:37:32) |
| 22:37:30-31 | 7 errors: connection termination, remote connection failure, Valkey connection refused |
| 22:37:36 - 22:38:31 | Timeouts: 524x Envoy `503 ... connection timeout`, 439x storefront `502` after its 5s client timeout, 78x checkout `500 dial tcp ...:6379: operation was canceled` (until 22:37:55) |
| 22:37:52 | The surviving checkout `flgwt` (worker3) goes unready: `/healthz` times out on Valkey. The endpoints still list the dead `xdbhl` as ready |
| 22:38:00 | `flgwt` killed by liveness. It then exits at every start with `dial tcp 10.96.19.172:6379: connect: connection refused` and backs off |
| 22:38:25 | worker2 `Ready=Unknown` (55s after the stop), `unreachable` taint. All worker2 pods unready; no ready checkout endpoint |
| 22:38:26 - 22:43:49 | 6459x `503 no healthy upstream`; nearly every request fails |
| 22:43:26 | Taint eviction (301s after 22:38:25). Replacements created: valkey on worker3, checkout and storefront on worker |
| 22:43:27 | CNPG ready instances 3 -> 2. It counted `shop-db-1` ready until here |
| 22:43:32 | New valkey Ready |
| 22:43:48 | `flgwt` Ready; new checkout `khnxt` Ready 22:43:50 |
| 22:43:49 | Last k6 error |
| 22:44:32 | `docker start sre-platform-worker2`; node Ready 22:44:33, taints cleared 22:44:46 |
| 22:44:42 | Old worker2 pods removed. `shop-db-1` recreated on worker2, Ready 22:44:57; CNPG 3/3 at 22:44:58 |
| 22:47:32 | k6 ends |

## Numbers

- k6: 7507 of 14341 requests failed (52.34%). Errors from 22:37:30 to 22:43:49 (6m19s). The outage ended through eviction and rescheduling, 43s before the node came back.
- `flgwt` restarted 6 times during the run: 1 liveness kill, then a crash loop, because checkout exits at start when Valkey is unreachable.
- CNPG: no failover, the primary was not on worker2. No replacement for `shop-db-1` was created while the node was down.
- Checkout availability SLI (5m): 0 until 22:42, no data 22:43 - 22:44. Checkout recorded no requests between 22:39 and 22:44, since Envoy rejected them first. Prometheus holds one checkout 500 sample for this run: `flgwt` = 92 at 22:37:54, 6s before liveness killed it.
- Surviving storefront `tmrzx`, read once per second by the recorder, from 22:35:31 to 22:48:30: 500 +79, 502 +439, 503 +6466. The 503s match k6's 6465 (`no healthy upstream` and the 6 early resets) within one. k6 failed 7507, so about 523 requests were never counted by a storefront: the 524 `connection timeout` 503s on the Gateway-to-storefront hop, toward the dead `lf9vg`.
- Checkout latency SLI: 5m window peaked at 0.55 (22:42), 1h window at 0.007, below the 14.4x threshold of 0.0144. No `Checkout*` alert was pending or firing.
- After the run, Valkey `orders_total` = 4438 and Postgres `orders` = 18284 rows. The counter started again from zero on the new Valkey pod (it runs with `--save "" --appendonly no`).

## Excerpts

### worker2 node status (recorder)
```
22:35:31 sre-platform-worker2 ready=True 
22:38:25 sre-platform-worker2 ready=Unknown 
22:38:25 sre-platform-worker2 ready=Unknown node.kubernetes.io/unreachable
22:38:25 sre-platform-worker2 ready=Unknown node.kubernetes.io/unreachable,node.cilium.io/agent-not-ready
22:38:26 sre-platform-worker2 ready=Unknown node.kubernetes.io/unreachable,node.cilium.io/agent-not-ready,node.kubernetes.io/unreachable
22:44:33 sre-platform-worker2 ready=True node.kubernetes.io/unreachable,node.cilium.io/agent-not-ready,node.kubernetes.io/unreachable
22:44:33 sre-platform-worker2 ready=True node.cilium.io/agent-not-ready,node.kubernetes.io/unreachable
22:44:36 sre-platform-worker2 ready=True node.cilium.io/agent-not-ready
22:44:46 sre-platform-worker2 ready=True 
```

### checkout endpoints
```
22:35:31 10.244.3.97 ready=true serving=true term=false | 10.244.2.84 ready=true serving=true term=false
22:37:52 10.244.3.97 ready=false serving=false term=false | 10.244.2.84 ready=true serving=true term=false
22:38:25 10.244.3.97 ready=false serving=false term=false | 10.244.2.84 ready=false serving=false term=false
22:43:26 10.244.3.97 ready=false serving=false term=false | 10.244.2.84 ready=false serving=false term=true
22:43:48 10.244.3.97 ready=true serving=true term=false | 10.244.2.84 ready=false serving=false term=true
22:43:49 10.244.3.97 ready=true serving=true term=false | 10.244.2.84 ready=false serving=false term=true | 10.244.1.129 ready=false serving=false term=false
22:43:50 10.244.3.97 ready=true serving=true term=false | 10.244.2.84 ready=false serving=false term=true | 10.244.1.129 ready=true serving=true term=false
22:44:34 10.244.3.97 ready=true serving=true term=false | 10.244.1.129 ready=true serving=true term=false
```

### Valkey pods
```
22:35:31 valkey-84dc8b6984-qbq2z ready=True phase=Running deleting=false node=sre-platform-worker2
22:38:25 valkey-84dc8b6984-qbq2z ready=False phase=Running deleting=false node=sre-platform-worker2
22:43:26 valkey-84dc8b6984-qbq2z ready=False phase=Running deleting=true node=sre-platform-worker2
22:43:26 valkey-84dc8b6984-9b6sf ready=null phase=Pending deleting=false node=null
22:43:26 valkey-84dc8b6984-9b6sf ready=null phase=Pending deleting=false node=sre-platform-worker3
22:43:26 valkey-84dc8b6984-9b6sf ready=False phase=Pending deleting=false node=sre-platform-worker3
22:43:31 valkey-84dc8b6984-9b6sf ready=False phase=Running deleting=false node=sre-platform-worker3
22:43:32 valkey-84dc8b6984-9b6sf ready=True phase=Running deleting=false node=sre-platform-worker3
22:44:42 valkey-84dc8b6984-qbq2z ready=False phase=Failed deleting=true node=sre-platform-worker2
```

### k6 non-200 responses by body (local time is UTC-3)
```
    1  connection termination                           T19:37:30 T19:37:30 
    5  remote connection failure                        T19:37:31 T19:37:31 
    1  6379: connect: connection refused                T19:37:30 T19:37:30 
  524  connection timeout                               T19:37:36 T19:38:30 
  420  Client.Timeout exceeded while awaiting headers   T19:37:36 T19:38:31 
   19  context deadline exceeded\\n                     T19:37:49 T19:38:30 
   78  6379: operation was canceled                     T19:37:36 T19:37:55 
 6459  no healthy upstream                              T19:38:26 T19:43:49 
```

### Surviving checkout pod flgwt: probe events and last crashed start

Event counts (`xN`) are cumulative since the pod started at 22:03, so they include bootstrap and run A.

```
2026-11-19T22:43:48Z x14 Unhealthy Readiness probe failed: Get "http://10.244.3.97:8080/healthz": dial tcp 10.244.3.97:8080: connect: connection 
2026-11-19T22:38:00Z x2 Killing Container checkout failed liveness probe, will be restarted
2026-11-19T22:37:53Z x3 Unhealthy Readiness probe failed: Get "http://10.244.3.97:8080/healthz": context deadline exceeded (Client.Timeout excee
2026-11-19T22:38:00Z x3 Unhealthy Liveness probe failed: Get "http://10.244.3.97:8080/healthz": context deadline exceeded (Client.Timeout exceed
2026-11-19T22:38:00Z x1 Unhealthy Readiness probe failed: Get "http://10.244.3.97:8080/healthz": EOF
2026-11-19T22:40:53.548164459Z dial tcp 10.96.19.172:6379: connect: connection refused
```

### Checkout series in Prometheus, 1m steps (missing steps = no samples)
```
request rate by code:
200 22:36=13.167 22:37=20.001 22:38=2.267 22:45=19.9 22:46=19.968 22:47=19.967 22:48=7.834
availability SLI:
5m 22:36=0 22:37=0 22:38=0 22:39=0 22:40=0 22:41=0 22:42=0 22:45=0 22:46=0 22:47=0 22:48=0
latency SLI:
5m 22:36=0 22:37=0 22:38=0.037 22:39=0.037 22:40=0.037 22:41=0.063 22:42=0.55 22:45=0 22:46=0 22:47=0 22:48=0
1h 22:36=0 22:37=0 22:38=0.007 22:39=0.007 22:40=0.007 22:41=0.007 22:42=0.007 22:43=0.007 22:44=0.007 22:45=0.006 22:46=0.006 22:47=0.005 22:48=0.005
```
