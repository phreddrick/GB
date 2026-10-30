# Vendored CRD schemas for offline validation

`kubeconform` validates core kinds against the published Kubernetes JSON schemas and custom resources against the files in this directory (`{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json`, lower-case). These files are **validation inputs only**: nothing under `deploy/` installs or changes a CRD; the CRDs are cluster prerequisites that the deployment runbooks verify with `kubectl get crd` before any sync.

| File | Source CRD | API version | Installed by | Exported |
|---|---|---|---|---|
| `podmonitor_v1.json` | `podmonitors.monitoring.coreos.com` | `monitoring.coreos.com/v1` | Rancher Monitoring, prometheus-operator 0.87.1 (`controller-gen v0.19.0`), CRD resourceVersion 100449408 | 2026-10-30, read-only `kubectl get crd -o json` on the home-lab cluster (context `local`) |
| `middleware_v1alpha1.json` | `middlewares.traefik.io` | `traefik.io/v1alpha1` | RKE2 Traefik chart `rke2-traefik-40.1.010` (Traefik v3.7.11, `controller-gen v0.19.0`), CRD resourceVersion 197101690 | same |

Conversion: `spec.versions[].schema.openAPIV3Schema` of each CRD, with `x-kubernetes-int-or-string` fields rewritten to `oneOf: [string, integer]` and the other `x-kubernetes-*` keywords removed (the same transformation `openapi2jsonschema` applies), `$schema` set to draft-07, `title` and `description` added. No other content was changed; the files are sorted-key JSON for stable diffs. The snippet that produced them is in `deploy/kustomize/tests/README.md` so they can be regenerated after a chart upgrade.

Pinned validator: `github.com/yannh/kubeconform/cmd/kubeconform@v0.8.0`, run through `go run` (no installation on the host). Core schemas: `yannh/kubernetes-json-schema`, directory `v1.36.4-standalone-strict` (the cluster's exact server version; the directory existed on 2026-10-30), fetched over the network at validation time. A fully offline run needs those core schemas vendored too; that is deferred until the CI job (Stage 3) decides where to cache them.
