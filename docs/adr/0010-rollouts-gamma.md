# 10. Checkout canaries with Argo Rollouts on a GAMMA HTTPRoute

Date: 2026-11-10
Status: Accepted

## Context

Stage 3 releases checkout as a canary: 20%, pause 2m, 50%, pause 2m, 100%.
#20 will abort it automatically on a canary-only burn rate. Before that, the
traffic split has to be real and measurable, and promotion itself must not
burn error budget.

Checkout isn't behind the shared Gateway. Storefront calls the ClusterIP
Service `checkout` directly (`CHECKOUT_URL=http://checkout`), so a traffic
router attached to the Gateway never sees checkout traffic.

## Decision

- **Argo Rollouts v1.10.2** (argo-helm chart 2.43.12) as the
  `argo-rollouts` Application. The dashboard is at `rollouts.localhost:8080`,
  and `kubectl-argo-rollouts` is pinned in `mise.toml`.
- **The split happens on the storefront→checkout hop with a GAMMA producer
  route.** `gitops/shop/checkout-route.yaml` is an HTTPRoute whose parentRef
  is the Service `checkout`. Cilium sends traffic for that Service through its
  per-node Envoy and splits it between `checkout-stable` and
  `checkout-canary`.
- **The Gateway API traffic-router plugin v0.17.0 owns the weights.** The
  controller downloads the linux-arm64 binary from GitHub at startup and
  checks it against a pinned sha256.
- **checkout is a `Rollout`, not a Deployment.** The root Service `checkout`
  stays as it is. It's the GAMMA parent and still selects every checkout pod.
  Rollouts adds `rollouts-pod-template-hash` to the stable and canary Service
  selectors. kustomize's `images:` pins the Rollout image without extra
  config, because its built-in image path matches any kind.
- **Storefront opens a new connection for every checkout call**
  (`DisableKeepAlives` on its HTTP transport). See "Promotion 503s" below for
  why. Measured cost: median request time 3.94 → 5.79 ms and p95 7.43 →
  9.37 ms, against a 300 ms latency SLO.
- **Argo CD leaves Rollouts' fields alone.** The `shop` app has
  `ignoreDifferences` on the route's `backendRefs` and on the two hash
  selectors, plus `RespectIgnoreDifferences=true` so a sync doesn't put the
  weights back.
- **The route spells out the API server's defaults** (`matches: PathPrefix /`,
  and `group`/`kind` on each backendRef). With `RespectIgnoreDifferences`,
  Argo copies the live rule, defaults included, into the manifest it applies.
  Git lacked those fields, so the app stayed OutOfSync forever and self-heal
  kept re-syncing.
- **A release that changes nothing is a pod-template annotation bump**
  (`release: "N"`), so a canary can run without building an image.
- **An abort is closed out with `git revert`.** Aborting returns all traffic
  to stable, but the Rollout stays Degraded, because git still holds the
  aborted spec. Reverting the commit makes git match the running stable
  ReplicaSet. The Rollout goes Healthy at once and no pods restart.

## Promotion 503s

The first two promotions under `make load` failed requests in a burst at the
moment of promotion:

- release 2: 601 storefront 503s;
- release 3: 299 storefront 503s.

Checkout itself logged no 5xx, and the 503 traces end at storefront's client
span, so Envoy produced them.

**Cause.** Cilium's GAMMA config for the route sets
`cec.cilium.io/use-original-source-address: "true"`. Cilium's L7 traffic
management caveats describe what that does:

> Envoy will bind the sockets for the upstream connection pools to the
> original source address/port. This may cause 5-tuple collisions when pods
> send multiple requests over the same pipelined HTTP/1.1 or HTTP/2
> connection.

Storefront reused one keepalive connection, so every upstream connection
Envoy opened for it had the same source address and port. Envoy keeps a
separate connection pool for each backend Service, and closes idle
connections after 60s. The failure then runs like this:

1. A pod in the canary Service holds a connection from storefront in the
   canary pool.
2. At promotion the pod joins the stable Service too, and Envoy opens a
   second connection to it from the stable pool, on the same 5-tuple.
3. That connect fails, on every request, until the first connection idles
   out.

**Evidence** (release 3, recorded every second):
- **One node:** all 299 503s came from the storefront pod on node `worker`.
  That node's Envoy counted exactly 299 `upstream_cx_connect_fail` on the
  stable cluster, and the other Envoys counted 0.
- **Ran exactly the idle timeout:** the errors lasted 60s, from 17:11:21 to
  17:12:21Z.
