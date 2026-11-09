package game

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/racegen"
	"github.com/phreddrick/GB/go/internal/universe"
)

// Expected values below are read from enroll_valid_race in the BUILT writer,
// source/GB_racegen.c:107-365, not the unbuilt enroll_valid.c draft. See
// docs/testing/cr07b-placement.md for the field/source index and deviations.
// maxsupport at source/max.c:28-41 gives 1100 after eff=10/fert=100. The C
// government row at source/shipdata.h:79 supplies the literal ship assertions.

func placementTestSpec(home racegen.Homeworld) racegen.Spec {
	spec := racegen.Default()
	spec.Name, spec.Info = "Placement fixture", "test species"
	spec.Homeworld = home
	spec.Attributes.Fertilize = .25
	if home == racegen.Jovian {
		spec.SectorCompat = [racegen.SectorCount]float64{}
	}
	best := 0
	for i, common := range racegen.Homeworlds()[home].CommonSectors {
		if common {
			best = i
		}
	}
	spec.SectorCompat[best] = 100
	return spec
}

func placementTestIdentity() PlacementIdentity {
	return PlacementIdentity{RaceCredential: "opaque-r", GovernorCredential: "opaque-g"}
}

func placementTestOrder() PlacementOrder {
	return PlacementOrder{Stars: []int{0, 1, 2}, Sectors: []int{4, 5, 8, 0, 1, 2, 3, 6, 7}}
}

func placementTestWorld() PlacementUniverse {
	w := PlacementUniverse{Planets: make(map[uint8]map[uint8]*model.Planet), Races: make([]*model.Race, model.MaxPlayers), Ships: make(map[uint32]*model.Ship)}
	for i := 0; i < 3; i++ {
		s := &model.Star{Number: uint8(i), Name: "test star", XPos: 101.5, YPos: -205.5, NumPlanets: 2, Gravity: 2, Explored: 1 << 20, Ships: 99} //nolint:gosec // test grid 0..2
		s.AP[1], s.AP[3], s.Governor[1] = 17, 33, 2
		w.Stars = append(w.Stars, s)
		w.Planets[s.Number] = make(map[uint8]*model.Planet)
		for j := 0; j < 2; j++ {
			p := &model.Planet{StarNum: s.Number, PlanetNum: uint8(j), Type: model.PlanetEarth, MaxX: 3, MaxY: 3, //nolint:gosec // test grid 0..1
				XPos: 12.25, YPos: 70.75, Popn: 37, Troops: 9, MaxPopn: 10, TotalResources: 987, Explored: true, ExplTimer: 4}
			for k := range p.Conditions {
				p.Conditions[k] = int16(k * 7) //nolint:gosec // k 0..10
			}
			p.Info[3] = model.PlInfo{Resource: 42, Fuel: 23, Destruct: 12, Crystals: 5, Popn: 100, Troops: 3, Tax: 6, NewTax: 9, Guns: 7, ProdTech: 1.25, AutoRep: 2}
			p.Info[3].Route[0] = model.MerchantRoute{Set: true, DestStar: 1, Load: 2}
			p.Info[9].Fuel = 77
			for k := 0; k < 9; k++ {
				p.Sectors = append(p.Sectors, model.Sector{Type: model.SectorMount, Condition: model.SectorForest,
					Eff: 31, Fert: 45, Mobilization: 8, Crystals: 2, Resource: 456})
			}
			for _, k := range []int{1, 5, 8} {
				p.Sectors[k].Condition = model.SectorLand
			}
			w.Planets[s.Number][p.PlanetNum] = p
		}
	}
	w.Races[1] = &model.Race{PlayerNum: 2, Name: "existing", Tech: 18}
	w.Ships[99] = &model.Ship{Number: 99, WhatOrbits: model.LevelStar, Alive: true, NextShip: 0}
	return w
}

