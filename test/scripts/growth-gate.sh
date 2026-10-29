#!/usr/bin/env bash
# Acknowledgment gate for pull requests (GB0-06A, simplified design).
#
#   test/scripts/growth-gate.sh <base-rev>
#
# Compares the working tree with <base-rev> and runs go/tools/growthgate on the
# allowance files (test/knowndefects.yaml, test/golden/expected/*.csv,
# docs/testing/lint-baseline.txt); additions and modifications need the label
# known-defect-added. Changes under the gate-tooling paths (PROTECTED below)
# need the label gate-change-reviewed. Both labels are explicit acknowledgments
# applied to the pull request by the repository's sole maintainer; they are
# not independent reviews.
#
# Context handling:
#   GITHUB_EVENT_NAME=pull_request  labels come from PR_LABELS_JSON (a JSON
#                                   array of exact label names, set by the
#                                   workflow from the event payload; label
#                                   add/remove re-runs the workflow). Missing
#                                   BASE, PR_LABELS_JSON unset or not a JSON
#                                   array: fail closed.
#   GITHUB_EVENT_NAME=push          post-merge technical validation only: this
#                                   gate is not applicable, performs no label
#                                   lookups and no API calls, and exits 0. It
#                                   does not authorize or modify any baseline;
#                                   intentional changes are acknowledged on the
#                                   pull request, and merging without one relies
#                                   on branch protection (UNVERIFIED, see
#                                   docs/testing/known-defects.md).
#   unset (local run)               PR_LABELS_JSON may be set to simulate labels.
# Commit messages, trailers, branch names and pull-request text are never
# consulted. Label matching is exact and case-sensitive.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BASE="${1:-}"
PROTECTED=(.github/ go/tools/ go/internal/testutil/ test/scripts/ go/.golangci.yml test/ctool/)
EVENT="${GITHUB_EVENT_NAME:-local}"

if [ "$EVENT" = "push" ]; then
  echo "growth-gate: push event: technical validation only; the acknowledgment gate applies to pull requests and is skipped here (no label lookups, nothing authorized or changed)"
  exit 0
fi
if [ "$EVENT" = "pull_request" ] && { [ -z "$BASE" ] || [ -z "${PR_LABELS_JSON+x}" ]; }; then
  echo "growth-gate: pull_request context is incomplete (base revision or label list missing); failing closed" >&2
  exit 1
fi
if [ -z "$BASE" ]; then
  echo "usage: growth-gate.sh <base-rev>  (set PR_LABELS_JSON to simulate pull-request labels locally)" >&2
  exit 2
fi
# Parse the label list strictly: it must be a JSON array of strings.
LABELS="$(printf '%s' "${PR_LABELS_JSON:-[]}" | python3 -c 'import sys,json
try:
    v=json.load(sys.stdin)
except Exception:
    sys.exit(3)
if not isinstance(v,list) or not all(isinstance(x,str) for x in v):
    sys.exit(3)
print("\n".join(v))' )" || { echo "growth-gate: PR_LABELS_JSON is not a JSON array of strings; failing closed" >&2; exit 1; }
if ! git -C "$ROOT" cat-file -e "$BASE^{commit}" 2>/dev/null; then
  echo "growth-gate: base revision $BASE is not available; failing closed" >&2
  exit 1
fi

has_label() { # exact, case-sensitive match against one label per line
  printf '%s\n' "$LABELS" | grep -qxF -- "$1"
}
growth_ack=0; gate_ack=0
has_label known-defect-added && growth_ack=1
has_label gate-change-reviewed && gate_ack=1
echo "growth-gate: event=$EVENT base=$BASE labels=[$(printf '%s' "$LABELS" | tr '\n' ' ')]"

WORK="$(mktemp -d)"; BIN="$(mktemp -d)"
trap 'rm -rf "$WORK" "$BIN"' EXIT
mkdir -p "$WORK/base" "$WORK/head"
export_rev() { # export_rev <rev> <dir>
  local rev="$1" dir="$2" f
  for f in test/knowndefects.yaml docs/testing/lint-baseline.txt; do
    if git -C "$ROOT" cat-file -e "$rev:$f" 2>/dev/null; then mkdir -p "$dir/$(dirname "$f")"; git -C "$ROOT" show "$rev:$f" > "$dir/$f"; fi
  done
  mkdir -p "$dir/test/golden/expected"
  for f in $(git -C "$ROOT" ls-tree --name-only "$rev" -- test/golden/expected/ 2>/dev/null); do
    git -C "$ROOT" show "$rev:$f" > "$dir/$f"
  done
}
export_rev "$BASE" "$WORK/base"
for f in test/knowndefects.yaml docs/testing/lint-baseline.txt; do
  mkdir -p "$WORK/head/$(dirname "$f")"; cp "$ROOT/$f" "$WORK/head/$f"
done
mkdir -p "$WORK/head/test/golden/expected"; cp "$ROOT"/test/golden/expected/*.csv "$WORK/head/test/golden/expected/" 2>/dev/null || true

status=0
(cd "$ROOT/go" && go build -o "$BIN/growthgate" ./tools/growthgate)
args=(-base "$WORK/base" -head "$WORK/head")
[ "$growth_ack" = "1" ] && args+=(-approved)
"$BIN/growthgate" "${args[@]}" || status=$?

changed="$( { git -C "$ROOT" diff --name-only "$BASE" -- "${PROTECTED[@]}"; git -C "$ROOT" ls-files --others --exclude-standard -- "${PROTECTED[@]}"; } | sort -u | tr '\n' ' ')"
if [ -n "$changed" ]; then
  echo "growth-gate: protected gate tooling changed: $changed"
  if [ "$gate_ack" = "1" ]; then
    echo "growth-gate: tooling change acknowledged by the maintainer through the label gate-change-reviewed"
  else
    echo "growth-gate: tooling change is NOT acknowledged: the maintainer must check it and apply the label gate-change-reviewed on the pull request"
    [ "$status" -eq 0 ] && status=1
  fi
else
  echo "growth-gate: no protected gate tooling changed"
fi
exit "$status"
