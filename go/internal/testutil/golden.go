package testutil

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Row is one CSV fixture row keyed by column name.
type Row map[string]string

// Int parses an integer column, failing the test on a malformed value.
func (r Row) Int(t *testing.T, col string) int {
	t.Helper()
	v, err := strconv.Atoi(r.mustGet(t, col))
	if err != nil {
		t.Fatalf("column %q: %v", col, err)
	}
	return v
}

// Float parses a float column.
func (r Row) Float(t *testing.T, col string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(r.mustGet(t, col), 64)
	if err != nil {
		t.Fatalf("column %q: %v", col, err)
	}
	return v
}

// Str returns a string column.
func (r Row) Str(t *testing.T, col string) string {
	t.Helper()
	return r.mustGet(t, col)
}

func (r Row) mustGet(t *testing.T, col string) string {
	t.Helper()
	v, ok := r[col]
	if !ok {
		t.Fatalf("fixture has no column %q", col)
	}
	return v
}

// LoadGolden reads test/golden/testdata/<name>.csv. A missing fixture is a
// hard failure with instructions: the fixtures are generated from the C
// reference by test/ctool and must be present (committed) for the golden
// tests to mean anything.
func LoadGolden(t *testing.T, name string) []Row {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	path := filepath.Join(root, "test", "golden", "testdata", name+".csv")
	f, err := os.Open(path) //nolint:gosec // fixture path is derived from the repository root
	if err != nil {
		t.Fatalf("golden fixture %s is missing (%v). Generate it from the C reference with: make -C test/ctool fixtures (needs gcc and make).", path, err)
	}
	defer func() { _ = f.Close() }()
	rd := csv.NewReader(f)
	recs, err := rd.ReadAll()
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if len(recs) < 2 {
		t.Fatalf("%s: no data rows", path)
	}
	hdr := recs[0]
	rows := make([]Row, 0, len(recs)-1)
	for _, rec := range recs[1:] {
		row := Row{}
		for i, h := range hdr {
			row[h] = rec[i]
		}
		rows = append(rows, row)
	}
	return rows
}

// FloatClose reports whether two floating-point results agree to within a
// relative tolerance (plus a tiny absolute floor near zero). The reference
// values come from glibc's libm; Go's math package may differ in the last
// bits, which is not a game-rule divergence.
func FloatClose(a, b, relTol float64) bool {
	if a == b {
		return true
	}
	diff := math.Abs(a - b)
	scale := math.Max(math.Abs(a), math.Abs(b))
	return diff <= relTol*scale || diff <= 1e-12
}

// U8, U16 and I16 narrow fixture integers with a range check, so that a
// malformed fixture fails the test instead of wrapping silently.
func (r Row) U8(t *testing.T, col string) uint8 {
	t.Helper()
	v := r.Int(t, col)
	if v < 0 || v > 255 {
		t.Fatalf("column %q: %d does not fit uint8", col, v)
	}
	return uint8(v) //nolint:gosec // range checked above
}

// U16 narrows to uint16 with a range check.
func (r Row) U16(t *testing.T, col string) uint16 {
	t.Helper()
	v := r.Int(t, col)
	if v < 0 || v > 65535 {
		t.Fatalf("column %q: %d does not fit uint16", col, v)
	}
	return uint16(v) //nolint:gosec // range checked above
}

// I16 narrows to int16 with a range check.
func (r Row) I16(t *testing.T, col string) int16 {
	t.Helper()
	v := r.Int(t, col)
	if v < -32768 || v > 32767 {
		t.Fatalf("column %q: %d does not fit int16", col, v)
	}
	return int16(v) //nolint:gosec // range checked above
}

// Describe formats a mismatch for the Check detail string.
func Describe(what string, got, want interface{}, row Row) string {
	return fmt.Sprintf("%s: go=%v c=%v inputs=%v", what, got, want, row)
}
