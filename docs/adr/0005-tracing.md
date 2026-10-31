# 5. Tracing: OpenTelemetry SDK, a tail-sampling gateway collector and Tempo

Date: 2026-10-31
Status: Accepted

## Context

Metrics (ADR 0004) say a request was slow or failed. Traces say where. Stage 3
canaries and chaos runs need to tie a burn-rate spike to a failing hop, and the
roadmap asks for a collector pipeline that shows sampling and PII scrubbing.

## Decision

- **OpenTelemetry SDK in both services.** `otelhttp` on the servers and on the
  storefront's HTTP client, so `traceparent` propagates. `otelpgx` and
  `valkeyotel` add the Postgres and Valkey spans. `/healthz` is not wrapped, so
  probes produce no traces. Spans are exported over OTLP/gRPC to
  `OTEL_EXPORTER_OTLP_ENDPOINT`. The SDK records every span; sampling is the
  collector's job.
- **One gateway collector** (contrib distribution, one replica, chart 0.178.1)
  in `monitoring`. One replica is deliberate: tail sampling needs every span of
  a trace in the same process. Pipeline, in order:
  1. `memory_limiter` (300 MiB hard, 60 MiB spike, under a 384Mi pod limit).
     When memory is tight it refuses data and senders retry, instead of the pod
     being OOM-killed.
  2. `redaction` hashes `user.email` and keeps every other attribute.
  3. `tail_sampling` waits 10s per trace, then keeps all errors, all traces
     slower than 300ms (the latency SLO threshold) and 10% of the rest.
  4. `batch`, then OTLP to Tempo.
- **Redaction runs before sampling**, so a raw email is never buffered by the
  sampler.
- **Tempo** single binary, 24h retention, no persistence (an emptyDir).
- **Exemplars**: the request-duration histogram carries a `trace_id` exemplar,
  `/metrics` serves OpenMetrics, Prometheus has exemplar storage on, and the
  Grafana Prometheus datasource links `trace_id` to the Tempo datasource.
- **PII demo path**: `POST /buy` accepts an optional `{"email": ...}`, which
  checkout records as the span attribute `user.email`. Nothing else in the repo
  handles personal data; this exists so the redaction has something to scrub.

## Rejected

- **Head sampling in the SDK.** It decides before the outcome is known, so it
  drops the slow and failed requests that matter most.
- **A collector DaemonSet with tail sampling.** Spans of one trace would land on
  different nodes and be sampled separately. The DaemonSet in the logs issue
  (#9) is for log collection only.
- **`tempo-distributed`.** Far heavier than a single-node demo needs.

## Consequences

- **Hash choice.** The redaction processor accepts `sha1`, `sha3`, `md5`,
  `hmac-sha256` and `hmac-sha512`. `sha256` is rejected, and the collector only
  finds that out at startup, so `helm template` can't catch it; the pod
  crash-looped until the value was changed. We use `sha3`, which is unsalted.
  An unsalted hash of a low-entropy value such as an email can be reversed with
  a dictionary. For real PII use `hmac-sha256` with a key kept in a secret.
- **Exemplars can point at dropped traces.** Only about 10% of successful
  requests are kept, so most exemplar links on healthy traffic lead to a trace
  that no longer exists. Errors and slow requests are always kept, so exemplars
  on the buckets that matter for an incident resolve.
- **Spans are lost at SIGTERM.** There is no shutdown hook, so spans still in the
  SDK's batch buffer when a pod terminates are dropped.
- **Deprecated chart.** `grafana/tempo` 1.24.4 prints "This chart is deprecated"
  but works. Revisit if it stops receiving Tempo updates.
- **502s around rollouts.** Six `502`s from storefront appeared around the
  checkout rollout in the error run, the same pattern ADR 0003 recorded. Not
  investigated yet.
- **Verified on the live cluster** (200 plain requests, 40 with an email, then
  100 with `ERROR_RATE=0.5`):
  - A trace shows storefront `POST /buy` → `HTTP POST` → checkout
    `POST /checkout` → `pool.acquire`, `INSERT`, `INCR`.
  - 200 successful requests left 20 traces in Tempo, the 10% baseline.
  - 56 failed requests (50 × 500, 6 × 502) left 56 error traces, so every error
    was kept. 4 of 44 successes were kept.
  - The raw email address appears 0 times in a stored trace.
  - Prometheus holds `trace_id` exemplars, and the Prometheus datasource links
    them to Tempo (checked through the APIs, not clicked in the UI).
