package universe

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

func digest(t *testing.T, u *Universe) string {
	t.Helper()
	h := sha256.New()
	enc := json.NewEncoder(h)
	if err := enc.Encode(u.StarData); err != nil {
		t.Fatal(err)
	}
	for _, s := range u.Stars {
		if err := enc.Encode(s); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range u.Planets {
		if err := enc.Encode(p); err != nil {
			t.Fatal(err)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestGenerateIsDeterministicPerSeed(t *testing.T) {
	p := Params{Stars: 12, MinPlanets: 1, MaxPlanets: 4}
	a, err := Generate(p, 42)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Generate(p, 42)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Generate(p, 43)
	if err != nil {
		t.Fatal(err)
	}
	if digest(t, a) != digest(t, b) {
		t.Fatal("same seed produced different universes")
	}
	if digest(t, a) == digest(t, c) {
		t.Fatal("different seeds produced the same universe")
	}
	if len(a.Stars) != 12 || a.StarData.NumStars != 12 {
		t.Fatalf("stars = %d / %d", len(a.Stars), a.StarData.NumStars)
	}
	for _, s := range a.Stars {
		if s.NumPlanets < 1 || s.NumPlanets > 4 {
			t.Fatalf("star %d has %d planets", s.Number, s.NumPlanets)
		}
		if s.Name == "" {
			t.Fatalf("star %d unnamed", s.Number)
		}
	}
	for _, pl := range a.Planets {
		if int(pl.MaxX)*int(pl.MaxY) != len(pl.Sectors) {
			t.Fatalf("planet %d/%d sector count mismatch", pl.StarNum, pl.PlanetNum)
		}
		if pl.MaxY%2 == 0 && pl.Type != model.PlanetAsteroid {
			t.Fatalf("planet %d/%d has even MaxY %d", pl.StarNum, pl.PlanetNum, pl.MaxY)
		}
	}
}

func TestNameLists(t *testing.T) {
	p := Params{Stars: 3, MinPlanets: 1, MaxPlanets: 1, StarNames: []string{"Sol", "Vega", "Deneb"}, PlanetNames: []string{"Terra"}}
	u, err := Generate(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range u.Stars {
		seen[s.Name] = true
	}
	if !seen["Sol"] || !seen["Vega"] || !seen["Deneb"] {
		t.Fatalf("star names not used: %v", seen)
	}
}

func TestParamsValidation(t *testing.T) {
	for _, p := range []Params{{Stars: 0, MinPlanets: 1, MaxPlanets: 1}, {Stars: 256}, {Stars: 5, MinPlanets: 11}, {Stars: 5, MinPlanets: 3, MaxPlanets: 2}} {
		if p.Stars == 0 {
			continue // zero means default
		}
		if _, err := Generate(p, 1); err == nil {
			t.Errorf("params %+v should be rejected", p)
		}
	}
	if _, err := Generate(Params{}, 1); err != nil {
		t.Errorf("defaults must validate: %v", err)
	}
}

func TestInitRefusesInitializedStore(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	init, err := store.IsInitialized()
	if err != nil || init {
		t.Fatalf("fresh store reported initialised=%v err=%v", init, err)
	}
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	rec, err := Init(store, Params{Stars: 5, MinPlanets: 1, MaxPlanets: 2}, 7, "test", now)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seed != 7 || rec.Stars != 5 || rec.Planets == 0 || !rec.InitializedAt.Equal(now) || rec.Tool != "test" {
		t.Fatalf("record = %+v", rec)
	}
	got, err := store.GetUniverseRecord()
	if err != nil || got == nil || got.Seed != 7 {
		t.Fatalf("stored record = %+v err=%v", got, err)
	}
	stars, planets, _, _, err := store.Counts()
	if err != nil || stars != 5 || planets != rec.Planets {
		t.Fatalf("counts stars=%d planets=%d err=%v", stars, planets, err)
	}

	// Second initialisation must be refused and must change nothing.
	_, err = Init(store, Params{Stars: 9, MinPlanets: 1, MaxPlanets: 1}, 8, "test", now)
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("expected ErrAlreadyInitialized, got %v", err)
	}
	got, _ = store.GetUniverseRecord()
	stars2, planets2, _, _, _ := store.Counts()
	if got.Seed != 7 || stars2 != 5 || planets2 != planets {
		t.Fatal("refused initialisation modified the store")
	}
}

func TestLegacyStoreWithoutRecordCountsAsInitialized(t *testing.T) {
	// A database written by the pre-Phase-1 makeuniv has stars but no record.
	store, err := db.Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.PutStarData(&model.StarData{NumStars: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutStar(&model.Star{Number: 0, Name: "Old"}); err != nil {
		t.Fatal(err)
	}
	init, err := store.IsInitialized()
	if err != nil || !init {
		t.Fatalf("legacy store must count as initialised: %v %v", init, err)
	}
	if _, err := Init(store, Params{Stars: 2, MinPlanets: 1, MaxPlanets: 1}, 1, "test", time.Now()); !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("expected refusal on legacy store, got %v", err)
	}
	// StarData alone (no star records) also counts.
	store2, err := db.Open(filepath.Join(t.TempDir(), "legacy2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store2.Close() }()
	if err := store2.PutStarData(&model.StarData{NumStars: 3}); err != nil {
		t.Fatal(err)
	}
	if init, _ := store2.IsInitialized(); !init {
		t.Fatal("StarData.NumStars>0 must count as initialised")
	}
}
