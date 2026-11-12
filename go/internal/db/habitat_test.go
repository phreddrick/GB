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

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

var habitatRates = commandrules.SizeFactors{Guns: .1, Crew: .01, Resource: .02, Fuel: .01, Destruct: .02}

// habitatStoreFixture: habitat #70 (owner 1, resource 100, max hanger 10,
// hanger 2) orbits planet 0/0 together with witness #8; factory #77 (size 2,
// build cost 7, crew 1) and witness #9 are docked aboard the habitat. The
// activation costs 28 and leaves the factory at size 1 and the hanger at 1.
func habitatStoreFixture(t *testing.T) (*Store, string, HabitatActivation) {
	t.Helper()
	s, path, _ := fixtureStore(t)
	star, e := s.GetStar(0)
	if e != nil {
		t.Fatal(e)
	}
	race, e := s.GetRace(1)
	if e != nil {
		t.Fatal(e)
	}
	race.Governor[0].Active = true
	p := &model.Planet{StarNum: 0, PlanetNum: 0, MaxX: 2, MaxY: 1, Ships: 70, Popn: 3, Sectors: []model.Sector{{Owner: 1, Race: 1, Popn: 3, Fert: 100}, {Resource: 42}}}
	p.Info[0].Resource = 100
	carrier := &model.Ship{Number: 70, Owner: 1, Race: 1, Type: model.StypeHabitat, Alive: true, Active: true, WhatOrbits: model.LevelPlan, WhatDest: model.LevelPlan, NextShip: 8, Ships: 77, Popn: 1, MaxCrew: 2000, MaxResource: 5000, MaxHanger: 10, Hanger: 2, Resource: 100, Notified: true}
	f := &model.Ship{Number: 77, Owner: 1, Race: 1, Type: model.OtypeFactory, Alive: true, Active: true, Docked: true, WhatOrbits: model.LevelShip, WhatDest: model.LevelShip, DestShipNo: 70, NextShip: 9, BuildCost: 7, Popn: 1, MaxCrew: 20, MaxResource: 50, Size: 2, Notified: true, Special: model.ShipSpecial{Timer: &model.TimerSpecial{Count: 3}}}
	docked := &model.Ship{Number: 9, Owner: 1, Alive: true, Active: true, Docked: true, WhatOrbits: model.LevelShip, WhatDest: model.LevelShip, DestShipNo: 70, Resource: 5}
	orbit := &model.Ship{Number: 8, Owner: 2, Alive: true, Active: true, WhatOrbits: model.LevelPlan, Resource: 19}
	n := *f
	tmp := *f.Special.Timer
	n.Special.Timer = &tmp
	n.On = true
	n.Notified = false
	n.Size = 1
	d := *carrier
	d.Resource = 72
	d.Hanger = 1
	for _, sh := range []*model.Ship{carrier, f, docked, orbit} {
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
	in := HabitatActivation{Player: 1, Governor: 0, Factory: CommandShip{f, &n}, Carrier: CommandShip{carrier, &d}, Context: CommandContext{Race: race, Stars: []*model.Star{star}, Planets: []*model.Planet{p}, Ships: []*model.Ship{orbit, docked, f, carrier}}, Size: habitatRates}
	return s, path, in
}
func assertHabitatRecords(t *testing.T, s *Store, in HabitatActivation, after bool) {
	t.Helper()
	for _, x := range in.Context.Ships {
		want := x
		if after && x.Number == in.Factory.Expected.Number {
			want = in.Factory.Proposed
		}
		if after && x.Number == in.Carrier.Expected.Number {
			want = in.Carrier.Proposed
		}
		got, e := s.GetShip(x.Number)
		if e != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("complete ship/witness mismatch", x.Number, e)
		}
	}
	for _, x := range in.Context.Stars {
		got, e := s.GetStar(x.Number)
		if e != nil || !reflect.DeepEqual(got, x) {
			t.Fatal("read-only star changed", e)
		}
	}
	for _, x := range in.Context.Planets {
		got, e := s.GetPlanet(x.StarNum, x.PlanetNum)
		if e != nil || !reflect.DeepEqual(got, x) {
			t.Fatal("read-only planet changed", e)
		}
	}
	race, e := s.GetRace(in.Player)
	if e != nil || !reflect.DeepEqual(race, in.Context.Race) {
		t.Fatal("read-only race changed", e)
	}
}

