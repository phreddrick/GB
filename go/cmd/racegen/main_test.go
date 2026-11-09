package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phreddrick/GB/go/internal/racegen"
)

func TestCLIDefaultsAndSharedValidation(t *testing.T) {
	r := defaultRace()
	if r.spec().Attributes != racegen.Default().Attributes || r.compat != racegen.Default().SectorCompat || r.address != "Unknown" {
		t.Fatal("CLI defaults changed")
	}
	if len(validate(&r, false)) != 0 {
		t.Fatal("unfinished default should still allow quitting")
	}
	r.name = "Test"
	r.compat[1] = 100
	if errs := validate(&r, true); len(errs) > 0 {
		t.Fatal(errs)
	}
	r.attr[attrFight] = 20
	if errs := validate(&r, true); !strings.Contains(strings.Join(errs, ";"), "point budget") {
		t.Fatal("CLI did not use authoritative budget")
	}
	r = defaultRace()
	r.compat[1] = 100
	r.attr[attrIQ] = 150.5
	if errs := validate(&r, false); !strings.Contains(strings.Join(errs, ";"), "attributes.iq") {
		t.Fatal("CLI did not use shared integer validation")
	}
	r.attr[attrIQ] = 150
	if errs := validate(&r, true); len(errs) != 0 {
		t.Fatal("offline designer must not require a password", errs)
	}

}

func TestCLIExistingModifyCommands(t *testing.T) {
	r := defaultRace()
	for _, tc := range []struct {
		field, value string
		index        racegen.Attribute
		want         float64
	}{
		{"adventurism", "0.5", attrAdvent, .5}, {"birthrate", "0.7", attrBirth, .7},
		{"collectiveiq", "1", attrColIQ, 1}, {"collective_iq", "0", attrColIQ, 0},
		{"fertilize", "0.5", attrFert, .5}, {"iq", "125", attrIQ, 125},
		{"fight", "5", attrFight, 5}, {"fighters", "4", attrFight, 4},
		{"mass", "1.2", attrMass, 1.2}, {"sexes", "3", attrSexes, 3},
		{"metabolism", "1.5", attrMetab, 1.5},
	} {
		doModify(&r, []string{tc.field, tc.value})
		if r.attr[tc.index] != tc.want {
			t.Errorf("%s alias failed", tc.field)
		}
	}
	doModify(&r, []string{"planet", "JoViAn"})
	if r.homePlanetType != 6 || r.compat != ([8]float64{0, 0, 0, 100}) {
		t.Fatal("Jovian transition changed")
	}
	doModify(&r, []string{"planet", "Airless"})
	if r.homePlanetType != 4 || r.compat[3] != 0 || r.compat[7] != 100 {
		t.Fatal("non-Jovian transition changed")
	}
	doModify(&r, []string{"race", "metamorph"})
	doModify(&r, []string{"absorb", "1"})
	doModify(&r, []string{"pods", "1"})
	if r.raceType != 1 || r.attr[attrAbsorb] != 1 || r.attr[attrPods] != 1 {
		t.Fatal("metamorph editing changed")
	}
	doModify(&r, []string{"race", "normal"})
	if r.raceType != 0 || r.attr[attrAbsorb] != 0 || r.attr[attrPods] != 0 {
		t.Fatal("normal reset changed")
	}
}

func TestCLINonfiniteInput(t *testing.T) {
	for _, input := range []string{"NaN", "+Inf", "-Inf", "1e999", "garbage"} {
		if v, ok := parseFloat(input); ok {
			t.Errorf("accepted %q as %g", input, v)
		}
	}
	r := defaultRace()
	before := r
	doModify(&r, []string{"mass", "NaN"})
	if r != before {
		t.Fatal("nonfinite input changed draft")
	}
	// A numeric out-of-range edit remains a draft with an error, never a panic.
	doModify(&r, []string{"mass", "1000"})
	if r.attr[attrMass] != 1000 || len(validate(&r, true)) == 0 {
		t.Fatal("invalid draft accepted")
	}
	r.attr[attrMass] = math.Inf(1)
	if len(validate(&r, true)) == 0 {
		t.Fatal("nonfinite draft accepted")
	}
}

func TestRetiredDatabaseFlagAndSpecRoundTrip(t *testing.T) {
	var out, errout bytes.Buffer
	path := filepath.Join(t.TempDir(), "world.db")
	if code := runDesigner([]string{"-db", path}, strings.NewReader(""), &out, &errout); code == 0 {
		t.Fatal("legacy db flag accepted")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("database touched", e)
	}
	spec := racegen.Default()
	spec.Name = "Round trip"
	spec.Attributes.Fighters = 7
	spec.Attributes.Fertilize = .25
	spec.SectorCompat[racegen.Land] = 100
	b, e := json.Marshal(spec)
	if e != nil {
		t.Fatal(e)
	}
	input := filepath.Join(t.TempDir(), "input.json")
	if e = os.WriteFile(input, b, 0600); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(t.TempDir(), "output.json")
	if code := runDesigner([]string{"-load", input, "-save", output}, strings.NewReader(""), &out, &errout); code != 0 {
		t.Fatal(code, errout.String())
	}
	b, e = os.ReadFile(output) //nolint:gosec // fixture path under t.TempDir
	if e != nil {
		t.Fatal(e)
	}
	var got racegen.Spec
	if json.Unmarshal(b, &got) != nil || got != spec {
		t.Fatal("lost full spec")
	}
}
