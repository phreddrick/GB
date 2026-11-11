package commandrules

import (
	"encoding/json"
	"errors"
	"github.com/phreddrick/GB/go/internal/model"
	"math"
	"reflect"
	"testing"
)

func TestTelescopeRulesDefinedSemantics(t *testing.T) {
	for _, typ := range []model.ShipType{model.OtypeStele, model.OtypeGtele} {
		for _, level := range []uint8{model.LevelUniv, model.LevelShip, model.LevelStar, model.LevelPlan} {
			sh := &model.Ship{Type: typ, Fuel: 10, Mass: 100, Special: model.ShipSpecial{AimedAt: &model.AimedAtSpecial{Intensity: 37}}}
			place := model.Place{Level: level}
			var st *model.Star
			var pl *model.Planet
			if level == model.LevelShip {
				place.ShipNo = 65535
			}
			if level == model.LevelStar || level == model.LevelPlan {
				st = &model.Star{NumPlanets: 1, XPos: 3, YPos: 4}
			}
			if level == model.LevelPlan {
				pl = &model.Planet{XPos: 3, YPos: 4}
			}
			before, _ := json.Marshal([]any{sh, place, st, pl})
			got, e := PlanTelescope(sh, place, st, pl, .3, .05)
			if e != nil {
				t.Fatal(e)
			}
			fuel, mass, rng := 9.7, 99.985, 1500.
			if typ == model.OtypeGtele {
				fuel = 10
				mass = 100
				rng = 400
			}
			if got.Fuel != fuel || got.Mass != mass || got.Aim.Intensity != 37 || got.Aim.Level != level || uint32(got.Aim.ShipNo) != place.ShipNo || got.Range != rng {
				t.Fatal("defined aim arithmetic changed")
			}
			d := 0.
			if level == model.LevelStar {
				d = 5
			}
			if level == model.LevelPlan {
				d = 10
			}
			if got.Distance != d || got.Explore != (level == model.LevelStar || level == model.LevelPlan) {
				t.Fatal("geometry or exploration changed")
			}
			after, _ := json.Marshal([]any{sh, place, st, pl})
			if !reflect.DeepEqual(before, after) {
				t.Fatal("pure rules mutated input")
			}
		}
	}
	// Explicitly document the pre-existing Go/C offset discrepancy. These literals
	// are source constants, not an invented C harness or a relaxed parity allowance.
	if TelescopeRange(model.OtypeGtele, 0) != 400 || TelescopeRange(model.OtypeStele, 0) != 1500 {
		t.Fatal("current Go telescope range changed")
	}
	cGravity, cSurvey := 2000/8, 2000/3
	if cGravity != 250 || cSurvey != 666 {
		t.Fatal("original C integer-offset reference changed")
	}
	for _, tech := range []float64{0, 1, 10, 100, 10000} {
		if TelescopeRange(model.OtypeStele, tech) != math.Log1p(tech)*1500+model.SystemGravConst/100 || TelescopeRange(model.OtypeGtele, tech) != math.Log1p(tech)*400+model.SystemGravConst/375 {
			t.Fatal("extraction changed existing Go range arithmetic")
		}
	}
}
func TestTelescopeRulesFiniteInputsFuelAndBoundaries(t *testing.T) {
	for _, n := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
		for _, field := range []string{"tech", "x", "fuel", "mass", "rate", "massrate"} {
			sh := &model.Ship{Type: model.OtypeStele, Fuel: 10, Mass: 100}
			a, b := .3, .05
			switch field {
			case "tech":
				sh.Tech = n
			case "x":
				sh.XPos = n
			case "fuel":
				sh.Fuel = n
			case "mass":
				sh.Mass = n
			case "rate":
				a = n
			case "massrate":
				b = n
			}
			_, e := PlanTelescope(sh, model.Place{Level: model.LevelUniv}, nil, nil, a, b)
			if field == "x" && n == -1 {
				if e != nil {
					t.Fatal("valid negative coordinate rejected")
				}
			} else if e == nil {
				t.Fatal("invalid numeric input accepted", field)
			}
		}
	}
	sh := &model.Ship{Type: model.OtypeStele, Fuel: math.Nextafter(.3, 0), Mass: 100}
	if _, e := PlanTelescope(sh, model.Place{Level: model.LevelUniv}, nil, nil, .3, .05); !errors.Is(e, ErrFuel) {
		t.Fatal("fuel lower boundary accepted")
	}
	sh.Fuel = .3
	if got, e := PlanTelescope(sh, model.Place{Level: model.LevelUniv}, nil, nil, .3, .05); e != nil || got.Fuel != 0 {
		t.Fatal("exact fuel boundary rejected")
	}
	sh.Fuel = 1
	st := &model.Star{XPos: 1500}
	for _, d := range []float64{math.Nextafter(1500, 0), 1500, math.Nextafter(1500, math.Inf(1))} {
		st.XPos = d
		got, e := PlanTelescope(sh, model.Place{Level: model.LevelStar}, st, nil, .3, .05)
		if e != nil || got.Explore != (d <= 1500) {
			t.Fatal("inclusive range changed")
		}
	}
	for _, target := range []model.Place{{Level: 99}, {Level: model.LevelShip}, {Level: model.LevelShip, ShipNo: 65536}, {Err: true}} {
		if _, e := PlanTelescope(sh, target, nil, nil, .3, .05); e == nil {
			t.Fatal("invalid target accepted")
		}
	}
}
