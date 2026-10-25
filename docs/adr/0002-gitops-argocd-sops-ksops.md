# 2. GitOps: Argo CD app-of-apps with SOPS + age + KSOPS

Date: 2026-10-25
Status: Accepted

## Context

After the cluster is up, everything in it should come from git, including
secrets. The repo is public, so secrets have to be encrypted in git and decrypted
only inside the cluster. The same layout must work on kind and on EKS.

## Decision

- **Argo CD** (chart 10.11.1, app v3.5.6) installed by `make gitops`, which
  `make up` calls last. A single `root` Application watches `gitops/apps/`; each
  file there is a child Application. Adding a component means adding a file.
- **HTTPS pull** from the public GitHub repo. No deploy key to create or rotate.
- **SOPS + age** for secrets. Only `data`/`stringData` are encrypted
  (`.sops.yaml`), so diffs stay readable. The private key lives at
  `~/.config/sops/age/keys.txt` on the Mac, outside the repo.
- **KSOPS** v4.5.1 decrypts at render time. An init container on repo-server
  copies the `ksops` and `kustomize` binaries into the pod; kustomize runs with
  `--enable-alpha-plugins --enable-exec`.
- **Key injection**: `make gitops` creates the `sops-age` Secret in `argocd`
  straight from the key file. The key never touches git or the shell history.
- Automated sync with prune and self-heal on both root and children.

## Rejected

- **Sealed Secrets / External Secrets Operator**: Sealed Secrets ties the
  ciphertext to one cluster's key, so a rebuilt cluster can't read old files. ESO
  needs a secret backend, which is more than a laptop needs. SOPS ciphertext is
  portable and the key is one file.
- **Helm-secrets / Argo CD Vault Plugin**: extra plugin machinery for the same
  result; KSOPS fits the Kustomize layout.

## Consequences

- Losing `keys.txt` means every `*.sops.yaml` must be re-encrypted from the
  plaintext values. Back it up outside the repo.
- Argo CD only sees what is on `main`, so a change is tested by pointing an app's
  `targetRevision` at a branch and switching it back before merging.
- The age key is in the cluster as a plain Secret. Fine for a laptop; on EKS it
  should come from a cloud KMS instead (SOPS supports both side by side).
- Argo's initial admin secret is left in place for now.
