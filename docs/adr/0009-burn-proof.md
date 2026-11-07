# 9. Burn proof: real availability and latency burns, and what a laptop lab can't show

Date: 2026-11-06
Status: Accepted

## Context

ADR 0007's alerts had only been tested with promtool and one injected burn.
Stage 2 ends by proving them end to end on a fresh cluster: a real burn, a
page in Slack, the signals in Grafana that explain it, then a revert and a
resolve. The evidence has to be in the repo, not in a terminal scrollback.

## Decision

- **Two burns on checkout, both through git** (ADR 0003: Argo CD reverts
  `kubectl set env`), each under steady k6 load at 20 req/s after a clean
  baseline:

  | | Availability | Latency |
  |---|---|---|
  | Fault committed | `ERROR_RATE=0.05` | `LATENCY_MS=400` |
  | Live in checkout | 15:42:26 | 22:58:26 |
  | Ticket fired | 15:43:48 (+1m22s) | 22:59:58 (+1m32s) |
  | Page fired | 15:44:48 (+2m22s) | 23:00:28 (+2m02s) |
  | Reverted | 15:51:32 | 23:08:16 |
  | Page resolved | about 16:15 (+24m) | 23:37:10 (+29m) |

  The page resolves once the faulty minutes leave its 30m short window, so
  about 25 minutes is expected; the 1h/5m pair clears within minutes.
- **Evidence is screenshots in `docs/evidence/`**, taken by hand in Grafana and
  Slack, scaled to 1600px wide (`sips -Z 1600`) to keep the repo small. Per
  burn: checkout overview, Sloth SLO detail, firing alert rules, a trace
  (a 500 for availability, a 401 ms request for latency) and the Slack
  messages.
- **Grafana's memory limit goes from 512Mi to 768Mi.** It was OOM-killed at
  15:46:35 while the Sloth detail dashboard rendered 6 hours of data. ADR 0004
  had already raised it once, from 256Mi.
- **Long runs on the laptop use `caffeinate -i`.** The load test is wrapped in
  it, so macOS doesn't idle-sleep while k6 runs. It doesn't stop sleep from
  closing the lid: the latency run still had one request hang for 27 minutes,
  after both burns had finished.

## Rejected

- **Waiting for a full hour of clean history before burning.** The issue
  predicted the page about 17 minutes in, assuming a full 1h window of clean
  traffic. Reproducing that would add an hour per burn for the same proof.
- **Grafana's image renderer for the screenshots.** One more component, and it
  can't capture Slack anyway.

## Consequences

- **On a young cluster the 6h page rule fires first.** When the availability
  page fired, the 1h error ratio was 0.64%, under the 1.44% (14.4x) threshold.
  The page came from the other pair, 6h and 30m above 0.6% (6x): with about
  18 minutes of history, the 6h window holds the same data as the 1h window,
  so its lower threshold trips first. The dashboards show the 1h and 6h burn
  lines on top of each other for the same reason. The latency burn was strong
  enough (a 5m burn rate of 100x) to cross 14.4x on 1h as well.
- **"Budget remaining" is meaningless on a cluster that lives for hours.** The
  30d SLI averages only the minutes the cluster has existed, so a 10-minute
  burn shows up as a 30-day budget of -122% to -536%. Sloth's detail dashboard
  even disagrees with itself during the latency burn: "Remaining error budget
  (month)" 99.3% next to "(30d window)" -363%. Read burn rates and alerts; treat
  the budget numbers as indicative only.
- **Laptop sleep pauses everything, including notifications.** The Mac slept
  after the availability revert. Prometheus has no data for most of 16:15 to
  22:34, apart from a few minutes awake each hour. The page had already stopped
  firing at 16:15, but Slack only received `[RESOLVED]` at 22:36, when Docker
  Desktop's VM woke up. A k6 run that was frozen through the sleep had to be
  restarted for the latency burn.
- **Unresolved:** one stat panel on Sloth's detail dashboard shows "No data";
  its query was not investigated.
- **Done-when for #12** is met by these two burns: the page fired from real
  burns and resolved after each revert, with the evidence in `docs/evidence/`.
