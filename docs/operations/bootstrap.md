# Bootstrap: prerequisites before the first `gb-dev` synchronisation

> **DRAFT. NOT YET APPROVED FOR DEPLOYMENT.** Written in Phase 2B (2026-10-30) from the read-only Phase 2A assessment. Every step that changes GitHub settings, the `k8s-helpers` repository, Argo CD or the cluster is an owner action that needs its own approval under the `k8s-helpers` rules. Nothing in this document has been executed.

This document lists what must exist before `gb-dev-platform` and `gb-dev-server` can be created and synced, who owns each item, and how to verify it read-only. The synchronisation itself, universe initialisation, restarts, recovery and acceptance are in `deploy.md`; the storage and network tests are in `storage-and-network-validation.md`.

## 1. Ownership of every resource

| Resource | Owner | Where | Notes |
|---|---|---|---|
| Container images `ghcr.io/phreddrick/gb-server`, `gbctl` | GitHub Actions in this repository (next stage) | `deploy/docker/Dockerfile` | public packages, digest-pinned in the manifests |
| Kustomize bases and entry points | this repository | `deploy/kustomize/` | `apps/dev/platform`, `apps/dev/server` are the two Application paths |
| AppProject `gb`, Applications `gb-dev-platform`, `gb-dev-server` | `k8s-helpers` (its owners) | `argocd/projects/gb.yaml`, `argocd/applications/gb-dev-*.yaml` | manual sync, three sync options, no finalizers |
| Argo CD resource inclusions, managed namespaces, controller Role in `gb-dev` | `k8s-helpers` Helm values + `helm upgrade` by the operator | `argocd/bootstrap/values.yaml`, `values-security.yaml` | write RBAC is a separate approval (their RBAC-REVIEW §5) |
| Namespace `gb-dev` with Pod Security labels | operator (out of GitOps) | `kubectl create` once; recorded in `k8s-helpers` docs | no Application renders or patches it (ADR-0005) |
| Secret `gb-server-secrets` | operator (out of GitOps) | `kubectl create secret` once | keys `jwt-secret`, `admin-token`; never in Git |
| DNS `gb-dev.home.spukhaus.com → 10.0.0.20` | operator | Pi-hole local DNS | per-host A record, no wildcard (lab convention) |
| Certificate `gb-dev-tls` | cert-manager ingress-shim | generated from the Ingress annotation | never rendered; production issuer (Decision B) |
| PVC `world-gb-engine-0` | StatefulSet controller | created on first sync of `gb-dev-server` | retained on delete/scale; deletion is the recovery step |
| ServiceAccount, NetworkPolicies, PodMonitor | Application `gb-dev-platform` | rendered from `base/platform` | |
| StatefulSet, Services, Ingress, Middleware, ConfigMap | Application `gb-dev-server` | rendered from `base/server` | |

## 2. GitHub: image publication (Phase 2C workflows; first publication owner-approved)

Implemented in Phase 2C as three workflows (protected tooling, `gate-change-reviewed`; design and status in `docs/testing/phase2c-handoff.md`):

- `ci.yml`: the `docker build from checkout (no push)` job builds both targets of `deploy/docker/Dockerfile` with buildx on every pull request and master push, checks them in the runner's Docker engine, and hands the archives to the Trivy, SBOM and kind jobs. Every `ci` job is `contents: read`; nothing is pushed.
- `publish-images.yml`: **manual only** (`workflow_dispatch` on `master`, confirmation word `publish`, environment `ghcr-publish`). Its preflight resolves **one** completed, successful run of `.github/workflows/ci.yml` for that exact commit (this repository, branch `master`, event `push`) through the workflow-runs API and requires every required job to belong to that run and attempt (`.github/scripts/ci_provenance.py`; check-run names from any other workflow are not evidence, and a run finished with "Re-run failed jobs" is refused when its jobs report mixed attempts: use "Re-run all jobs"). The publish job rebuilds both targets, re-scans them (the report must be a well-formed Trivy report about the built archive, `trivy_policy.py`), logs in to GHCR with the workflow `GITHUB_TOKEN` (the only job with `packages: write`), pushes the scanned bytes as `ghcr.io/phreddrick/gb-server:<sha>` and `ghcr.io/phreddrick/gbctl:<sha>`, verifies each by digest, tags both `master` only after both verified, and uploads the publish record (`publish-record.json`, validated by `publish_record.py` before the run may report success: `status` is `published`, `partial` or `failed`). Automatic publication on every master push is **not** configured; it is migration plan Decision 31, pending.
- `digest-update-pr.yml`: called from the publish run (input `open_digest_pr`, only after a `published` record) or dispatched by hand with a digest and source commit; verifies the digest against the registry and the commit, rewrites the one `digest:` line of `deploy/kustomize/apps/dev/server/kustomization.yaml`, runs the offline validation in strict mode, pushes `bot/gb-server-digest-<12 hex>` and opens a pull request with `gh`. Permissions `contents: write`, `pull-requests: write`, `packages: read` for that job only. What happens next is in "Token-created pull requests" below.

