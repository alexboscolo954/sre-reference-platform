# 4. Metrics: kube-prometheus-stack and RED metrics on the shop services

Date: 2026-10-29
Status: Accepted

## Context

Stage 2 puts SLOs on checkout. The SLIs come from request metrics, so the
services have to expose them and Prometheus has to scrape them. Injected faults
(`LATENCY_MS`, `ERROR_RATE`) must show up in those metrics, while probes and
scrapes must not.

## Decision

- **kube-prometheus-stack** 92.14.1 in `monitoring`, installed by an Argo
  Application with values in `gitops/observability/kps-values.yaml`. 2d
  retention, exemplar storage on. Etcd, scheduler, controller-manager and
  kube-proxy targets are off: kind doesn't expose them and Cilium replaces
  kube-proxy. Prometheus selects every PodMonitor and PrometheusRule, not only
  those labelled with the Helm release.
- **RED metrics** in `apps/internal/metrics`: `http_server_requests_total{route,method,code}`
  and `http_server_request_duration_seconds{route,method}`. The histogram has a
  0.3s bucket, the checkout latency SLO threshold.
- **`/metrics` on its own port (9090)**, so scrapes never pass through the
  business handlers.
- **Wrapping order**: `metrics.Wrap(fault.Middleware(handler))` on business routes
  only. Instrumentation is outside the fault middleware, so injected 500s and
  latency are counted. `/healthz` is not wrapped at all, so probes neither fail
  under fault injection nor inflate the request counts. This replaces the old
  `fault.Middleware(mux)`, which also delayed and failed `/healthz`.
- **One PodMonitor** (`gitops/shop/podmonitor.yaml`) selects both services by
  `app` label. CNPG's own PodMonitor comes from `monitoring.enablePodMonitor`.
- **Grafana** is exposed on the shared Gateway at `grafana.localhost:8080`. The
  admin password is a random value stored with SOPS in `gitops/monitoring/`.

## Rejected

- **Two ServiceMonitors**: they need labels and named ports on the Services.
  A PodMonitor needs only a named container port.
- **Instrumenting `/healthz`**: probe traffic would dominate the counts at low
  request rates and skew the SLI.

## Consequences

- Grafana needs a 512Mi limit. At 256Mi it was OOMKilled on startup.
- An Argo auto-sync that keeps retrying on a Deployment that never becomes Ready
  ignores new commits. Removing `/operation` from the Application
  (`kubectl patch application <name> -n argocd --type json -p '[{"op":"remove","path":"/operation"}]'`)
  starts a fresh sync.
- Testing fault flags locally with `kubectl set env` means pausing auto-sync on
  both `shop` and `root`. Otherwise `root` self-heals the `shop` Application's
  sync policy back on, and `shop` then reverts the env change. Committing the
  flag through git avoids this and is what the burn-rate proof will do.
- Verified on the live cluster: 7 targets up (2 storefront, 2 checkout, 3
  `shop-db`), and `ERROR_RATE=0.5` gave 49 of 100 requests with code 500 while
  `/healthz` stayed 200.
