package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

func factoryStoreFixture(t *testing.T) (*Store, string, FactoryActivation) {
	t.Helper()
	s, path, _ := fixtureStore(t)
	star, e := s.GetStar(0)
	if e != nil {
		t.Fatal(e)
	}
	p := &model.Planet{StarNum: 0, PlanetNum: 0, MaxX: 2, MaxY: 1, Ships: 7, Popn: 3, Sectors: []model.Sector{{Owner: 1, Race: 1, Popn: 3, Fert: 100}, {Resource: 42}}}
	p.Info[0].Resource = 100
	f := &model.Ship{Number: 7, Owner: 1, Race: 1, Type: model.OtypeFactory, Alive: true, Active: true, Docked: true, WhatOrbits: model.LevelPlan, WhatDest: model.LevelPlan, NextShip: 8, BuildCost: 7, Popn: 1, Notified: true, Special: model.ShipSpecial{Timer: &model.TimerSpecial{Count: 3}}}
	witness := &model.Ship{Number: 8, Owner: 1, Alive: true, Active: true, WhatOrbits: model.LevelPlan, NextShip: 0, Resource: 19}
	n := *f
	tmp := *f.Special.Timer
	n.Special.Timer = &tmp
	n.On = true
	n.Notified = false
	q := *p
	q.Sectors = append([]model.Sector(nil), p.Sectors...)
	q.Info[0].Resource = 86
	for _, sh := range []*model.Ship{f, witness} {
		if e := s.PutShip(sh); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.PutPlanet(p); e != nil {
		t.Fatal(e)
	}
	return s, path, FactoryActivation{Player: 1, Governor: 0, Factory: CommandShip{f, &n}, Planet: CommandPlanet{p, &q}, Star: star, Orbit: []*model.Ship{f, witness}}
}
func assertFactoryRecords(t *testing.T, s *Store, in FactoryActivation, after bool) {
	t.Helper()
	p, sh := in.Planet.Expected, in.Factory.Expected
	if after {
		p = in.Planet.Proposed
		sh = in.Factory.Proposed
	}
	got, e := s.GetPlanet(p.StarNum, p.PlanetNum)
	if e != nil || !reflect.DeepEqual(p, got) {
		t.Fatal("complete planet mismatch", e)
	}
	ship, e := s.GetShip(sh.Number)
	if e != nil || !reflect.DeepEqual(sh, ship) {
		t.Fatal("complete factory mismatch", e)
	}
	st, e := s.GetStar(in.Star.Number)
	if e != nil || !reflect.DeepEqual(st, in.Star) {
		t.Fatal("read-only star changed", e)
	}
	for _, w := range in.Orbit {
		if w.Number != sh.Number {
			got, e := s.GetShip(w.Number)
			if e != nil || !reflect.DeepEqual(got, w) {
				t.Fatal("orbit witness changed", e)
			}
		}
	}
}
func TestFactoryActivationAtomicAndReopen(t *testing.T) {
	s, path, in := factoryStoreFixture(t)
	before, _ := json.Marshal(in)
	meta := map[string][]byte{}
	for _, key := range [][]byte{KeyCommit, KeySchedule, KeyMaintenance, KeyUniverse} {
		b, e := s.GetMeta(key)
		if e != nil {
			t.Fatal(e)
		}
		meta[string(key)] = b
	}
	out, e := s.CommitFactoryActivation(in)
	if out != CommandCommitted || e != nil {
		t.Fatal(out, e)
	}
	assertFactoryRecords(t, s, in, true)
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("input proposals mutated")
	}
	for k, v := range meta {
		b, e := s.GetMeta([]byte(k))
		if e != nil || !bytes.Equal(b, v) {
			t.Fatal("metadata changed", k, e)
		}
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = reopened.Close() }()
	assertFactoryRecords(t, reopened, in, true)
}
func TestFactoryActivationFailureAtomicity(t *testing.T) {
	for _, stage := range []string{"factory.before", "factory.validated", "factory.write:1", "factory.write:2", "factory.before-commit"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := factoryStoreFixture(t)
				before := enrollmentSnapshot(t, s)
				reached := false
				s.SetMetaFault(func(op string) error {
					if op == stage {
						reached = true
						if panics {
							panic("private fault text")
						}
						return errors.New("private fault text")
					}
					return nil
				})
				out, e := s.CommitFactoryActivation(in)
				assertCommandError(t, out, e, CommandIO, CommandNotCommitted)
				if !reached || s.CommandWriteUncertain() || before != enrollmentSnapshot(t, s) {
					t.Fatal("aborted write left records/allocator/metadata changes")
				}
				assertFactoryRecords(t, s, in, false)
				if strings.Contains(e.Error(), "private") {
					t.Fatal("hook text escaped")
				}
			})
		}
	}
}
func TestFactoryActivationCommittedAndUncertain(t *testing.T) {
	for _, stage := range []string{"factory.commit-error", "factory.after-commit"} {
		for _, panics := range []bool{false, true} {
			for _, readFail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/panic=%t/readFail=%t", stage, panics, readFail), func(t *testing.T) {
					s, _, in := factoryStoreFixture(t)
					reached := false
					s.SetMetaFault(func(op string) error {
						if op == "command.readback" && readFail {
							return errors.New("read unavailable")
						}
						if op == stage {
							reached = true
							if panics {
								panic("private report failure")
							}
							return errors.New("private report failure")
						}
						return nil
					})
					want := CommandCommitted
					if stage == "factory.commit-error" {
						want = CommandUncertain
					}
					out, e := s.CommitFactoryActivation(in)
					assertCommandError(t, out, e, CommandIO, want)
					if !reached {
						t.Fatal("fault boundary not reached")
					}
					assertFactoryRecords(t, s, in, true)
					if want == CommandUncertain {
						visibility := "proposal_visible"
						if readFail {
							visibility = "unavailable"
						}
						if e.(*CommandError).Readback != visibility || !s.CommandWriteUncertain() {
							t.Fatal("visibility promoted to durability")
						}
						s.SetMetaFault(nil)
						before := enrollmentSnapshot(t, s)
						out, e = s.CommitFactoryActivation(in)
						assertCommandError(t, out, e, CommandIO, CommandUncertain)
						out, e = s.CommitCommandRecords(CommandRecords{})
						assertCommandError(t, out, e, CommandIO, CommandUncertain)
						if before != enrollmentSnapshot(t, s) {
							t.Fatal("uncertainty guard bypassed by changing operation")
						}
					}
				})
			}
		}
	}
}
func TestFactoryActivationInvalidFields(t *testing.T) {
	cases := map[string]func(*FactoryActivation){
		"player": func(i *FactoryActivation) { i.Player = 0 }, "governor": func(i *FactoryActivation) { i.Governor = model.MaxGovernors + 1 }, "nil factory": func(i *FactoryActivation) { i.Factory.Expected = nil }, "nil proposed": func(i *FactoryActivation) { i.Factory.Proposed = nil }, "nil planet": func(i *FactoryActivation) { i.Planet.Expected = nil }, "nil star": func(i *FactoryActivation) { i.Star = nil }, "missing orbit": func(i *FactoryActivation) { i.Orbit = nil }, "truncated orbit": func(i *FactoryActivation) { i.Orbit = i.Orbit[:1] }, "extra orbit": func(i *FactoryActivation) { i.Orbit = append(i.Orbit, i.Orbit[0]) },
		"owner": func(i *FactoryActivation) { i.Factory.Expected.Owner = 2 }, "governor authority": func(i *FactoryActivation) { i.Governor = 1; i.Factory.Expected.Governor = 2 }, "not active": func(i *FactoryActivation) { i.Factory.Expected.Active = false }, "dead": func(i *FactoryActivation) { i.Factory.Expected.Alive = false }, "already on": func(i *FactoryActivation) { i.Factory.Expected.On = true }, "no crew": func(i *FactoryActivation) { i.Factory.Expected.Popn = 0 },
		"unsupported carrier": func(i *FactoryActivation) { i.Factory.Expected.WhatOrbits = model.LevelShip }, "not landed": func(i *FactoryActivation) { i.Factory.Expected.Docked = false }, "destination": func(i *FactoryActivation) { i.Factory.Expected.DestPNum = 1 }, "coordinates": func(i *FactoryActivation) { i.Factory.Expected.LandX = 3 }, "geometry": func(i *FactoryActivation) { i.Planet.Expected.Sectors = nil }, "planet outside star": func(i *FactoryActivation) { i.Star.NumPlanets = 0 },
		"unsupported cargo": func(i *FactoryActivation) { i.Factory.Proposed.Resource++ }, "unsupported population": func(i *FactoryActivation) { i.Planet.Proposed.Popn++ }, "unsupported sectors": func(i *FactoryActivation) { i.Planet.Proposed.Sectors[0].Resource++ }, "unsupported orbit": func(i *FactoryActivation) { i.Factory.Proposed.NextShip = 1 }, "wrong cost": func(i *FactoryActivation) { i.Planet.Proposed.Info[0].Resource++ }, "wrong notified": func(i *FactoryActivation) { i.Factory.Proposed.Notified = true }, "nested alias": func(i *FactoryActivation) { i.Factory.Proposed.Special.Timer.Count++ }, "nonfinite": func(i *FactoryActivation) { i.Star.XPos = math.Inf(1) },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, in := factoryStoreFixture(t)
			before := enrollmentSnapshot(t, s)
			change(&in)
			out, e := s.CommitFactoryActivation(in)
			assertCommandError(t, out, e, CommandInvalid, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("invalid operation changed records")
			}
		})
	}
}
func TestFactoryActivationStaleAndFinalPreconditions(t *testing.T) {
	for _, kind := range []string{"planet", "factory", "star AP", "orbit witness", "missing factory", "missing planet", "noncanonical", "after preparation"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := factoryStoreFixture(t)
			change := func() {
				switch kind {
				case "planet", "after preparation":
					p := *in.Planet.Expected
					p.Info[0].Resource++
					if e := s.PutPlanet(&p); e != nil {
						t.Error(e)
					}
				case "factory":
					f := *in.Factory.Expected
					f.Fuel = 2
					if e := s.PutShip(&f); e != nil {
						t.Error(e)
					}
				case "star AP":
					st := *in.Star
					st.AP[0]++
					if e := s.PutStar(&st); e != nil {
						t.Error(e)
					}
				case "orbit witness":
					sh := *in.Orbit[1]
					sh.Resource++
					if e := s.PutShip(&sh); e != nil {
						t.Error(e)
					}
				case "missing factory":
					if e := s.DeleteShip(in.Factory.Expected.Number); e != nil {
						t.Error(e)
					}
				default:
					if e := s.db.Update(func(tx *bolt.Tx) error {
						b := tx.Bucket(BucketPlanets)
						k := PlanetKey(0, 0)
						if kind == "missing planet" {
							return b.Delete(k)
						}
						return b.Put(k, append([]byte(" \n"), b.Get(k)...))
					}); e != nil {
						t.Error(e)
					}
				}
			}
			var before string
			if kind == "after preparation" {
				s.SetMetaFault(func(op string) error {
					if op == "factory.before" {
						change()
						before = enrollmentSnapshot(t, s)
					}
					return nil
				})
			} else {
				change()
				before = enrollmentSnapshot(t, s)
			}
			out, e := s.CommitFactoryActivation(in)
			assertCommandError(t, out, e, CommandStale, CommandNotCommitted)
			if before == "" || before != enrollmentSnapshot(t, s) {
				t.Fatal("stale preimage was overwritten")
			}
		})
	}
}
func TestFactoryActivationCWidthCostAndNoAP(t *testing.T) {
	for _, cost := range []uint16{0, 7, 32767, 32768, 65535} {
		t.Run(fmt.Sprint(cost), func(t *testing.T) {
			s, _, in := factoryStoreFixture(t)
			in.Factory.Expected.BuildCost = cost
			in.Factory.Proposed.BuildCost = cost
			in.Factory.Expected.Damage = 31
			in.Factory.Proposed.Damage = 31
			in.Planet.Expected.Info[0].Resource = 65535
			switch cost {
			case 0:
				in.Planet.Proposed.Info[0].Resource = 65535
			case 7:
				in.Planet.Proposed.Info[0].Resource = 65521
			case 32767, 65535:
				in.Planet.Proposed.Info[0].Resource = 1
			case 32768:
				in.Planet.Proposed.Info[0].Resource = 65535
			} // High-cost cases propose the old low-word result; they must fail.
			if e := s.PutShip(in.Factory.Expected); e != nil {
				t.Fatal(e)
			}
			if e := s.PutPlanet(in.Planet.Expected); e != nil {
				t.Fatal(e)
			}
			before := enrollmentSnapshot(t, s)
			out, e := s.CommitFactoryActivation(in)
			if cost <= 32767 {
				if e != nil || out != CommandCommitted {
					t.Fatal("defined C cost/damaged activation rejected", out, e)
				}
				assertFactoryRecords(t, s, in, true)
			} else {
				assertCommandError(t, out, e, CommandInvalid, CommandNotCommitted)
				if before != enrollmentSnapshot(t, s) {
					t.Fatal("overflow wrapped to an affordable cost")
				}
			}
		})
	}
}
func TestFactoryActivationFenceAndConcurrentConflict(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late=%t", late), func(t *testing.T) {
			s, _, in := factoryStoreFixture(t)
			before := enrollmentSnapshot(t, s)
			if late {
				s.SetMetaFault(func(op string) error {
					if op == "factory.before" {
						s.SetWriteFence(true)
					}
					return nil
				})
			} else {
				s.SetWriteFence(true)
			}
			out, e := s.CommitFactoryActivation(in)
			assertCommandError(t, out, e, CommandFenced, CommandNotCommitted)
			if !errors.Is(e, ErrWriteFenced) || s.FencedWrites() != 1 || s.LastFencedWrite() != "CommitFactoryActivation" || before != enrollmentSnapshot(t, s) {
				t.Fatal("fence bypassed")
			}
		})
	}
	t.Run("same handle distinct adapters", func(t *testing.T) {
		s, _, in := factoryStoreFixture(t)
		adapter := &Store{db: s.db}
		start := make(chan struct{})
		outcomes := make(chan CommandOutcome, 2)
		var wg sync.WaitGroup
		for _, store := range []*Store{s, adapter} {
			wg.Add(1)
			go func(store *Store) {
				defer wg.Done()
				<-start
				o, e := store.CommitFactoryActivation(in)
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
		commits, aborts := 0, 0
		for o := range outcomes {
			switch o {
			case CommandCommitted:
				commits++
			case CommandNotCommitted:
				aborts++
			}
		}
		if commits != 1 || aborts != 1 {
			t.Fatal("concurrent activation double-debited")
		}
		assertFactoryRecords(t, s, in, true)
	})
}
