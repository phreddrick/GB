#!/usr/bin/env bash
# Adversarial checks for the golden tests and the known-defect inventory
# (GB0-01). Each scenario runs in a fresh temporary copy of go/ and test/, never
# in the working repository, and must produce the stated exit status:
#   1. unchanged baseline                                  -> pass, summary shows the expected XFAIL counts
#   2. new mismatch in a previously passing row            -> fail
#   3. the observed value of an existing XFAIL row changes -> fail
#   4. an expected mismatch is fixed                       -> fail (XPASS)
#   5. an inventory entry is removed                       -> fail
#   6. a fixture file is missing                           -> fail
# Inventory validation (GB0-08), all packages, full package runs:
#   7. an entry naming TestDoesNotExist is added           -> fail
#   8. an unused expected case under an existing test      -> fail
#   9. a test referenced by the inventory is deleted       -> fail
#  10. an entry is moved to another package                -> fail
#  11. a legitimate untested: true entry is added          -> pass, visible as UNTESTED
# Execution-based ownership (GB0-08R), through test/scripts/inventory-check.sh:
#  12. owning test and TestMain exist only in a windows-only file (on Linux) -> fail
#  13. owning package has no selected test files at all                 -> fail
#  14. the owning package's TestMain validator is removed               -> fail
#  15. the unchanged tree reconciles (records written and matched)       -> pass
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Full package runs (no -run filter) so that TestMain inventory validation and
# the repository-wide static validation both execute.
run_golden() { (cd "$WORK/repo/go" && go test -count=1 ./internal/game ./internal/testutil "$@" 2>&1); }
run_inventory() { (cd "$WORK/repo" && test/scripts/inventory-check.sh 2>&1); }
# output_matches <text> <ERE>: true when the pattern occurs in the text. The
# text is supplied as a here-string, never through a pipe from echo: with
# `set -o pipefail`, `echo "$out" | grep -q` fails spuriously when grep exits
# at the first match while echo is still writing more than a pipe buffer
# (SIGPIPE on echo), which turned correct outputs into false mutation
# failures (GB1-10). grep consumes the complete input here.
output_matches() { grep -E -- "$2" <<< "$1" > /dev/null; }
# check_outcome <0|1> <label> <ERE> <got-exit> <output>: the assertion shared
# by expect and expect_inv; prints the verdict and fails the run on mismatch.
check_outcome() {
  local want="$1" label="$2" pat="$3" got="$4" out="$5"
  [ "$got" -ne 0 ] && got=1
  if [ "$got" -ne "$want" ]; then echo "MUTATION FAIL: $label: exit $got, wanted $want"; tail -20 <<< "$out"; exit 1; fi
  if ! output_matches "$out" "$pat"; then echo "MUTATION FAIL: $label: output lacks /$pat/"; tail -20 <<< "$out"; exit 1; fi
  echo "mutation ok: $label (exit $got)"
}
expect() { # expect <0|1> <label> <grep-pattern>
  local want="$1" label="$2" pat="$3"
  set +e; out="$(run_golden)"; got=$?; set -e
  check_outcome "$want" "$label" "$pat" "$got" "$out"
}
expect_inv() { # expect_inv <0|1> <label> <grep-pattern>
  local want="$1" label="$2" pat="$3"
  set +e; out="$(run_inventory)"; got=$?; set -e
  check_outcome "$want" "$label" "$pat" "$got" "$out"
}
# Self-test hook (GB1-10): test/scripts/golden-mutations-selftest.sh sources
# this file with GOLDEN_MUTATIONS_LIB_ONLY=1 to exercise expect/expect_inv on
# synthetic output. Only when sourced; running the script is unaffected.
if [ "${BASH_SOURCE[0]}" != "$0" ] && [ "${GOLDEN_MUTATIONS_LIB_ONLY:-}" = 1 ]; then return 0; fi
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
fresh() {
  rm -rf "$WORK/repo"; mkdir -p "$WORK/repo"
  (cd "$ROOT" && tar --exclude='./test/ctool/obj' --exclude='./test/ctool/gb_golden' -cf - ./go ./test) | (cd "$WORK/repo" && tar -xf -)
}

fresh
expect 0 "1 unchanged baseline" 'ok'
out="$(cd "$WORK/repo/go" && go test -count=1 -v ./internal/game -run TestGolden 2>&1 | grep 'individual cases')"
echo "    $out"

fresh
# 2. flip the C reference odds of one previously matching hit_odds row
python3 - "$WORK/repo/test/golden/testdata/hit_odds.csv" <<'PY'
import sys,csv
p=sys.argv[1]; rows=list(csv.reader(open(p)))
hdr=rows[0]; i=hdr.index("odds"); rows[1][i]=str(int(rows[1][i])+7)
csv.writer(open(p,"w",newline="")).writerows(rows)
PY
expect 1 "2 new mismatch in a passing row fails" 'FAIL golden/hit_odds/odds'

fresh
# 3. change the Go result of an XFAIL row (planet_guns: mob/5 -> mob/6) in the copy only
sed -i 's#g := mobPoints / 5#g := mobPoints / 6#' "$WORK/repo/go/internal/game/planet.go"
grep -q 'mobPoints / 6' "$WORK/repo/go/internal/game/planet.go"
expect 1 "3 changed divergence of an XFAIL row fails" 'the documented divergence changed'

