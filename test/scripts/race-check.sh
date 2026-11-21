#!/usr/bin/env bash
# Keep the Go package deadline for each invocation. Run from any directory.
set -euo pipefail
cd "$(dirname "$0")/../../go"

game=$(go list -race ./internal/game)
packages=$(go list -race ./...)
other=()
while IFS= read -r pkg; do
  if [[ "$pkg" != "$game" ]]; then other+=("$pkg"); fi
done <<< "$packages"

# Match top-level names only. Go runs their complete subtest trees. Using
# the same filter with -skip includes new/unclassified tests by default.
# Inventory owners (TestGolden and TestCEWCallSite) run together in the
# complement, where TestMain enforces the inventory. The normal suite also
# continues to run unfiltered and reconcile the repository-wide inventory.
construction_and_commands='^Test(CR17|Cargo|PlanetFactory)'
status=0
go test -count=1 -race "${other[@]}" || status=1
go test -count=1 -race -run "$construction_and_commands" "$game" || status=1
go test -count=1 -race -skip "$construction_and_commands" "$game" || status=1
exit "$status"
