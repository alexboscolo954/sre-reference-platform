# 3. Workloads: storefront and checkout on CloudNativePG and Valkey

Date: 2026-10-26
Status: Accepted

## Context

Stages 2 and 3 (SLOs, canaries, chaos) need a real request path that can be
slowed down and broken on purpose. Stage 4 needs Tetragon enforcement to work on
Docker Desktop's kernel. This ADR covers the app shape and what the Tetragon
check showed.

## Decision

- **Two Go services**, `storefront` -> `checkout`, in one module under `apps/`.
  Standard-library HTTP; the only dependencies are `pgx` (Postgres) and
  `valkey-go`. `POST /buy` on storefront calls checkout, which inserts an order
  row and increments a Valkey counter.
- **Fault flags** `LATENCY_MS` and `ERROR_RATE` (0-1) are read from the
  environment on every request by one shared middleware (`apps/internal/fault`).
- **CloudNativePG** `Cluster` with 3 instances and required pod anti-affinity, so
  each lands on its own kind worker and a node kill leaves 2 of 3. CNPG creates
  the `shop-db-app` Secret itself, so no SOPS secret is needed for the database.
- **Valkey**: one Deployment, no persistence. It holds a counter and nothing that
  can't be rebuilt.
- **One shared Gateway** (`gateway/shared`, listener 8080). Cilium's host-network
  mode owns host port 8080, so a second Gateway would collide. Routes attach by
  hostname (`shop.localhost`); preview environments will do the same.
- **Images** go to GHCR, tagged `sha-<full commit>`, never `:latest`. GitHub
  Actions are pinned to commit SHAs. Tags in `gitops/shop/kustomization.yaml` are
  bumped by hand for now.

## Rejected

- **Bitnami charts** for Postgres and Redis: the free images are no longer
  maintained. CNPG and the official Valkey image replace them.
- **A web framework or ORM**: nothing here needs one, and every dependency is
  something to patch.
- **Flipping flags with `kubectl set env`**: see Consequences.

## Consequences

- **Flags change through git.** Argo's self-heal reverts `kubectl set env`
  within seconds, so a flag change is a commit and therefore a new revision. That
  is what Stage 3 canaries need. To poke a pod by hand, pause auto-sync on `root`
  and `shop` first, then re-apply `gitops/root-app.yaml`.
- Checkout exits if Postgres is unreachable at start and Kubernetes restarts it.
  On a fresh cluster it restarted three times while CNPG came up. The `shop` app
  also needs `SkipDryRunOnMissingResource` and retries because the `Cluster` CRD
  arrives from the operator app.
- One 502 (storefront could not reach checkout) appeared in 20 calls right after
  a checkout rollout. Not investigated; look for it again under Stage 3 load.
- Valkey is a single point of failure. Replication is added when a Stage 3
  experiment kills it.

## Tetragon on Docker Desktop (Stage 4 prerequisites)

`Sigkill` enforcement works on the linuxkit kernel (7.0.14, arm64): in a test pod
`/usr/bin/nc` exited 137 while `ls` ran fine, and `nc` in another pod was
untouched. Tetragon was installed by hand for the check and removed afterwards.
Four things only showed up by trying it:

1. **Hook choice.** A kprobe on `security_bprm_check` emitted no events at all.
   A `sys_execve` syscall kprobe (arg 0 = path) works.
2. **Pod scoping.** Processes started by `kubectl exec` (runc exec) carry no pod
   or namespace info at kprobe time, so `TracingPolicyNamespaced` and
   `podSelector` never matched. Scope by Linux mount namespace instead
   (`matchNamespaces: Mnt`), or test with a process the pod started itself.
3. **Shared kernel.** Docker Desktop runs every container on one kernel, so
   Tetragon sees other clusters' processes too (it reported Postgres and Python
   processes that are not in this cluster). An unscoped `Sigkill` policy can kill
   matching processes in unrelated containers. Every enforcement policy here must
   be scoped to specific workloads.
4. **Reading events.** `tetra getevents --namespaces <ns>` hid the kprobe events;
   use unfiltered output when debugging.
