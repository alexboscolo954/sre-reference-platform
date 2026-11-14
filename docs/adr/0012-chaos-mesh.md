# 12. Chaos Mesh: latency injected into the canary, and the last promotion 503s

Date: 2026-11-14
Status: Accepted

## Context

ADR 0011 aborts a bad canary on its own burn rate, but the only fault it had
seen was one built into the app (`ERROR_RATE`). #21 injects a fault from
outside, into the canary pods only, and checks that the analysis still catches
it.

Chaos Mesh needs a privileged daemon on every node, and Docker Desktop runs
every container on one kernel (ADR 0003). So the work started with a spike on
what that daemon can reach.

ADR 0011 also left three storefront 503s after a good promotion unexplained.
They were investigated here, because every run in this issue promotes or
aborts a canary under load.

## Decision

- **Chaos Mesh 2.8.8 is the `chaos-mesh` Application**, with one controller
  replica and without the dashboard and DNS server. The chart still creates
  the dashboard's ServiceAccount and RBAC objects.
- **The daemon talks to containerd, not Docker**
  (`chaosDaemon.runtime: containerd`, socket `/run/containerd/containerd.sock`).
  The chart defaults to `docker.sock`, which kind nodes don't have.
- **Only `shop` can be injected.** `controllerManager.enableFilterNamespace`
  limits experiments to namespaces annotated `chaos-mesh.org/inject=enabled`,
  and the `shop` Application sets that annotation
  (`managedNamespaceMetadata`).
- **Argo CD ignores what the chart regenerates.** Every render of the chart
  produces new certificates (4 Secrets, the `caBundle` of 3 webhook
  configurations) and a random `rollme` annotation on the controller and the
  daemon. With `selfHeal` the app would never settle and would restart both
  on every sync. Five `ignoreDifferences` rules plus
  `RespectIgnoreDifferences=true` keep the first render.
- **The experiment is applied by hand, with `make chaos`.**
  `chaos/checkout-canary-delay.yaml` delays every packet the canary sends by
  200ms. Its selector is the canary's `rollouts-pod-template-hash`, which only
  exists once the rollout has started, so it can't be synced from git ahead
  of time. `make chaos` fills in the current hash; `make chaos-stop` removes
  the experiment, and `duration: 5m` ends it if nobody does.
- **Envoy's upstream idle timeout is 2s, not 60s**
  (`envoy.idleTimeoutDurationSeconds` in `cilium-values.yaml`). See
  "Promotion 503s" below.
- **k6 prints the body of every failed response** (`load/checkout.js`), so a
  failure shows its reason.

## Isolation spike

Throwaway pods and experiments, applied with kubectl and deleted afterwards.

| Check | Result |
|---|---|
| No `docker.sock` | The daemon mounts the node's `/run/containerd`, `/sys` and `/lib/modules`. `/var/run/docker.sock` doesn't exist in the pod. |
| Limited to `shop` | An experiment created in an unannotated namespace, and one created in `shop` but selecting that namespace, injected nothing. |
| netem works under Cilium on linuxkit 7.0.14 | Ping from a `shop` pod: 0.16 ms, 200.4 ms with a 200ms delay, 0.17 ms after deleting it. |
| Other namespaces unaffected | A pod in the unannotated namespace stayed at 0.15 ms during the delay. |

- **The daemon is privileged with `hostPID: true` and `SYS_PTRACE`.** Its
  process view is the kind node container: it saw 86 processes, one kubelet
  and one containerd.
- **It mounts the whole `/run/containerd` directory,** not only the socket.
- **Not tested:** what a privileged container holding the node's `/sys` and
  `/lib/modules` can reach on a kernel shared with other clusters. The spike
  only shows where injection lands and which processes the daemon sees.

## Promotion 503s

Four good promotions under load each failed a few requests:

| Release | Failed | When, after promotion |
|---|---|---|
| 6 (ADR 0011) | 3 | within about 40s |
| 7 | 3 | 6 to 10s |
| 8 | 8 | 4 to 11s |
| 9 | 6 | 20 to 34s |

**Cause.** It is ADR 0010's collision, left over as a matter of chance:

1. Cilium's GAMMA Envoy binds each upstream connection to the caller's address
   and port, and kept it open for 60s after the request.
2. Storefront opens a new connection per call, but its kernel reuses a source
   port within seconds now and then.
3. During a canary, connections to the canary pod sit in Envoy's canary pool.
   At promotion that pod joins the stable Service.
4. A request that reuses a port, and is routed to that same pod through the
   stable pool, asks for an address and port pair that the canary pool still
   holds. The connect is refused on Envoy's own node and storefront gets a
   503.

The window is the 60s after promotion, which fits every failure above.

This is inferred, not observed. A connect that is refused before any packet
leaves produces no flow, so the failing ports were never seen. The conclusion
rests on the empty row in the table below.

**Evidence** (recorded every second):
- **One Envoy, one cluster** (releases 7 to 9). Each 503 matched one
  `upstream_cx_connect_fail` on the stable cluster of one node's Envoy. No
  connect timed out, and the other three Envoys counted nothing.
- **One pod** (releases 8 and 9). Per-host statistics for release 8: 8 of
  2000 connects to the long-lived canary pod failed, and 0 of 2005 to the
  second new pod. Release 9: 6 and 0.
- **Nothing left the node** (release 9). Hubble flows show all 1558
  connection attempts from that storefront pod to the canary pod answered,
  and no reset from the pod.
- **Ports are reused** (release 9). Of about 7160 requests per storefront
  pod, 174 and 307 used a port the same pod had used less than 60s earlier.
  The shortest gap was 4.9s.
