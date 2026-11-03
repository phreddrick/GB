# Bootstrap: prerequisites before the first `gb-dev` synchronisation

> **DRAFT. NOT YET APPROVED FOR DEPLOYMENT.** Written in Phase 2B (2026-10-30) from the read-only Phase 2A assessment; corrected 2026-11-01 after the read-only Phase 2D Checkpoint 1 preflight (results in `docs/testing/phase2-validation.md` section 1) and again the same day after the independent review of Checkpoint 2 (GB2D-01: the namespace precedes the Helm upgrade; GB2D-02: sync gates; GB2D-03: bounded RWOP observation). Every step that changes GitHub settings, the `k8s-helpers` repository, Argo CD or the cluster is an owner action that needs its own approval under the `k8s-helpers` rules. Nothing in this document has been executed.

This document lists what must exist before `gb-dev-platform` and `gb-dev-server` can be created and synced, who owns each item, and how to verify it read-only. The synchronisation itself, universe initialisation, restarts, recovery and acceptance are in `deploy.md`; the storage and network tests are in `storage-and-network-validation.md`.

## 1. Ownership of every resource

| Resource | Owner | Where | Notes |
|---|---|---|---|
| Container images `ghcr.io/phreddrick/gb-server`, `gbctl` | GitHub Actions in this repository (next stage) | `deploy/docker/Dockerfile` | public packages, digest-pinned in the manifests |
| Kustomize bases and entry points | this repository | `deploy/kustomize/` | `apps/dev/platform`, `apps/dev/server` are the two Application paths |
| AppProject `gb-dev` (the name `gb` is reserved for future production; see section 4), Applications `gb-dev-platform`, `gb-dev-server` | `k8s-helpers` (its owners) | `argocd/projects/gb-dev.yaml`, `argocd/applications/gb-dev-*.yaml` | manual sync, three sync options, no finalizers |
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

