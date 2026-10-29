// Command growthgate enforces review of changes to the expected-failure
// allowances: the known-defect inventory (test/knowndefects.yaml), the per-case
// expected-divergence files (test/golden/expected/*.csv) and the static-analysis
// baseline (docs/testing/lint-baseline.txt).
//
// It parses the base and head snapshots canonically (the inventory with the
// same parser the tests use, the CSVs by case key and value, the baseline by
// line) and classifies every semantic difference:
//
//	growth        new inventory entry, new expected case, new baseline line
//	modification  an existing entry's test, package, expected file, finding,
//	              untested status, reason or phase changed; an existing
//	              expected case's Go value changed
//	reduction     entries, cases or baseline lines removed
//
// Growth and modification require an acknowledgment; reductions never do. The
// acknowledgment is a single boolean, -approved, that the caller sets ONLY from
// the pull-request label known-defect-added applied by the repository's sole
// maintainer (test/scripts/growth-gate.sh, docs/testing/known-defects.md). It
// is a deliberate owner acknowledgment of each listed change, not an
// independent review. Commit messages, trailers, branch names and pull-request
// text are never consulted. Without it the gate fails (exit 1); unreadable
// inputs are fatal (exit 2); a file absent in the base snapshot is treated as
// empty, so first introduction is growth.
//
// Usage: growthgate -base DIR -head DIR [-approved]
package main

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/phreddrick/GB/go/internal/testutil"
)

const (
	exitOK     = 0
	exitGrowth = 1
	exitFatal  = 2

	inventoryPath = "test/knowndefects.yaml"
	expectedDir   = "test/golden/expected"
	baselinePath  = "docs/testing/lint-baseline.txt"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type snapshot struct {
	entries  map[string]*testutil.Defect
	expected map[string]map[string]string // file -> case key -> go value
	baseline map[string]int               // line -> count
}

func run(args []string, stdout, stderr *os.File) int {
	var baseDir, headDir string
	approved := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-base":
			i++
			if i < len(args) {
				baseDir = args[i]
			}
		case "-head":
			i++
			if i < len(args) {
				headDir = args[i]
			}
		case "-approved":
			approved = true
		default:
			_, _ = fmt.Fprintf(stderr, "growthgate: unknown flag %q\n", args[i])
			return exitFatal
		}
	}
	if baseDir == "" || headDir == "" {
		_, _ = fmt.Fprintln(stderr, "usage: growthgate -base DIR -head DIR [-approved]")
		return exitFatal
	}
	base, err := load(baseDir, true)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "growthgate: FATAL: base snapshot:", err)
		return exitFatal
	}
	head, err := load(headDir, false)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "growthgate: FATAL: head snapshot:", err)
		return exitFatal
	}
	d := diff(base, head)
	switch {
	case len(d.growth) == 0 && len(d.modified) == 0 && len(d.reduction) == 0:
		_, _ = fmt.Fprintln(stdout, "growthgate: expected-failure allowances unchanged")
		return exitOK
	case len(d.growth) == 0 && len(d.modified) == 0:
		_, _ = fmt.Fprintf(stdout, "growthgate: allowances reduced only (%d removals); no approval needed\n", len(d.reduction))
		for _, l := range d.reduction {
			_, _ = fmt.Fprintln(stdout, "  -", l)
		}
		return exitOK
	}
	_, _ = fmt.Fprintf(stdout, "growthgate: allowances CHANGED in ways that need review (%d additions, %d modifications, %d removals)\n", len(d.growth), len(d.modified), len(d.reduction))
	for _, l := range d.growth {
		_, _ = fmt.Fprintln(stdout, "  +", l)
	}
	for _, l := range d.modified {
		_, _ = fmt.Fprintln(stdout, "  ~", l)
	}
	for _, l := range d.reduction {
		_, _ = fmt.Fprintln(stdout, "  -", l)
	}
	if approved {
		_, _ = fmt.Fprintln(stdout, "growthgate: change acknowledged by the maintainer through the pull-request label known-defect-added (policy: docs/testing/known-defects.md)")
		return exitOK
	}
	_, _ = fmt.Fprintln(stdout, "growthgate: change is NOT acknowledged. Open a pull request; the maintainer applies the label known-defect-added after checking each addition and modification. Commit messages and trailers are not consulted.")
	return exitGrowth
}

