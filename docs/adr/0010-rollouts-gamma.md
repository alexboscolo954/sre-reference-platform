# 10. Checkout canaries with Argo Rollouts on a GAMMA HTTPRoute

Date: 2026-11-10
Status: Accepted

## Context

Stage 3 releases checkout as a canary: 20%, pause 2m, 50%, pause 2m, 100%.
#20 will abort it automatically on a canary-only burn rate. Before that, the
traffic split has to be real and measurable.

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
  (`release: "2"`), so a canary can run without building an image.
- **An abort is closed out with `git revert`.** Aborting returns all traffic
  to stable, but the Rollout stays Degraded, because git still holds the
  aborted spec. Reverting the commit makes git match the running stable
  ReplicaSet. The Rollout goes Healthy at once and no pods restart.

## Rejected

- **A traffic router on the shared Gateway.** It only sees north-south traffic
  (`shop.localhost` → storefront). Checkout's SLO would never see a split.
- **Canarying storefront instead.** The SLOs are on checkout, so canary
  analysis would measure the wrong service.
- **A replica-weighted canary without a router.** 20% would need 5 pods, and
  the split would only be as exact as kube-proxy-style load balancing across
  pods. This was the fallback if GAMMA had failed the spike.
- **`workloadRef` to the existing Deployment.** It keeps two objects in sync
  and makes Argo CD fight Rollouts over the Deployment's replica count.
- **Ignoring all of `/spec/rules`.** That would hide any real route change in
  git, not just the weights.

## Evidence (2026-11-10, kind, Cilium 1.20.3)

- **Spike** (throwaway namespace, two podinfo versions behind a root Service,
  500 requests each):
  - with the route at 80/20: 395/105;
  - with the route deleted: 240/260.

  Cilium reported the route `Accepted` and `ResolvedRefs`.
- **Promotion of release 2** under `make load` (20 req/s). Share of
  `POST /checkout` per ReplicaSet in Prometheus, 1m rate:

  | Step | Stable | Canary |
  |---|---|---|
  | 20% | 81.5% | 18.5% |
  | 50% | 52.1% | 47.9% |
  | Promoted | — | 100% |

  The whole rollout took 4m15s. `shop` stayed Synced while the plugin moved
  the weights.
- **Abort of release 3** at 20%:
  - measured split before the abort: 84.8/15.2;
  - `kubectl argo rollouts abort`: route back to 100/0 and 100% of traffic on
    stable;
  - `git revert`: Rollout Healthy and app Synced/Healthy within 10s.
- **Errors:** k6 reported no failed requests across both rollouts.

## Consequences

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
