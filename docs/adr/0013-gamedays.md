# 13. GameDays: hypotheses first, observed evidence only, fixes proven by re-runs

Date: 2026-11-20
Status: Accepted

## Context

ADR 0012 injected one fault into a canary that existed to be aborted. #22 is
the first GameDay: faults against the running platform (the CNPG primary, then
a whole worker), with the outcome unknown beforehand. It needs a method that
can be repeated, and a postmortem that can't drift towards what was expected.
[GameDay 0001](../gamedays/0001-cnpg-primary-and-worker-loss.md) is the worked
example.

## Decision

- **Hypotheses are committed before the first run and never edited.** The
  postmortem keeps them verbatim and grades each one against a recording:
  Held, Wrong, Wrong mechanism, Wrong detail or Not exercised. In 0001, 6 of 11
  were wrong or not exercised.
- **Placement is part of the scenario.** It is recorded before each run, since
  what a node loss breaks depends on what runs there. Run B was aimed at
  worker2 because it held Valkey, not at the node of the primary as planned.
- **One fault per run, under `make load`, with a timeline file.** A fault is a
  manifest in `chaos/` (a one-shot PodChaos, deleted before it is re-applied)
  or a command (`docker stop` of a kind node: Chaos Mesh has no fault that
  stops a node). The time of every action goes into the timeline file.
- **Recording is `hack/recorder.sh` plus k6, read afterwards.** The recorder
  writes pods, endpoints, CNPG status and node status once per second; k6
  logs the body of every non-200 response. Prometheus, Alertmanager
  (`/api/v2/alerts`), events and previous container logs are read after the
  run. Alerts count only from `ALERTS` and Alertmanager, not from a dashboard.
- **Evidence is text, committed per run in `docs/evidence/`.** Timelines,
  numbers, and excerpts copied unchanged from the recordings. Raw recordings
  stay out of the repo: Envoy and Hubble logs reached 18 MB each per run.
- **Fixes only for failures that happened**, each tied to a numbered
  contributing factor and to the re-run that checks it. A fix is done when the
  same scenario, with the same placement, has been re-run and the result is in
  the postmortem. 0001 took five runs: A and B, then A2, B2 and B3.
- **The template is [`docs/gamedays/template.md`](../gamedays/template.md).**

### Changes to earlier decisions from GameDay 0001

- **Checkout availability is measured at the Gateway** (amends ADR 0007).
  The SLI reads Envoy's `upstream_rq_completed` and `upstream_rq_xx` for the
  storefront route, scraped by the `cilium-envoy` PodMonitor. Measured in
  checkout, it missed every request Envoy rejected and every count lost with
  a killed container; neither outage paged. The latency SLO stays on checkout.
- **Liveness is process-only** (`/livez`); readiness checks Postgres. A
  dependency outage no longer restarts checkout.
- **Valkey is optional instead of replicated** (replaces the "replication
  in Stage 3" note in ADR 0003). A failed `INCR`, bounded to 200ms, is logged
  and the order still succeeds; `orders_total` is a cache that any Valkey
  restart already resets.

## Rejected

- **Screenshots as evidence** (ADR 0009). Nothing reached the alerting path in
  the first runs, so there was nothing to capture, and text can be searched
  and diffed.
- **Editing the hypotheses after a run**, even to fix a wrong mechanism. The
  wrong ones are the finding.
- **Fixes the runs did not call for.** PDBs don't apply to a pod kill or a
  stopped node, and storefront retries would not cover a 22s failover or a
  6-minute Valkey outage. Both are left until a run shows they would help.
- **Replicating Valkey.** Making it optional removed the failure for less: no
  Sentinel and no client failover.

## Consequences

- **The first result of a GameDay can be about the measurement.** The worst
  finding of 0001 was not the outage but that two outages (32s and 6m19s)
  paged nobody.
- **Re-runs cost about 15 minutes each, plus setup.** Recreating placement
  needs care: cordoning the primary's node makes CNPG switch it away within
  seconds. A switchover is a `status.targetPrimary` patch (the `cnpg` kubectl
  plugin is not installed).
- **Times need a note.** The cluster logs in UTC; k6 prints local time.
- **Done-when for #22** is met: both scenarios ran, the postmortem is written
  from recordings, and every fix was re-run except
  the start-up retry: no checkout restarted in A2, so it was only checked
  locally.
