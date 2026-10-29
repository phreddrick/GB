#!/usr/bin/env bash
# Runs the Go test suite with inventory completion records enabled and then
# reconciles test/knowndefects.yaml against what actually executed on this
# platform (GB0-08R). Used by CI and locally.
#
#   test/scripts/inventory-check.sh [extra go test args]
#
# Exit: the `go test` status if tests fail; otherwise the inventorycheck status
# (0 reconciled, 1 problems, 2 input errors). Records are written to
# $GB_INVENTORY_RECORDS (default: a temporary directory).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RECORDS="${GB_INVENTORY_RECORDS:-$(mktemp -d)}"
BIN="$(mktemp -d)"
trap 'rm -rf "$BIN"' EXIT
export GB_INVENTORY_RECORDS="$RECORDS"
rm -f "$RECORDS"/*.json 2>/dev/null || true
cd "$ROOT/go"
go test -count=1 ./... "$@"
go build -o "$BIN/inventorycheck" ./tools/inventorycheck
"$BIN/inventorycheck" -root "$ROOT" -records "$RECORDS"