func placementTestSnapshot(t *testing.T, values ...any) string {
	t.Helper()
	b, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func placementTestPropose(t *testing.T, spec racegen.Spec, w PlacementUniverse, o PlacementOrder, id PlacementIdentity) *Placement {
	t.Helper()
	before := placementTestSnapshot(t, spec, w, o, id)
	p, err := ProposePlacement(spec, 4, 42, w, o, id)
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || before != placementTestSnapshot(t, spec, w, o, id) {
		t.Fatal("success mutated inputs or returned nil")
	}
	return p
}

func TestPlacementInitialFields(t *testing.T) {
	w, spec, o, id := placementTestWorld(), placementTestSpec(racegen.Earth), placementTestOrder(), placementTestIdentity()
	p := placementTestPropose(t, spec, w, o, id)
	wantRace := &model.Race{PlayerNum: 4, Name: "Placement fixture", Info: "test species", Password: "opaque-r",
		Adventurism: .4, BirthRate: .6, Fertilize: 25, IQ: 150, Fighters: 4, Mass: 1, NumberSexes: 2, Metabolism: 1,
		LikesBest: model.SectorLand, GovShip: 42}
	wantRace.Likes[model.SectorLand], wantRace.Likes[model.SectorPlated] = 1, 1
	wantRace.Conditions = [11]int16{0, 7, 14, 21, 28, 35, 42, 49, 56, 63, 0} // toxic deliberately not copied
	for i := range wantRace.Translate {
		wantRace.Translate[i] = 1
	}
	wantRace.Translate[3] = 100
	wantRace.Governor[0] = model.Governor{Password: "opaque-g", Active: true,
		HomeLevel: model.LevelPlan, DefLevel: model.LevelPlan, Toggle: model.Toggle{Highlight: 4, Inverse: true}}
	if !reflect.DeepEqual(p.Race, wantRace) {
		t.Fatalf("race fields:\ngot  %+v\nwant %+v", p.Race, wantRace)
	}
	wantPlanet := *w.Planets[0][0]
	wantPlanet.Sectors = slices.Clone(wantPlanet.Sectors)
	wantSector := model.Sector{Type: model.SectorMount, Condition: model.SectorLand, Owner: 4, Race: 4,
		Popn: 2, Fert: 100, Eff: 10, Mobilization: 8, Crystals: 2, Resource: 456}
	wantPlanet.Sectors[5] = wantSector
	wantPlanet.Popn, wantPlanet.Troops, wantPlanet.MaxPopn = 2, 0, 4950 // 1100 * 3 * 3 / 2, NOT species population or Info.Popn
	wantPlanet.Ships, wantPlanet.Explored = 42, false
	wantPlanet.Info[3].NumSectsOwned, wantPlanet.Info[3].Explored = 1, true
	if !reflect.DeepEqual(p.Planet, &wantPlanet) {
		t.Fatalf("planet fields:\ngot  %+v\nwant %+v", p.Planet, &wantPlanet)
	}
	wantStar := *w.Stars[0]
	wantStar.Inhabited, wantStar.Explored = 1<<3, (1<<20)|(1<<3)
	wantStar.AP[3] = 5
	if !reflect.DeepEqual(p.Star, &wantStar) {
		t.Fatalf("star fields: got %+v want %+v", p.Star, &wantStar)
	}
	// Full literal, not getshipDefaults output: enrollment intentionally has
	// fixed size/mass/cost, no retaliate/damage, and no arbitrary starting cargo.
	wantShip := &model.Ship{Number: 42, Owner: 4, Race: 4, Class: "Standard", Type: model.OtypeGov,
		XPos: 113.75, YPos: -134.75, LandX: 2, LandY: 1,
		WhatOrbits: model.LevelPlan, WhatDest: model.LevelPlan,
		Mass: 110, BaseMass: 100, Tech: 100, Armor: 20, Size: 100,
		MaxCrew: 10, MaxResource: 500, MaxDestruct: 100, MaxFuel: 1000, MaxSpeed: 0,
		Popn: 10, Guns: model.GunPrimary, Primary: 10, PrimType: 1, Secondary: 10, SecType: 0,
		BuildType: 26, BuildCost: 500, Active: true, Alive: true, Docked: true, On: true, Protect: model.ProtectState{Self: true}}
	if !reflect.DeepEqual(p.Ship, wantShip) {
		t.Fatalf("government ship:\ngot  %+v\nwant %+v", p.Ship, wantShip)
	}
	if p.CapitalX != 2 || p.CapitalY != 1 {
		t.Fatal("capital coordinates disagree")
	}
	s := &State{ships: map[uint32]*model.Ship{42: p.Ship}}
	if !s.governed(p.Race) {
		t.Fatal("C-style government ship is not governed")
	}
}

func TestPlacementAllHomeworlds(t *testing.T) {
	for _, home := range racegen.Homeworlds() {
		t.Run(home.Name, func(t *testing.T) {
			w, spec, o := placementTestWorld(), placementTestSpec(home.ID), placementTestOrder()
			// Nonzero indices prove home/default scope and ship destinations.
			o.Stars = []int{2, 0, 1}
			w.Planets[2][0].Type = model.PlanetAsteroid
			w.Planets[2][1].Type = home.PlanetType
			best := -1
			for i, c := range spec.SectorCompat {
				if c == 100 && home.CommonSectors[i] {
					best = i
				}
			}
			for i := range w.Planets[2][1].Sectors {
				w.Planets[2][1].Sectors[i].Condition = uint8(best) //nolint:gosec // validated common terrain 0..7
			}
			p := placementTestPropose(t, spec, w, o, placementTestIdentity())
			g, sh := p.Race.Governor[0], p.Ship
			if p.Planet.Type != home.PlanetType || p.Planet.PlanetNum != 1 || p.Star.Number != 2 || int(p.Race.LikesBest) != best ||
				g.HomeSystem != 2 || g.DefSystem != 2 || g.HomePlanetNum != 1 || g.DefPlanetNum != 1 ||
				sh.StOrbits != 2 || sh.DestStar != 2 || sh.PNumOrbits != 1 || sh.DestPNum != 1 {
				t.Fatal("homeworld mapping or scope mismatch")
			}
		})
	}
}

func TestPlacementSearchRules(t *testing.T) {
	for _, tc := range []struct {
		name       string
		edit       func(PlacementUniverse)
		wantPlanet uint8
	}{
		{"cold-boundary", func(w PlacementUniverse) { w.Planets[0][0].Conditions[0] = -200 }, 0},
		{"hot-boundary", func(w PlacementUniverse) { w.Planets[0][0].Conditions[0] = 100 }, 0},
		{"too-cold", func(w PlacementUniverse) { w.Planets[0][0].Conditions[0] = -201 }, 1},
		{"too-hot", func(w PlacementUniverse) { w.Planets[0][0].Conditions[0] = 101 }, 1},
		{"wrong-type", func(w PlacementUniverse) { w.Planets[0][0].Type = model.PlanetForest }, 1},
		{"stale-inhabited-flags", func(w PlacementUniverse) { w.Planets[0][0].Info[63].NumSectsOwned = 1 }, 1},
		{"stale-ownership-counts", func(w PlacementUniverse) { w.Planets[0][0].Sectors[0].Owner = 64 }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := placementTestWorld()
			tc.edit(w)
			p := placementTestPropose(t, placementTestSpec(racegen.Earth), w, placementTestOrder(), placementTestIdentity())
			if p.Star.Number != 0 || p.Planet.PlanetNum != tc.wantPlanet {
				t.Fatal("wrong candidate")
			}
		})
	}
	for _, tc := range []struct {
		name string
		edit func(PlacementUniverse)
	}{
		{"inhabited-low", func(w PlacementUniverse) { w.Stars[0].Inhabited = 1 }},
		{"inhabited-high", func(w PlacementUniverse) { w.Stars[0].Inhabited = 1 << 63 }},
		{"one-planet", func(w PlacementUniverse) { w.Stars[0].NumPlanets = 1 }},
		{"no-planets", func(w PlacementUniverse) { w.Stars[0].NumPlanets = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := placementTestWorld()
			tc.edit(w)
			p := placementTestPropose(t, placementTestSpec(racegen.Earth), w, placementTestOrder(), placementTestIdentity())
			if p.Star.Number != 1 {
				t.Fatal("ineligible star was selected")
			}
		})
	}
}

