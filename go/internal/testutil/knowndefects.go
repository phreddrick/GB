// Package testutil holds the Phase 0 test infrastructure shared by the Go
// test suites: the known-defect inventory (expected failures) and the golden
// fixture loader.
//
// The inventory file test/knowndefects.yaml lists behaviours that are known
// to differ from the C reference, each tied to a code-review finding. A test
// case reports its result through Check, which classifies it as:
//
//	PASS   the case matches the reference and is not expected to diverge
//	FAIL   a new mismatch, or a documented divergence whose Go value changed
//	XFAIL  the case differs exactly as recorded in the entry's expected file
//	XPASS  the case matches although the entry expects it to diverge (the
//	       expected row must be removed so the inventory stays honest)
//
// Expectations are per case: each inventory entry points to a CSV of the
// cases that diverge and the Go value each one currently produces, so a
// category can never be "allowed to fail" wholesale. Nothing is ever skipped.
// See docs/testing/known-defects.md for the policy.
package testutil

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Defect is one entry of test/knowndefects.yaml.
type Defect struct {
	ID         string // unique key, e.g. "golden/planet_guns"
	Test       string // the Go test that exercises it, or "" when Untested
	Finding    string // code-review finding id, e.g. "CR-26", or a GD-nn id
	Reason     string
	PhaseToFix string
	Since      string
	Untested   bool   // true when no test can exercise the defect yet
	Expected   string // repository-relative CSV of expected divergent Go values per case (required unless Untested)
	Package    string // Go package (relative to go/) whose tests own this entry, e.g. "internal/game" (required unless Untested)

	expected map[string]string // case key -> expected Go value (loaded lazily)
	seenCase map[string]bool   // case keys checked during this run
}

// Outcome is the classification of one checked case.
type Outcome string

const (
	Pass  Outcome = "PASS"
	Fail  Outcome = "FAIL"
	XFail Outcome = "XFAIL"
	XPass Outcome = "XPASS"
)

var (
	loadOnce  sync.Once
	loadErr   error
	inventory map[string]*Defect
	invPath   string

	tallyMu  sync.Mutex
	tally    = map[Outcome]int{}
	seen     = map[string]bool{}
	finished = map[string]bool{} // top-level tests that called Finish in this process
)

// RepoRoot walks up from the current working directory (the package directory
// under `go test`) until it finds the repository root, identified by the
// `test/` directory next to `go/`.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, "test", "knowndefects.yaml")); err == nil && !st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root (containing test/knowndefects.yaml) not found above %s", dir)
		}
		dir = parent
	}
}

// Load parses the inventory once. It is strict: unknown keys, duplicate ids
// and missing required fields are errors, so a malformed inventory cannot
// silently turn failures into expected failures.
func Load() (map[string]*Defect, error) {
	loadOnce.Do(func() {
		root, err := RepoRoot()
		if err != nil {
			loadErr = err
			return
		}
		invPath = filepath.Join(root, "test", "knowndefects.yaml")
		inventory, loadErr = parseInventory(invPath)
	})
	return inventory, loadErr
}

// ParseInventoryFile parses an inventory file at an explicit path (used by the
// gate tools, which compare two revisions of the file).
func ParseInventoryFile(path string) (map[string]*Defect, error) {
	return parseInventory(path)
}

