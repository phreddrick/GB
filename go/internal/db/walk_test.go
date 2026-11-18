package db

import (
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	"reflect"
	"testing"
	"time"
)

func walkFixture(t *testing.T) (*Store, string, NoncombatWalk) {
	t.Helper()
	s, path, x := stockpileFixture(t)
	p, st, r := x.Planet.Expected, x.Star.Expected, x.Caller
	r.Likes[model.SectorLand] = 1
	f := &model.Ship{Number: 77, Owner: 1, Type: model.OtypeAFV, Alive: true, Docked: true, WhatOrbits: model.LevelPlan, WhatDest: model.LevelPlan, Popn: 1, Fuel: 10, Mass: 100, NextShip: 78}
	other := &model.Ship{Number: 78, Owner: 2, Type: model.StypeCargo, Alive: true, WhatOrbits: model.LevelPlan}
	p.Ships = f.Number
	in := NoncombatWalk{Input: commandrules.WalkInput{Player: 1, RecipientCount: model.MaxPlayers, Ship: f, Star: st, Planet: p, Races: []*model.Race{r, x.Target}, Ships: []*model.Ship{f, other}, Direction: '6', MassFuel: .05}, Scope: x.Scope}
	for _, sh := range in.Input.Ships {
		if e := s.PutShip(sh); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.PutPlanet(p); e != nil {
		t.Fatal(e)
	}
	if e := s.PutRace(r); e != nil {
		t.Fatal(e)
	}
	var e error
	in.Proposed, e = commandrules.PlanWalk(in.Input)
	if e != nil {
		t.Fatal(e)
	}
	return s, path, in
}
func assertWalkWorld(t *testing.T, s *Store, in NoncombatWalk, committed bool) {
	t.Helper()
	f, e := s.GetShip(in.Input.Ship.Number)
	if e != nil {
		t.Fatal(e)
	}
	st, e := s.GetStar(in.Input.Star.Number)
	if e != nil {
		t.Fatal(e)
	}
	wantF, wantSt := in.Input.Ship, in.Input.Star
	if committed {
		wantF, wantSt = in.Proposed.Ship, in.Proposed.Star
	}
	if !reflect.DeepEqual(f, wantF) || !reflect.DeepEqual(st, wantSt) {
		t.Fatal("partial or incorrect walk ship/AP records")
	}
	p, e := s.GetPlanet(in.Input.Planet.StarNum, in.Input.Planet.PlanetNum)
	if e != nil || !reflect.DeepEqual(p, in.Input.Planet) {
		t.Fatal("read-only planet changed", e)
	}
	for _, r := range in.Input.Races {
		got, e := s.GetRace(r.PlayerNum)
		if e != nil || !reflect.DeepEqual(got, r) {
			t.Fatal("read-only race changed", e)
		}
	}
	other, e := s.GetShip(78)
	if e != nil || !reflect.DeepEqual(other, in.Input.Ships[1]) {
		t.Fatal("read-only orbit changed", e)
	}
}
func TestWalkContractCommitReopen(t *testing.T) {
	s, path, in := walkFixture(t)
	out, e := s.CommitNoncombatWalk(in)
	if e != nil || out != CommandCommitted {
		t.Fatal(out, e)
	}
	assertWalkWorld(t, s, in, true)
	if in.Proposed.Ship.Fuel != 9 || in.Proposed.Ship.Mass != 99.95 || in.Proposed.Ship.LandX != 1 || in.Proposed.Star.AP[0] != 49 {
		t.Fatal("incorrect expected gameplay")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close() }()
	assertWalkWorld(t, s, in, true)
}
func TestWalkContractRollback(t *testing.T) {
	for _, stage := range []string{"walk.before", "walk.check:1", "walk.check:2", "walk.check:3", "walk.check:4", "walk.check:5", "walk.check:6", "walk.validated", "walk.write:1", "walk.write:2", "walk.before-commit"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := walkFixture(t)
				before := enrollmentSnapshot(t, s)
				reached := false
				s.SetMetaFault(func(op string) error {
					if op == stage {
						reached = true
						if panics {
							panic("test")
						}
						return errors.New("test")
					}
					return nil
				})
				out, e := s.CommitNoncombatWalk(in)
				if !reached || out != CommandNotCommitted || e == nil || enrollmentSnapshot(t, s) != before {
					t.Fatal("abort changed records", out, e)
				}
			})
		}
	}
}
func TestWalkContractOutcomes(t *testing.T) {
	for _, stage := range []string{"walk.commit-error", "walk.after-commit"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := walkFixture(t)
				s.SetMetaFault(func(op string) error {
					if op == "command.readback" {
						return errors.New("readback unavailable")
					}
					if op == stage {
						if panics {
							panic("test")
						}
						return errors.New("test")
					}
					return nil
				})
				out, e := s.CommitNoncombatWalk(in)
				want := CommandCommitted
				if stage == "walk.commit-error" {
					want = CommandUncertain
				}
				if out != want || e == nil {
					t.Fatal(out, e)
				}
				assertWalkWorld(t, s, in, true)
				if s.CommandWriteUncertain() != (want == CommandUncertain) {
					t.Fatal("guard")
				}
				s.SetMetaFault(nil)
				if want == CommandUncertain {
					out, e = s.CommitNoncombatWalk(in)
					if out != CommandUncertain || e == nil {
						t.Fatal("implicit retry", out, e)
					}
				}
			})
		}
	}
}
func TestWalkContractUnsupportedProposals(t *testing.T) {
	for _, which := range []string{"fuel", "mass", "position", "AP", "cargo", "owner", "sector", "omit-chain", "duplicate", "selected-witness", "foreign-sector", "mechanical"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := walkFixture(t)
			before := enrollmentSnapshot(t, s)
			switch which {
			case "fuel":
				in.Proposed.Ship.Fuel++
			case "mass":
				in.Proposed.Ship.Mass++
			case "position":
				in.Proposed.Ship.LandY++
			case "AP":
				in.Proposed.Star.AP[0]++
			case "cargo":
				in.Proposed.Ship.Resource++
			case "owner":
				in.Proposed.Ship.Owner = 2
			case "sector":
				in.Input.Planet.Sectors[1].Owner = 2
			case "omit-chain":
				in.Input.Ships = in.Input.Ships[:1]
			case "duplicate":
				in.Input.Ships = append(in.Input.Ships, in.Input.Ships[0])
			case "selected-witness":
				v := *in.Input.Ship
				v.NextShip = 0
				in.Input.Ships[0] = &v
			case "foreign-sector":
				in.Input.Planet.Sectors[1].Owner = 2
			case "mechanical":
				sh := in.Input.Ships[1]
				sh.Type = model.OtypeAFV
				sh.Docked = true
				sh.LandX = 1
				sh.Popn = 1
				sh.Primary = 1
				sh.Guns = model.GunPrimary
				sh.Retaliate = 1
				sh.Destruct = 1
			}
			out, e := s.CommitNoncombatWalk(in)
			if out != CommandNotCommitted || e == nil || enrollmentSnapshot(t, s) != before {
				t.Fatal("unsupported proposal persisted", out, e)
			}
		})
	}
}
func TestWalkContractLatePreimages(t *testing.T) {
	for _, record := range []string{"ship", "star", "planet", "caller", "alliance", "defender"} {
		t.Run(record, func(t *testing.T) {
			s, _, in := walkFixture(t)
			s.SetMetaFault(func(op string) error {
				if op != "walk.before" {
					return nil
				}
				switch record {
				case "ship":
					q := *in.Input.Ship
					q.Fuel++
					return s.PutShip(&q)
				case "star":
					q := *in.Input.Star
					q.AP[1]++
					return s.PutStar(&q)
				case "planet":
					q := *in.Input.Planet
					q.Info[1].Resource++
					return s.PutPlanet(&q)
				case "caller", "alliance":
					i := 0
					if record == "alliance" {
						i = 1
					}
					q := *in.Input.Races[i]
					q.Morale++
					return s.PutRace(&q)
				default:
					q := *in.Input.Ships[1]
					q.Destruct++
					return s.PutShip(&q)
				}
			})
			out, e := s.CommitNoncombatWalk(in)
			var ce *CommandError
			if out != CommandNotCommitted || !errors.As(e, &ce) || ce.Code != CommandStale {
				t.Fatal(out, e)
			}
			got, e := s.GetStar(in.Input.Star.Number)
			if e != nil || got.AP[0] != 50 {
				t.Fatal("partial AP", e)
			}
			sh, e := s.GetShip(77)
			if e != nil || sh.LandX != 0 {
				t.Fatal("partial move", e)
			}
		})
	}
}
func TestWalkContractFenceMaintenance(t *testing.T) {
	s, _, in := walkFixture(t)
	before := enrollmentSnapshot(t, s)
	s.SetWriteFence(true)
	out, e := s.CommitNoncombatWalk(in)
	s.SetWriteFence(false)
	if out != CommandNotCommitted || !errors.Is(e, ErrWriteFenced) || enrollmentSnapshot(t, s) != before {
		t.Fatal(out, e)
	}
	if e := s.PutMaintenance(&MaintenanceRecord{SchemaVersion: MaintenanceSchemaVersion, Quiesced: true, SetAt: time.Now().UTC(), Reason: "test"}); e != nil {
		t.Fatal(e)
	}
	before = enrollmentSnapshot(t, s)
	out, e = s.CommitNoncombatWalk(in)
	if out != CommandNotCommitted || e == nil || enrollmentSnapshot(t, s) != before {
		t.Fatal(out, e)
	}
}

func TestWalkContractDeadReadOnlyWitness(t *testing.T) {
	s, _, in := walkFixture(t)
	dead := in.Input.Ships[1]
	dead.Alive = false
	dead.Docked = true
	dead.LandX = 1
	dead.Guns = model.GunPrimary
	dead.Primary = 1
	dead.Retaliate = 1
	dead.Destruct = 1
	if e := s.PutShip(dead); e != nil {
		t.Fatal(e)
	}
	var e error
	in.Proposed, e = commandrules.PlanWalk(in.Input)
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.CommitNoncombatWalk(in)
	if e != nil || out != CommandCommitted {
		t.Fatal(out, e)
	}
	assertWalkWorld(t, s, in, true)
}