func TestPlacementCapitalAndSpecies(t *testing.T) {
	t.Run("last-common-terrain", func(t *testing.T) {
		w, spec := placementTestWorld(), placementTestSpec(racegen.Airless)
		spec.SectorCompat[racegen.Land], spec.SectorCompat[racegen.Mountain], spec.SectorCompat[racegen.Sand] = 100, 100, 100
		w.Planets[0][0].Type = model.PlanetMars
		w.Planets[0][0].Sectors[0].Condition = model.SectorDesert
		w.Planets[0][0].Sectors[2].Condition = model.SectorDesert
		o := placementTestOrder()
		o.Sectors = []int{2, 0, 1, 3, 4, 5, 6, 7, 8}
		p := placementTestPropose(t, spec, w, o, placementTestIdentity())
		if p.Race.LikesBest != model.SectorDesert || p.CapitalX != 2 || p.CapitalY != 0 {
			t.Fatal("C last-common terrain or first-in-order rule lost")
		}
	})
	t.Run("populated-unowned-sector", func(t *testing.T) {
		w := placementTestWorld()
		w.Planets[0][0].Sectors[5].Popn = 9
		w.Planets[0][0].Sectors[8].Troops = 2
		p := placementTestPropose(t, placementTestSpec(racegen.Earth), w, placementTestOrder(), placementTestIdentity())
		if p.CapitalX != 1 || p.CapitalY != 0 || p.Planet.Sectors[5].Popn != 9 || p.Planet.Sectors[8].Troops != 2 {
			t.Fatal("occupied sector overwritten")
		}
	})
	t.Run("metamorph-collective", func(t *testing.T) {
		w, spec := placementTestWorld(), placementTestSpec(racegen.Earth)
		spec.RaceType = racegen.Metamorph
		spec.Attributes.Absorb = 1
		spec.Attributes.Pods = 1
		spec.Attributes.CollectiveIQ = 1
		spec.Attributes.Mass = 1.75
		spec.Attributes.Sexes = 3
		p := placementTestPropose(t, spec, w, placementTestOrder(), placementTestIdentity())
		if !p.Race.Metamorph || !p.Race.Absorb || !p.Race.Pods || !p.Race.CollectiveIQ || p.Race.IQ != 0 || p.Race.IQLimit != 150 || p.Race.NumberSexes != 3 || p.Planet.Popn != 3 || p.Planet.Sectors[5].Popn != 3 || p.Ship.Mass != 117.5 {
			t.Fatal("species conversion mismatch")
		}
	})
	for _, tc := range []struct {
		name   string
		player int
		id     PlacementIdentity
		all    bool
	}{
		{"normal", 4, placementTestIdentity(), false}, {"player-one", 1, placementTestIdentity(), true},
		{"god", 4, PlacementIdentity{RaceCredential: "opaque-r", GovernorCredential: "opaque-g", God: true}, true},
		{"guest", 4, PlacementIdentity{RaceCredential: "opaque-r", GovernorCredential: "opaque-g", Guest: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ProposePlacement(placementTestSpec(racegen.Earth), tc.player, 42, placementTestWorld(), placementTestOrder(), tc.id)
			if err != nil {
				t.Fatal(err)
			}
			for i, v := range p.Race.Translate {
				want := int8(1)
				if tc.all || i == tc.player-1 {
					want = 100
				}
				if v != want {
					t.Errorf("translation %d=%d want %d", i, v, want)
				}
			}
			if p.Race.God != tc.id.God || p.Race.Guest != tc.id.Guest || p.Ship.Fuel != 0 || p.Ship.Resource != 0 || p.Ship.BuildCost != 500 || p.Race.Governor[0].Money != 0 {
				t.Fatal("privilege accidentally grants inventory or money")
			}
		})
	}
}

func TestPlacementFailuresLeaveInputsUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		code PlacementErrorCode
		edit func(*racegen.Spec, *PlacementUniverse, *PlacementOrder, *PlacementIdentity, *int, *uint32)
	}{
		{"invalid-spec", PlacementInvalidSpec, func(s *racegen.Spec, _ *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			s.Attributes.IQ = 1
		}},
		{"player-zero", PlacementInvalidPlayer, func(_ *racegen.Spec, _ *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, p *int, _ *uint32) {
			*p = 0
		}},
		{"player-negative", PlacementInvalidPlayer, func(_ *racegen.Spec, _ *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, p *int, _ *uint32) {
			*p = -1
		}},
		{"player-high", PlacementInvalidPlayer, func(_ *racegen.Spec, _ *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, p *int, _ *uint32) {
			*p = 65
		}},
		{"race-in-use", PlacementPlayerInUse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			w.Races[3] = &model.Race{PlayerNum: 4}
		}},
		{"ship-zero", PlacementInvalidShip, func(_ *racegen.Spec, _ *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, n *uint32) {
			*n = 0
		}},
		{"ship-in-use", PlacementShipInUse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			w.Ships[42] = &model.Ship{Number: 42}
		}},
		{"nil-ship-slot", PlacementShipInUse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			w.Ships[42] = nil
		}},
		{"no-race-credential", PlacementInvalidIdentity, func(_ *racegen.Spec, _ *PlacementUniverse, _ *PlacementOrder, id *PlacementIdentity, _ *int, _ *uint32) {
			id.RaceCredential = ""
		}},
		{"no-governor-credential", PlacementInvalidIdentity, func(_ *racegen.Spec, _ *PlacementUniverse, _ *PlacementOrder, id *PlacementIdentity, _ *int, _ *uint32) {
			id.GovernorCredential = ""
		}},
		{"conflicting-privileges", PlacementInvalidIdentity, func(_ *racegen.Spec, _ *PlacementUniverse, _ *PlacementOrder, id *PlacementIdentity, _ *int, _ *uint32) {
			id.God = true
			id.Guest = true
		}},
		{"star-order-prefix", PlacementInvalidOrder, func(_ *racegen.Spec, _ *PlacementUniverse, o *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			o.Stars = []int{0}
		}},
		{"star-order-duplicate-after-hit", PlacementInvalidOrder, func(_ *racegen.Spec, _ *PlacementUniverse, o *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			o.Stars = []int{0, 1, 1}
		}},
		{"star-order-negative", PlacementInvalidOrder, func(_ *racegen.Spec, _ *PlacementUniverse, o *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			o.Stars = []int{-1, 0, 1}
		}},
		{"star-order-high", PlacementInvalidOrder, func(_ *racegen.Spec, _ *PlacementUniverse, o *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			o.Stars = []int{0, 1, 3}
		}},
		{"sector-order-prefix", PlacementInvalidOrder, func(_ *racegen.Spec, _ *PlacementUniverse, o *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			o.Sectors = []int{5}
		}},
		{"sector-order-duplicate-after-hit", PlacementInvalidOrder, func(_ *racegen.Spec, _ *PlacementUniverse, o *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			o.Sectors[8] = 5
		}},
		{"sector-order-negative", PlacementInvalidOrder, func(_ *racegen.Spec, _ *PlacementUniverse, o *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			o.Sectors[8] = -1
		}},
		{"sector-order-high", PlacementInvalidOrder, func(_ *racegen.Spec, _ *PlacementUniverse, o *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			o.Sectors[8] = 9
		}},
		{"no-home", PlacementNoHomePlanet, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			for _, s := range w.Stars {
				s.Inhabited = 1
			}
		}},
		{"no-capital", PlacementNoCapitalSector, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			for i := range w.Planets[0][0].Sectors {
				w.Planets[0][0].Sectors[i].Condition = model.SectorForest
			}
		}},
		{"all-capitals-populated", PlacementNoCapitalSector, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			for i := range w.Planets[0][0].Sectors {
				w.Planets[0][0].Sectors[i].Popn = 1
			}
		}},
		{"nil-star", PlacementInvalidUniverse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			w.Stars[2] = nil
		}},
		{"wrong-star-id", PlacementInvalidUniverse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			w.Stars[2].Number = 1
		}},
		{"planet-count", PlacementInvalidUniverse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			w.Stars[0].NumPlanets = 11
		}},
		{"missing-planet", PlacementInvalidUniverse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			delete(w.Planets[0], 0)
		}},
		{"wrong-planet-id", PlacementInvalidUniverse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			w.Planets[0][0].StarNum = 2
		}},
		{"empty-map", PlacementInvalidUniverse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			w.Planets[0][0].MaxX = 0
		}},
		{"short-map", PlacementInvalidUniverse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			w.Planets[0][0].Sectors = w.Planets[0][0].Sectors[:8]
		}},
		{"race-slice", PlacementInvalidUniverse, func(_ *racegen.Spec, w *PlacementUniverse, _ *PlacementOrder, _ *PlacementIdentity, _ *int, _ *uint32) {
			w.Races = append(w.Races, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w, o, id, p, n := placementTestSpec(racegen.Earth), placementTestWorld(), placementTestOrder(), placementTestIdentity(), 4, uint32(42)
			tc.edit(&s, &w, &o, &id, &p, &n)
			before := placementTestSnapshot(t, s, w, o, id)
			result, err := ProposePlacement(s, p, n, w, o, id)
			var pe *PlacementError
			if result != nil || !errors.As(err, &pe) || pe.Code != tc.code {
				t.Fatalf("got %v %v, want %s", result, err, tc.code)
			}
			if before != placementTestSnapshot(t, s, w, o, id) {
				t.Fatal("failure mutated inputs")
			}
		})
	}
}

