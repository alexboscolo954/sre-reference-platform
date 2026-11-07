# Architecture

Everything runs on one kind cluster on a laptop (ADR 0001), deployed by Argo CD
from this repo (ADR 0002). This page covers the request path and the telemetry
built on it in Stage 2; the decisions behind each part are in `docs/adr/`.

## Request path

```mermaid
flowchart LR
  user["k6 / curl"] -->|"shop.localhost:8080"| gw["Cilium Gateway"]
  gw --> sf["storefront"]
  sf -->|"POST /checkout"| co["checkout"]
  co --> pg[("CloudNativePG x3")]
  co --> vk[("Valkey")]
```

`checkout` has two fault flags, `ERROR_RATE` and `LATENCY_MS`. They are changed
by committing to `gitops/shop/checkout.yaml`, because Argo CD reverts
`kubectl set env` (ADR 0003).

## Telemetry

```mermaid
flowchart LR
  subgraph shop
    sf["storefront"]
    co["checkout"]
  end
  subgraph monitoring
    agent["OTel agent<br/>(DaemonSet)"]
    gwc["OTel gateway<br/>memory_limiter, redaction,<br/>tail_sampling"]
    tempo[("Tempo")]
    loki[("Loki")]
    prom[("Prometheus<br/>+ Sloth rules")]
    am["Alertmanager"]
    graf["Grafana"]
  end
  slack["Slack"]

  sf & co -->|"OTLP traces"| gwc --> tempo
  sf & co -->|"JSON logs on stdout"| agent -->|"OTLP"| loki
  prom -->|"scrape /metrics<br/>(RED + exemplars)"| sf & co
  prom -->|"scrape"| gwc
  prom -->|"page / ticket"| am --> slack
  graf --- prom & tempo & loki
```

| Signal | Path | ADR |
|---|---|---|
| Metrics | `metrics.Wrap` exposes RED metrics on `:9090`; a PodMonitor scrapes them, plus CNPG and the gateway collector's own metrics | 0004, 0008 |
| Traces | OTel SDK → gateway collector (hashes `user.email`, keeps errors, slow and 10% of the rest) → Tempo | 0005, 0008 |
| Logs | `slog` JSON access logs with `trace_id` → agent tails `/var/log/pods` → Loki | 0006 |
| SLOs | `slos/checkout.yaml` → `make slo` → committed PrometheusRule → Alertmanager → Slack | 0007, 0008 |
| Dashboards | ConfigMaps in `gitops/monitoring/dashboards/`, loaded by Grafana's sidecar | 0008 |

Grafana links the three signals: an exemplar dot on a latency panel opens its
trace, a trace opens the logs with the same `trace_id`, and a log line opens
its trace.

## What a burn looks like

ADR 0009 records two real burns on checkout (5% errors, then 400 ms latency),
with screenshots in [`docs/evidence/`](docs/evidence/).