// parseInventory reads the restricted YAML subset used by the inventory: a
// top-level sequence of flat mappings with plain scalar values. Full-line
// comments (#) and blank lines are ignored. This keeps the file valid YAML
// for other tools without adding a YAML dependency to the module.
func parseInventory(path string) (map[string]*Defect, error) {
	f, err := os.Open(path) //nolint:gosec // path is derived from the repository root found by RepoRoot
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]*Defect{}
	var cur *Defect
	flush := func(line int) error {
		if cur == nil {
			return nil
		}
		if cur.ID == "" || cur.Finding == "" || cur.Reason == "" || cur.PhaseToFix == "" || cur.Since == "" {
			return fmt.Errorf("%s: entry ending before line %d is missing id, finding, reason, phase_to_fix or since", path, line)
		}
		if cur.Test == "" && !cur.Untested {
			return fmt.Errorf("%s: entry %q has no test and is not marked untested: true", path, cur.ID)
		}
		if cur.Expected == "" && !cur.Untested {
			return fmt.Errorf("%s: entry %q has no expected: file (the per-case expected divergent values)", path, cur.ID)
		}
		if cur.Package == "" && !cur.Untested {
			return fmt.Errorf("%s: entry %q has no package: (the Go package whose tests own it)", path, cur.ID)
		}
		if cur.Untested && (cur.Test != "" || cur.Expected != "") {
			return fmt.Errorf("%s: entry %q is untested: true but names a test or an expected file; an untested entry must not pretend to be covered", path, cur.ID)
		}
		if !cur.Untested && !strings.HasPrefix(cur.Test, "Test") {
			return fmt.Errorf("%s: entry %q test %q does not name a Go test function (Test...)", path, cur.ID, cur.Test)
		}
		if _, dup := out[cur.ID]; dup {
			return fmt.Errorf("%s: duplicate id %q", path, cur.ID)
		}
		out[cur.ID] = cur
		cur = nil
		return nil
	}
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		n++
		raw := sc.Text()
		line := strings.TrimRight(raw, " \t")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		isNew := strings.HasPrefix(trim, "- ")
		if isNew {
			if err := flush(n); err != nil {
				return nil, err
			}
			cur = &Defect{}
			trim = strings.TrimSpace(strings.TrimPrefix(trim, "- "))
		} else if cur == nil || !strings.HasPrefix(line, "  ") {
			return nil, fmt.Errorf("%s:%d: expected '- key: value' or an indented 'key: value' line", path, n)
		}
		key, val, ok := strings.Cut(trim, ":")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected 'key: value'", path, n)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		switch key {
		case "id":
			cur.ID = val
		case "test":
			cur.Test = val
		case "finding":
			cur.Finding = val
		case "reason":
			cur.Reason = val
		case "phase_to_fix":
			cur.PhaseToFix = val
		case "since":
			cur.Since = val
		case "untested":
			cur.Untested = val == "true"
		case "expected":
			cur.Expected = val
		case "package":
			cur.Package = strings.Trim(val, "/")
		default:
			return nil, fmt.Errorf("%s:%d: unknown key %q", path, n, key)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := flush(n + 1); err != nil {
		return nil, err
	}
	return out, nil
}

// updateExpected rewrites the expected-divergence files from the observed Go
// values of mismatching cases instead of checking them. It is a deliberate,
// reviewable operation: run `go test ./internal/game -run TestGolden
// -args -golden.update-expected`, then review the CSV diff. The test fails
// after writing so that an update can never be mistaken for a green run.
var updateExpected = flag.Bool("golden.update-expected", false,
	"rewrite test/golden/expected/*.csv from the observed divergent Go values (review the diff afterwards)")

// CaseKey builds the stable identifier of a fixture row from its input
// columns, e.g. "points=5". Expected-divergence files are keyed by it.
func CaseKey(row map[string]string, inputCols ...string) string {
	parts := make([]string, 0, len(inputCols))
	for _, c := range inputCols {
		parts = append(parts, c+"="+row[c])
	}
	return strings.Join(parts, ";")
}

// CheckCase records the outcome of one fixture case (one row and output
// column). matched reports whether the Go value equals the C reference; got
// is the Go value formatted as text (%.17g for floats, %d for integers), and
// detail describes the comparison for the log.
//
// Classification:
//
//	matched, case not expected to diverge   PASS
//	matched, case expected to diverge       XPASS  (fails: remove the expected row)
//	differs, case not expected to diverge   FAIL   (new mismatch)
//	differs, Go value equals the expected   XFAIL  (documented divergence)
//	differs, Go value changed since recorded FAIL  (the divergence itself changed)
func CheckCase(t *testing.T, id, caseKey string, matched bool, got, detail string) Outcome {
	t.Helper()
	inv, err := Load()
	if err != nil {
		t.Fatalf("known-defect inventory unavailable: %v", err)
	}
	d := inv[id]
	var exp string
	var expected bool
	if d != nil {
		if err := d.loadExpected(); err != nil {
			t.Fatalf("inventory entry %s: %v", id, err)
		}
		exp, expected = d.expected[caseKey]
		d.seenCase[caseKey] = true
	}
	if *updateExpected {
		recordObserved(id, caseKey, matched, got)
		return Pass
	}
	var o Outcome
	switch {
	case matched && !expected:
		o = Pass
	case matched && expected:
		o = XPass
		t.Errorf("XPASS %s [%s]: Go now matches the reference but %s lists an expected divergence (go=%s); remove that row", id, caseKey, d.Expected, exp)
	case !matched && !expected:
		o = Fail
		if d == nil {
			t.Errorf("FAIL %s [%s]: %s (no inventory entry)", id, caseKey, detail)
		} else {
			t.Errorf("FAIL %s [%s]: %s (inventory entry %s exists but this case is not in %s: a new mismatch)", id, caseKey, detail, d.Finding, d.Expected)
		}
	case valuesEqual(got, exp):
		o = XFail
	default:
		o = Fail
		t.Errorf("FAIL %s [%s]: the documented divergence changed: Go now gives %s, the recorded divergent value was %s (%s); the behaviour moved without the inventory being updated", id, caseKey, got, exp, detail)
	}
	record(id, caseKey, o, d, detail)
	return o
}