func TestHabitatActivationAtomicAndReopen(t *testing.T) {
	s, path, in := habitatStoreFixture(t)
	before, _ := json.Marshal(in)
	meta := map[string][]byte{}
	for _, key := range [][]byte{KeyCommit, KeySchedule, KeyMaintenance, KeyUniverse} {
		b, e := s.GetMeta(key)
		if e != nil {
			t.Fatal(e)
		}
		meta[string(key)] = b
	}
	free, _ := s.FreeShipNums()
	out, e := s.CommitHabitatActivation(in)
	if out != CommandCommitted || e != nil {
		t.Fatal(out, e)
	}
	assertHabitatRecords(t, s, in, true)
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
	if f2, _ := s.FreeShipNums(); !reflect.DeepEqual(f2, free) {
		t.Fatal("allocator changed")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = reopened.Close() }()
	assertHabitatRecords(t, reopened, in, true)
	got, _ := reopened.GetShip(70)
	if got.Resource != 72 || got.Hanger != 1 || got.Mass != in.Carrier.Expected.Mass {
		t.Fatal("carrier resource/hanger/mass semantics", got.Resource, got.Hanger)
	}
}
func TestHabitatActivationFailureAtomicity(t *testing.T) {
	for _, stage := range []string{"habitat.before", "habitat.validated", "habitat.write:1", "habitat.write:2", "habitat.before-commit"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := habitatStoreFixture(t)
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
				out, e := s.CommitHabitatActivation(in)
				assertCommandError(t, out, e, CommandIO, CommandNotCommitted)
				if !reached || s.CommandWriteUncertain() || before != enrollmentSnapshot(t, s) {
					t.Fatal("aborted write left records/allocator/metadata changes")
				}
				assertHabitatRecords(t, s, in, false)
				if strings.Contains(e.Error(), "private") {
					t.Fatal("hook text escaped")
				}
			})
		}
	}
}
func TestHabitatActivationCommittedAndUncertain(t *testing.T) {
	for _, stage := range []string{"habitat.commit-error", "habitat.after-commit"} {
		for _, panics := range []bool{false, true} {
			for _, readFail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/panic=%t/readFail=%t", stage, panics, readFail), func(t *testing.T) {
					s, _, in := habitatStoreFixture(t)
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
					if stage == "habitat.commit-error" {
						want = CommandUncertain
					}
					out, e := s.CommitHabitatActivation(in)
					assertCommandError(t, out, e, CommandIO, want)
					if !reached {
						t.Fatal("fault boundary not reached")
					}
					assertHabitatRecords(t, s, in, true)
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
						out, e = s.CommitHabitatActivation(in)
						assertCommandError(t, out, e, CommandIO, CommandUncertain)
						out, e = s.CommitFactoryActivation(FactoryActivation{})
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
func TestHabitatActivationInvalidFields(t *testing.T) {
	cases := map[string]func(*HabitatActivation){
		"player": func(i *HabitatActivation) { i.Player = 0 }, "governor": func(i *HabitatActivation) { i.Governor = model.MaxGovernors + 1 }, "inactive governor": func(i *HabitatActivation) { i.Governor = 1 }, "nil factory": func(i *HabitatActivation) { i.Factory.Expected = nil }, "nil proposed": func(i *HabitatActivation) { i.Factory.Proposed = nil }, "nil carrier": func(i *HabitatActivation) { i.Carrier.Expected = nil }, "nil carrier proposed": func(i *HabitatActivation) { i.Carrier.Proposed = nil }, "nil race": func(i *HabitatActivation) { i.Context.Race = nil }, "dissolved race": func(i *HabitatActivation) { i.Context.Race.Dissolved = true },
		"owner": func(i *HabitatActivation) { i.Factory.Expected.Owner = 2 }, "carrier owner": func(i *HabitatActivation) { i.Carrier.Expected.Owner = 2 }, "governor authority": func(i *HabitatActivation) {
			i.Governor = 1
			i.Context.Race.Governor[1].Active = true
			i.Factory.Expected.Governor = 2
		}, "not active": func(i *HabitatActivation) { i.Factory.Expected.Active = false }, "dead": func(i *HabitatActivation) { i.Factory.Expected.Alive = false }, "dead carrier": func(i *HabitatActivation) { i.Carrier.Expected.Alive = false }, "already on": func(i *HabitatActivation) { i.Factory.Expected.On = true }, "no crew": func(i *HabitatActivation) { i.Factory.Expected.Popn = 0 },
		"landed not carried": func(i *HabitatActivation) { i.Factory.Expected.WhatOrbits = model.LevelPlan }, "undocked": func(i *HabitatActivation) { i.Factory.Expected.Docked = false }, "destination level": func(i *HabitatActivation) { i.Factory.Expected.WhatDest = model.LevelPlan }, "other carrier": func(i *HabitatActivation) { i.Factory.Expected.DestShipNo = 9 }, "not a habitat": func(i *HabitatActivation) { i.Carrier.Expected.Type = model.StypeCargo }, "carrier location": func(i *HabitatActivation) { i.Carrier.Expected.PNumOrbits = 1 }, "self carrier": func(i *HabitatActivation) {
			i.Carrier.Expected = i.Factory.Expected
			i.Carrier.Proposed = i.Factory.Proposed
		},
		"missing context ship": func(i *HabitatActivation) { i.Context.Ships = i.Context.Ships[1:] }, "missing carrier context": func(i *HabitatActivation) { i.Context.Ships = i.Context.Ships[:3] }, "duplicate context": func(i *HabitatActivation) { i.Context.Ships = append(i.Context.Ships, i.Context.Ships[0]) }, "missing planet": func(i *HabitatActivation) { i.Context.Planets = nil }, "missing star": func(i *HabitatActivation) { i.Context.Stars = nil }, "geometry": func(i *HabitatActivation) { i.Context.Planets[0].Sectors = nil },
		"unsupported cargo": func(i *HabitatActivation) { i.Factory.Proposed.Resource++ }, "unsupported mass": func(i *HabitatActivation) { i.Carrier.Proposed.Mass++ }, "unsupported carrier popn": func(i *HabitatActivation) { i.Carrier.Proposed.Popn++ }, "unsupported orbit": func(i *HabitatActivation) { i.Factory.Proposed.NextShip = 1 }, "unsupported carrier chain": func(i *HabitatActivation) { i.Carrier.Proposed.Ships = 9 }, "wrong cost": func(i *HabitatActivation) { i.Carrier.Proposed.Resource++ }, "wrong hanger": func(i *HabitatActivation) { i.Carrier.Proposed.Hanger++ }, "wrong size": func(i *HabitatActivation) { i.Factory.Proposed.Size++ }, "wrong notified": func(i *HabitatActivation) { i.Factory.Proposed.Notified = true }, "nested alias": func(i *HabitatActivation) { i.Factory.Proposed.Special.Timer.Count++ }, "nonfinite": func(i *HabitatActivation) { i.Context.Stars[0].XPos = math.Inf(1) }, "nonfinite rate": func(i *HabitatActivation) { i.Size.Crew = math.NaN() },
		"insufficient resources": func(i *HabitatActivation) { i.Carrier.Expected.Resource = 27 }, "insufficient hanger": func(i *HabitatActivation) { i.Carrier.Expected.Hanger = 10; i.Factory.Expected.Size = 0 }, "hanger below docked size": func(i *HabitatActivation) { i.Carrier.Expected.Hanger = 1 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, in := habitatStoreFixture(t)
			before := enrollmentSnapshot(t, s)
			change(&in)
			out, e := s.CommitHabitatActivation(in)
			assertCommandError(t, out, e, CommandInvalid, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("invalid operation changed records")
			}
		})
	}
}
func TestHabitatActivationChainIntegrity(t *testing.T) {
	// Each case rewrites the stored and context ships consistently so that only
	// the chain/location validation can refuse it.
	cases := map[string]func(*HabitatActivation, map[uint32]*model.Ship){
		"factory absent from carrier chain": func(i *HabitatActivation, m map[uint32]*model.Ship) { m[70].Ships = 9; m[9].NextShip = 0 },
		"factory twice in chain":            func(i *HabitatActivation, m map[uint32]*model.Ship) { m[9].NextShip = 77 },
		"docked witness on other carrier":   func(i *HabitatActivation, m map[uint32]*model.Ship) { m[9].DestShipNo = 8 },
		"carrier absent from planet chain": func(i *HabitatActivation, m map[uint32]*model.Ship) {
			i.Context.Planets[0].Ships = 8
			m[8].NextShip = 0
		},
		"planet chain cycle":          func(i *HabitatActivation, m map[uint32]*model.Ship) { m[8].NextShip = 70 },
		"orbit witness dead":          func(i *HabitatActivation, m map[uint32]*model.Ship) { m[8].Alive = false },
		"carrier planet outside star": func(i *HabitatActivation, m map[uint32]*model.Ship) { i.Context.Stars[0].NumPlanets = 0 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, in := habitatStoreFixture(t)
			m := map[uint32]*model.Ship{}
			for _, sh := range in.Context.Ships {
				m[sh.Number] = sh
			}
			change(&in, m)
			for _, sh := range in.Context.Ships {
				if e := s.PutShip(sh); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.PutPlanet(in.Context.Planets[0]); e != nil {
				t.Fatal(e)
			}
			if e := s.PutStar(in.Context.Stars[0]); e != nil {
				t.Fatal(e)
			}
			// Keep proposals consistent with the rewritten expected records.
			n := *m[77]
			n.On = true
			n.Notified = false
			n.Size = 1
			if m[77].Special.Timer != nil {
				tmp := *m[77].Special.Timer
				n.Special.Timer = &tmp
			}
			d := *m[70]
			d.Resource = 72
			d.Hanger = 1
			in.Factory = CommandShip{m[77], &n}
			in.Carrier = CommandShip{m[70], &d}
			before := enrollmentSnapshot(t, s)
			out, e := s.CommitHabitatActivation(in)
			assertCommandError(t, out, e, CommandInvalid, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("invalid chain changed records")
			}
		})
	}
}
func TestHabitatActivationStaleAndFinalPreconditions(t *testing.T) {
	for _, kind := range []string{"carrier", "factory", "docked witness", "orbit witness", "planet", "star", "race", "missing factory", "missing carrier", "noncanonical", "after preparation"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := habitatStoreFixture(t)
			change := func() {
				switch kind {
				case "carrier", "after preparation":
					c := *in.Carrier.Expected
					c.Resource++
					if e := s.PutShip(&c); e != nil {
						t.Error(e)
					}
				case "factory":
					f := *in.Factory.Expected
					f.Fuel = 2
					if e := s.PutShip(&f); e != nil {
						t.Error(e)
					}
				case "docked witness":
					sh := *in.Context.Ships[1]
					sh.Resource++
					if e := s.PutShip(&sh); e != nil {
						t.Error(e)
					}
				case "orbit witness":
					sh := *in.Context.Ships[0]
					sh.Resource++
					if e := s.PutShip(&sh); e != nil {
						t.Error(e)
					}
				case "planet":
					p := *in.Context.Planets[0]
					p.Info[0].Resource++
					if e := s.PutPlanet(&p); e != nil {
						t.Error(e)
					}
				case "star":
					st := *in.Context.Stars[0]
					st.AP[0]++
					if e := s.PutStar(&st); e != nil {
						t.Error(e)
					}
				case "race":
					r := *in.Context.Race
					r.Tech++
					if e := s.PutRace(&r); e != nil {
						t.Error(e)
					}
				case "missing factory":
					if e := s.DeleteShip(77); e != nil {
						t.Error(e)
					}
				case "missing carrier":
					if e := s.DeleteShip(70); e != nil {
						t.Error(e)
					}
				default:
					if e := s.db.Update(func(tx *bolt.Tx) error {
						b := tx.Bucket(BucketShips)
						k := ShipKey(70)
						return b.Put(k, append([]byte(" \n"), b.Get(k)...))
					}); e != nil {
						t.Error(e)
					}
				}
			}
			var before string
			if kind == "after preparation" {
				s.SetMetaFault(func(op string) error {
					if op == "habitat.before" {
						change()
						before = enrollmentSnapshot(t, s)
					}
					return nil
				})
			} else {
				change()
				before = enrollmentSnapshot(t, s)
			}
			out, e := s.CommitHabitatActivation(in)
			assertCommandError(t, out, e, CommandStale, CommandNotCommitted)
			if before == "" || before != enrollmentSnapshot(t, s) {
				t.Fatal("stale preimage was overwritten")
			}
		})
	}
}

