package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestInventoryStatic validates every entry of test/knowndefects.yaml against
// what Go would actually build on this platform, independent of which tests
// ran: ownership is explicit (package:), the package must have test files
// selected by `go list -json -test` (a Windows-only _test.go does not count on
// Linux; a package reporting no test files cannot own a tested entry), the
// named test function must be defined in a selected file, the owning package
// must validate its entries at the end of its run (a TestMain in a selected
// file calling ValidatePackageInventory with the same package name), expected
// files must exist and parse, and untested entries must be explicit. Execution
// itself is proven separately by the completion records that
// tools/inventorycheck reconciles after the run.
func TestInventoryStatic(t *testing.T) {
	inv, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	if len(inv) == 0 {
		t.Fatal("inventory is empty; the file should be removed rather than left empty")
	}
	untested := 0
	for _, id := range sortedIDs(inv) {
		d := inv[id]
		if d.Untested {
			untested++
			t.Logf("UNTESTED (explicit) %s %s: %s (fix in phase %s)", id, d.Finding, d.Reason, d.PhaseToFix)
			continue
		}
		dir := filepath.Join(root, "go", filepath.FromSlash(d.Package))
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Errorf("%s: package %q does not exist under go/", id, d.Package)
			continue
		}
		sel, err := SelectedTestFiles(dir)
		if err != nil {
			t.Errorf("%s: go list for package %s failed: %v", id, d.Package, err)
			continue
		}
		if len(sel.Ignored) > 0 {
			t.Logf("%s: test files of %s excluded on %s/%s: %s", id, d.Package, runtime.GOOS, runtime.GOARCH, strings.Join(sel.Ignored, ", "))
		}
		if len(sel.Files) == 0 {
			t.Errorf("%s is owned by package %s, but go list selects no test files for it on %s/%s ([no test files]); a tested entry cannot live there", id, d.Package, runtime.GOOS, runtime.GOARCH)
			continue
		}
		tests, err := TestFuncsInFiles(sel.Dir, sel.Files)
		if err != nil {
			t.Errorf("%s: cannot parse tests of %s: %v", id, d.Package, err)
			continue
		}
		top, _, _ := strings.Cut(d.Test, "/")
		if !tests[top] {
			t.Errorf("%s names test %s, but no func %s(t *testing.T) is defined in the test files go list selects for go/%s on %s/%s (%s)", id, d.Test, top, d.Package, runtime.GOOS, runtime.GOARCH, strings.Join(sel.Files, ", "))
		}
		if !FilesDeclareValidator(sel.Dir, sel.Files, d.Package) {
			t.Errorf("%s is owned by package %s, whose selected test files have no TestMain calling testutil.ValidatePackageInventory(%q, ...); entries there would never be validated at the end of a run", id, d.Package, d.Package)
		}
		exp := filepath.Join(root, filepath.FromSlash(d.Expected))
		if st, err := os.Stat(exp); err != nil || st.Size() == 0 {
			t.Errorf("%s: expected file %s missing or empty", id, d.Expected)
		} else if err := d.loadExpected(); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
	t.Logf("inventory: %d entries, %d explicit UNTESTED", len(inv), untested)
}

func TestInventoryParserRejectsEvasions(t *testing.T) {
	cases := map[string]string{
		"untested with test":   "- id: x\n  untested: true\n  test: TestGolden\n  finding: CR-00\n  reason: r\n  phase_to_fix: 3c\n  since: d\n",
		"no package":           "- id: x\n  test: TestGolden\n  expected: e.csv\n  finding: CR-00\n  reason: r\n  phase_to_fix: 3c\n  since: d\n",
		"no expected":          "- id: x\n  test: TestGolden\n  package: internal/game\n  finding: CR-00\n  reason: r\n  phase_to_fix: 3c\n  since: d\n",
		"test not a Test func": "- id: x\n  test: golden\n  package: internal/game\n  expected: e.csv\n  finding: CR-00\n  reason: r\n  phase_to_fix: 3c\n  since: d\n",
		"unknown key":          "- id: x\n  test: TestGolden\n  package: internal/game\n  expected: e.csv\n  finding: CR-00\n  reason: r\n  phase_to_fix: 3c\n  since: d\n  skip: true\n",
		"duplicate id":         "- id: x\n  untested: true\n  finding: CR-00\n  reason: r\n  phase_to_fix: 3c\n  since: d\n- id: x\n  untested: true\n  finding: CR-00\n  reason: r\n  phase_to_fix: 3c\n  since: d\n",
	}
	for name, body := range cases {
		p := filepath.Join(t.TempDir(), "knowndefects.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := parseInventory(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := "- id: x\n  untested: true\n  finding: CR-00\n  reason: r\n  phase_to_fix: 3c\n  since: d\n"
	p := filepath.Join(t.TempDir(), "knowndefects.yaml")
	_ = os.WriteFile(p, []byte(ok), 0o600)
	if _, err := parseInventory(p); err != nil {
		t.Errorf("valid untested entry rejected: %v", err)
	}
}
