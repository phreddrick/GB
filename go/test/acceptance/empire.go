// Package acceptance supplies independent C-derived assertions to tests only.
// It imports no game constructors, placement rules or authentication code.
package acceptance

import (
	"fmt"
	"reflect"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/racegen"
)

// Design identifies an expected placement. Hashes are compared but never placed
// in diagnostics; callers must not dump this object in test output.
type Design struct {
	Spec                   racegen.Spec
	Player                 int
	Ship                   uint32
	X, Y                   uint8
	God, Guest             bool
	RaceHash, GovernorHash string
}

// InitialDifferences compares every model field, including unchanged/zero ones.
// Authority: source/GB_racegen.c:150-250,265-363; shipdata.h:79; max.c:28-41.
// Differences contain field names only, never credential-bearing values.
func InitialDifferences(d Design, beforeStar *model.Star, beforePlanet *model.Planet, race *model.Race, star *model.Star, planet *model.Planet, ship *model.Ship) []string {
	if beforeStar == nil || beforePlanet == nil || race == nil || star == nil || planet == nil || ship == nil {
		return []string{"missing initial entity"}
	}
	if d.Player < 1 || d.Player > 63 || int(d.X) >= int(beforePlanet.MaxX) || int(d.Y) >= int(beforePlanet.MaxY) || len(racegen.Validate(d.Spec)) != 0 {
		return []string{"invalid assertion input"}
	}
	a := d.Spec.Attributes
	want := &model.Race{PlayerNum: d.Player, Name: d.Spec.Name, Info: d.Spec.Info, Password: d.RaceHash, God: d.God, Guest: d.Guest,
		Metamorph: d.Spec.RaceType == racegen.Metamorph, Absorb: a.Absorb == 1, Pods: a.Pods == 1, CollectiveIQ: a.CollectiveIQ == 1,
		Adventurism: a.Adventurism, BirthRate: a.Birthrate, Mass: a.Mass, Metabolism: a.Metabolism, GovShip: d.Ship}
	want.Fighters, want.NumberSexes, want.Fertilize = uint8(a.Fighters), uint8(a.Sexes), uint8(a.Fertilize*100) //nolint:gosec // validated bounded CR-07A design
	if want.CollectiveIQ {
		want.IQLimit = uint8(a.IQ)
	} else {
		want.IQ = uint8(a.IQ)
	} //nolint:gosec // validated integral 50..220
	copy(want.Conditions[:10], beforePlanet.Conditions[:10]) // TOXIC intentionally zero
	// C planet covariance==1 common terrains, in sector index order. This table
	// is an independent literal, not racegen.Homeworlds or ProposePlacement output.
	common := [][]int{{0, 1}, {5}, {6}, {0}, {1, 2, 6}, {2, 4}, {3}}
	for i, c := range d.Spec.SectorCompat {
		want.Likes[i] = c / 100
		for _, j := range common[d.Spec.Homeworld] {
			if i == j && c == 100 {
				want.LikesBest = uint8(i)
			}
		}
	} //nolint:gosec // eight sectors
	for i := range want.Translate {
		want.Translate[i] = 1
		if i == d.Player-1 || d.Player == 1 || d.God {
			want.Translate[i] = 100
		}
	}
	want.Governor[0] = model.Governor{Password: d.GovernorHash, Active: true, HomeLevel: model.LevelPlan, DefLevel: model.LevelPlan,
		HomeSystem: beforeStar.Number, DefSystem: beforeStar.Number, HomePlanetNum: beforePlanet.PlanetNum, DefPlanetNum: beforePlanet.PlanetNum,
		Toggle: model.Toggle{Highlight: uint8(d.Player), Inverse: true}} //nolint:gosec // player checked 1..63
	p := *beforePlanet
	p.Sectors = append([]model.Sector(nil), beforePlanet.Sectors...)
	index := int(d.Y)*int(p.MaxX) + int(d.X)
	if index >= len(p.Sectors) {
		return []string{"invalid assertion sector shape"}
	}
	terrain := p.Sectors[index].Condition
	p.Sectors[index].Owner, p.Sectors[index].Race = uint8(d.Player), uint8(d.Player) //nolint:gosec // player checked 1..63
	p.Sectors[index].Popn = uint16(want.NumberSexes)
	p.Sectors[index].Fert = 100
	p.Sectors[index].Eff = 10
	p.Sectors[index].Troops = 0
	p.Popn = uint64(want.NumberSexes)
	p.Troops = 0
	p.MaxPopn = 1100 * uint64(p.MaxX) * uint64(p.MaxY) / 2
	p.Info[d.Player-1].NumSectsOwned = 1
	p.Info[d.Player-1].Explored = true
	p.Explored = false
	p.Ships = d.Ship
	st := *beforeStar
	st.Inhabited |= uint64(1) << uint(d.Player-1)
	st.Explored |= uint64(1) << uint(d.Player-1)
	st.AP[d.Player-1] = 5
	sh := &model.Ship{Number: d.Ship, Owner: uint8(d.Player), Race: uint8(d.Player), Class: "Standard", Type: model.OtypeGov, BuildType: 26, BuildCost: 500, //nolint:gosec // player checked 1..63
		XPos: st.XPos + p.XPos, YPos: st.YPos + p.YPos, LandX: d.X, LandY: d.Y, WhatOrbits: model.LevelPlan, WhatDest: model.LevelPlan,
		StOrbits: st.Number, DestStar: st.Number, PNumOrbits: p.PlanetNum, DestPNum: p.PlanetNum, NextShip: beforePlanet.Ships,
		Mass: 100 + 10*a.Mass, BaseMass: 100, Tech: 100, Armor: 20, Size: 100, MaxCrew: 10, MaxResource: 500, MaxDestruct: 100, MaxFuel: 1000,
		Popn: 10, Guns: model.GunPrimary, Primary: 10, PrimType: 1, Secondary: 10, Active: true, Alive: true, Docked: true, On: true, Protect: model.ProtectState{Self: true}}
	differences := []string{}
	if terrain != want.LikesBest {
		differences = append(differences, "capital.terrain")
	}
	for _, pair := range []struct {
		name      string
		got, want any
	}{{"race", race, want}, {"star", star, &st}, {"planet", planet, &p}, {"ship", ship, sh}} {
		got, expected := reflect.ValueOf(pair.got).Elem(), reflect.ValueOf(pair.want).Elem()
		for i := 0; i < got.NumField(); i++ {
			if !reflect.DeepEqual(got.Field(i).Interface(), expected.Field(i).Interface()) {
				differences = append(differences, fmt.Sprintf("%s.%s", pair.name, got.Type().Field(i).Name))
			}
		}
	}
	return differences
}
