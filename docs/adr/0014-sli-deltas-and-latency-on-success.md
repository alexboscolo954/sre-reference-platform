# 14. SLIs: counters as window deltas, latency on successful requests

Date: 2026-11-20
Status: Accepted

## Context

[GameDay 0002](../gamedays/0002-valkey-kill-and-checkout-db-partition.md)
found two faults in the checkout SLIs, both in the alerting rather than in the
shop:

- **A page with no failed request.** The availability SLI that ADR 0013 moved
  to the Gateway is `completed - xx{2,3,4}` over `rate()`. Envoy creates
  `upstream_rq_completed` at 0 when it starts, but each `upstream_rq_xx` class
  series only at its first response. On an idle Envoy, `rate()` counts the
  step from 0 in `completed` and cannot see it in class 2, so the first scrape
  interval of successes reads as errors. The first load on a fresh cluster
  paged at once (SLI 1.0), and the ticket stayed active for over an hour.
- **A database outage burned the latency budget.** The latency SLI (ADR 0007)
  is all requests minus those under 300ms, so a request that fails slowly is
  also slow. With checkout cut off from Postgres, the latency 5m SLI reached
  0.143 (page at 0.144). Bounding the `INSERT` to 1s made it worse, 0.1785,
  because more requests got through to fail.

## Decision

- **Availability counters are deltas over the window** (amends ADR 0013's
  Gateway SLI). Each counter is `x - (x offset {{.window}} or x * 0)`, clamped
  at 0. A series with no sample a window ago is counted from 0, so a first 2xx
  and a first 5xx are both counted once. The query stays `completed` minus
  2xx-4xx, so Envoy's own 503s count.
- **Latency counts only successful requests** (amends ADR 0007 and the
  canary's `latency-burn` in ADR 0011). The duration histogram has a `code`
  label, the SLI and the analysis query select `code=~"2.."`, and the 200
  series is created at zero on start. Failed requests are availability's.
- **Checkout's `INSERT` is bounded to 1s.** A lost database answers 500 within
  a second rather than at storefront's 5s timeout, which held all 100 k6 VUs.
- **Both are tested.** A promtool case replays run A's start-up (idle Envoy,
  then traffic) and fails on the old query; a Go test checks that a 500 and a
  200 land in separate histogram series.

## Rejected

- **A warm-up request after `make up`.** It hides the fault on a fresh cluster
  only; any Envoy idle long enough to be scraped at 0 would page again.
- **Counting every class from `upstream_rq_xx`.** Symmetric, but a first 5xx
  disappears; GameDay 0001 lost outages that way.
- **Created timestamps** (Prometheus zero injection). They need the target to
  expose them; the metrics `cilium-envoy` serves here carry none.
- **An `INSERT` bound under 300ms**, so that failures are fast enough not to
  count as slow. A slow database would then fail orders instead of slowing
  them, and the latency SLI would still mix the two.

## Consequences

- **The SLI no longer extrapolates.** On run B's data it read 0.398 at the
  peak against 0.443 with `rate()`, nearer to k6's count. The first non-zero
  point came one evaluation earlier.
- **An Envoy restart drops that pod's counts** until the window passes it: the
  counter resets and the clamp turns the negative delta into 0. Failures
  during that time are missed, not doubled.
- **A series with no sample a window ago counts from 0, even when it is old.**
  If Prometheus's history starts after Envoy's (an emptied TSDB, a target
  added late), the SLI reads lifetime totals until the window fills. Not seen
  in a run; a promtool probe with lifetime counters at 5% errors paged at 2m.
- **A request that is slow and then fails counts once, as an error.** A
  latency regression that ends in timeouts shows up only in availability, and
  a canary whose errors are slow aborts on `availability-burn`, not
  `latency-burn`.
- **Re-runs:** A2 and B3, each the first load on a fresh cluster, raised no
  alert before the fault; in B3 the latency SLI stayed at 0 while
  availability paged 53s after the fault ([evidence](../evidence/gameday2-reruns.md)).
