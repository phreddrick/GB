package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/phreddrick/GB/go/internal/testutil"
)

const inv = `- id: golden/planet_guns
  test: TestGolden/planet_guns
  package: internal/game
  expected: test/golden/expected/planet_guns.csv
  finding: CR-26
  reason: r
  phase_to_fix: 3c
  since: d

- id: combat/x
  untested: true
  finding: CR-27
  reason: no test yet
  phase_to_fix: 3c
  since: d
`

func setup(t *testing.T, recs ...testutil.Record) (string, string) {
	t.Helper()
	dir := t.TempDir()
	invPath := filepath.Join(dir, "knowndefects.yaml")
	if err := os.WriteFile(invPath, []byte(inv), 0o600); err != nil {
		t.Fatal(err)
	}
	rdir := filepath.Join(dir, "records")
	if err := os.MkdirAll(rdir, 0o750); err != nil {
		t.Fatal(err)
	}
	for i, r := range recs {
		raw, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(rdir, "r"+string(rune('a'+i))+".json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return invPath, rdir
}

func good() testutil.Record {
	return testutil.Record{Package: "internal/game", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Entries: []string{"golden/planet_guns"}, Finished: []string{"TestGolden"}}
}

func exitOf(t *testing.T, recs ...testutil.Record) int {
	invPath, rdir := setup(t, recs...)
	return exitWith(t, invPath, rdir)
}

func exitWith(t *testing.T, invPath, rdir string) int {
	t.Helper()
	devnull, _ := os.Open(os.DevNull)
	defer func() { _ = devnull.Close() }()
	return run([]string{"-records", rdir, "-inventory", invPath}, devnull)
}

func TestReconciledRunPasses(t *testing.T) {
	if c := exitOf(t, good()); c != 0 {
		t.Fatalf("exit %d", c)
	}
}

func TestMissingRecordFails(t *testing.T) {
	if c := exitOf(t); c != 1 {
		t.Fatalf("exit %d", c)
	}
}

func TestEntryNotValidatedFails(t *testing.T) {
	r := good()
	r.Entries = nil
	if c := exitOf(t, r); c != 1 {
		t.Fatalf("exit %d", c)
	}
}

func TestTestNotFinishedFails(t *testing.T) {
	r := good()
	r.Finished = []string{"TestOther"}
	if c := exitOf(t, r); c != 1 {
		t.Fatalf("exit %d", c)
	}
}

func TestRecordFromOtherPlatformFails(t *testing.T) {
	r := good()
	r.GOOS = "plan9"
	if c := exitOf(t, r); c != 1 {
		t.Fatalf("exit %d", c)
	}
}

func TestWrongPackageRecordFails(t *testing.T) {
	r := good()
	r.Package = "internal/other"
	if c := exitOf(t, r); c != 1 {
		t.Fatalf("exit %d", c)
	}
}

func TestMissingRecordsDirIsFatal(t *testing.T) {
	invPath, _ := setup(t)
	devnull, _ := os.Open(os.DevNull)
	defer func() { _ = devnull.Close() }()
	if c := run([]string{"-records", filepath.Join(t.TempDir(), "none"), "-inventory", invPath}, devnull); c != 2 {
		t.Fatalf("exit %d", c)
	}
}
