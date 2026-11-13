# 11. Canary analysis: abort on the canary's own burn rate

Date: 2026-11-13
Status: Accepted

## Context

ADR 0010 made the checkout canary's traffic split real, but a bad release
still ran all the way to 100% unless someone aborted it. The SLO page (ADR
0007) can't do that job: it measures the whole service, so a fault that only
20% of requests can hit is diluted five times, and it needs minutes of
history before it fires.

## Decision

- **The pod-template hash is a metric label.** The `shop` PodMonitor copies
  the pod label `rollouts-pod-template-hash` onto every scraped series
  (`podTargetLabels`), so any query can be narrowed to one ReplicaSet.
- **`AnalysisTemplate checkout-canary-burn`** (`gitops/shop/checkout-analysis.yaml`)
  has two metrics, `availability-burn` and `latency-burn`. Each is the SLI
  from `slos/checkout.yaml` with a hash matcher added, over a 2m window,
  divided by the SLO's error budget (0.001 and 0.01). The result is a burn
  rate, measured every 30s.
- **A burn rate of 14.4x or more fails a measurement**, the same factor as the
  page alert. `failureLimit: 1`, so the second failure aborts.
- **It runs in the background for the whole canary**
  (`strategy.canary.analysis`, hash from `podTemplateHashValue: Latest`), from
  the first step to promotion.
- **An empty result passes.** `successCondition` is
  `len(result) == 0 || result[0] < 14.4`, and the total has the same `> 0`
  guard as the SLO queries, so a canary with no traffic gives an empty result
  and not NaN. The analysis starts before the canary has two scrapes, so the
  first measurements are always empty: two or three of them, 60 to 90
  seconds. Only `successCondition` is set, so a measurement either passes or
  fails and never comes back inconclusive.
- **The bad release is `ERROR_RATE` committed in the Rollout template**, and
  the abort is closed out with `git revert` (ADR 0010).

## Rejected

- **Empty result as inconclusive.** An inconclusive measurement pauses the
  rollout until someone promotes it by hand. Since the first measurements are
  always empty, every good release would stop there.
- **`result[0]` without the length check.** On an empty result the expression
  errors, and repeated measurement errors abort the rollout.
- **Delaying the analysis until the canary has data** (`startingStep` or
  `initialDelay`). Not needed once empty passes, and the whole canary is only
  4 minutes long.

## Evidence (2026-11-13, kind, Cilium 1.20.3, 20 req/s)

Both releases ran under one `make load DURATION=30m`. k6 counted 23 failed
requests of 36000 (0.06%): the 20 from the bad canary and the 3 503s after
the good promotion. Per-release counts are from the storefront and checkout
request counters.

- **Good release** (release 6, `f6826b8`, hash `cd9d98c4d`):

  | Time (UTC) | Event |
  |---|---|
  | 14:14:02 | canary and AnalysisRun start, weights 80/20 |
  | 14:16:12 | weights 50/50 |
  | 14:18:08 | AnalysisRun Successful, promoted |

  - Both metrics: 9 measurements, 0 failed. Three were empty, six were 0.
  - Measured canary share: 16.4% at the 20% step, 49.4% at the 50% step.
  - Three storefront 503s followed the promotion (see Consequences).
- **Bad release** (`ERROR_RATE=0.05`, `70bd81b`, hash `7ddc6d5847`):

  | Time (UTC) | Event |
  |---|---|
  | 14:21:17 | pushed |
  | 14:25:11 | canary and AnalysisRun start |
  | 14:25:21 | weights 80/20 |
  | about 14:26:11 | `availability-burn` 71.1x, first failure |
  | 14:26:41 | 57.9x, second failure, AnalysisRun Failed |
  | 14:26:43 | Rollout aborted, weights 100/0 |
  | 14:28:03 | revert `1b439de` pushed |
  | 14:30:14 | Rollout Healthy, app Synced/Healthy |

  - Aborted 90 seconds after the canary started, inside the 20% pause. It
    never reached 50%.
  - The canary pod served 357 requests and failed 20 (5.6%). Storefront
    returned those as 20 failed `POST /buy`, and none after the abort.
  - `latency-burn` stayed at 0.
  - The revert started no new canary: git matched the running stable
    ReplicaSet again.
- **The stable SLO page did not fire.** No `Checkout*` alert went pending.
  Right after the abort the service-wide availability burn was 3.59x over 5m
  and 0.59x over 1h; the page needs 14.4x on both.
- **Tooling:** the cluster was created with Homebrew's helm 3.17.3 and kubectl
  1.32.2, not the versions pinned in `mise.toml` (4.3.1 and 1.37.1). kind,
  the node image and the chart versions were the pinned ones.

## Consequences

- **A canary that gets no traffic promotes unanalysed.** Empty passes, so the
  analysis only protects a release that is serving requests. Storefront is
  checkout's only caller, and the proofs ran under load.
- **A bad release serves real requests for about 90 seconds.** The first 60
  to 90 seconds have no data, and the abort needs two failures 30s apart. At
  20 req/s and a 5% error rate that cost 20 failed requests.
- **`failureLimit` counts failures over the whole run, not in a row.** Two
  noisy measurements minutes apart abort a release. Not seen here: a good
  release measured 0 on both metrics throughout.
- **The budgets and the 14.4x factor are copied from `slos/checkout.yaml`.**
  Nothing checks the two against each other, so a changed objective has to be
  changed in both files.
- **An aborted Rollout stays Degraded until git is reverted.** Here that took
  2m11s from the push, most of it Argo CD's poll interval.
- **Unresolved: three storefront 503s after the good promotion.** All three
  came from one storefront pod within about 40 seconds of 14:18:08, and
  checkout logged no 5xx for them. That is the same shape as ADR 0010's
  promotion 503s, at a much smaller size (ADR 0010's release 5 had 0 of
  14400). Not investigated. One untested guess is the old ReplicaSet scaling
  down 30s after promotion.