func TestPlacementOrbitChains(t *testing.T) {
	w := placementTestWorld()
	w.Planets[0][0].Ships = 10
	w.Ships[10] = &model.Ship{Number: 10, WhatOrbits: model.LevelPlan, NextShip: 11, Alive: true, Special: model.ShipSpecial{Pod: &model.PodSpecial{Decay: 3}}}
	w.Ships[11] = &model.Ship{Number: 11, WhatOrbits: model.LevelPlan, Alive: true}
	p := placementTestPropose(t, placementTestSpec(racegen.Earth), w, placementTestOrder(), placementTestIdentity())
	if p.Planet.Ships != 42 || p.Ship.NextShip != 10 || w.Ships[10].NextShip != 11 || w.Ships[11].NextShip != 0 {
		t.Fatal("existing chain lost")
	}
	for _, tc := range []struct {
		name string
		edit func(PlacementUniverse)
	}{
		{"dangling", func(w PlacementUniverse) { w.Ships[10].NextShip = 12 }},
		{"cycle", func(w PlacementUniverse) { w.Ships[11].NextShip = 10 }},
		{"wrong-location", func(w PlacementUniverse) { w.Ships[10].PNumOrbits = 1 }},
		{"wrong-scope", func(w PlacementUniverse) { w.Ships[10].WhatOrbits = model.LevelStar }},
		{"wrong-number", func(w PlacementUniverse) { w.Ships[10].Number = 22 }},
		{"unlinked", func(w PlacementUniverse) { w.Planets[0][0].Ships = 11 }},
		{"dead-chain-member", func(w PlacementUniverse) { w.Ships[10].Alive = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Reconstruct a valid world each time, then damage only the chain.
			x := placementTestWorld()
			x.Planets[0][0].Ships = 10
			x.Ships[10] = &model.Ship{Number: 10, WhatOrbits: model.LevelPlan, NextShip: 11, Alive: true}
			x.Ships[11] = &model.Ship{Number: 11, WhatOrbits: model.LevelPlan, Alive: true}
			tc.edit(x)
			before := placementTestSnapshot(t, x)
			result, err := ProposePlacement(placementTestSpec(racegen.Earth), 4, 42, x, placementTestOrder(), placementTestIdentity())
			var pe *PlacementError
			if result != nil || !errors.As(err, &pe) || pe.Code != PlacementInvalidOrbitChain {
				t.Fatalf("got %v %v", result, err)
			}
			if before != placementTestSnapshot(t, x) {
				t.Fatal("chain failure mutated world")
			}
		})
	}
}