### Partial publication (operator procedure; nothing here changes registry state automatically)

Two GHCR pushes are not one transaction. If `publish-push.sh` ends with `publish-push: PARTIAL_PUBLICATION`, the run is failed, the record says `status: partial`, the `published` list names exactly what reached the registry (image, `<sha>` tag, digest, `verified` or `pushed_unverified`), no `master` tag was moved unless both images had verified, no digest is exported and no digest pull request is opened from that run. The same line with `registry writes complete ... record was NOT finalised` means both images verified and were tagged `master` but the record could not be written at its exact pathname (the step log names the kept temporary file and every digest; the artifact then has no `publish-record.json`): treat it as unpublished for Git purposes and re-dispatch as in step 3, which re-verifies the same bytes and writes the record. Then:

1. Read the record and the step log in the `publish-<sha>` artifact; note which image is on the registry and with which digest.
2. Decide nothing about the registry in a hurry: a pushed manifest is content-addressed and immutable. Deleting the `<sha>` tag or the package version in the GitHub UI removes the *reference*, not the fact that those bytes were published under that digest; do not describe such a deletion as a rollback.
3. Fix the cause (registry outage, token scope, a digest mismatch means the bytes on the registry are not the scanned archive and that image must not be referenced anywhere) and **re-dispatch `publish-images.yml` for the same commit**: `crane push` of the identical layout is idempotent (same bytes, same digest), the second image is pushed, both are verified, and only a `published` record moves `master` and may open the digest pull request.
4. If the commit must not be published after all, leave the partial `<sha>` tags unreferenced and record the fact in the operations log; nothing in Git points at them (the dev entry point is edited only by the digest pull request, from a `published` record).

### Token-created pull requests: five separate things

GitHub's documentation ("GITHUB_TOKEN", section "When `GITHUB_TOKEN` triggers workflow runs", <https://docs.github.com/en/actions/concepts/security/github_token>) states that events triggered by `GITHUB_TOKEN` do not create workflow runs, **except** that the `opened`, `synchronize` and `reopened` activity types of a pull request created with it create workflow runs in an **approval-required** state, shown as a banner in the pull request's merge box; a user with write access starts them with **Approve workflows to run**. Other activity types from the token, such as `labeled`, create no run. Keep these apart:

