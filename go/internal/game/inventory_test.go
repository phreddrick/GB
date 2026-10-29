package game

import (
	"os"
	"testing"

	"github.com/phreddrick/GB/go/internal/testutil"
)

// TestMain runs the package tests and then validates every known-defect
// inventory entry owned by internal/game against what actually ran: the named
// test must exist, must have completed, and must have checked the entry and
// all of its expected cases (docs/testing/known-defects.md, GB0-08).
func TestMain(m *testing.M) {
	code := m.Run()
	if n := testutil.ValidatePackageInventory("internal/game", os.Stderr); n > 0 && code == 0 {
		code = 1
	}
	os.Exit(code)
}
