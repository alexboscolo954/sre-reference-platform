# GameDay NNNN: <fault>

Status: hypotheses written YYYY-MM-DD, before any run, and left unchanged. Everything from "Observations" on was written afterwards, from [`docs/evidence/gamedayN-*.md`](../evidence/).

<!-- Write and commit everything down to "What gets recorded" before the first run. -->

## Steady state

- Load: what runs, at what rate, for how long.
- What a request touches, and which failure of each dependency fails it.
- Probes, timeouts, retries on the path.
- SLOs and the alerts expected to fire.
- Placement: which pods run on which node, including Prometheus and Alertmanager.

## Scenario A: <fault>

Method: the manifest or command, and when it is applied under load.

Hypothesis (numbered, each one checkable from a recording):

1. ...

## What gets recorded

- k6 summary and the bodies of non-200 responses.
- `hack/recorder.sh`, once per second.
- A timeline file with the time of every action (load start, fault, recovery action, load end).
- After the run: Prometheus (`ALERTS`, SLI recording rules), Alertmanager `/api/v2/alerts`, events, previous container logs.

Abort: if the system has not recovered N minutes after the fault ends, stop load and record the state as it is.

## Observations

### Impact

| Run | Fault | Failed requests (k6) | Error window | Alerts |
|---|---|---|---|---|

### Scenario A against the hypothesis

| # | Hypothesis | Result | What was observed |
|---|---|---|---|

Result is one of: Held, Wrong, Wrong mechanism, Wrong detail, Not exercised.

## Contributing factors

Only failures that happened in the runs. Numbered, so fixes can point at them.

## Fixes and re-runs

| # | Fix | Factor | Check | Result |
|---|---|---|---|---|

Each fix names the re-run that checks it. Re-runs recreate the original placement; how is recorded in the evidence.

### What is left

## Not explained