1. **Permission to create the pull request**: the repository setting "Allow GitHub Actions to create and approve pull requests" (Settings > Actions > General > Workflow permissions). Whether it is on here is **unverified**; if it is off, `gh pr create` fails, the job fails after pushing the branch and prints the compare URL, and the maintainer opens the pull request by hand (that pull request then runs `ci` like any other).
2. **Approval of the workflow runs** on a bot-created pull request: a maintainer selects **Approve workflows to run** on the pull request. Adding a label does not approve anything (a maintainer's `labeled` event would start `ci` only because `ci.yml` lists it; it is not the documented path and is not needed).
3. **Required status checks** execute after that approval and must pass; `ci` runs its full gate set on the one-line change.
4. **Acknowledgment labels** (`known-defect-added`, `gate-change-reviewed`) are a different gate for protected tooling; the digest file is not protected tooling, so none is needed.
5. **Review and merge** are a human decision under the ruleset; merging deploys nothing (Argo CD sync is manual, `deploy.md` section 2).

No personal access token and no GitHub App is introduced to skip the approval; the approval is the control.

Owner actions around the first publication (in order): review the Phase 2C pull request and its hosted run; merge; optionally protect the `ghcr-publish` environment (required reviewer, deployment branch `master` only); dispatch `publish-images.yml` on `master` with `publish`; after the run, set both packages to **public** in GitHub package settings (new packages are private), or decide on an `imagePullSecret` (a third out-of-band Secret). Verify read-only from any host:

```sh
curl -fsS 'https://ghcr.io/token?scope=repository:phreddrick/gb-server:pull' | jq -r .token > /tmp/t
curl -fsS -H "Authorization: Bearer $(cat /tmp/t)" https://ghcr.io/v2/phreddrick/gb-server/tags/list
```

Until the digest-update pull request is merged, the dev entry point carries the all-zero sentinel digest and `deploy/kustomize/tests/run.sh --require-published-image` fails by design (the `manifests` job of `ci` asserts exactly that). Merging the digest pull request changes Git only; synchronisation stays a separately approved manual step (`deploy.md` section 2).

## 3. Argo CD: repository access

The repository is public: the Applications can source `https://github.com/phreddrick/GB.git` anonymously once the `gb` AppProject lists that URL in `sourceRepos`; no credential Secret is needed. If the `k8s-helpers` owner prefers SSH, a **read-only deploy key** is created following their `docs/argocd/GITLAB-ACCESS.md` procedure (a hand-made Secret in `argocd` labelled `argocd.argoproj.io/secret-type: repository`, never in Git); personal access tokens are not acceptable there. Verify read-only after registration: `argocd repo list` or `kubectl -n argocd get secret -l argocd.argoproj.io/secret-type=repository -o name` (names only).

Repo-server egress to github.com is assumed from the fact that GitLab fetches already work and the `argocd` NetworkPolicies are ingress-only; the first `argocd app get` will confirm it.

## 4. Argo CD: AppProject and controller RBAC (`k8s-helpers` pull request)

Minimum change set, mirroring their Copilot pilot commit:

1. `argocd/projects/gb.yaml`:
   - `sourceRepos: [https://github.com/phreddrick/GB.git]`
   - `destinations: [{server: https://kubernetes.default.svc, namespace: gb-dev}]`
   - `clusterResourceWhitelist: []` (nothing cluster-scoped; the Namespace is not managed)
   - `namespaceResourceWhitelist`: `v1 ServiceAccount`, `v1 ConfigMap`, `v1 Service`, `apps StatefulSet`, `networking.k8s.io NetworkPolicy`, `networking.k8s.io Ingress`, `traefik.io Middleware`, `monitoring.coreos.com PodMonitor`. Not `Secret`, not `PersistentVolumeClaim` (the StatefulSet controller creates it).
   - the same deny-all sync window with `manualSync: true`, `orphanedResources: {warn: true}`.
2. `argocd/applications/gb-dev-platform.yaml` and `gb-dev-server.yaml`: project `gb`, `repoURL` as above, `targetRevision: master`, `path: deploy/kustomize/apps/dev/platform` and `.../server`, destination `gb-dev`, `syncPolicy.syncOptions: [ApplyOutOfSyncOnly=true, FailOnSharedResource=true, Validate=true]`, no `automated`, no finalizers, `revisionHistoryLimit: 10`. The header comment lists the out-of-band dependencies: Namespace `gb-dev`, Secret `gb-server-secrets`, the DNS record.
3. `argocd/bootstrap/values.yaml`: add to `resource.inclusions` for both cluster URLs: `apps StatefulSet, ControllerRevision`; `"" ServiceAccount`; `networking.k8s.io NetworkPolicy`; `monitoring.coreos.com PodMonitor`.
4. `values-security.yaml`: managed namespaces `homebox,copilot,gb-dev`; a `gb-dev` Role for `argocd-application-controller` with `get,list,watch,create,update,patch,delete` on exactly the kinds in item 1 plus `get,list,watch` on `pods`, `persistentvolumeclaims`, `controllerrevisions` and `events`; a read-only Role for `argocd-server` as in `homebox`. No Namespace verbs.
5. `scripts/validate-argocd-install.py` and its tests, `argocd/README.md`, `docs/argocd/OWNERSHIP.md` (GB row).

Operator actions after merge: `helm upgrade` Argo CD with the new values (shared component: quiet window, `helm rollback` is the fallback), then `kubectl apply` of the project and the two Applications from the merged commit. Verify read-only:

```sh
kubectl auth can-i create statefulsets -n gb-dev --as=system:serviceaccount:argocd:argocd-application-controller   # yes
kubectl auth can-i create statefulsets -n homebox --as=system:serviceaccount:argocd:argocd-application-controller  # no
kubectl auth can-i patch namespaces --as=system:serviceaccount:argocd:argocd-application-controller                # no
kubectl -n argocd get appproject gb -o yaml; kubectl -n argocd get application gb-dev-platform gb-dev-server
```

## 5. Namespace and Secret (operator, out of band)

Namespace, before anything else:

```sh
kubectl create namespace gb-dev
kubectl label namespace gb-dev \
  pod-security.kubernetes.io/enforce=restricted pod-security.kubernetes.io/enforce-version=latest \
  pod-security.kubernetes.io/warn=restricted pod-security.kubernetes.io/audit=restricted
# optional, lab convention: field.cattle.io/projectId=<Rancher project> if GB should appear in a Rancher project
```

The manifests are written for `restricted` (non-root 65532, seccomp RuntimeDefault, no escalation, drop ALL, read-only root). The cluster-wide Pod Security default is unknown (the admission config file is not readable by the operator account), so the label is the enforcement.

Secret, generated without ever echoing a value (ADR-0004 rules: JWT secret at least 32 bytes, admin token at least 16 characters, not equal, no placeholder, single line):

```sh
umask 077; d=$(mktemp -d)
openssl rand -base64 48 | tr -d '\n' > "$d/jwt-secret"
openssl rand -base64 36 | tr -d '\n' > "$d/admin-token"
kubectl -n gb-dev create secret generic gb-server-secrets \
  --from-file=jwt-secret="$d/jwt-secret" --from-file=admin-token="$d/admin-token"
install -m 0600 "$d/admin-token" ~/.config/gb/gb-dev-admin-token   # for GBCTL_TOKEN_FILE on the workstation
shred -u "$d"/*; rmdir "$d"
```

The server reads the whole file with trailing newlines trimmed, so the files must be single-line. Rotation: recreate the Secret, update the workstation copy, then an orderly restart (`deploy.md`). Verify read-only without printing values: `kubectl -n gb-dev get secret gb-server-secrets -o jsonpath='{.data}' | jq 'keys'` must print `["admin-token","jwt-secret"]`.

## 6. DNS and TLS

- Pi-hole local DNS: add exactly `gb-dev.home.spukhaus.com. A 10.0.0.20` (the Traefik LoadBalancer VIP). No wildcard, no CNAME (lab convention from `k8s-helpers/docs/argocd/INGRESS-DESIGN.md`). Verify: `getent hosts gb-dev.home.spukhaus.com` from a lab host.
- TLS: nothing to create. The Ingress annotation `cert-manager.io/cluster-issuer: letsencrypt-production-cloudflare` makes ingress-shim request `gb-dev-tls` through Cloudflare DNS-01 on first sync. Verify afterwards: `kubectl -n gb-dev get certificate gb-dev-tls` Ready.
- No DNS record and no certificate is created in Phase 2B.

## 7. Cluster prerequisites to verify, not install

```sh
kubectl get crd podmonitors.monitoring.coreos.com middlewares.traefik.io        # present (schemas vendored from these)
kubectl get ingressclass traefik                                                 # the only class, default
kubectl get clusterissuer letsencrypt-production-cloudflare                     # Ready
kubectl get sc longhorn                                                          # Delete, Immediate, 2 replicas
kubectl get nodes -l workload.home-apps=true                                     # backup, wkr1 today
kubectl -n cattle-monitoring-system get prometheus -o jsonpath='{.items[0].spec.podMonitorNamespaceSelector}'  # {}
```

## 8. Workstation tooling for the operator

`kubectl` (context `local`), `gbctl` (built from this repository: `cd go && go build ./cmd/gbctl`), `GBCTL_TOKEN_FILE` pointing at the 0600 copy from section 5. No container runtime is needed or allowed on the control-plane workstation.