func load(dir string, allowMissing bool) (*snapshot, error) {
	s := &snapshot{entries: map[string]*testutil.Defect{}, expected: map[string]map[string]string{}, baseline: map[string]int{}}
	invPath := filepath.Join(dir, inventoryPath)
	if _, err := os.Stat(invPath); err == nil { //nolint:gosec // snapshot directory chosen by the CI script
		inv, err := testutil.ParseInventoryFile(invPath)
		if err != nil {
			return nil, err
		}
		s.entries = inv
	} else if !allowMissing || !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	matches, _ := filepath.Glob(filepath.Join(dir, expectedDir, "*.csv"))
	for _, m := range matches {
		rel, _ := filepath.Rel(dir, m)
		rel = filepath.ToSlash(rel)
		f, err := os.Open(m) //nolint:gosec // paths come from a directory listing of the snapshot
		if err != nil {
			return nil, err
		}
		recs, err := csv.NewReader(f).ReadAll()
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		if len(recs) == 0 || len(recs[0]) != 2 || recs[0][0] != "case" || recs[0][1] != "go" {
			return nil, fmt.Errorf("%s: expected header case,go", rel)
		}
		cases := map[string]string{}
		for i, r := range recs[1:] {
			if len(r) != 2 {
				return nil, fmt.Errorf("%s: line %d is malformed", rel, i+2)
			}
			if _, dup := cases[r[0]]; dup {
				return nil, fmt.Errorf("%s: duplicate case %q", rel, r[0])
			}
			cases[r[0]] = r[1]
		}
		s.expected[rel] = cases
	}
	if err := readLines(filepath.Join(dir, baselinePath), allowMissing, func(line string) error {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			return nil
		}
		s.baseline[line]++
		return nil
	}); err != nil {
		return nil, err
	}
	return s, nil
}

func readLines(p string, allowMissing bool, fn func(string) error) error {
	f, err := os.Open(p) //nolint:gosec // repository-relative paths under the snapshot directory
	if err != nil {
		if allowMissing && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		if err := fn(sc.Text()); err != nil {
			return err
		}
	}
	return sc.Err()
}

type changes struct {
	growth, modified, reduction []string
}

func diff(base, head *snapshot) changes {
	var c changes
	for id, h := range head.entries {
		b, ok := base.entries[id]
		if !ok {
			c.growth = append(c.growth, "inventory entry "+id)
			continue
		}
		for _, m := range entryChanges(b, h) {
			c.modified = append(c.modified, "inventory entry "+id+": "+m)
		}
	}
	for id := range base.entries {
		if _, ok := head.entries[id]; !ok {
			c.reduction = append(c.reduction, "inventory entry "+id)
		}
	}
	for file, cases := range head.expected {
		for k, v := range cases {
			bv, ok := base.expected[file][k]
			switch {
			case !ok:
				c.growth = append(c.growth, fmt.Sprintf("expected case %s [%s] (go=%s)", file, k, v))
			case bv != v:
				c.modified = append(c.modified, fmt.Sprintf("expected case %s [%s]: go value %s -> %s", file, k, bv, v))
			}
		}
	}
	for file, cases := range base.expected {
		for k := range cases {
			if _, ok := head.expected[file][k]; !ok {
				c.reduction = append(c.reduction, fmt.Sprintf("expected case %s [%s]", file, k))
			}
		}
	}
	for k, n := range head.baseline {
		if n > base.baseline[k] {
			c.growth = append(c.growth, fmt.Sprintf("lint baseline line (x%d) %s", n-base.baseline[k], strings.ReplaceAll(k, "\t", " | ")))
		}
	}
	for k, n := range base.baseline {
		if n > head.baseline[k] {
			c.reduction = append(c.reduction, fmt.Sprintf("lint baseline line (x%d) %s", n-head.baseline[k], strings.ReplaceAll(k, "\t", " | ")))
		}
	}
	sort.Strings(c.growth)
	sort.Strings(c.modified)
	sort.Strings(c.reduction)
	return c
}

// entryChanges lists the semantic fields of an inventory entry that differ.
// Every field except the date is part of the allowance's definition: what is
// tested, by whom, against which expected values, under which finding, and
// whether it is tested at all.
func entryChanges(b, h *testutil.Defect) []string {
	var out []string
	cmp := func(name, bv, hv string) {
		if bv != hv {
			out = append(out, fmt.Sprintf("%s %q -> %q", name, bv, hv))
		}
	}
	cmp("test", b.Test, h.Test)
	cmp("package", b.Package, h.Package)
	cmp("expected", b.Expected, h.Expected)
	cmp("finding", b.Finding, h.Finding)
	cmp("reason", b.Reason, h.Reason)
	cmp("phase_to_fix", b.PhaseToFix, h.PhaseToFix)
	cmp("untested", fmt.Sprint(b.Untested), fmt.Sprint(h.Untested))
	return out
}