// valuesEqual compares recorded and observed Go values: numerically within
// 1e-9 relative when both parse as floats, textually otherwise.
func valuesEqual(got, exp string) bool {
	if got == exp {
		return true
	}
	g, err1 := strconv.ParseFloat(got, 64)
	e, err2 := strconv.ParseFloat(exp, 64)
	if err1 != nil || err2 != nil {
		return false
	}
	return FloatClose(g, e, 1e-9)
}

func (d *Defect) loadExpected() error {
	if d.expected != nil {
		return nil
	}
	d.expected = map[string]string{}
	d.seenCase = map[string]bool{}
	if d.Expected == "" {
		return nil
	}
	root, err := RepoRoot()
	if err != nil {
		return err
	}
	p := filepath.Join(root, filepath.FromSlash(d.Expected))
	f, err := os.Open(p) //nolint:gosec // path is derived from the repository root and the inventory
	if err != nil {
		if *updateExpected && os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("expected-divergence file %s: %w", d.Expected, err)
	}
	defer func() { _ = f.Close() }()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return fmt.Errorf("%s: %w", d.Expected, err)
	}
	if len(recs) == 0 || len(recs[0]) != 2 || recs[0][0] != "case" || recs[0][1] != "go" {
		return fmt.Errorf("%s: header must be exactly case,go", d.Expected)
	}
	for i, rec := range recs[1:] {
		if _, dup := d.expected[rec[0]]; dup {
			return fmt.Errorf("%s: duplicate case %q at line %d", d.Expected, rec[0], i+2)
		}
		d.expected[rec[0]] = rec[1]
	}
	if len(d.expected) == 0 {
		return fmt.Errorf("%s: lists no cases; an inventory entry must document at least one divergent case", d.Expected)
	}
	return nil
}

// ---- tallies -------------------------------------------------------------

type caseLine struct {
	id, key, detail string
	outcome         Outcome
}

var (
	cases    []caseLine
	observed = map[string]map[string]string{} // id -> case -> got (update mode)
)

func record(id, key string, o Outcome, d *Defect, detail string) {
	tallyMu.Lock()
	defer tallyMu.Unlock()
	tally[o]++
	seen[id] = true
	cases = append(cases, caseLine{id: id, key: key, outcome: o, detail: detail})
}

func recordObserved(id, key string, matched bool, got string) {
	tallyMu.Lock()
	defer tallyMu.Unlock()
	seen[id] = true
	if matched {
		return
	}
	if observed[id] == nil {
		observed[id] = map[string]string{}
	}
	observed[id][key] = got
}