func TestPlacementCopyIsolationAndDeterminism(t *testing.T) {
	w, spec, o, id := placementTestWorld(), placementTestSpec(racegen.Earth), placementTestOrder(), placementTestIdentity()
	s := &State{stars: w.Stars, planets: w.Planets, races: w.Races, ships: w.Ships, sdata: &model.StarData{AP: [model.MaxPlayers]uint8{7}}}
	before := placementTestSnapshot(t, w, s.sdata)
	p := placementTestPropose(t, spec, w, o, id)
	for i := 0; i < 20; i++ {
		q := placementTestPropose(t, spec, w, o, id)
		if !reflect.DeepEqual(p, q) {
			t.Fatal("placement nondeterministic")
		}
	}
	p.Planet.Sectors[0].Resource = 999
	p.Planet.Info[3].Route[0].Load = 9
	p.Planet.Conditions[0] = -100
	p.Star.AP[0] = 99
	p.Star.PlanetNames[0] = "modified"
	p.Race.Name = "modified"
	p.Race.Governor[0].Toggle.Color = true
	p.Ship.Protect.Self = false
	if before != placementTestSnapshot(t, w, s.sdata) {
		t.Fatal("output aliases State or universe AP changed")
	}
	q := placementTestPropose(t, spec, w, o, id)
	if q.Planet.Sectors[0].Resource == 999 || q.Race.Name == "modified" {
		t.Fatal("proposals alias each other")
	}
}

