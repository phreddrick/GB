package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/model"
	"math"
	"reflect"
	"sync"
	"testing"
)

func telescopeStoreFixture(t *testing.T) (*Store, string, TelescopeAim) {
	t.Helper()
	s, path, _ := fixtureStore(t)
	st, e := s.GetStar(0)
	if e != nil {
		t.Fatal(e)
	}
	race, e := s.GetRace(1)
	if e != nil {
		t.Fatal(e)
	}
	race.Governor[0].Active = true
	p := &model.Planet{StarNum: 0, PlanetNum: 0, MaxX: 2, MaxY: 1, Ships: 7, Sectors: []model.Sector{{Resource: 42}, {Resource: 37}}}
	p.Info[0].Resource = 100
	f := &model.Ship{Number: 7, Owner: 1, Race: 1, Type: model.OtypeStele, Alive: true, Active: true, Docked: true, WhatOrbits: model.LevelPlan, WhatDest: model.LevelPlan, Popn: 1, MaxCrew: 10, NextShip: 8, Fuel: 10, Mass: 100, Notified: true, Special: model.ShipSpecial{AimedAt: &model.AimedAtSpecial{Intensity: 37}, Timer: &model.TimerSpecial{Count: 3}}}
	witness := &model.Ship{Number: 8, Owner: 2, Alive: true, Active: true, WhatOrbits: model.LevelPlan, Resource: 19}
	n := *f
	timer := *f.Special.Timer
	n.Special.Timer = &timer
	aa := *f.Special.AimedAt
	aa.Level = model.LevelPlan
	n.Special.AimedAt = &aa
	n.Fuel = 10 - .3
	n.Mass = 100 - .3*.05
	n.Notified = false
	q := *p
	q.Sectors = append([]model.Sector(nil), p.Sectors...)
	q.Info[0].Explored = true
	starNext := *st
	starNext.SetExplored(1)
	for _, sh := range []*model.Ship{f, witness} {
		if e := s.PutShip(sh); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.PutPlanet(p); e != nil {
		t.Fatal(e)
	}
	if e := s.PutRace(race); e != nil {
		t.Fatal(e)
	}
	return s, path, TelescopeAim{Player: 1, Governor: 0, Ship: CommandShip{f, &n}, Target: model.Place{Level: model.LevelPlan}, Star: &CommandStar{Expected: st, Proposed: &starNext}, Planet: &CommandPlanet{p, &q}, Context: TelescopeContext{Race: race, Stars: []*model.Star{st}, Planets: []*model.Planet{p}, Ships: []*model.Ship{witness, f}}, ManeuverFuel: .3, MassFuel: .05}
}
func assertTelescopeRecords(t *testing.T, s *Store, in TelescopeAim, after bool) {
	t.Helper()
	for _, x := range in.Context.Ships {
		want := x
		if after && x.Number == in.Ship.Expected.Number {
			want = in.Ship.Proposed
		}
		got, e := s.GetShip(x.Number)
		if e != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("complete ship/witness mismatch", e)
		}
	}
	for _, x := range in.Context.Stars {
		want := x
		if after && in.Star != nil && x.Number == in.Star.Expected.Number {
			want = in.Star.Proposed
		}
		got, e := s.GetStar(x.Number)
		if e != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("complete star/witness mismatch", e)
		}
	}
	for _, x := range in.Context.Planets {
		want := x
		if after && in.Planet != nil && x.StarNum == in.Planet.Expected.StarNum && x.PlanetNum == in.Planet.Expected.PlanetNum {
			want = in.Planet.Proposed
		}
		got, e := s.GetPlanet(x.StarNum, x.PlanetNum)
		if e != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("complete planet/witness mismatch", e)
		}
	}
	got, e := s.GetRace(in.Player)
	if e != nil || !reflect.DeepEqual(got, in.Context.Race) {
		t.Fatal("read-only authority changed", e)
	}
}
func TestTelescopeAimAtomicAndReopen(t *testing.T) {
	s, path, in := telescopeStoreFixture(t)
	before, e := json.Marshal(in)
	if e != nil {
		t.Fatal(e)
	}
	aux := dumpTelescopeAux(t, s)
	calls := []string{}
	s.SetMetaFault(func(op string) error { calls = append(calls, op); return nil })
	out, e := s.CommitTelescopeAim(in)
	if e != nil || out != CommandCommitted {
		t.Fatal(out, e)
	}
	assertTelescopeRecords(t, s, in, true)
	after, e := json.Marshal(in)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("borrowed input mutated", e)
	}
	for _, op := range []string{"telescope.before", "telescope.validated", "telescope.write:1", "telescope.write:2", "telescope.write:3", "telescope.before-commit", "telescope.after-commit"} {
		if !containsTelescope(calls, op) {
			t.Fatal("missing real write boundary", op)
		}
	}
	if aux != dumpTelescopeAux(t, s) {
		t.Fatal("allocator,turn,maintenance or other metadata changed")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = reopened.Close() }()
	assertTelescopeRecords(t, reopened, in, true)
}
func containsTelescope(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
func dumpTelescopeAux(t *testing.T, s *Store) string {
	t.Helper()
	var rows []any
	for _, key := range [][]byte{KeyCommit, KeySchedule, KeyMaintenance, KeyUniverse} {
		b, e := s.GetMeta(key)
		if e != nil {
			t.Fatal(e)
		}
		rows = append(rows, b)
	}
	ships, e := s.FreeShipNums()
	if e != nil {
		t.Fatal(e)
	}
	com, e := s.FreeCommodNums()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := s.GetStarData()
	if e != nil {
		t.Fatal(e)
	}
	rows = append(rows, ships, com, sd)
	b, e := json.Marshal(rows)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestTelescopeAimFailureAtomicity(t *testing.T) {
	for _, stage := range []string{"telescope.before", "telescope.validated", "telescope.write:1", "telescope.write:2", "telescope.write:3", "telescope.before-commit"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := telescopeStoreFixture(t)
				before := enrollmentSnapshot(t, s)
				hit := false
				s.SetMetaFault(func(op string) error {
					if op == stage {
						hit = true
						if panics {
							panic("private telescope fault")
						}
						return errors.New("private telescope fault")
					}
					return nil
				})
				out, e := s.CommitTelescopeAim(in)
				assertCommandError(t, out, e, CommandIO, CommandNotCommitted)
				if !hit || before != enrollmentSnapshot(t, s) {
					t.Fatal("known abort leaked partial records or boundary not reached")
				}
				assertTelescopeRecords(t, s, in, false)
			})
		}
	}
}
func TestTelescopeAimCommittedAndUncertain(t *testing.T) {
	for _, stage := range []string{"telescope.commit-error", "telescope.after-commit"} {
		for _, panics := range []bool{false, true} {
			for _, readFail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/panic=%t/readFail=%t", stage, panics, readFail), func(t *testing.T) {
					s, _, in := telescopeStoreFixture(t)
					hit := false
					s.SetMetaFault(func(op string) error {
						if op == "command.readback" && readFail {
							return errors.New("read unavailable")
						}
						if op == stage {
							hit = true
							if panics {
								panic("private telescope fault")
							}
							return errors.New("private telescope fault")
						}
						return nil
					})
					want := CommandCommitted
					if stage == "telescope.commit-error" {
						want = CommandUncertain
					}
					out, e := s.CommitTelescopeAim(in)
					assertCommandError(t, out, e, CommandIO, want)
					if !hit {
						t.Fatal("report boundary not reached")
					}
					assertTelescopeRecords(t, s, in, true)
					if want == CommandUncertain {
						rb := "proposal_visible"
						if readFail {
							rb = "unavailable"
						}
						if e.(*CommandError).Readback != rb || !s.CommandWriteUncertain() {
							t.Fatal("readback promoted to durability")
						}
						s.SetMetaFault(nil)
						before := enrollmentSnapshot(t, s)
						out, e = s.CommitFactoryActivation(FactoryActivation{})
						assertCommandError(t, out, e, CommandIO, CommandUncertain)
						out, e = s.CommitCommandRecords(CommandRecords{})
						assertCommandError(t, out, e, CommandIO, CommandUncertain)
						out, e = s.CommitTelescopeAim(in)
						assertCommandError(t, out, e, CommandIO, CommandUncertain)
						if before != enrollmentSnapshot(t, s) {
							t.Fatal("shared uncertainty guard bypassed")
						}
					}
				})
			}
		}
	}
}
func TestTelescopeAimInvalidAndStale(t *testing.T) {
	cases := map[string]func(*TelescopeAim){
		"player": func(i *TelescopeAim) { i.Player = 0 }, "governor": func(i *TelescopeAim) { i.Governor = model.MaxGovernors + 1 }, "nilship": func(i *TelescopeAim) { i.Ship.Expected = nil }, "nilproposal": func(i *TelescopeAim) { i.Ship.Proposed = nil }, "nilstar": func(i *TelescopeAim) { i.Star = nil }, "nilplanet": func(i *TelescopeAim) { i.Planet = nil }, "nilrace": func(i *TelescopeAim) { i.Context.Race = nil }, "inactivegovernor": func(i *TelescopeAim) { i.Context.Race.Governor[0].Active = false }, "authority": func(i *TelescopeAim) {
			i.Governor = 1
			i.Context.Race.Governor[1].Active = true
			i.Ship.Expected.Governor = 2
		}, "owner": func(i *TelescopeAim) { i.Ship.Expected.Owner = 2 }, "dead": func(i *TelescopeAim) { i.Ship.Expected.Alive = false }, "inactive": func(i *TelescopeAim) { i.Ship.Expected.Active = false }, "nocrew": func(i *TelescopeAim) { i.Ship.Expected.Popn = 0 }, "nontelescope": func(i *TelescopeAim) { i.Ship.Expected.Type = model.StypeMirror }, "NaNfuel": func(i *TelescopeAim) { i.ManeuverFuel = math.NaN() }, "Infmass": func(i *TelescopeAim) { i.MassFuel = math.Inf(1) }, "negativerate": func(i *TelescopeAim) { i.MassFuel = -1 }, "targetoverflow": func(i *TelescopeAim) { i.Target.ShipNo = 65536 }, "wronglevel": func(i *TelescopeAim) { i.Target.Level = 99 }, "wrongaim": func(i *TelescopeAim) { i.Ship.Proposed.Special.AimedAt.StarNum = 9 }, "wrongintensity": func(i *TelescopeAim) { i.Ship.Proposed.Special.AimedAt.Intensity++ }, "wrongfuel": func(i *TelescopeAim) { i.Ship.Proposed.Fuel++ }, "wrongmass": func(i *TelescopeAim) { i.Ship.Proposed.Mass++ }, "wrongnotification": func(i *TelescopeAim) { i.Ship.Proposed.Notified = true }, "AP": func(i *TelescopeAim) { i.Star.Proposed.AP[0]++ }, "stockpile": func(i *TelescopeAim) { i.Planet.Proposed.Info[0].Resource++ }, "sector": func(i *TelescopeAim) { i.Planet.Proposed.Sectors[0].Resource++ }, "othergovernorexploration": func(i *TelescopeAim) { i.Planet.Proposed.Info[1].Explored = true }, "sourcechain": func(i *TelescopeAim) { i.Context.Ships = i.Context.Ships[:1] }, "duplicatechain": func(i *TelescopeAim) { i.Context.Ships = append(i.Context.Ships, i.Context.Ships[0]) }, "orbitcycle": func(i *TelescopeAim) { i.Context.Ships[0].NextShip = 7 }, "starchain": func(i *TelescopeAim) { i.Context.Stars = nil }, "missingplanet": func(i *TelescopeAim) { i.Context.Planets = nil }, "otherSpecial": func(i *TelescopeAim) { i.Ship.Proposed.Special.Timer.Count++ },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, in := telescopeStoreFixture(t)
			before := enrollmentSnapshot(t, s)
			change(&in)
			out, e := s.CommitTelescopeAim(in)
			assertCommandError(t, out, e, CommandInvalid, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("invalid request wrote")
			}
		})
	}
	for _, kind := range []string{"ship", "star", "planet", "witness", "race", "orbitalhead"} {
		t.Run("stale/"+kind, func(t *testing.T) {
			s, _, in := telescopeStoreFixture(t)
			expected := ""
			hit := false
			s.SetMetaFault(func(op string) error {
				if op != "telescope.before" {
					return nil
				}
				hit = true
				var err error
				switch kind {
				case "ship":
					q := *in.Ship.Expected
					q.Fuel--
					err = s.PutShip(&q)
				case "star":
					q := *in.Star.Expected
					q.AP[0]++
					err = s.PutStar(&q)
				case "planet":
					q := *in.Planet.Expected
					q.Info[0].Resource++
					err = s.PutPlanet(&q)
				case "witness":
					q := *in.Context.Ships[0]
					q.Resource++
					err = s.PutShip(&q)
				case "race":
					q := *in.Context.Race
					q.Motto = "intervening write"
					err = s.PutRace(&q)
				case "orbitalhead":
					q := *in.Planet.Expected
					q.Ships = 0
					err = s.PutPlanet(&q)
				}
				if err != nil {
					return err
				}
				expected = enrollmentSnapshot(t, s)
				return nil
			})
			out, e := s.CommitTelescopeAim(in)
			assertCommandError(t, out, e, CommandStale, CommandNotCommitted)
			if !hit || expected == "" || expected != enrollmentSnapshot(t, s) {
				t.Fatal("stale request changed complete records after intervening write")
			}
		})
	}
}