// Finish is called once at the end of the top-level test (after all subtests
// have run). It fails on stale inventory entries and stale expected rows for
// that test, logs the per-case tally and the per-category summary, and writes
// the tally to $GB_TEST_SUMMARY when set (used by the CI job summary). In
// -golden.update-expected mode it rewrites the expected files and fails.
func Finish(t *testing.T, testName string) {
	t.Helper()
	inv, err := Load()
	if err != nil {
		t.Fatalf("known-defect inventory unavailable: %v", err)
	}
	if *updateExpected {
		writeExpected(t, inv, testName)
		t.Fatalf("-golden.update-expected rewrote the expected-divergence files; review the diff under test/golden/expected and rerun without the flag")
	}
	RequireExercised(t, testName)
	tallyMu.Lock()
	defer tallyMu.Unlock()
	finished[testName] = true
	// Expected rows that were never checked are stale (the fixture changed or
	// the case key format changed); they must be reconciled, not ignored.
	ids := sortedIDs(inv)
	for _, id := range ids {
		d := inv[id]
		if d.Untested || !strings.HasPrefix(d.Test, testName) || d.expected == nil {
			continue
		}
		for key := range d.expected {
			if !d.seenCase[key] {
				t.Errorf("STALE expected row %s [%s] in %s: no fixture case with that key was checked", id, key, d.Expected)
			}
		}
	}
	// Summary: individual cases first, then per category.
	var b strings.Builder
	fmt.Fprintf(&b, "golden/known-defect summary (individual cases): PASS=%d XFAIL=%d XPASS=%d FAIL=%d\n",
		tally[Pass], tally[XFail], tally[XPass], tally[Fail])
	perID := map[string]map[Outcome]int{}
	for _, c := range cases {
		if perID[c.id] == nil {
			perID[c.id] = map[Outcome]int{}
		}
		perID[c.id][c.outcome]++
	}
	var idList []string
	for id := range perID {
		idList = append(idList, id)
	}
	sort.Strings(idList)
	b.WriteString("per category (cases): \n")
	for _, id := range idList {
		m := perID[id]
		finding := ""
		if d := inv[id]; d != nil {
			finding = " " + d.Finding
		}
		fmt.Fprintf(&b, "  %-36s PASS=%d XFAIL=%d XPASS=%d FAIL=%d%s\n", id, m[Pass], m[XFail], m[XPass], m[Fail], finding)
	}
	for _, id := range ids {
		d := inv[id]
		if d.Untested {
			fmt.Fprintf(&b, "  UNTESTED %s %s (no test yet; %s)\n", id, d.Finding, d.Reason)
		}
	}
	t.Log(b.String())
	if p := os.Getenv("GB_TEST_SUMMARY"); p != "" {
		if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil { //nolint:gosec // destination chosen by the operator through GB_TEST_SUMMARY
			t.Logf("could not write %s: %v", p, err)
		}
	}
}