// Defined C arithmetic at the record widths: oncost is an unsigned int, the
// hanger expression is int. A build cost of 16384 or more can never be paid
// from a uint16 stockpile and must not wrap; 16383 (cost 65532) is payable.
func TestHabitatActivationCWidthCostHangerAndNoAP(t *testing.T) {
	for _, cost := range []uint16{0, 7, 16383, 16384, 32768, 65535} {
		t.Run(fmt.Sprint(cost), func(t *testing.T) {
			s, _, in := habitatStoreFixture(t)
			in.Factory.Expected.BuildCost = cost
			in.Factory.Proposed.BuildCost = cost
			in.Factory.Expected.Damage = 31
			in.Factory.Proposed.Damage = 31
			in.Carrier.Expected.Resource = 65535
			switch cost {
			case 0:
				in.Carrier.Proposed.Resource = 65535
			case 7:
				in.Carrier.Proposed.Resource = 65507
			case 16383:
				in.Carrier.Proposed.Resource = 3
			case 16384:
				in.Carrier.Proposed.Resource = 65535 // 4*16384 = 65536 wraps to 0 in a uint16 temporary
			case 32768:
				in.Carrier.Proposed.Resource = 65535
			case 65535:
				in.Carrier.Proposed.Resource = 65535 - 65532 // old low-word result 65532
			}
			for _, sh := range []*model.Ship{in.Factory.Expected, in.Carrier.Expected} {
				if e := s.PutShip(sh); e != nil {
					t.Fatal(e)
				}
			}
			before := enrollmentSnapshot(t, s)
			out, e := s.CommitHabitatActivation(in)
			if cost <= 16383 {
				if e != nil || out != CommandCommitted {
					t.Fatal("defined C cost/damaged activation rejected", out, e)
				}
				assertHabitatRecords(t, s, in, true)
				st, _ := s.GetStar(0)
				if st.AP[0] != in.Context.Stars[0].AP[0] {
					t.Fatal("AP changed")
				}
			} else {
				assertCommandError(t, out, e, CommandInvalid, CommandNotCommitted)
				if before != enrollmentSnapshot(t, s) {
					t.Fatal("overflow wrapped to an affordable cost")
				}
			}
		})
	}
	t.Run("hanger over capacity is signed arithmetic, not uint16 underflow", func(t *testing.T) {
		// max_hanger 10, hanger 12 (over-full), docked size 2: C computes
		// (10-12)+2 = 0 free, needed 1 > 0 -> refusal. The old Go uint16
		// subtraction gave 65534 free and would have activated.
		s, _, in := habitatStoreFixture(t)
		in.Carrier.Expected.Hanger = 12
		in.Carrier.Proposed.Hanger = 11
		if e := s.PutShip(in.Carrier.Expected); e != nil {
			t.Fatal(e)
		}
		before := enrollmentSnapshot(t, s)
		out, e := s.CommitHabitatActivation(in)
		assertCommandError(t, out, e, CommandInvalid, CommandNotCommitted)
		if before != enrollmentSnapshot(t, s) {
			t.Fatal("underflow activated an over-full habitat")
		}
	})
	t.Run("zero-size factory", func(t *testing.T) {
		// Stored size 0 with hanger 0: new size 1 needs 1 of the free 10.
		s, _, in := habitatStoreFixture(t)
		in.Factory.Expected.Size = 0
		in.Carrier.Expected.Hanger = 0
		in.Carrier.Proposed.Hanger = 1
		for _, sh := range []*model.Ship{in.Factory.Expected, in.Carrier.Expected} {
			if e := s.PutShip(sh); e != nil {
				t.Fatal(e)
			}
		}
		out, e := s.CommitHabitatActivation(in)
		if out != CommandCommitted || e != nil {
			t.Fatal(out, e)
		}
		assertHabitatRecords(t, s, in, true)
	})
}
func TestHabitatActivationFenceAndConcurrentConflict(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late=%t", late), func(t *testing.T) {
			s, _, in := habitatStoreFixture(t)
			before := enrollmentSnapshot(t, s)
			if late {
				s.SetMetaFault(func(op string) error {
					if op == "habitat.before" {
						s.SetWriteFence(true)
					}
					return nil
				})
			} else {
				s.SetWriteFence(true)
			}
			out, e := s.CommitHabitatActivation(in)
			assertCommandError(t, out, e, CommandFenced, CommandNotCommitted)
			if !errors.Is(e, ErrWriteFenced) || s.FencedWrites() != 1 || s.LastFencedWrite() != "CommitHabitatActivation" || before != enrollmentSnapshot(t, s) {
				t.Fatal("fence bypassed")
			}
		})
	}
	t.Run("same handle distinct adapters", func(t *testing.T) {
		s, _, in := habitatStoreFixture(t)
		adapter := &Store{db: s.db}
		start := make(chan struct{})
		outcomes := make(chan CommandOutcome, 2)
		var wg sync.WaitGroup
		for _, store := range []*Store{s, adapter} {
			wg.Add(1)
			go func(store *Store) {
				defer wg.Done()
				<-start
				o, e := store.CommitHabitatActivation(in)
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
		assertHabitatRecords(t, s, in, true)
	})
}
