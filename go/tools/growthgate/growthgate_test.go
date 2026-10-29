package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func snapshotDir(t *testing.T, inventory, expectedCSV, baseline string) string {
	t.Helper()
	dir := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if inventory != "" {
		must(os.MkdirAll(filepath.Join(dir, "test"), 0o750))
		must(os.WriteFile(filepath.Join(dir, inventoryPath), []byte(inventory), 0o600))
	}
	if expectedCSV != "" {
		must(os.MkdirAll(filepath.Join(dir, expectedDir), 0o750))
		must(os.WriteFile(filepath.Join(dir, expectedDir, "planet_guns.csv"), []byte(expectedCSV), 0o600))
	}
	if baseline != "" {
		must(os.MkdirAll(filepath.Join(dir, "docs", "testing"), 0o750))
		must(os.WriteFile(filepath.Join(dir, baselinePath), []byte(baseline), 0o600))
	}
	return dir
}

const (
	entry1 = "- id: golden/planet_guns\n  test: TestGolden/planet_guns\n  package: internal/game\n  expected: test/golden/expected/planet_guns.csv\n  finding: CR-26\n  reason: r\n  phase_to_fix: 3c\n  since: 2026-10-28\n"
	entry2 = "\n- id: golden/other\n  test: TestGolden/other\n  package: internal/game\n  expected: test/golden/expected/other.csv\n  finding: GD-09\n  reason: r\n  phase_to_fix: 3c\n  since: 2026-10-28\n"
	inv1   = entry1
	inv2   = entry1 + entry2
	exp1   = "case,go\npoints=5,1\npoints=999,199\n"
	exp2   = exp1 + "points=1000,200\n"
	base1  = "# lintdiff baseline v3\ngo/a.go\terrcheck\tmsg\tfunc A\tfp\ngo/b.go\tgosec\tG115\tfunc B\tfp\n"
	base2  = base1 + "go/c.go\terrcheck\tmsg\tfunc C\tfp\n"
)

func gate(t *testing.T, base, head string, approved bool) (int, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	f, err := os.Create(out) //nolint:gosec // temp file
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-base", base, "-head", head}
	if approved {
		args = append(args, "-approved")
	}
	code := run(args, f, f)
	_ = f.Close()
	b, _ := os.ReadFile(out) //nolint:gosec // temp file
	return code, string(b)
}

func TestUnchanged(t *testing.T) {
	if c, out := gate(t, snapshotDir(t, inv1, exp1, base1), snapshotDir(t, inv1, exp1, base1), false); c != exitOK || !strings.Contains(out, "unchanged") {
		t.Fatalf("exit %d: %s", c, out)
	}
}

func TestReductionNeedsNoApproval(t *testing.T) {
	if c, out := gate(t, snapshotDir(t, inv2, exp2, base2), snapshotDir(t, inv1, exp1, base1), false); c != exitOK || !strings.Contains(out, "reduced only") {
		t.Fatalf("exit %d: %s", c, out)
	}
}

func TestGrowthWithoutApprovalFails(t *testing.T) {
	if c, _ := gate(t, snapshotDir(t, inv1, exp1, base1), snapshotDir(t, inv2, exp1, base1), false); c != exitGrowth {
		t.Errorf("new inventory id: exit %d", c)
	}
	if c, _ := gate(t, snapshotDir(t, inv1, exp1, base1), snapshotDir(t, inv1, exp2, base1), false); c != exitGrowth {
		t.Errorf("new expected case: exit %d", c)
	}
	if c, _ := gate(t, snapshotDir(t, inv1, exp1, base1), snapshotDir(t, inv1, exp1, base2), false); c != exitGrowth {
		t.Errorf("new baseline line: exit %d", c)
	}
}

