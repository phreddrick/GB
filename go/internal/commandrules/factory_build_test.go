package commandrules

import (
	"math"
	"testing"

	"github.com/phreddrick/GB/go/internal/model"
)

func factoryPlanFixture() PlanetFactoryInput {
	r := &model.Race{PlayerNum: 1, Tech: 100}
	r.Governor[0].Active = true
	p := &model.Planet{MaxX: 1, MaxY: 1, Sectors: []model.Sector{{Owner: 1, Popn: 20, Condition: model.SectorLand, Fert: 47}}}
	p.Info[0].Resource = 100
	return PlanetFactoryInput{Player: 1, Number: 1, Scope: model.Directory{Level: model.LevelPlan}, Race: r, Star: &model.Star{NumPlanets: 1}, Planet: p, SizeCrew: .01, SizeResource: .01, MassSize: 1}
}
func TestPlanetFactoryRulesPrivateAndZeroAP(t *testing.T) {
	in := factoryPlanFixture()
	q, e := PlanPlanetFactory(in)
	if e != nil {
		t.Fatal(e)
	}
	if q.Ship.BuildCost != 23 || q.Ship.Popn != 0 || q.Ship.Fuel != 0 || q.Ship.Damage != 75 || q.Ship.On || !q.Ship.Docked || in.Planet.Ships != 0 || in.Planet.Info[0].Resource != 100 || in.Star.AP[0] != 0 {
		t.Fatal("factory or zero-AP semantics")
	}
	q.Planet.Sectors[0].Fert++
	if in.Planet.Sectors[0].Fert != 47 {
		t.Fatal("borrowed sectors")
	}
}
func TestPlanetFactoryRulesNumericAndWitnesses(t *testing.T) {
	for _, which := range []string{"negative-size", "NaN", "infinite", "size-overflow", "mass-overflow", "position-overflow", "wrong-star", "wrong-planet", "missing-chain", "duplicate-chain", "wrong-orbit", "dead-chain"} {
		t.Run(which, func(t *testing.T) {
			in := factoryPlanFixture()
			switch which {
			case "negative-size":
				in.SizeCrew = -1
			case "NaN":
				in.Race.Tech = math.NaN()
			case "infinite":
				in.SizeResource = math.Inf(1)
			case "size-overflow":
				in.SizeResource = 65536
			case "mass-overflow":
				in.MassSize = math.MaxFloat64
				in.SizeCrew = 1
			case "position-overflow":
				in.Planet.XPos = math.MaxFloat64
				in.Star.XPos = math.MaxFloat64
			case "wrong-star":
				in.Planet.StarNum = 1
			case "wrong-planet":
				in.Scope.PlanetNum = 1
			case "missing-chain":
				in.Planet.Ships = 2
			case "duplicate-chain", "wrong-orbit", "dead-chain":
				sh := &model.Ship{Number: 2, Owner: 1, WhatOrbits: model.LevelPlan}
				in.Planet.Ships = 2
				in.Ships = []*model.Ship{sh}
				if which == "duplicate-chain" {
					in.Ships = append(in.Ships, sh)
				}
				if which == "wrong-orbit" {
					sh.WhatOrbits = model.LevelShip
				}
			}
			_, e := PlanPlanetFactory(in)
			if (e == nil) != (which == "dead-chain") {
				t.Fatal("invalid numeric/witness acceptance", which, e)
			}
		})
	}
}