func sortedIDs(inv map[string]*Defect) []string {
	var ids []string
	for id := range inv {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func writeExpected(t *testing.T, inv map[string]*Defect, testName string) {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	tallyMu.Lock()
	defer tallyMu.Unlock()
	for _, id := range sortedIDs(inv) {
		d := inv[id]
		if d.Untested || !strings.HasPrefix(d.Test, testName) {
			continue
		}
		rows := observed[id]
		var keys []string
		for k := range rows {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("case,go\n")
		w := csv.NewWriter(&b)
		for _, k := range keys {
			_ = w.Write([]string{k, rows[k]})
		}
		w.Flush()
		p := filepath.Join(root, filepath.FromSlash(d.Expected))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil { //nolint:gosec // path from the inventory under the repository root
			t.Fatal(err)
		}
		t.Logf("wrote %d expected divergent cases to %s", len(keys), d.Expected)
	}
	for id := range observed {
		if inv[id] == nil {
			t.Errorf("%s has mismatching cases but no inventory entry; add one (with a finding) before recording expected values", id)
		}
	}
}

// RequireExercised fails the test if an inventory entry whose Test field
// names the given test (or one of its subtests) was never checked. Finish
// calls it; it is exported for tests that manage their own summary.
func RequireExercised(t *testing.T, testName string) {
	t.Helper()
	inv, err := Load()
	if err != nil {
		t.Fatalf("known-defect inventory unavailable: %v", err)
	}
	tallyMu.Lock()
	defer tallyMu.Unlock()
	for id, d := range inv {
		if d.Untested || !strings.HasPrefix(d.Test, testName) {
			continue
		}
		if !seen[id] {
			t.Errorf("STALE inventory entry %s (%s): test %s ran but never checked it; remove or correct the entry", id, d.Finding, d.Test)
		}
	}
}

// ---- package-level validation ---------------------------------------------

// ValidatePackageInventory is called from a package's TestMain after m.Run()
// and validates every inventory entry owned by that package against what
// actually ran in this process:
//
//   - the entry's top-level test function must exist in the package's test
//     sources (a nonexistent test is an error, never an implicit UNTESTED);
//   - that test must have run to completion (called Finish) in this process;
//   - the entry's id must have been checked, and every expected row seen;
//   - ids checked in this package must belong to entries owned by it;
//   - explicit untested entries are printed so they stay visible.
//
// It returns the number of problems (0 means valid). When the run was
// filtered with -run, or expected files were being rewritten, validation
// cannot be meaningful and is skipped with a loud notice; CI runs the full
// package tests, where it is enforced.
func ValidatePackageInventory(pkg string, w io.Writer) int {
	if f := flag.Lookup("test.run"); f != nil && f.Value.String() != "" {
		_, _ = fmt.Fprintf(w, "inventory validation for %s SKIPPED: -run filter active; run the full package tests to validate the inventory\n", pkg)
		return 0
	}
	if *updateExpected {
		return 0
	}
	inv, err := Load()
	if err != nil {
		_, _ = fmt.Fprintf(w, "inventory validation for %s: %v\n", pkg, err)
		return 1
	}
	// Only the test files Go selected for this platform and build tags count;
	// a Windows-only _test.go file does not define a test on Linux.
	sel, err := SelectedTestFiles(".")
	if err != nil {
		_, _ = fmt.Fprintf(w, "inventory validation for %s: cannot determine selected test files: %v\n", pkg, err)
		return 1
	}
	tests, err := TestFuncsInFiles(sel.Dir, sel.Files)
	if err != nil {
		_, _ = fmt.Fprintf(w, "inventory validation for %s: cannot list test functions: %v\n", pkg, err)
		return 1
	}
	if len(sel.Ignored) > 0 {
		_, _ = fmt.Fprintf(w, "inventory validation for %s: test files excluded on %s/%s: %s\n", pkg, runtime.GOOS, runtime.GOARCH, strings.Join(sel.Ignored, ", "))
	}
	tallyMu.Lock()
	defer tallyMu.Unlock()
	problems := 0
	var validated, finishedTests []string
	report := func(format string, args ...interface{}) {
		problems++
		_, _ = fmt.Fprintf(w, "inventory validation for %s: "+format+"\n", append([]interface{}{pkg}, args...)...)
	}
	owned := map[string]bool{}
	for _, id := range sortedIDs(inv) {
		d := inv[id]
		if d.Untested {
			if d.Package == "" || d.Package == pkg {
				_, _ = fmt.Fprintf(w, "inventory validation for %s: UNTESTED (explicit) %s %s (%s)\n", pkg, id, d.Finding, d.Reason)
			}
			continue
		}
		if d.Package != pkg {
			continue
		}
		owned[id] = true
		top, _, _ := strings.Cut(d.Test, "/")
		if !tests[top] {
			report("entry %s names test %s, but no func %s(t *testing.T) exists in the test files selected for %s/%s", id, d.Test, top, runtime.GOOS, runtime.GOARCH)
			continue
		}
		if !finished[top] {
			report("entry %s: test %s exists but did not run to completion in this process (it must call testutil.Finish)", id, top)
			continue
		}
		if !seen[id] {
			report("STALE entry %s (%s): %s ran but never checked it", id, d.Finding, top)
			continue
		}
		rowsOK := true
		for key := range d.expected {
			if !d.seenCase[key] {
				rowsOK = false
				report("STALE expected row %s [%s] in %s: no case with that key was checked", id, key, d.Expected)
			}
		}
		if rowsOK {
			validated = append(validated, id)
		}
	}
	for id := range seen {
		d := inv[id]
		if d == nil {
			continue // a passing id with no entry: nothing to own
		}
		if !d.Untested && d.Package != pkg {
			report("id %s was checked in this package but its inventory entry is owned by package %q", id, d.Package)
		}
	}
	if problems == 0 {
		_, _ = fmt.Fprintf(w, "inventory validation for %s: %d owned entries valid\n", pkg, len(owned))
		for name := range finished {
			finishedTests = append(finishedTests, name)
		}
		if err := writeRecord(pkg, validated, finishedTests); err != nil {
			_, _ = fmt.Fprintf(w, "inventory validation for %s: cannot write validation record: %v\n", pkg, err)
			return 1
		}
	}
	return problems
}

// Record is the completion record a package validator writes when
// GB_INVENTORY_RECORDS names a directory. tools/inventorycheck reconciles the
// inventory against these records after the whole test run, so that an entry
// is only considered covered when its owning package actually ran on this
// platform and validated it.
type Record struct {
	Package   string   `json:"package"`
	GOOS      string   `json:"goos"`
	GOARCH    string   `json:"goarch"`
	Entries   []string `json:"entries"`  // ids validated (checked, all expected rows seen)
	Finished  []string `json:"finished"` // top-level tests that called Finish
	Timestamp string   `json:"timestamp"`
}

func writeRecord(pkg string, validated, finishedTests []string) error {
	dir := os.Getenv("GB_INVENTORY_RECORDS")
	if dir == "" {
		return nil
	}
	sort.Strings(validated)
	sort.Strings(finishedTests)
	rec := Record{Package: pkg, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Entries: validated, Finished: finishedTests, Timestamp: time.Now().UTC().Format(time.RFC3339)}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // destination chosen by the operator through GB_INVENTORY_RECORDS
		return err
	}
	name := strings.ReplaceAll(pkg, "/", "__") + ".json"
	return os.WriteFile(filepath.Join(dir, name), raw, 0o600) //nolint:gosec // destination chosen by the operator through GB_INVENTORY_RECORDS
}

// Selection describes the test files Go selected for a package on the
// current platform (from `go list -json -test`).
type Selection struct {
	ImportPath string
	Dir        string
	Files      []string // TestGoFiles + XTestGoFiles
	Ignored    []string // IgnoredGoFiles (build constraints exclude them here)
}

// SelectedTestFiles runs `go list -json -test` for the package at dir (a
// directory path, or an import pattern) and returns the selected test files.
// It reflects the real build constraints of GOOS, GOARCH, GOFLAGS (for
// example -tags) and other build constraints instead of a directory listing,
// so a platform- or tag-specific test file cannot satisfy an inventory entry
// in a run where it is excluded.
func SelectedTestFiles(dir string) (*Selection, error) {
	// Run in the package directory with the inherited environment so that
	// GOOS, GOARCH, GOFLAGS (including -tags) and the module context are
	// exactly those of the invoking test run; nothing is overridden.
	cmd := exec.Command("go", "list", "-json", "-test", ".") //nolint:gosec // fixed command; dir is a package directory chosen by the test code
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		cmd.Dir = dir
	} else {
		cmd = exec.Command("go", "list", "-json", "-test", dir) //nolint:gosec // import pattern chosen by the test code
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("go list %s: %v: %s", dir, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("go list %s: %w", dir, err)
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var pkg struct {
			ImportPath     string
			Dir            string
			ForTest        string
			TestGoFiles    []string
			XTestGoFiles   []string
			IgnoredGoFiles []string
		}
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("go list %s: %w", dir, err)
		}
		if pkg.ForTest != "" || strings.HasSuffix(pkg.ImportPath, ".test") {
			continue
		}
		sel := &Selection{ImportPath: pkg.ImportPath, Dir: pkg.Dir}
		sel.Files = append(sel.Files, pkg.TestGoFiles...)
		sel.Files = append(sel.Files, pkg.XTestGoFiles...)
		for _, f := range pkg.IgnoredGoFiles {
			if strings.HasSuffix(f, "_test.go") {
				sel.Ignored = append(sel.Ignored, f)
			}
		}
		return sel, nil
	}
	return nil, fmt.Errorf("go list %s: no package in output", dir)
}

// TestFuncsInFiles parses the given files in dir and returns the names of
// the top-level test functions (func TestXxx(t *testing.T)) they define.
func TestFuncsInFiles(dir string, files []string) (map[string]bool, error) {
	out := map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			if fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
				continue
			}
			star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			sel, ok := star.X.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "T" {
				continue
			}
			out[fn.Name.Name] = true
		}
	}
	return out, nil
}

// FilesDeclareValidator reports whether one of the given files contains a
// TestMain that calls ValidatePackageInventory for exactly pkg.
func FilesDeclareValidator(dir string, files []string, pkg string) bool {
	want := `ValidatePackageInventory("` + pkg + `"`
	for _, name := range files {
		src, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // test sources selected by go list
		if err != nil {
			continue
		}
		if strings.Contains(string(src), "func TestMain(m *testing.M)") && strings.Contains(string(src), want) {
			return true
		}
	}
	return false
}
