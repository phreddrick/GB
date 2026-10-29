#!/usr/bin/env bash
# Proves the acknowledgment behaviour of the growth gate (GB0-06A/B) using the
# exact gate script in a temporary copy of the repository with its own
# throwaway git history. Scenarios:
#   1. growth with no labels (PR context)                       -> rejected
#   2. a self-authored "Known-Defect-Added:" commit trailer      -> still rejected
#   3. exact label known-defect-added (PR context)               -> accepted
#   4. changed expected Go value (points=5: 1 -> 2) without label -> rejected,
#      reported as a modification; with the label -> accepted
#   5. pure removal of an expected case                          -> accepted without labels
#   6. gate-tooling change without gate-change-reviewed          -> rejected; with it -> accepted
#   7. unchanged tree                                            -> accepted
#   8. partially matching / combined label names                 -> rejected
#   9. label removed again (re-evaluation)                       -> rejected
#  10. pull_request context without a label list or base         -> fails closed
#  11. push event                                                -> technical-validation-only notice, exit 0,
#      no network access attempted (curl is shadowed and must not be called)
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/repo" "$WORK/fakebin"
(cd "$ROOT" && tar --exclude='./.git' --exclude='./test/ctool/obj' --exclude='./test/ctool/gb_golden' -cf - ./go ./test ./docs ./.github) | (cd "$WORK/repo" && tar -xf -)
printf '#!/bin/sh\necho "SELFTEST FAIL: curl was invoked" >&2; exit 99\n' > "$WORK/fakebin/curl"; chmod +x "$WORK/fakebin/curl"
cd "$WORK/repo"
git init -q -b master . && git -c user.name=t -c user.email=t@t add -A >/dev/null && git -c user.name=t -c user.email=t@t commit -q -m base
BASE="$(git rev-parse HEAD)"
G="test/scripts/growth-gate.sh"
pr() { # pr <json-array-of-labels>
  env GITHUB_EVENT_NAME=pull_request PR_LABELS_JSON="$1" "$G" "$BASE"; }
expect() { # expect <wanted-exit> <label> <command...>
  local want="$1" label="$2"; shift 2
  set +e; out="$("$@" 2>&1)"; local got=$?; set -e
  if [ "$got" -ne "$want" ]; then echo "APPROVAL SELFTEST FAIL: $label: exit $got, wanted $want"; echo "$out" | tail -12; exit 1; fi
  echo "approval selftest ok: $label (exit $got)"
}
reset_tree() { git checkout -q -- . && git clean -qfd; }

expect 0 "7 unchanged tree accepted" pr "[]"

printf '\n- id: self/new\n  test: TestGolden\n  package: internal/game\n  expected: test/golden/expected/planet_guns.csv\n  finding: CR-00\n  reason: selftest\n  phase_to_fix: 3c\n  since: 2026-10-28\n' >> test/knowndefects.yaml
expect 1 "1 growth without labels rejected" pr "[]"
git -c user.name=t -c user.email=t@t commit -qam $'add entry\n\nKnown-Defect-Added: I approve my own change'
expect 1 "2 self-authored commit trailer rejected" pr "[]"
expect 0 "3 exact label known-defect-added accepted" pr '["known-defect-added"]'
expect 1 "8a partially matching label rejected" pr '["known-defect-added-not"]'
expect 1 "8b wrong case rejected" pr '["Known-Defect-Added"]'
expect 1 "8c combined names in one label rejected" pr '["known-defect-added gate-change-reviewed"]'
expect 1 "8d unrelated label rejected" pr '["bug"]'
expect 0 "8e exact label among others accepted" pr '["bug","known-defect-added","enhancement"]'
expect 1 "9 label removed again: re-evaluation rejects" pr "[]"
git reset -q --hard "$BASE"

sed -i 's/^points=5,1$/points=5,2/' test/golden/expected/planet_guns.csv
set +e; out="$(pr "[]" 2>&1)"; got=$?; set -e
[ "$got" -eq 1 ] && echo "$out" | grep -q 'go value 1 -> 2' && echo "approval selftest ok: 4 changed expected value rejected and reported as a modification (exit 1)" || { echo "APPROVAL SELFTEST FAIL: 4"; echo "$out" | tail -8; exit 1; }
expect 0 "4b changed expected value accepted with the label" pr '["known-defect-added"]'
reset_tree

sed -i '/^points=999,/d' test/golden/expected/planet_guns.csv
expect 0 "5 removal accepted without labels" pr "[]"
reset_tree

printf '\n# selftest change\n' >> test/scripts/lint-gate.sh
expect 1 "6 gate tooling change without gate-change-reviewed rejected" pr "[]"
expect 1 "6a gate tooling change with only known-defect-added rejected" pr '["known-defect-added"]'
expect 0 "6b gate tooling change accepted with gate-change-reviewed" pr '["gate-change-reviewed"]'
reset_tree

expect 1 "10a pull_request without a label list fails closed" env GITHUB_EVENT_NAME=pull_request "$G" "$BASE"
expect 1 "10d pull_request with a malformed label list fails closed" env GITHUB_EVENT_NAME=pull_request PR_LABELS_JSON='not json' "$G" "$BASE"
expect 1 "10e pull_request with a non-array label list fails closed" env GITHUB_EVENT_NAME=pull_request PR_LABELS_JSON='"known-defect-added"' "$G" "$BASE"
expect 1 "10b pull_request without a base revision fails closed" env GITHUB_EVENT_NAME=pull_request PR_LABELS_JSON="[]" "$G" ""
expect 1 "10c pull_request with an unknown base revision fails closed" env GITHUB_EVENT_NAME=pull_request PR_LABELS_JSON="[]" "$G" 0000000000000000000000000000000000000000

printf '\n- id: self/new2\n  test: TestGolden\n  package: internal/game\n  expected: test/golden/expected/planet_guns.csv\n  finding: CR-00\n  reason: selftest\n  phase_to_fix: 3c\n  since: 2026-10-28\n' >> test/knowndefects.yaml
set +e; out="$(env PATH="$WORK/fakebin:$PATH" GITHUB_EVENT_NAME=push GITHUB_TOKEN=x GITHUB_REPOSITORY=o/r GITHUB_SHA="$BASE" "$G" "$BASE" 2>&1)"; got=$?; set -e
if [ "$got" -eq 0 ] && echo "$out" | grep -q 'technical validation only' && ! echo "$out" | grep -q 'curl was invoked'; then echo "approval selftest ok: 11 push event: technical validation only, no label lookup, no network call (exit 0)"; else echo "APPROVAL SELFTEST FAIL: 11"; echo "$out" | tail -6; exit 1; fi
reset_tree
echo "gate approval selftest: all scenarios behaved as required"