func TestTelescopeAimFenceAndConcurrent(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprint(late), func(t *testing.T) {
			s, _, in := telescopeStoreFixture(t)
			before := enrollmentSnapshot(t, s)
			if late {
				s.SetMetaFault(func(op string) error {
					if op == "telescope.before" {
						s.SetWriteFence(true)
					}
					return nil
				})
			} else {
				s.SetWriteFence(true)
			}
			out, e := s.CommitTelescopeAim(in)
			assertCommandError(t, out, e, CommandFenced, CommandNotCommitted)
			if !errors.Is(e, ErrWriteFenced) || s.FencedWrites() != 1 || s.LastFencedWrite() != "CommitTelescopeAim" || before != enrollmentSnapshot(t, s) {
				t.Fatal("write fence bypassed")
			}
		})
	}
	t.Run("finalpreimages", func(t *testing.T) {
		s, _, in := telescopeStoreFixture(t)
		adapter := &Store{db: s.db}
		start := make(chan struct{})
		outcomes := make(chan CommandOutcome, 2)
		var wg sync.WaitGroup
		for _, store := range []*Store{s, adapter} {
			wg.Add(1)
			go func(store *Store) {
				defer wg.Done()
				<-start
				o, e := store.CommitTelescopeAim(in)
				if e != nil {
					var ce *CommandError
					if !errors.As(e, &ce) || ce.Code != CommandStale {
						t.Error(e)
					}
				}
				outcomes <- o
			}(store)
		}
		close(start)
		wg.Wait()
		close(outcomes)
		counts := map[CommandOutcome]int{}
		for o := range outcomes {
			counts[o]++
		}
		if counts[CommandCommitted] != 1 || counts[CommandNotCommitted] != 1 {
			t.Fatal("competing transactions double charged")
		}
		assertTelescopeRecords(t, s, in, true)
	})
}
