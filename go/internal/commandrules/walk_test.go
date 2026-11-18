package commandrules

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/model"
	"math"
	"testing"
)

func walkRulesInput() WalkInput {
	r := &model.Race{PlayerNum: 1}
	r.Governor[0].Active = true
	r.Likes[model.SectorLand] = 1
	other := &model.Race{PlayerNum: 2}
	sh := &model.Ship{Number: 77, Owner: 1, Type: model.OtypeAFV, Alive: true, Docked: true, WhatOrbits: model.LevelPlan, WhatDest: model.LevelPlan, Popn: 1, Fuel: 10, Mass: 100, LandX: 1, LandY: 1}
	p := &model.Planet{MaxX: 3, MaxY: 3, Ships: 77, Sectors: make([]model.Sector, 9)}
	for i := range p.Sectors {
		p.Sectors[i].Condition = model.SectorLand
	}
	st := &model.Star{NumPlanets: 1}
	st.AP[0] = 50
	return WalkInput{Player: 1, RecipientCount: model.MaxPlayers, Ship: sh, Planet: p, Star: st, Races: []*model.Race{r, other}, Ships: []*model.Ship{sh}, Direction: '6', MassFuel: .05}
}
func TestWalkRulesDirectionsAndPurity(t *testing.T) {
	for _, tt := range []struct {
		chars string
		x, y  uint8
	}{{"1b", 0, 2}, {"2k", 1, 2}, {"3n", 2, 2}, {"4h", 0, 1}, {"6l", 2, 1}, {"7y", 0, 0}, {"8j", 1, 0}, {"9u", 2, 0}} {
		for _, c := range []byte(tt.chars) {
			t.Run(string(c), func(t *testing.T) {
				in := walkRulesInput()
				in.Direction = c
				before, e := json.Marshal(in)
				if e != nil {
					t.Fatal(e)
				}
				got, e := PlanWalk(in)
				if e != nil {
					t.Fatal(e)
				}
				after, e := json.Marshal(in)
				if e != nil {
					t.Fatal(e)
				}
				if string(before) != string(after) || got.Ship.LandX != tt.x || got.Ship.LandY != tt.y || got.Ship.Fuel != 9 || got.Ship.Mass != 99.95 || got.Star.AP[0] != 49 {
					t.Fatal("bad step or input mutation")
				}
			})
		}
	}
	in := walkRulesInput()
	in.Planet.MaxX = 1
	in.Planet.MaxY = 1
	in.Planet.Sectors = in.Planet.Sectors[:1]
	in.Ship.LandX = 0
	in.Ship.LandY = 0
	got, e := PlanWalk(in)
	if e != nil || got.Ship.LandX != 0 || got.Ship.Fuel != 9 || got.Star.AP[0] != 49 {
		t.Fatal("C one-column wrap semantics", e)
	}
	in = walkRulesInput()
	in.Ship.LandX = 2
	got, e = PlanWalk(in)
	if e != nil || got.Ship.LandX != 0 {
		t.Fatal("wrap", e)
	}
}
func TestWalkRulesDefenseAndBounds(t *testing.T) {
	cases := []struct {
		name string
		edit func(*WalkInput)
		kind string
	}{
		{"missingRecipientBound", func(i *WalkInput) { i.RecipientCount = 0 }, "bad"},
		{"overflowRecipientBound", func(i *WalkInput) { i.RecipientCount = model.MaxPlayers + 1 }, "bad"},
		{"noAP", func(i *WalkInput) { i.Star.AP[0] = 0 }, "refuse"},
		{"lowFuel", func(i *WalkInput) { i.Ship.Fuel = .5 }, "refuse"},
		{"noCrew", func(i *WalkInput) { i.Ship.Popn = 0 }, "refuse"},
		{"pole", func(i *WalkInput) { i.Ship.LandY = 0; i.Direction = '8' }, "refuse"},
		{"negativeMass", func(i *WalkInput) { i.Ship.Mass = .01 }, "bad"},
		{"nan", func(i *WalkInput) { i.Ship.Fuel = math.NaN() }, "bad"},
		{"inf", func(i *WalkInput) { i.MassFuel = math.Inf(1) }, "bad"},
		{"cycle", func(i *WalkInput) { i.Ship.NextShip = i.Ship.Number }, "bad"},
		{"missing", func(i *WalkInput) { i.Planet.Ships = 0 }, "bad"},
		{"staleDestination", func(i *WalkInput) { i.Ship.DestShipNo = 88; i.Ship.DestStar = 99; i.Ship.DestPNum = 99 }, "ok"},
		{"duplicateRace", func(i *WalkInput) { i.Races = append(i.Races, i.Races[0]) }, "bad"},
		{"foreignSector", func(i *WalkInput) { i.Planet.SectorAt(2, 1).Owner = 2 }, "legacy"},
		{"oneWayAlliance", func(i *WalkInput) { i.Planet.SectorAt(2, 1).Owner = 2; i.Races[0].Allied = 2 }, "legacy"},
		{"mutualAlliance", func(i *WalkInput) { i.Planet.SectorAt(2, 1).Owner = 2; i.Races[0].Allied = 2; i.Races[1].Allied = 1 }, "ok"},
		{"inactiveAFV", func(i *WalkInput) { i.Ship.Active = false }, "ok"},
		{"staleDestinationLevel", func(i *WalkInput) { i.Ship.WhatDest = model.LevelShip }, "ok"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			in := walkRulesInput()
			tt.edit(&in)
			_, e := PlanWalk(in)
			var refusal *WalkRefusal
			good := (tt.kind == "ok" && e == nil) || (tt.kind == "bad" && errors.Is(e, ErrWalkInput)) || (tt.kind == "legacy" && errors.Is(e, ErrWalkLegacy)) || (tt.kind == "refuse" && errors.As(e, &refusal))
			if !good {
				t.Fatal(tt.kind, e)
			}
		})
	}
	for _, armed := range []bool{false, true} {
		for _, mutual := range []bool{false, true} {
			t.Run(fmt.Sprintf("armed=%t/allied=%t", armed, mutual), func(t *testing.T) {
				in := walkRulesInput()
				sh := *in.Ship
				sh.Number = 78
				sh.Owner = 2
				sh.LandX = 2
				sh.Active = false
				sh.On = false
				if armed {
					sh.Guns = model.GunSecondary
					sh.Secondary = 1
					sh.Destruct = 1
					sh.Retaliate = 1
				}
				if mutual {
					in.Races[0].Allied = 2
					in.Races[1].Allied = 1
				}
				in.Ship.NextShip = 78
				in.Ships = append(in.Ships, &sh)
				_, e := PlanWalk(in)
				if armed && !mutual {
					if !errors.Is(e, ErrWalkLegacy) {
						t.Fatal("defender escaped", e)
					}
				} else if e != nil {
					t.Fatal(e)
				}
			})
		}
	}
}