func TestPlacementIdentifierBoundariesAndInvalidPositions(t *testing.T) {
	// These are model limits, not an allocation policy or permission to enroll.
	p, err := ProposePlacement(placementTestSpec(racegen.Earth), 64, math.MaxUint32, placementTestWorld(), placementTestOrder(), placementTestIdentity())
	if err != nil || p.Race.PlayerNum != 64 || p.Race.GovShip != math.MaxUint32 || p.Star.Inhabited != uint64(1)<<63 {
		t.Fatalf("model limits: %v", err)
	}
	for _, x := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		w := placementTestWorld()
		w.Stars[0].XPos = x
		result, err := ProposePlacement(placementTestSpec(racegen.Earth), 4, 42, w, placementTestOrder(), placementTestIdentity())
		var pe *PlacementError
		if result != nil || !errors.As(err, &pe) || pe.Code != PlacementInvalidUniverse {
			t.Fatalf("invalid position accepted: %v", err)
		}
		if math.Float64bits(w.Stars[0].XPos) != math.Float64bits(x) {
			t.Fatal("invalid position mutated")
		}
	}
}

func TestPlacementSeededUniverse(t *testing.T) {
	u, err := universe.Generate(universe.Params{Stars: 8, MinPlanets: 2, MaxPlanets: 3}, 11)
	if err != nil {
		t.Fatal(err)
	}
	w := PlacementUniverse{Stars: u.Stars, Planets: make(map[uint8]map[uint8]*model.Planet)}
	o := PlacementOrder{}
	for i := range u.Stars {
		o.Stars = append(o.Stars, i)
	}
	for _, p := range u.Planets {
		if w.Planets[p.StarNum] == nil {
			w.Planets[p.StarNum] = make(map[uint8]*model.Planet)
		}
		w.Planets[p.StarNum][p.PlanetNum] = p
	}
	// Pin the candidate independently of placementHome: this catches search drift.
	for i := range w.Planets[6][1].Sectors {
		o.Sectors = append(o.Sectors, i)
	}
	p := placementTestPropose(t, placementTestSpec(racegen.Earth), w, o, placementTestIdentity())
	if p.Star.Number != 6 || p.Planet.PlanetNum != 1 || p.CapitalX != 0 || p.CapitalY != 0 {
		t.Fatalf("seed 11 placement changed: %d/%d at %d,%d", p.Star.Number, p.Planet.PlanetNum, p.CapitalX, p.CapitalY)
	}
}

