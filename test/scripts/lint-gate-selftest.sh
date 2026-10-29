#!/usr/bin/env bash
# Proves the lint gate's failure propagation (GB0-02) and fail-closed behaviour
# (GB0-03) using the EXACT gate script, in a temporary copy of the tree:
#   1. clean tree         -> exit 0
#   2. injected finding   -> exit 1, also when the output is piped into a log
#   3. tool failure       -> exit 2 (invalid golangci configuration)
#   4. missing baseline   -> exit 2
# Nothing in the working repository is modified.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
copy_tree() {
  rm -rf "$WORK/repo"; mkdir -p "$WORK/repo"
  (cd "$ROOT" && tar --exclude='./test/ctool/obj' --exclude='./test/ctool/gb_golden' -cf - ./go ./test ./docs/testing) | (cd "$WORK/repo" && tar -xf -)
}
expect() { # expect <wanted-exit> <label> <command...>
  local want="$1" label="$2"; shift 2
  set +e; "$@"; local got=$?; set -e
  if [ "$got" -ne "$want" ]; then echo "SELFTEST FAIL: $label: exit $got, wanted $want"; exit 1; fi
  echo "selftest ok: $label (exit $got)"
}

copy_tree
expect 0 "clean tree passes" "$WORK/repo/test/scripts/lint-gate.sh" "$WORK/r1.json"

copy_tree
printf '\nfunc injectedForSelftest() { f, _ := os.Open("x"); f.Close() }\n' >> "$WORK/repo/go/tools/lintdiff/main.go"
expect 1 "injected unchecked error fails" "$WORK/repo/test/scripts/lint-gate.sh" "$WORK/r2.json"
# the same command with its output piped into a log must still fail (pipefail)
set +e
bash -o pipefail -c "\"$WORK/repo/test/scripts/lint-gate.sh\" \"$WORK/r3.json\" | tee \"$WORK/lint.log\" >/dev/null"; got=$?
set -e
if [ "$got" -ne 1 ]; then echo "SELFTEST FAIL: piped into tee: exit $got, wanted 1"; exit 1; fi
grep -q 'NEW findings' "$WORK/lint.log" && echo "selftest ok: piped into a log still fails (exit $got) and the log names the finding"

copy_tree
printf 'version: "2"\nlinters:\n  enable:\n    - no-such-linter\n' > "$WORK/repo/go/.golangci.yml"
expect 2 "invalid configuration is a tool failure" "$WORK/repo/test/scripts/lint-gate.sh" "$WORK/r4.json"

copy_tree
rm -f "$WORK/repo/docs/testing/lint-baseline.txt"
expect 2 "missing baseline is fatal" "$WORK/repo/test/scripts/lint-gate.sh" "$WORK/r5.json"
echo "lint-gate selftest: all scenarios behaved as required"