- **Envoy held about 300 connections per pod** (releases 8 and 9). The
  release 9 flows show each one closed 60s after its request.
- **The 503 body** (releases 8 and 9) reads `upstream connect error or
  disconnect/reset before headers. reset reason: remote connection failure`,
  with no transport failure reason. How "remote" fits a connect refused on
  Envoy's own node was not checked against Envoy's source.
- **Only the cross-pool case fails** (release 9, requests on a port reused
  within 60s):

  | Earlier connection on that port | New request goes to | Result |
  |---|---|---|
  | same pod, same pool | same pod | 206 succeeded |
  | canary pod, before promotion | the other new pod | 5 succeeded |
  | canary pod, before promotion | the same pod, after promotion | 0 succeeded, 6 failed |

- **Not the scale-down.** ADR 0011's guess was wrong:
  - The old pods were terminated 30s after promotion. Of the 17 failures in
    releases 7 to 9, 14 came before that.
  - In releases 8 and 9 every failed connect was aimed at the new canary
    pod, not at an old pod.

**Fix.** With a 2s idle timeout the old connection is gone long before a port
comes round again. Envoy then held 8 and 12 connections to the two checkout
pods instead of about 300.

| Release | Failed | Requests matching the failing case |
|---|---|---|
| 10 | 0 | 0 |
| 11 | 0 | 2, both succeeded (earlier connection 33s old at the shortest) |

Release 10 proves little, since nothing matched. Release 11 is the evidence,
and two requests is a small sample.

## Rejected

- **Syncing the NetworkChaos from git with the release.** Its selector needs
  the canary's hash, which git can't know before the rollout starts.
- **`make chaos` as the only way Chaos Mesh is installed** (a Helm release
  like Cilium and Argo CD). No ignore rules, but it would be the one
  component outside Argo CD.
- **Retries on the HTTPRoute.** The installed Gateway API CRDs (v1.6.3,
  standard channel) have no `retry` field on HTTPRoute rules.
- **`envoy.useOriginalSourceAddress: false`.** Cilium documents it as not
  affecting Ingress or Gateway API. The generated config for the checkout
  route carries `use-original-source-address: "true"`, and a route can't
  override it (ADR 0010).
- **A retry in storefront for Envoy's connect-failure 503.** Safe, because
  the request never reached checkout, but it would be a second workaround in
  the app for the same mesh behaviour.

## Evidence (2026-11-14, kind, Cilium 1.20.3, 20 req/s)

- **Install:** the `chaos-mesh` app was Synced at 18:45:17 and Healthy at
  18:48:52. Over the next 5 minutes it stayed Synced/Healthy with one sync,
  the same four pods and unchanged `rollme` annotations.
- **Chaos run** (release 12, `1f15caa`, canary `6596b548bc`):

  | Time (UTC) | Event |
  |---|---|
  | 21:34:36 | canary and AnalysisRun start |
  | 21:34:37 | canary pod Ready, weights 80/20, `make chaos` |
  | 21:34:42 | delay reported injected, at the latest |
  | 21:35:36 | `latency-burn` 100, first failure |
  | 21:36:06 | `latency-burn` 100, second failure, rollout aborted, weights 100/0 |
  | 21:44:53 | revert `0c0601d` pushed |
  | 21:47:22 | Rollout Healthy, app Synced/Healthy |

  - Aborted 90 seconds after the canary started, inside the 20% pause.
  - The canary served 353 requests, all 200. None finished under 300ms:
    294 took 300 to 500ms, 48 took 0.5 to 1s, and 11 took 1 to 5s.
  - `availability-burn` stayed at 0, and k6 logged no failed request.
  - The canary pod stayed Ready until it was terminated, so the analysis
    measured real traffic.
- **The stable SLO page did not fire.** No `Checkout*` alert went pending.
  After the abort the service-wide latency burn was 6.77x over 5m, 1.14x
  over 30m and 0.81x over 1h.

## Consequences

- **An experiment aimed at an unannotated namespace never reaches an injected
  state.** No condition became true and no pod record appeared. Whether its
  other conditions or events give a reason was not read, so check an
  experiment's status before trusting a run.
- **`make chaos` outside a rollout delays the stable pods.** With no canary
  in progress the Rollout's current hash is the stable one, so both stable
  pods are selected and the real SLO burns.
- **The delay slows the canary's health checks too,** to an estimated 600ms
  against a 1s probe timeout. A larger delay could make the pod NotReady. It
  would then get no traffic, the analysis would see an empty result, and
  empty passes (ADR 0011).
- **The 1 to 5s tail was not investigated.** New database connections opened
  under the delay are a likely cause.
- **The idle timeout is global.** It also applies to the shared Gateway's
  connections to storefront, which are reopened after 2 idle seconds.
- **Changing it on a running cluster needs the operator restarted.**
  `helm upgrade` only updates the ConfigMap. The operator generates the
  routes' Envoy config and reads the value at startup. A fresh `make up`
  should start with 2s everywhere; that was not tested, and here the agents
  kept running with 60s.
- **A port reused within 2s would still collide.** None was seen in about
  14000 requests.
- **Storefront keeps one connection per call** (ADR 0010). A kept-alive
  connection would hold its port in use all the time, so the idle timeout
  would never apply. Not re-tested.
- **One storefront pod never failed** in three recorded promotions. In
  release 9 it had no port reuse of the failing kind. Whether that is chance
  was not settled.
- **Unlike `ERROR_RATE`, this fault isn't in git.** The release bump and its
  revert are, but the delay itself only exists as a manifest and a `make`
  target that someone runs.
