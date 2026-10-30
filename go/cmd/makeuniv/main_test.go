package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phreddrick/GB/go/internal/db"
)

func TestMakeunivRefusesInitializedDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gb.db")
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devnull.Close() }()

	if code := run([]string{"-db", dbPath, "-s", "4", "-l", "1", "-m", "2", "-seed", "99"}, devnull, devnull); code != 0 {
		t.Fatalf("first run exit %d", code)
	}
	if code := run([]string{"-db", dbPath, "-s", "4", "-l", "1", "-m", "2", "-seed", "100"}, devnull, devnull); code != 3 {
		t.Fatalf("second run must refuse with exit 3, got %d", code)
	}
	if code := run([]string{"-db", dbPath, "-skip-if-initialized"}, devnull, devnull); code != 0 {
		t.Fatalf("-skip-if-initialized must exit 0, got %d", code)
	}
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	rec, err := store.GetUniverseRecord()
	if err != nil || rec == nil || rec.Seed != 99 || rec.Stars != 4 || rec.Tool != "makeuniv" {
		t.Fatalf("record after refusals = %+v err=%v", rec, err)
	}
	if code := run([]string{"-db", dbPath, "-s", "300"}, devnull, devnull); code != 1 {
		t.Fatalf("bad params must exit 1, got %d", code)
	}
}