func TestPlacementAdditionalEdges(t *testing.T) {
	t.Run("rectangular-map", func(t *testing.T) {
		w, o := placementTestWorld(), placementTestOrder()
		w.Planets[0][0].MaxY = 2
		w.Planets[0][0].Sectors = w.Planets[0][0].Sectors[:6]
		o.Sectors = []int{5, 0, 1, 2, 3, 4}
		p := placementTestPropose(t, placementTestSpec(racegen.Earth), w, o, placementTestIdentity())
		if p.Planet.MaxPopn != 3300 || p.CapitalX != 2 || p.CapitalY != 1 {
			t.Fatal("rectangular map arithmetic is wrong")
		}
	})
	t.Run("dead-unlinked-record", func(t *testing.T) {
		w := placementTestWorld()
		w.Ships[10] = &model.Ship{Number: 10, WhatOrbits: model.LevelPlan, Alive: false}
		p := placementTestPropose(t, placementTestSpec(racegen.Earth), w, placementTestOrder(), placementTestIdentity())
		if p.Ship.NextShip != 0 || w.Ships[10].Alive {
			t.Fatal("dead historical record affected the live orbit chain")
		}
	})
	t.Run("empty-universe", func(t *testing.T) {
		p, err := ProposePlacement(placementTestSpec(racegen.Earth), 4, 42, PlacementUniverse{}, PlacementOrder{}, placementTestIdentity())
		var pe *PlacementError
		if p != nil || !errors.As(err, &pe) || pe.Code != PlacementNoHomePlanet {
			t.Fatalf("empty universe: %v", err)
		}
	})
	t.Run("overflowed-position", func(t *testing.T) {
		w := placementTestWorld()
		w.Stars[0].XPos, w.Planets[0][0].XPos = math.MaxFloat64, math.MaxFloat64
		before := placementTestSnapshot(t, w)
		p, err := ProposePlacement(placementTestSpec(racegen.Earth), 4, 42, w, placementTestOrder(), placementTestIdentity())
		var pe *PlacementError
		if p != nil || !errors.As(err, &pe) || pe.Code != PlacementInvalidUniverse || before != placementTestSnapshot(t, w) {
			t.Fatalf("overflowed position: %v", err)
		}
	})
	t.Run("parallel-pure-proposals", func(t *testing.T) {
		w, spec, o, id := placementTestWorld(), placementTestSpec(racegen.Earth), placementTestOrder(), placementTestIdentity()
		want := placementTestPropose(t, spec, w, o, id)
		for i := 0; i < 12; i++ {
			t.Run("proposal", func(t *testing.T) {
				t.Parallel()
				got := placementTestPropose(t, spec, w, o, id)
				if !reflect.DeepEqual(want, got) {
					t.Fatal("parallel proposal changed output")
				}
			})
		}
	})
}
