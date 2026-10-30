#!/usr/bin/env bash
# Offline validation of the Phase 2 deployment artifacts:
#   1. render every entry point and check structure/ownership/identity
#      (validate.py; kubectl's embedded kustomize or the kustomize CLI);
#   2. require the validator to reject every unsafe variant (mutations.py);
#   3. assert the artifacts carry no runnable recovery, cleanup or deletion
#      automation and no shell evaluation of API data (safety-check.py, with
#      its self-test);
#   4. schema-validate the renders with kubeconform (pinned version, run
#      through `go run`, nothing installed on the host), core kinds against
#      the published Kubernetes 1.36.4 strict schemas, CRDs against the
#      vendored schemas in ../schemas.
# Nothing here touches a cluster. Usage: deploy/kustomize/tests/run.sh [--require-published-image]
set -euo pipefail

HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
KROOT=$(cd "$HERE/.." && pwd)
OUT=${KUSTOMIZE_RENDER_DIR:-$(mktemp -d)}
KUBECONFORM_VERSION=${KUBECONFORM_VERSION:-v0.8.0}
K8S_VERSION=${K8S_SCHEMA_VERSION:-1.36.4}

echo "== validate.py"
python3 "$HERE/validate.py" --out-dir "$OUT" "$@"

echo
echo "== mutations.py (the validator must reject every unsafe variant for its stated reason)"
python3 "$HERE/mutations.py"

echo
echo "== safety-check.py (no executable recovery/deletion automation, no shell-evaluated API data)"
python3 "$HERE/safety-check.py"
python3 "$HERE/safety-check.py" --selftest

echo
echo "== kubeconform ${KUBECONFORM_VERSION} (kubernetes ${K8S_VERSION}, strict; CRDs from ${KROOT}/schemas)"
# Run from outside the Go module so the repository's go.mod/go.sum are not
# consulted or modified (`go run pkg@version` uses the module cache only).
( cd "$OUT" && GOFLAGS= GOTOOLCHAIN=auto go run "github.com/yannh/kubeconform/cmd/kubeconform@${KUBECONFORM_VERSION}" \
    -strict -summary -verbose \
    -kubernetes-version "$K8S_VERSION" \
    -schema-location default \
    -schema-location "${KROOT}/schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json" \
    ./*.yaml )

echo
echo "renders kept in $OUT"
