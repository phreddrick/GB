package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeModule creates a throwaway module with one package and the given
// files, and returns the package directory.
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module tmpmod\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	files["doc.go"] = "package pkg\n"
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const plainTest = "package pkg\n\nimport \"testing\"\n\nfunc TestPlain(t *testing.T) {}\n"
const taggedTest = "//go:build gbtag\n\npackage pkg\n\nimport \"testing\"\n\nfunc TestTagged(t *testing.T) {}\n"
const windowsTest = "//go:build windows\n\npackage pkg\n\nimport \"testing\"\n\nfunc TestWindows(t *testing.T) {}\n"

func names(sel *Selection) map[string]bool {
	m := map[string]bool{}
	for _, f := range sel.Files {
		m[f] = true
	}
	return m
}

func TestSelectionDefault(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	dir := writeModule(t, map[string]string{"plain_test.go": plainTest, "tagged_test.go": taggedTest})
	sel, err := SelectedTestFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := names(sel)
	if !n["plain_test.go"] || n["tagged_test.go"] {
		t.Fatalf("default selection wrong: %v (ignored %v)", sel.Files, sel.Ignored)
	}
	if len(sel.Ignored) != 1 || sel.Ignored[0] != "tagged_test.go" {
		t.Fatalf("tag-excluded file not reported as ignored: %v", sel.Ignored)
	}
	funcs, err := TestFuncsInFiles(sel.Dir, sel.Files)
	if err != nil || !funcs["TestPlain"] || funcs["TestTagged"] {
		t.Fatalf("funcs %v err %v", funcs, err)
	}
}

func TestSelectionHonoursGOFLAGSTags(t *testing.T) {
	t.Setenv("GOFLAGS", "-tags=gbtag")
	dir := writeModule(t, map[string]string{"plain_test.go": plainTest, "tagged_test.go": taggedTest})
	sel, err := SelectedTestFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := names(sel)
	if !n["plain_test.go"] || !n["tagged_test.go"] || len(sel.Ignored) != 0 {
		t.Fatalf("GOFLAGS -tags not honoured: %v (ignored %v)", sel.Files, sel.Ignored)
	}
}

func TestSelectionExcludedByActiveTags(t *testing.T) {
	// A file excluded under the active tags is neither selected nor defines
	// a test, so an inventory entry naming it fails validation.
	t.Setenv("GOFLAGS", "-tags=othertag")
	dir := writeModule(t, map[string]string{"tagged_test.go": taggedTest})
	sel, err := SelectedTestFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Files) != 0 || len(sel.Ignored) != 1 {
		t.Fatalf("selection %v ignored %v", sel.Files, sel.Ignored)
	}
}

func TestSelectionPackageWithoutTests(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	dir := writeModule(t, map[string]string{})
	sel, err := SelectedTestFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Files) != 0 {
		t.Fatalf("expected no test files, got %v", sel.Files)
	}
}

func TestSelectionWindowsOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the regression is about non-Windows platforms; on Windows the file is legitimately selected")
	}
	t.Setenv("GOFLAGS", "")
	dir := writeModule(t, map[string]string{"w_windows_test.go": windowsTest})
	sel, err := SelectedTestFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Files) != 0 || len(sel.Ignored) != 1 || sel.Ignored[0] != "w_windows_test.go" {
		t.Fatalf("windows-only file handling wrong: selected %v ignored %v", sel.Files, sel.Ignored)
	}
}