- **Hit the pod that moved:** the failed requests were the ones meant for the
  long-lived canary pod. Its rate fell from 10/s to 5/s while it moved into
  the stable Service.
- **Config was fine:** xDS updates reached that Envoy within 1 ms and were
  ACKed.
- **Nothing failed when no pod moved:** the 20% and 50% steps and the abort
  never put a pod into a second Service.

With a new connection per call, every upstream connection gets a fresh
source port and nothing collides. Release 5 promoted with 0 failures.

## Rejected

- **A traffic router on the shared Gateway.** It only sees north-south traffic
  (`shop.localhost` → storefront). Checkout's SLO would never see a split.
- **Canarying storefront instead.** The SLOs are on checkout, so canary
  analysis would measure the wrong service.
- **A replica-weighted canary without a router.** 20% would need 5 pods, and
  the split would only be as exact as load balancing across pods. It avoids
  Envoy entirely, so it stays the fallback if GAMMA misbehaves again.
- **Rollouts ping-pong services** (`pingPong`, plugin support needs Rollouts
  v1.10 or later). Two Services swap the stable and canary roles, so no pod
  changes Service at promotion. It was tested (release 4) and failed with 560
  errors, with 559 failed connections on the pong cluster:
  - Until the first promotion, neither Service has a hash selector, so every
    pod sits in both Envoy clusters.
  - With `stablePingPong` empty, Rollouts treats pong as stable
    (`IsStablePing` in `rolloututil`) and moved all traffic onto it while the
    other cluster still held connections.
  - Pong also kept selecting every pod, including the canary. The canary got
    6–12 req/s instead of 20% of 20 req/s.
- **Turning the setting off in Cilium.** Since Cilium 1.20, `cec.cilium.io/*`
  annotations from Gateway API infrastructure metadata no longer reach the
  generated config, so a route can't override it.
- **`workloadRef` to the existing Deployment.** It keeps two objects in sync
  and makes Argo CD fight Rollouts over the Deployment's replica count.
- **Ignoring all of `/spec/rules`.** That would hide any real route change in
  git, not just the weights.

## Evidence (2026-11-10, kind, Cilium 1.20.3, 20 req/s)

- **GAMMA spike** (throwaway namespace, two podinfo versions behind a root
  Service, 500 requests each):
  - with the route at 80/20: 395/105;
  - with the route deleted: 240/260.
- **Promotion runs:**

  | Release | Mode | Storefront keepalive | Failed (k6) | Split at 20% / 50% |
  |---|---|---|---|---|
  | 2 | stable/canary | yes | 601 of 35998 (whole 30m run) | 18.5% / 47.9% |
  | 3 | stable/canary | yes | 299 of 14401 | — |
  | 4 | ping-pong | yes | 560 (+21 at the switch back) of 14400 | leaked |
  | 5 | stable/canary | no | **0 of 14400** | 20.0% / 51.8% |

  Release 5 ended at 100% on the new ReplicaSet with 0 Envoy connect
  failures on all four nodes.
- **Abort of release 3** at 20%:
  - measured split before the abort: 84.8/15.2;
  - `kubectl argo rollouts abort`: route back to 100/0 and 100% of traffic on
    stable;
  - `git revert`: Rollout Healthy and app Synced/Healthy within 10s.

## Consequences

- **Renaming a route backend Service needs the route recreated.** Argo
  ignores the route's `backendRefs`, so a rename in git never reaches the
  live route, while the old Services are pruned. Recreate the route right
  after the sync, and Argo applies it from git in full. During branch
  testing this caused two outages:
  - 17:32:11–17:36:29Z: checkout was down while the route pointed at pruned
    Services. No load was running, so only manual test requests hit it.
  - 17:47:24–25Z: 21 503s under load in the gap between the prune and the
    route's recreation.
- **Any client of a GAMMA-routed Service must not reuse connections across a
  promotion.** Today that's only storefront. A new caller needs the same
  setting, or it gets the same 60s of failures at every promotion.
- If GitHub is unreachable when the controller starts, the plugin doesn't
  load and canaries can't shift traffic. Vendoring the binary into an image
  would remove that dependency. Not done, because the cluster already needs
  the internet for image pulls.
- Cilium only supports producer routes. A route bound to checkout has to live
  in `shop`, next to the Service.
- The Rollouts plugin support matrix lists Cilium for north-south only, so
  the GAMMA path relies on this repo's own evidence, not an upstream test.
- The 1m rate window lags about a minute behind each step change, so splits
  were read at least 50s after each step started.