Status 2026-11-01: the first image was published by run 41266551465 (`ghcr.io/phreddrick/gb-server@sha256:a2e433228760d9556e1824aad3fc4b09321a50d7dd8e225bf69e489a9432535a`, built from `46b7bc0`), both packages are public (anonymous `tags/list` works), and the digest pull request (#6) was merged on 2026-11-01, so `deploy/kustomize/apps/dev/server/kustomization.yaml` pins that digest and `deploy/kustomize/tests/run.sh --require-published-image` passes. While the sentinel was pinned, strict mode failed by design; the `manifests` job of `ci` handles both states. Merging the digest pull request changed Git only; synchronisation stays a separately approved manual step (`deploy.md` section 2).

## 3. Argo CD: repository access

The repository is public: the Applications can source `https://github.com/phreddrick/GB.git` anonymously once the `gb-dev` AppProject lists that URL in `sourceRepos`; no credential Secret is needed. Verified 2026-11-01: the only repository Secret in `argocd` is `repo-gitlab-k8s-helpers`; the prepared `gb-dev` project (section 4) registers nothing. If the `k8s-helpers` owner prefers SSH, a **read-only deploy key** is created following their `docs/argocd/GITLAB-ACCESS.md` procedure (a hand-made Secret in `argocd` labelled `argocd.argoproj.io/secret-type: repository`, never in Git); personal access tokens are not acceptable there. Verify read-only after registration: `argocd repo list` or `kubectl -n argocd get secret -l argocd.argoproj.io/secret-type=repository -o name` (names only).

Repo-server egress to github.com is assumed from the fact that GitLab fetches already work and the `argocd` NetworkPolicies are ingress-only; the first `argocd app get` will confirm it.

## 4. Argo CD: AppProject and controller RBAC (`k8s-helpers` pull request)

Status 2026-11-01: verified read-only that none of this exists yet (AppProjects `default` and `homelab-apps` only; five Applications, all sourcing `k8s-helpers` from GitLab; no `gb-dev` in the managed-namespace list; no Role in `gb-dev`). The change set below was **prepared the same day on the `k8s-helpers` branch `feature/argocd-gb-dev-onboarding-isolated` (a dedicated worktree, never the shared checkout) for independent review; nothing is merged, upgraded or applied.** It mirrors the live conventions (Homebox, Plex and Copilot Gateway) rather than the earlier draft of this section:

> **Naming, 2026-11-02 (owner decision).** Development is namespace `gb-dev` with AppProject **`gb-dev`** (`argocd/projects/gb-dev.yaml`) and Applications `gb-dev-platform`, `gb-dev-server` (project `gb-dev`). The name **`gb` is reserved for a future production environment** with its own namespace, AppProject, controller Roles, Secrets, persistent storage, network policies and deployment gates; nothing in Phase 2 prepares it, and CR-18 (non-atomic turn flush) remains a production-readiness blocker. **Live state on 2026-11-02:** the Argo CD Helm upgrade with the `gb-dev` values is deployed (release revision 14; revision 13 was interrupted with `context canceled` after applying every object and the identical retry recorded revision 14 with no pod change), and an AppProject named **`gb`** was created from the earlier `argocd/projects/gb.yaml` (UID `b0e8db37-b253-47a3-8274-85d5fe92d7b6`, destination `gb-dev` only, no Application references it). Correcting the live name is a later, separately approved sequence in `k8s-helpers`: create `gb-dev` from `projects/gb-dev.yaml`, verify its restrictions, confirm no Application references `gb`, delete only `gb` after re-checking its UID, confirm it is absent, then register the two Applications under `gb-dev`. Earlier text in this repository that says AppProject `gb` describes the pre-rename design.

1. `argocd/projects/gb-dev.yaml` (AppProject `gb-dev`; before 2026-11-02 `gb.yaml` / `gb`):
   - `sourceRepos: [https://github.com/phreddrick/GB.git]`
   - `destinations: [{server: https://kubernetes.default.svc, namespace: gb-dev}]`
   - `clusterResourceWhitelist: []` (nothing cluster-scoped; the Namespace is not managed)
   - `namespaceResourceWhitelist`: `v1 ServiceAccount`, `v1 ConfigMap`, `v1 Service`, `apps StatefulSet`, `networking.k8s.io NetworkPolicy`, `networking.k8s.io Ingress`, `traefik.io Middleware`, `monitoring.coreos.com PodMonitor`. Not `Secret`, not `PersistentVolumeClaim` (the StatefulSet controller creates it).
   - the same deny-all sync window with `manualSync: true`, `orphanedResources: {warn: true}`.
2. `argocd/applications/gb-dev-platform.yaml` and `gb-dev-server.yaml`: project `gb-dev`, `repoURL` as above, `targetRevision: master`, `path: deploy/kustomize/apps/dev/platform` and `.../server` (plain Kustomize: no `directory`, `kustomize`, `helm` or plugin options), destination `gb-dev`, the **four** established sync options `syncPolicy.syncOptions: [ApplyOutOfSyncOnly=true, FailOnSharedResource=true, Validate=true, Prune=false]` (`Prune=false` is the Application-level default every `k8s-helpers` Application has carried since Plex: a manual sync never prunes, even when asked), **no `automated` block** (manual sync only, no self-heal), no finalizers, `revisionHistoryLimit: 10`. The header comment lists the out-of-band dependencies: Namespace `gb-dev`, Secret `gb-server-secrets`, the DNS record.
3. `argocd/bootstrap/values.yaml`: add to `resource.inclusions` for both cluster URLs: `apps StatefulSet, ControllerRevision`; `"" ServiceAccount`; `networking.k8s.io NetworkPolicy`; `monitoring.coreos.com PodMonitor`. **The inclusion list is global:** Argo CD's cluster cache lists every included kind in every managed namespace and fails to load if any list is forbidden (`resource.respectRBAC` is deliberately unset there), so the same kinds become visible, read-only, in `homebox`, `copilot`, `plex`, `jellyfin` and `ollama`, and every existing observe Role must gain `get,list,watch` on them (item 4). Inclusion changes what Argo CD sees; it authorises no write.
4. `values-security.yaml`: managed namespaces `homebox,copilot,plex,jellyfin,ollama,gb-dev` (the existing five retained, `gb-dev` appended; the live list already contained `plex`, `jellyfin` and `ollama` on 2026-11-01). In `gb-dev`: the controller observe Role identical to the other namespaces (`get,list,watch` on every included kind, including `pods`, `persistentvolumeclaims`, `controllerrevisions`); a separate `argocd-application-controller-sync` Role with `create,patch,update` on exactly the eight kinds in item 1 (`create` is required because nothing exists yet and cannot be limited by `resourceNames`; it is confined to `gb-dev` and these kinds); **no `delete`** (the `k8s-helpers` barrier behind `Prune=false`; removing objects is an operator action, `deploy.md` section 7), no Secret, PersistentVolumeClaim, Pod or Namespace verb, nothing cluster-wide; an `argocd-server-observe` Role as in `homebox` (`get` on those kinds, pods and pod logs, `list` events). **No new cluster-wide grant for either ServiceAccount:** the server's `namespaces` `get` keeps `homebox` as its only `resourceName` (the review of 2026-11-01 found no evidence that the UI needs the Namespace object for `gb-dev`; whether it shows anything less without it is **UNVERIFIED** and would be a separate, evidenced change). Every other Role is unchanged apart from the read-only additions of item 3. **All six `gb-dev` objects are namespaced:** Helm creates no namespace, so section 5's namespace must exist, be `Active` and carry its labels **before** the Helm upgrade below.
5. `scripts/validate-argocd-install.py` and `tests/test_validate_argocd_install.py` (exact project, Applications, inclusions, grants, managed namespaces, file inventory), `argocd/README.md` ("Galactic Bloodshed (gb-dev) onboarding"), `docs/argocd/OWNERSHIP.md` (GB row), `docs/network.md` (Pi-hole identity).

Operator sequence (GB2D-01; each step is its own approval; the `k8s-helpers` `argocd/README.md` onboarding section carries the same order):

1. Separate owner authorisation to create namespace `gb-dev`; the operator creates and labels it as in section 5.
2. Verify read-only that `gb-dev` exists, is `Active` and carries the restricted Pod Security labels; record its UID. **If the namespace is absent, not `Active`, or its identity or labels cannot be verified, stop here: the Helm upgrade must not run**, because the render contains three Roles and three RoleBindings in `gb-dev` and Helm would fail or leave the release half-applied.
3. Independent review and merge of the `k8s-helpers` branch (the merge may also come before step 1; the Helm upgrade may not come before steps 1 and 2).
4. Separate approval for the shared Argo CD Helm upgrade.
5. `helm upgrade` Argo CD with the reviewed values files (shared component: quiet window, `helm rollback` is the fallback; the step reloads the cluster cache for every existing Application and restarts the server, repo-server and controller pods because `argocd-cm` changed).
6. Verify before continuing: every Argo CD component Running and Ready, the five existing Applications still Synced / Healthy, no `forbidden` list error in the controller log, the six `gb-dev` Roles and RoleBindings present.
7. `kubectl apply` of the project, then the two Applications, from the merged commit (creates nothing in `gb-dev`, syncs nothing).
8. The identity-verified permission check below, against the direct RKE2 API.

**Verifying the controller's permissions (read-only, identity-verified).** The Rancher proxy context `local` (`https://rancher.home.spukhaus.com/k8s/clusters/local`) **discards impersonation**: through it, `kubectl auth can-i ... --as=system:serviceaccount:argocd:argocd-application-controller` answers for the admin user, so every check returns `yes` and proves nothing (observed 2026-11-01: `kubectl auth whoami --as=...` still reported the admin identity, and a non-existent ServiceAccount was told it may create namespaces). Run the checks only against the direct RKE2 API with the admin kubeconfig that `k8s-helpers/docs/argocd/INSTALLATION-RUNBOOK.md` uses (`KUBECONFIG=~/.kube/rke2-admin.yaml`), and prove the identity first; never print or copy that file. If that kubeconfig is unavailable, or using it would bypass a tooling or policy restriction, the result is **UNVERIFIED** and this prerequisite is not met.

```sh
export KUBECONFIG=~/.kube/rke2-admin.yaml                       # direct RKE2 API, never the Rancher proxy
C=system:serviceaccount:argocd:argocd-application-controller
kubectl auth whoami --as=$C                                      # must name $C, not the admin
kubectl auth can-i create namespaces --as=system:serviceaccount:argocd:no-such-account   # must be "no"
kubectl auth can-i create statefulsets -n gb-dev --as=$C         # yes
kubectl auth can-i patch statefulsets -n gb-dev --as=$C          # yes
kubectl auth can-i create secrets -n gb-dev --as=$C              # no
kubectl auth can-i create persistentvolumeclaims -n gb-dev --as=$C   # no
kubectl auth can-i create statefulsets -n homebox --as=$C        # no
kubectl auth can-i patch namespaces --as=$C                      # no
kubectl auth can-i get namespaces/gb-dev --as=system:serviceaccount:argocd:argocd-server   # no (not granted; homebox only)
kubectl -n argocd get appproject gb-dev -o yaml; kubectl -n argocd get application gb-dev-platform gb-dev-server
```

If the first two lines do not behave as shown, impersonation is not being honoured and nothing after them is evidence. Ask the same way whether the controller may remove a StatefulSet in `gb-dev` (the removal verb on `statefulsets`): the answer must be `no`. That line is deliberately kept out of the code block, because this repository's offline safety check (`deploy/kustomize/tests/safety-check.py`, S3) rejects any deletion verb inside a fenced block of an operational document.

## 5. Namespace and Secret (operator, out of band)

Status 2026-11-01: neither exists (verified read-only). No existing namespace on the cluster carries a `pod-security.kubernetes.io/enforce` label, and the cluster-wide admission default remains unknown, so the label below is the enforcement.

Namespace, before anything else, and **in particular before the Helm upgrade of section 4** (the Argo CD values render Roles inside `gb-dev`; Helm does not create it). Separate owner authorisation first, then:

```sh
kubectl create namespace gb-dev
kubectl label namespace gb-dev \
  pod-security.kubernetes.io/enforce=restricted pod-security.kubernetes.io/enforce-version=latest \
  pod-security.kubernetes.io/warn=restricted pod-security.kubernetes.io/audit=restricted
# optional, lab convention: field.cattle.io/projectId=<Rancher project> if GB should appear in a Rancher project
```

Verify, read-only, and record the UID before section 4 step 5 may run; stop if any line disagrees:

```sh
kubectl get namespace gb-dev -o jsonpath='{.status.phase}{"\n"}{.metadata.uid}{"\n"}'      # Active, then the UID to record
kubectl get namespace gb-dev -o jsonpath='{.metadata.labels}{"\n"}'                           # the three pod-security labels, enforce=restricted
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

- Pi-hole local DNS: add exactly `gb-dev.home.spukhaus.com. A 10.0.0.20` (the Traefik LoadBalancer VIP). No wildcard, no CNAME (lab convention from `k8s-helpers/docs/argocd/INGRESS-DESIGN.md`). Verify: `getent hosts gb-dev.home.spukhaus.com` from a lab host. Status 2026-11-01: the lab resolver is `10.0.0.42` (Pi-hole, identified by its `version.bind` answer); `dig @10.0.0.42 gb-dev.home.spukhaus.com` returns NOERROR with no address (the query falls through to the public `spukhaus.com` zone), while `homebox.home.spukhaus.com` resolves to `10.0.0.20` from the same server. The record does not exist yet; `10.0.0.20` is still the address of Service `kube-system/rke2-traefik` (MetalLB pool `home-lab`).
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

Verified 2026-11-01 (read-only, Checkpoint 1): both CRDs present; `traefik` the only and default IngressClass, served by the RKE2 Traefik DaemonSet (`kubernetescrd` and `kubernetesingress` providers, no cross-namespace Middleware references, so the Middleware must live in `gb-dev`, which it does); ClusterIssuer Ready (ACMEAccountRegistered, production ACME server; seven certificates issued by it are Ready); StorageClass `longhorn` as expected; nodes `backup` and `wkr1` labelled, Ready and untainted; Prometheus `podMonitorNamespaceSelector` and `podMonitorSelector` both `{}`; Cilium v1.19.6 with `enable-policy=default` and Kubernetes NetworkPolicy enabled; the Traefik, Prometheus and CoreDNS pod labels equal the selectors in `base/platform`. A manifest that matches the cluster's labels is not a demonstration that the policy is enforced; that is `storage-and-network-validation.md` section 2.

## 8. Workstation tooling for the operator

`kubectl` with two kubeconfigs, used for different purposes: the Rancher proxy context `local` for ordinary reads, port-forwards and approved mutations, and the direct RKE2 admin kubeconfig (`~/.kube/rke2-admin.yaml`, the `k8s-helpers` convention) **only** for the identity-verified RBAC checks of section 4, because the proxy discards impersonation; neither file is ever printed, copied into a document or committed. `gbctl` (built from this repository: `cd go && go build ./cmd/gbctl`), `GBCTL_TOKEN_FILE` pointing at the 0600 copy from section 5. Status 2026-11-01: `gbctl` is not built and `~/.config/gb` does not exist on the workstation. No container runtime is needed or allowed on the control-plane workstation.
