// Command inventorycheck reconciles the known-defect inventory with what the
// test run actually executed. Each inventory-owning package writes a
// completion record (testutil.Record) from its TestMain when
// GB_INVENTORY_RECORDS is set; this tool requires, for every tested entry,
// that its owning package produced a record on this platform in this run,
// that the entry is listed as validated in it, and that the entry's top-level
// test is listed as finished. Static source inspection is not used as
// evidence of execution.
//
// With -root it additionally runs `go list -json -test` for each owning
// package and prints the test files Go selected and ignored on this platform,
// so platform-specific exclusions are visible, and fails when a package has
// no selected test files.
//
// Exit codes: 0 all tested entries reconciled; 1 problems; 2 usage or input
// errors (fail closed).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/phreddrick/GB/go/internal/testutil"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout))
}

func run(args []string, w *os.File) int {
	var records, inventory, root string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-records":
			i++
			if i < len(args) {
				records = args[i]
			}
		case "-inventory":
			i++
			if i < len(args) {
				inventory = args[i]
			}
		case "-root":
			i++
			if i < len(args) {
				root = args[i]
			}
		default:
			_, _ = fmt.Fprintf(w, "inventorycheck: unknown flag %q\n", args[i])
			return 2
		}
	}
	if records == "" {
		_, _ = fmt.Fprintln(w, "usage: inventorycheck -records DIR [-inventory FILE] [-root REPO]")
		return 2
	}
	if inventory == "" {
		if root == "" {
			_, _ = fmt.Fprintln(w, "inventorycheck: -inventory or -root is required")
			return 2
		}
		inventory = filepath.Join(root, "test", "knowndefects.yaml")
	}
	inv, err := testutil.ParseInventoryFile(inventory)
	if err != nil {
		_, _ = fmt.Fprintf(w, "inventorycheck: inventory: %v\n", err)
		return 2
	}
	recs, err := loadRecords(records)
	if err != nil {
		_, _ = fmt.Fprintf(w, "inventorycheck: records: %v\n", err)
		return 2
	}
	problems := 0
	report := func(format string, a ...interface{}) {
		problems++
		_, _ = fmt.Fprintf(w, "inventorycheck: "+format+"\n", a...)
	}
	ids := make([]string, 0, len(inv))
	for id := range inv {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	checkedPkgs := map[string]bool{}
	for _, id := range ids {
		d := inv[id]
		if d.Untested {
			_, _ = fmt.Fprintf(w, "inventorycheck: UNTESTED (explicit) %s %s: %s\n", id, d.Finding, d.Reason)
			continue
		}
		rec, ok := recs[d.Package]
		if !ok {
			report("entry %s is owned by package %s, but that package produced no validation record in this run (its tests did not execute, or its TestMain validator did not run or did not succeed)", id, d.Package)
			continue
		}
		if rec.GOOS != runtime.GOOS || rec.GOARCH != runtime.GOARCH {
			report("entry %s: record for %s is from %s/%s, this run is %s/%s", id, d.Package, rec.GOOS, rec.GOARCH, runtime.GOOS, runtime.GOARCH)
			continue
		}
		top, _, _ := strings.Cut(d.Test, "/")
		if !contains(rec.Finished, top) {
			report("entry %s: test %s did not run to completion in package %s during this run", id, top, d.Package)
			continue
		}
		if !contains(rec.Entries, id) {
			report("entry %s was not validated by package %s during this run", id, d.Package)
			continue
		}
		if root != "" && !checkedPkgs[d.Package] {
			checkedPkgs[d.Package] = true
			sel, err := testutil.SelectedTestFiles(filepath.Join(root, "go", filepath.FromSlash(d.Package)))
			if err != nil {
				report("package %s: go list failed: %v", d.Package, err)
				continue
			}
			_, _ = fmt.Fprintf(w, "inventorycheck: package %s on %s/%s: selected test files %v, excluded %v\n", d.Package, runtime.GOOS, runtime.GOARCH, sel.Files, sel.Ignored)
			if len(sel.Files) == 0 {
				report("package %s has no test files selected on this platform", d.Package)
			}
		}
	}
	if problems == 0 {
		_, _ = fmt.Fprintf(w, "inventorycheck: all tested entries reconciled with %d validation record(s) on %s/%s\n", len(recs), runtime.GOOS, runtime.GOARCH)
		return 0
	}
	return 1
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func loadRecords(dir string) (map[string]testutil.Record, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]testutil.Record{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // records directory chosen by the operator
		if err != nil {
			return nil, err
		}
		var r testutil.Record
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if r.Package == "" {
			return nil, fmt.Errorf("%s: record without package", e.Name())
		}
		out[r.Package] = r
	}
	return out, nil
}