// Codex's GB0-06B reproduction: the expected Go value for points=5 changes
// from 1 to 2 with no new key. This redefines the allowance and needs review.
func TestChangedExpectedValueNeedsApproval(t *testing.T) {
	changed := "case,go\npoints=5,2\npoints=999,199\n"
	c, out := gate(t, snapshotDir(t, inv1, exp1, base1), snapshotDir(t, inv1, changed, base1), false)
	if c != exitGrowth || !strings.Contains(out, "points=5]: go value 1 -> 2") {
		t.Fatalf("exit %d: %s", c, out)
	}
	if c, _ := gate(t, snapshotDir(t, inv1, exp1, base1), snapshotDir(t, inv1, changed, base1), true); c != exitOK {
		t.Fatalf("approved change refused: exit %d", c)
	}
}

func TestChangedEntryFieldsNeedApproval(t *testing.T) {
	mods := map[string]string{
		"finding":  strings.Replace(entry1, "finding: CR-26", "finding: CR-99", 1),
		"test":     strings.Replace(entry1, "test: TestGolden/planet_guns", "test: TestOther", 1),
		"package":  strings.Replace(entry1, "package: internal/game", "package: internal/other", 1),
		"expected": strings.Replace(entry1, "expected: test/golden/expected/planet_guns.csv", "expected: test/golden/expected/pg2.csv", 1),
		"reason":   strings.Replace(entry1, "reason: r", "reason: broader scope", 1),
		"phase":    strings.Replace(entry1, "phase_to_fix: 3c", "phase_to_fix: 9", 1),
		"untested": "- id: golden/planet_guns\n  untested: true\n  finding: CR-26\n  reason: r\n  phase_to_fix: 3c\n  since: 2026-10-28\n",
	}
	for name, head := range mods {
		c, out := gate(t, snapshotDir(t, inv1, exp1, base1), snapshotDir(t, head, exp1, base1), false)
		if c != exitGrowth || !strings.Contains(out, "~ inventory entry golden/planet_guns") {
			t.Errorf("%s: exit %d: %s", name, c, out)
		}
	}
}

func TestReplacedCaseIsGrowth(t *testing.T) {
	// one case removed, another added: net size unchanged but coverage moved
	exp := "case,go\npoints=5,1\npoints=2000,400\n"
	if c, _ := gate(t, snapshotDir(t, inv1, exp1, base1), snapshotDir(t, inv1, exp, base1), false); c != exitGrowth {
		t.Fatalf("exit %d", c)
	}
}

func TestFirstIntroductionIsGrowth(t *testing.T) {
	empty := t.TempDir()
	if c, _ := gate(t, empty, snapshotDir(t, inv1, exp1, base1), false); c != exitGrowth {
		t.Fatalf("exit %d", c)
	}
	if c, _ := gate(t, empty, snapshotDir(t, inv1, exp1, base1), true); c != exitOK {
		t.Fatalf("approved exit %d", c)
	}
}

func TestMissingHeadInputsAreFatal(t *testing.T) {
	if c, _ := gate(t, snapshotDir(t, inv1, exp1, base1), t.TempDir(), true); c != exitFatal {
		t.Fatalf("exit %d", c)
	}
}

func TestMalformedInventoryIsFatal(t *testing.T) {
	bad := "- id: x\n  skip: true\n"
	if c, _ := gate(t, snapshotDir(t, inv1, exp1, base1), snapshotDir(t, bad, exp1, base1), true); c != exitFatal {
		t.Fatalf("exit %d", c)
	}
}

// Contributor-controlled metadata is never an approval: the tool has no
// trailer, message or branch inputs at all, so the only way to pass with a
// change is the -approved flag the CI script derives from the label.
func TestNoTrailerFlagExists(t *testing.T) {
	devnull, _ := os.Open(os.DevNull)
	defer func() { _ = devnull.Close() }()
	if c := run([]string{"-base", t.TempDir(), "-head", t.TempDir(), "-trailer-from", "x"}, devnull, devnull); c != exitFatal {
		t.Fatalf("unknown flag accepted: exit %d", c)
	}
}
