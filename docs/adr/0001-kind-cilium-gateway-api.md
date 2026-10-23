# 1. Local cluster: kind + Cilium Gateway API in host network mode

Date: 2026-10-23
Status: Accepted

## Context

The platform runs on a laptop first (Docker Desktop on macOS, 32 GB) and on EKS
later. Ingress has to look the same in both places. The laptop also runs other
kind clusters, and this one must not touch them.

## Decision

- **kind** with 1 control plane + 3 workers. Three workers let CloudNativePG put
  one instance on each and survive a node kill.
- **Cilium** as CNI with kube-proxy replacement, and its **Gateway API**
  implementation for ingress. No ingress-nginx: it is in maintenance mode and
  Gateway API is the portable spec EKS can also run.
- **Host network mode** for the Gateway: Envoy listens on port 8080 of every
  node, and kind maps the control plane's 8080 to `127.0.0.1:8080` on macOS.
- **Isolation**: the cluster gets its own Docker network (`sre-platform`) and its
  own kubeconfig in the repo (gitignored), set by `mise.toml` and the Makefile.
  `~/.kube/config` is never written.
- Versions pinned: kind node image by digest, Gateway API CRDs v1.6.3 (standard
  channel, the version Cilium 1.20.3 documents), Cilium chart 1.20.3.

## Rejected: cloud-provider-kind

Tried first. It mounts the Docker socket and attaches to *every* kind cluster
on the host: on first run it installed Gateway API CRDs, a GatewayClass and a
default IngressClass into an unrelated cluster. It also didn't publish LB ports
to macOS without `--enable-lb-port-mapping`. Too much reach for a laptop shared
with other work.

## Consequences

- One shared Gateway on port 8080 with host-based routing: previews will be
  `pr-N.localhost:8080`. Two Gateways can't both use 8080 locally.
- No LoadBalancer Services locally; EKS uses Cilium's LoadBalancer mode instead
  (different values file, same Gateway/HTTPRoute objects).
- kube-proxy replacement means `k8sServiceHost` must point at the control-plane
  container name; renaming the cluster means updating `cilium-values.yaml`.