fresh
# 4. fix planet_guns to the C formula in the copy: expected rows must turn XPASS
python3 - "$WORK/repo/go/internal/game/planet.go" <<'PY'
import sys,pathlib
p=pathlib.Path(sys.argv[1]); s=p.read_text()
old="""	g := mobPoints / 5
	if g > 255 {
		return 255
	}
	return uint8(g)"""
new="""	g := mobPoints / 1000
	if g > 20 {
		return 20
	}
	return uint8(g)"""
assert s.count(old)==1; p.write_text(s.replace(old,new))
PY
expect 1 "4 fixed divergence produces XPASS and fails" 'XPASS golden/planet_guns'

fresh
# 5. remove the planet_guns inventory entry
python3 - "$WORK/repo/test/knowndefects.yaml" <<'PY'
import sys,re,pathlib
p=pathlib.Path(sys.argv[1]); s=p.read_text()
blk=re.search(r"- id: golden/planet_guns\n(?:  .*\n)+", s).group(0); p.write_text(s.replace(blk,""))
PY
expect 1 "5 removed inventory entry fails" 'FAIL golden/planet_guns .*no inventory entry'

fresh
rm "$WORK/repo/test/golden/testdata/ap_add.csv"
expect 1 "6 missing fixture fails" 'golden fixture .*ap_add.csv is missing'

entry() { # entry <id> <test> <package>
  printf '\n- id: %s\n  test: %s\n  package: %s\n  expected: test/golden/expected/planet_guns.csv\n  finding: CR-00\n  reason: mutation scenario\n  phase_to_fix: 3c\n  since: 2026-10-28\n' "$1" "$2" "$3" >> "$WORK/repo/test/knowndefects.yaml"
}
fresh
entry mutation/nonexistent TestDoesNotExist internal/game
expect 1 "7 entry naming TestDoesNotExist fails" 'no func TestDoesNotExist\(t \*testing.T\) exists'

fresh
printf 'points=424242,7\n' >> "$WORK/repo/test/golden/expected/planet_guns.csv"
expect 1 "8 unused expected case fails" 'STALE expected row golden/planet_guns \[points=424242\]'

fresh
rm "$WORK/repo/go/internal/game/cew_callsite_test.go"
expect 1 "9 deleting a referenced test fails" 'no func TestCEWCallSite\(t \*testing.T\) exists'

fresh
sed -i '0,/  package: internal\/game/s//  package: internal\/model/' "$WORK/repo/test/knowndefects.yaml"
grep -q 'package: internal/model' "$WORK/repo/test/knowndefects.yaml"
expect 1 "10 moving an entry to another package fails" 'no TestMain calling testutil.ValidatePackageInventory|does not exist in go/internal/model|owned by package'

fresh
printf '\n- id: mutation/untested\n  untested: true\n  finding: CR-00\n  reason: legitimate untested entry for the mutation check\n  phase_to_fix: 3c\n  since: 2026-10-28\n' >> "$WORK/repo/test/knowndefects.yaml"
expect 0 "11 legitimate untested entry passes" 'ok'
out="$(cd "$WORK/repo/go" && go test -count=1 -v ./internal/testutil -run TestInventoryStatic 2>&1 | grep 'UNTESTED (explicit) mutation/untested')"
[ -n "$out" ] && echo "    visible: $out"
winpkg() { # a package whose only test file is windows-only
  mkdir -p "$WORK/repo/go/internal/winonly"
  printf 'package winonly\n' > "$WORK/repo/go/internal/winonly/doc.go"
  cat > "$WORK/repo/go/internal/winonly/w_windows_test.go" <<'GO'
//go:build windows

package winonly

import (
	"os"
	"testing"

	"github.com/phreddrick/GB/go/internal/testutil"
)

func TestWinOnly(t *testing.T) {
	testutil.CheckCase(t, "mutation/winonly", "k=1", false, "1", "d")
	testutil.Finish(t, "TestWinOnly")
}

func TestMain(m *testing.M) {
	code := m.Run()
	if n := testutil.ValidatePackageInventory("internal/winonly", os.Stderr); n > 0 && code == 0 {
		code = 1
	}
	os.Exit(code)
}
GO
}

fresh
winpkg
printf 'case,go\nk=1,1\n' > "$WORK/repo/test/golden/expected/winonly.csv"
printf '\n- id: mutation/winonly\n  test: TestWinOnly\n  package: internal/winonly\n  expected: test/golden/expected/winonly.csv\n  finding: CR-00\n  reason: windows-only scenario\n  phase_to_fix: 3c\n  since: 2026-10-28\n' >> "$WORK/repo/test/knowndefects.yaml"
expect_inv 1 "12 windows-only owning test fails on $(go env GOOS)" 'no test files|selects no test files|no validation record|not among|is not defined in the test files'

fresh
printf '\n- id: mutation/notests\n  test: TestNothing\n  package: internal/model\n  expected: test/golden/expected/planet_guns.csv\n  finding: CR-00\n  reason: package without tests\n  phase_to_fix: 3c\n  since: 2026-10-28\n' >> "$WORK/repo/test/knowndefects.yaml"
expect_inv 1 "13 owning package with no test files fails" 'selects no test files|no test files|no validation record'

fresh
rm "$WORK/repo/go/internal/game/inventory_test.go"
expect_inv 1 "14 removed TestMain validator fails" 'no TestMain calling|produced no validation record'

fresh
expect_inv 0 "15 unchanged tree reconciles" 'all tested entries reconciled'
echo "golden mutations: all fifteen scenarios behaved as required"
