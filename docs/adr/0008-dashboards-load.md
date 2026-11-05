# 8. Error-budget dashboards, k6 load, collector spike test

Date: 2026-11-05
Status: Accepted

## Context

The SLO rules from ADR 0007 exist only as Prometheus series. Nobody looks at
those during an incident, and #12's burn proof needs something to screenshot.
The rules had also only been tested against injected errors, never against
plain healthy traffic. The roadmap names one failure mode for the telemetry
pipeline itself: the trace collector running out of memory under a traffic
spike.

## Decision

- **Dashboards are ConfigMaps in git.** A `configMapGenerator` in
  `gitops/monitoring/kustomization.yaml` builds one `dashboards` ConfigMap
  labelled `grafana_dashboard: "1"`, which Grafana's sidecar loads. It holds:
  - Sloth's published dashboards: grafana.com 14348 rev 5 (`SLO / Detail`)
    and 14643 rev 2 (`High level Sloth SLOs`). The only edit replaces the
    `${DS_PROMETHEUS}` import input with the chart's datasource uid
    `prometheus`, because sidecar-loaded dashboards are never imported and the
    input would stay unresolved.
  - A hand-written `Checkout overview`: SLI (5m), 30d error budget remaining,
    requests/s by code, 1h and 6h burn rate with lines at 6x and 14.4x, and
    p99 latency with exemplars that link to Tempo.
- **k6 runs on the host**, pinned in `mise.toml` (2.3.0). `load/checkout.js`
  has two modes:
  - `make load`: 20 req/s for 10m. It fails if more than 1% of requests error.
  - `make load-spike`: ramps to 1000 req/s, holds 3m, drops back. It has no
    error threshold, because it tests the collector, not the shop.

  It posts to `127.0.0.1:8080` with `Host: shop.localhost`, since `*.localhost`
  only resolves reliably in curl and browsers. Each request carries an email,
  so the redaction processor has real input.
- **Collector self-metrics.** The chart's own `serviceMonitor` and metrics port
  are enabled on `otel-gateway`, so `otelcol_*` series reach Prometheus.
- **Two SLO rule fixes** found by the baseline (spec in `slos/checkout.yaml`,
  each with a promtool case that fails against the old rules):
  - Availability `errorQuery` gets `or vector(0)`. On a fresh cluster no 5xx
    series exists yet, and `sum()` over nothing is empty, not 0, so every
    availability rule and its panels had no data.
  - Both `totalQuery`s get `> 0`. With no traffic the SLI is 0/0 = NaN, and
    Sloth's 30d SLI is `sum_over_time / count_over_time` of the 5m SLI, so a
    single idle NaN sample turned the 30d budget into NaN for 30 days. After
    about 12 idle hours only 35 of 685 samples were real numbers. Idle time
    now records no sample, and the 30d SLI averages only time with traffic.
- **Tail-sampling buffer raised from 5000 to 50000 traces** (see the spike
  results below).

## Rejected

- **Loading dashboards by `gnetId` from grafana.com at Grafana start.** It needs
  internet at boot, and what's shown would not be in the repo.
- **The k6 operator in the cluster.** One more controller, and the load
  generator would compete with the shop for the same Docker Desktop CPUs.
- **Lowering `memory_limiter` just to make it refuse.** That would prove
  behaviour of a config the platform doesn't run.
- **Pre-creating the `code="500"` series in Go.** That's the usual "initialize
  your counters" practice, but it needs an image rebuild, and the PromQL
  fallback protects any service the SLO spec points at.

## Consequences

- **Spike results.** Both spikes reached about 835 req/s with 0% failed
  requests:

  | | Spike 1 (`num_traces: 5000`) | Spike 2 (`num_traces: 50000`) |
  |---|---|---|
  | Collector restarts / OOM | 0 | 0 |
  | Peak working set / heap | 181 / 110 MiB | 279 / 213 MiB (limit 384Mi) |
  | Spans refused by `memory_limiter` | 0 | 0 |
  | Traces evicted before a decision | 224k of 250k (90%) | 0 |
  | Traces kept | 1.2k | 23.7k of 231.8k (10.2%) |

  At 835 req/s and a 10s `decision_wait`, the buffer needs about 8,400 traces.
  With 5000, the oldest traces were evicted before any policy ran. The error
  and slow-trace policies never saw them, and the only signal was
  `otelcol_processor_tail_sampling_sampling_trace_dropped_too_early`. An error
  during a spike would most likely have had no trace.
- **`memory_limiter` refusing data is still unproven.** Its soft limit is
  240 MiB (`limit_mib` 300 minus `spike_limit_mib` 60), and the heap peaked at
  213 MiB. About 835 req/s is this setup's ceiling: k6 ran out of VUs (719
  dropped iterations), so more pressure needs a bigger load generator, not a
  setting change.
- **The 50k buffer stays full.** The `traces_on_memory` gauge sits at 50000
  after the spike, because decided traces stay in the buffer until evicted. So
  roughly 200 MiB of heap is probably the steady state once enough traffic has
  passed, not a spike peak. That is close to the 240 MiB soft limit.
- **Two memory limits sit close together.** The chart's `useGOMEMLIMIT` sets
  `GOMEMLIMIT=307MiB` (80% of 384Mi), while `memory_limiter` uses 300 MiB.
  Neither was reached, so which one acts first is untested.
- **Part of ADR 0007 changed.** With `> 0`, an idle service stops producing SLI
  samples, so its alerts resolve once traffic leaves the longest alert window,
  instead of firing on with the last ratio.
- **The live cluster keeps old NaN samples for 30 days.** Only a fresh cluster
  shows a real 30d budget. Each session starts with `make up`, so #12 does.
- **Not done:** no alert on `dropped_too_early`; no promtool case for the
  latency SLO; dashboard panels were checked through the Grafana API, not
  looked at (#12 takes the screenshots). `kubectl top` doesn't work, because
  there is no metrics-server; Prometheus has the same data.
- **Verified on the live cluster:**
  - All 11 Argo apps Synced and Healthy. Grafana lists the 3 dashboards.
    Prometheus has the `otel-gateway` target up, with 34 of 34 targets up.
  - Baseline: 12,001 requests over 10m, 0 failed, p95 3.87 ms. Latency SLI 1,
    burn rate 0, checkout p99 about 25 ms, no SLO alerts. After the first fix,
    the availability SLI read 0 over 1d and 3d.
