#!/usr/bin/env bash
# Self-test for the assertion helpers of golden-mutations.sh (GB1-10).
#
# The helpers expect/expect_inv used `echo "$out" | grep -qE "$pat"` under
# `set -o pipefail`; when the captured test output exceeded a pipe buffer and
# the pattern matched early, grep -q exited first, echo took SIGPIPE and the
# pipeline failed although the required message was present. This script
# sources the real helpers (no scenario runs, no temporary copy) and drives
# them with synthetic outputs well above the pipe-buffer size, repeatedly:
#   A. expected failure, required message near the start of a large output
#   B. expected failure, required message on the last line
#   C. expected failure, required message absent                  -> MUTATION FAIL
#   D. expected failure (exit 1) whose output carries the exact golden message
#   E. unexpected success (exit 0) where a failure was required    -> MUTATION FAIL
#   F. expected pass (exit 0) with its "ok" line
# each for expect (golden runs) and expect_inv (inventory runs). It then
# reports, for information, how often the former pipeline fails on the same
# input. Usage: test/scripts/golden-mutations-selftest.sh [iterations]
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
N="${1:-100}"
GOLDEN_MUTATIONS_LIB_ONLY=1 source "$ROOT/test/scripts/golden-mutations.sh"
for fn in expect expect_inv check_outcome output_matches; do
  declare -F "$fn" > /dev/null || { echo "selftest: helper $fn not defined after sourcing"; exit 1; }
done

# Synthetic output: about 400 KiB of filler, several times the 64 KiB pipe
# buffer (the reviewer reproduced the flake with 25 KB); kept moderate so
# that the repeated bash string handling stays fast.
filler="$(seq -f 'ok  	github.com/phreddrick/GB/go/internal/filler	line %g ........................' 1 5000)"
bytes=${#filler}
[ "$bytes" -gt 200000 ] || { echo "selftest: filler too small ($bytes bytes)"; exit 1; }
golden_msg='--- FAIL: TestGolden/hit_odds (0.01s)
    golden_test.go:88: FAIL golden/hit_odds/odds [factor=12,odds=55]: go 62, C 55 (no inventory entry)
FAIL	github.com/phreddrick/GB/go/internal/game	0.9s'
inv_msg='inventorycheck: entry golden/planet_guns: no validation record for package internal/game'

# The stubs replace the real runners: SYN_OUT is their output, SYN_EXIT their status.
run_golden() { printf '%s\n' "$SYN_OUT"; return "$SYN_EXIT"; }
run_inventory() { printf '%s\n' "$SYN_OUT"; return "$SYN_EXIT"; }

# try <helper> <want> <label> <pattern> <exit> <output>: runs the helper in a
# subshell (it calls exit on failure) and prints "<status>|<first line>".
try() {
  local helper="$1" want="$2" label="$3" pat="$4"
  SYN_EXIT="$5" SYN_OUT="$6"
  local res st
  set +e; res="$( "$helper" "$want" "$label" "$pat" 2>&1 )"; st=$?; set -e
  printf '%s|%s\n' "$st" "$(head -1 <<< "$res")"
}

fail=0
case_check() { # <name> <expected-status> <expected-first-line-ERE> <helper> <want> <pattern> <exit> <output>
  local name="$1" wst="$2" wline="$3"; shift 3
  local bad=0 i r
  for ((i=0; i<N; i++)); do
    r="$(try "$@")"
    if [ "${r%%|*}" != "$wst" ] || ! grep -E -- "$wline" <<< "${r#*|}" > /dev/null; then
      bad=$((bad+1)); [ "$bad" -le 3 ] && echo "    unexpected: $r"
    fi
  done
  if [ "$bad" -eq 0 ]; then echo "selftest ok: $name ($N/$N as required)"; else echo "SELFTEST FAIL: $name ($bad/$N wrong)"; fail=1; fi
}

for helper in expect expect_inv; do
  if [ "$helper" = expect ]; then msg="$golden_msg"; pat='FAIL golden/hit_odds/odds'; else msg="$inv_msg"; pat='no validation record'; fi
  case_check "$helper A early match in large output"   0 '^mutation ok: ' "$helper" 1 "$helper-A" "$pat" 1 "$msg
$filler"
  case_check "$helper B late match in large output"    0 '^mutation ok: ' "$helper" 1 "$helper-B" "$pat" 1 "$filler
$msg"
  case_check "$helper C no match in large output"      1 '^MUTATION FAIL: .*output lacks' "$helper" 1 "$helper-C" "$pat" 1 "$filler"
  case_check "$helper D expected failure, exact text"  0 '^mutation ok: ' "$helper" 1 "$helper-D" "$pat" 1 "$filler
$msg
$filler"
  case_check "$helper E unexpected success"            1 '^MUTATION FAIL: .*exit 0, wanted 1' "$helper" 1 "$helper-E" "$pat" 0 "$filler
$msg"
  case_check "$helper F expected pass"                 0 '^mutation ok: ' "$helper" 0 "$helper-F" '^ok' 0 "$filler"
done

# Information only: the former pipeline on case A's input.
old_bad=0
big="$golden_msg
$filler"
for ((i=0; i<N; i++)); do
  if ! (set -o pipefail; echo "$big" | grep -qE 'FAIL golden/hit_odds/odds') 2>/dev/null; then old_bad=$((old_bad+1)); fi
done
echo "former pipeline (echo | grep -q under pipefail) on the same ${bytes}-byte input: $old_bad/$N false failures (informational)"

if [ "$fail" -ne 0 ]; then echo "golden-mutations selftest: FAIL"; exit 1; fi
echo "golden-mutations selftest: all cases behaved as required ($N iterations each)"
