# ADR-0005: Kustomize in the GB repository; one entry point per Argo CD Application; operator-owned Namespace

Status: Proposed 2026-10-30 (Phase 2B draft for owner review; artifacts in `deploy/kustomize/`, nothing deployed)

## Context

Phase 2 deploys the Phase 1 monolith to the home-lab cluster through Argo CD under the rules of the `k8s-helpers` repository (one Application per app, manual sync, AppProjects with resource allow-lists, no finalizers, Secrets never in Git, every cluster change separately approved). The Phase 2A assessment (2026-10-30, read-only) verified the facts that shape this decision:

- Argo CD v3.5.6 is installed but **observe-only**: its controller holds get/list/watch Roles in two namespaces, tracks only Namespace, ConfigMap, PersistentVolumeClaim, Service, Pod, Deployment, ReplicaSet, Ingress and Traefik `Middleware`, manages two namespaces, and has no GitHub repository registered. StatefulSets are outside its `resource.inclusions`.
- `k8s-helpers` RBAC-REVIEW §5 reserves write access for a separate approval and wants **no Namespace write for Argo CD**; Argo CD cannot create namespaces today.
- The GB repository is public on GitHub; its default branch is `master`.
- The migration plan (12.2, 12.3) placed manifests in the GB repository, Applications in `k8s-helpers`, and the Namespace under the platform Application. The last point conflicts with the `k8s-helpers` policy.

## Decision

1. **Manifests live with the code.** `deploy/kustomize/base/{platform,server}` and the entry points `deploy/kustomize/apps/<env>/<app>/kustomization.yaml` are in the GB repository. Each entry point is the `path` of exactly one Argo CD Application defined in `k8s-helpers` (`argocd/applications/gb-<env>-<app>.yaml`, project `gb`, `targetRevision: master`, the three sync options `ApplyOutOfSyncOnly=true,FailOnSharedResource=true,Validate=true`, no `automated` block, no finalizers). Phase 2 has two: `gb-dev-platform` and `gb-dev-server`.
2. **Ownership split.** `gb-dev-platform` owns the namespaced prerequisites: ServiceAccount `gb-engine`, the NetworkPolicies (default deny, DNS egress, player port from Traefik, admin port from Prometheus) and the PodMonitor. `gb-dev-server` owns the workload: StatefulSet `gb-engine`, headless Service `gb-engine`, Service `gb-player`, Ingress `gb-player`, Middleware `https-redirect` and the hash-suffixed ConfigMap. No object is rendered by both; `FailOnSharedResource` is the runtime guard and `deploy/kustomize/tests/validate.py` the pre-merge guard.
3. **The Namespace `gb-dev` is operator-owned.** It is created in a separately authorised infrastructure step (with its Pod Security labels), recorded in `k8s-helpers`, and **neither Application renders, adopts or patches it**. This replaces plan 12.3 item 2's "the Namespace itself" for Phase 2 and avoids granting Argo CD any Namespace write. The validator fails if an entry point renders a `Namespace`.
4. **Secrets are never rendered.** `gb-server-secrets` (keys `jwt-secret`, `admin-token`) is created out of band (ADR-0004); the AppProject allow-list omits `Secret`; the validator fails on a `Secret` object or on secret-looking values in a render.
5. **Argo CD write access is scoped to the rendered kinds in `gb-dev`.** The `k8s-helpers` change that enables Phase 2 adds exactly StatefulSet, ControllerRevision (read, for health), ServiceAccount, NetworkPolicy, PodMonitor, ConfigMap, Service, Ingress and Middleware to the controller's `gb-dev` Role and to the `gb` AppProject's namespace allow-list, adds `gb-dev` to the managed namespaces and to the resource inclusions, and nothing cluster-scoped. It is specified, not performed, by this repository (`docs/operations/bootstrap.md`).
6. **Repository access.** The repository is public, so the Applications may source `https://github.com/phreddrick/GB.git` anonymously; the `gb` AppProject lists that URL. A read-only deploy key remains an option if the `k8s-helpers` owner prefers SSH; a personal access token is never used (their rule).
7. **`ci` entry points** (`apps/ci/*`) render the same bases for a kind cluster (local-path storage, RWO, no Ingress, Middleware or PodMonitor, no node selector) so the ownership and identity tests run on the structure CI can execute.

## Alternatives considered

- Rendered manifests copied into `k8s-helpers/apps/gb/` per release (plan Decision 2 option b): rejected; a rollback would span two repositories and a bot.
- Argo CD owning the Namespace with `Prune=false,Delete=false` and a `patch` right on the one name: workable, but it still needs a Namespace write grant that `k8s-helpers` does not want; rejected for Phase 2 (Phase 2B Decision C).
- One Application for everything: rejected; the platform/workload split is what lets the network fence exist before the workload (sync order) and matches plan 12.3.
- Helm chart: rejected; `k8s-helpers` has no Helm-source Applications and Kustomize is enough for two bases.

## Consequences

- Deploying GB requires a `k8s-helpers` pull request and an Argo CD Helm values change (inclusions, managed namespaces, Role) that the Phase 2A report lists as prerequisites; until then the Applications cannot be created.
- The Namespace, its labels and the Secret are operator state outside GitOps; the runbooks record them and the Application header comment in `k8s-helpers` lists the out-of-band dependencies (their convention).
- A resource added to a base must also be added to the AppProject allow-list and the controller Role, or the sync fails with a permission error; this is intended (allow-list, not deny-list).

## Validation

`deploy/kustomize/tests/run.sh`: renders every entry point, fails on any duplicate `(apiVersion, kind, namespace, name)` across the two entry points of an environment, on a `Namespace` or `Secret` object, on an object outside `gb-dev`, and schema-checks the renders with kubeconform (vendored CRD schemas for PodMonitor and Middleware). The `k8s-helpers` validator checks the Application and AppProject shape on its side.

## References

Migration plan 10, 12.1 to 12.6, 16.4 Phase 2 (as amended 2026-10-30); `k8s-helpers` `docs/argocd/ARCHITECTURE.md`, `OWNERSHIP.md`, `RBAC-REVIEW.md` §5; Phase 2A assessment sections 3, 9 and 10 (discrepancies B, G, I, J); ADR-0004.
