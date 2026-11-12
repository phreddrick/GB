package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
)

func cargoStoreFixture(t *testing.T, ships bool) (*Store, string, CargoTransfer) {
	t.Helper()
	s, path, h := habitatStoreFixture(t)
	f, o, p := h.Factory.Expected, h.Carrier.Expected, h.Context.Planets[0]
	f.Type = model.StypeCargo
	f.Resource = 10
	f.Crystals = 10
	f.MaxResource = 50
	f.Mass = 100
	o.Type = model.StypeCargo
	o.Resource = 30
	o.Crystals = 30
	o.MaxResource = 50
	o.Mass = 200
	p.Info[0].Resource = 100
	p.Info[0].Crystals = 100
	if !ships {
		f.WhatOrbits = model.LevelPlan
		f.WhatDest = model.LevelPlan
		f.DestShipNo = 0
		f.NextShip = o.Number
		p.Ships = f.Number
		o.Ships = 9
	}
	for _, ship := range []*model.Ship{f, o} {
		if e := s.PutShip(ship); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.PutPlanet(p); e != nil {
		t.Fatal(e)
	}
	in := CargoTransfer{Player: 1, Selected: CommandShip{Expected: f}, Context: h.Context, Request: commandrules.CargoRequest{Commodity: 'r', Amount: 2, MassResource: .1}}
	next := *f
	next.Resource = 12
	next.Mass = 100.2
	in.Selected.Proposed = &next
	if ships {
		next := *o
		next.Resource = 28
		in.Partner = &CommandShip{o, &next}
	} else {
		next := *p
		next.Info[0].Resource = 98
		in.Planet = &CommandPlanet{p, &next}
	}
	return s, path, in
}
func assertCargoRecords(t *testing.T, s *Store, in CargoTransfer) {
	t.Helper()
	got, e := s.GetShip(in.Selected.Expected.Number)
	if e != nil || !reflect.DeepEqual(got, in.Selected.Proposed) {
		t.Fatal("selected complete record", e)
	}
	if in.Partner != nil {
		got, e = s.GetShip(in.Partner.Expected.Number)
		if e != nil || !reflect.DeepEqual(got, in.Partner.Proposed) {
			t.Fatal("partner complete record", e)
		}
	} else {
		p, e := s.GetPlanet(in.Planet.Expected.StarNum, in.Planet.Expected.PlanetNum)
		if e != nil || !reflect.DeepEqual(p, in.Planet.Proposed) {
			t.Fatal("planet complete record", e)
		}
	}
	for _, st := range in.Context.Stars {
		got, e := s.GetStar(st.Number)
		if e != nil || !reflect.DeepEqual(got, st) {
			t.Fatal("read-only AP star changed", e)
		}
	}
	r, e := s.GetRace(in.Player)
	if e != nil || !reflect.DeepEqual(r, in.Context.Race) {
		t.Fatal("caller race changed", e)
	}
	for _, sh := range in.Context.Ships {
		if sh.Number == in.Selected.Expected.Number || (in.Partner != nil && sh.Number == in.Partner.Expected.Number) {
			continue
		}
		got, e := s.GetShip(sh.Number)
		if e != nil || !reflect.DeepEqual(got, sh) {
			t.Fatal("read-only ship changed", e)
		}
	}
}
func TestCargoStoreAtomicReopenAndInputs(t *testing.T) {
	for _, ships := range []bool{false, true} {
		t.Run(fmt.Sprint(ships), func(t *testing.T) {
			s, path, in := cargoStoreFixture(t, ships)
			before, e := json.Marshal(in)
			if e != nil {
				t.Fatal(e)
			}
			commit, e := s.GetMeta(KeyCommit)
			if e != nil {
				t.Fatal(e)
			}
			free, e := s.FreeShipNums()
			if e != nil {
				t.Fatal(e)
			}
			out, e := s.CommitCargoTransfer(in)
			if out != CommandCommitted || e != nil {
				t.Fatal(out, e)
			}
			assertCargoRecords(t, s, in)
			after, e := json.Marshal(in)
			if e != nil || !bytes.Equal(before, after) {
				t.Fatal("input mutated", e)
			}
			meta, e := s.GetMeta(KeyCommit)
			if e != nil || !bytes.Equal(meta, commit) {
				t.Fatal("commit changed", e)
			}
			gotFree, e := s.FreeShipNums()
			if e != nil || !reflect.DeepEqual(gotFree, free) {
				t.Fatal("allocator changed", e)
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			s, e = Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer func() { _ = s.Close() }()
			assertCargoRecords(t, s, in)
		})
	}
}
func TestCargoStoreFailuresAndOutcomes(t *testing.T) {
	for _, ships := range []bool{false, true} {
		for _, stage := range []string{"cargo.before", "cargo.check:1", "cargo.validated", "cargo.write:1", "cargo.write:2", "cargo.before-commit", "cargo.commit-error", "cargo.after-commit"} {
			for _, panics := range []bool{false, true} {
				t.Run(fmt.Sprintf("ships=%t/%s/panic=%t", ships, stage, panics), func(t *testing.T) {
					s, _, in := cargoStoreFixture(t, ships)
					before := enrollmentSnapshot(t, s)
					reached := false
					s.SetMetaFault(func(op string) error {
						if op == stage {
							reached = true
							if panics {
								panic("private cargo fault")
							}
							return errors.New("private cargo fault")
						}
						return nil
					})
					out, e := s.CommitCargoTransfer(in)
					want := CommandNotCommitted
					if stage == "cargo.commit-error" {
						want = CommandUncertain
					}
					if stage == "cargo.after-commit" {
						want = CommandCommitted
					}
					if !reached || e == nil || out != want {
						t.Fatal("fault/outcome", reached, out, e)
					}
					if want == CommandNotCommitted {
						if before != enrollmentSnapshot(t, s) {
							t.Fatal("partial aborted cargo records")
						}
					} else {
						assertCargoRecords(t, s, in)
					}
					if want == CommandUncertain {
						if !s.CommandWriteUncertain() {
							t.Fatal("uncertainty guard absent")
						}
						again, e := s.CommitCargoTransfer(in)
						if again != CommandUncertain || e == nil {
							t.Fatal("uncertain retry allowed")
						}
						h, e := s.CommitHabitatActivation(HabitatActivation{})
						if h != CommandUncertain || e == nil {
							t.Fatal("uncertainty guard not shared")
						}
					}
				})
			}
		}
	}
	t.Run("failed readback", func(t *testing.T) {
		s, _, in := cargoStoreFixture(t, false)
		s.SetMetaFault(func(op string) error {
			if op == "cargo.commit-error" || op == "command.readback" {
				return errors.New("private failure")
			}
			return nil
		})
		o, e := s.CommitCargoTransfer(in)
		var ce *CommandError
		if o != CommandUncertain || !errors.As(e, &ce) || ce.Readback != "unavailable" {
			t.Fatal(o, e)
		}
		assertCargoRecords(t, s, in)
	})
}
func TestCargoStoreClosedContract(t *testing.T) {
	cases := map[string]func(*CargoTransfer){
		"nil source": func(i *CargoTransfer) { i.Selected.Expected = nil }, "nil proposal": func(i *CargoTransfer) { i.Selected.Proposed = nil }, "no recipient": func(i *CargoTransfer) { i.Planet = nil }, "both recipients": func(i *CargoTransfer) { i.Partner = &i.Selected },
		"invalid player": func(i *CargoTransfer) { i.Player = 0 }, "invalid governor": func(i *CargoTransfer) { i.Governor = model.MaxGovernors + 1 }, "inactive governor": func(i *CargoTransfer) { i.Governor = 1 }, "foreign source": func(i *CargoTransfer) { i.Selected.Expected.Owner = 2 }, "dead": func(i *CargoTransfer) { i.Selected.Expected.Alive = false }, "inactive": func(i *CargoTransfer) { i.Selected.Expected.Active = false }, "undocked": func(i *CargoTransfer) { i.Selected.Expected.Docked = false },
		"wrong commodity": func(i *CargoTransfer) { i.Request.Commodity = 'c' }, "overflow amount": func(i *CargoTransfer) { i.Request.Amount = math.MaxInt64 }, "nonfinite rate": func(i *CargoTransfer) { i.Request.MassResource = math.NaN() },
		"extra source field": func(i *CargoTransfer) { i.Selected.Proposed.On = !i.Selected.Proposed.On }, "wrong cargo": func(i *CargoTransfer) { i.Selected.Proposed.Resource++ }, "wrong mass": func(i *CargoTransfer) { i.Selected.Proposed.Mass++ }, "other planet player": func(i *CargoTransfer) { i.Planet.Proposed.Info[1].Resource++ }, "sector": func(i *CargoTransfer) {
			i.Planet.Proposed.Sectors = append([]model.Sector(nil), i.Planet.Proposed.Sectors...)
			i.Planet.Proposed.Sectors[0].Owner = 2
		},
		"missing chain": func(i *CargoTransfer) { i.Context.Ships = nil }, "missing star": func(i *CargoTransfer) { i.Context.Stars = nil }, "duplicate ship": func(i *CargoTransfer) { i.Context.Ships = append(i.Context.Ships, i.Context.Ships[0]) }, "bad scope": func(i *CargoTransfer) { i.Scope.PlanetNum++ },
	}
	for n, edit := range cases {
		t.Run(n, func(t *testing.T) {
			s, _, in := cargoStoreFixture(t, false)
			before := enrollmentSnapshot(t, s)
			edit(&in)
			o, e := s.CommitCargoTransfer(in)
			assertCommandError(t, o, e, CommandInvalid, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("invalid request wrote records")
			}
		})
	}
}
func TestCargoStoreStalePreimagesFenceAndMaintenance(t *testing.T) {
	for _, which := range []string{"selected", "recipient", "star AP", "race", "witness", "after preparation", "missing"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := cargoStoreFixture(t, true)
			var before string
			change := func() {
				switch which {
				case "star AP":
					st := *in.Context.Stars[0]
					st.AP[0]++
					if e := s.PutStar(&st); e != nil {
						t.Error(e)
					}
				case "race":
					r := *in.Context.Race
					r.Morale++
					if e := s.PutRace(&r); e != nil {
						t.Error(e)
					}
				case "missing":
					if e := s.DeleteShip(in.Selected.Expected.Number); e != nil {
						t.Error(e)
					}
				default:
					id := in.Selected.Expected.Number
					if which == "recipient" || which == "after preparation" {
						id = in.Partner.Expected.Number
					}
					if which == "witness" {
						id = 8
					}
					sh, e := s.GetShip(id)
					if e != nil {
						t.Error(e)
						return
					}
					sh.Resource++
					if e := s.PutShip(sh); e != nil {
						t.Error(e)
					}
				}
				before = enrollmentSnapshot(t, s)
			}
			if which == "after preparation" {
				s.SetMetaFault(func(op string) error {
					if op == "cargo.before" {
						change()
					}
					return nil
				})
			} else {
				change()
			}
			o, e := s.CommitCargoTransfer(in)
			assertCommandError(t, o, e, CommandStale, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("stale records overwritten")
			}
		})
	}
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("fence/%t", late), func(t *testing.T) {
			s, _, in := cargoStoreFixture(t, true)
			before := enrollmentSnapshot(t, s)
			if late {
				s.SetMetaFault(func(op string) error {
					if op == "cargo.before" {
						s.SetWriteFence(true)
					}
					return nil
				})
			} else {
				s.SetWriteFence(true)
			}
			o, e := s.CommitCargoTransfer(in)
			assertCommandError(t, o, e, CommandFenced, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) || s.FencedWrites() != 1 || s.LastFencedWrite() != "CommitCargoTransfer" {
				t.Fatal("fence bypass/accounting")
			}
		})
	}
	t.Run("maintenance", func(t *testing.T) {
		s, _, in := cargoStoreFixture(t, false)
		if e := s.PutMaintenance(&MaintenanceRecord{SchemaVersion: MaintenanceSchemaVersion, Quiesced: true, SetAt: time.Now().UTC(), Reason: "test"}); e != nil {
			t.Fatal(e)
		}
		before := enrollmentSnapshot(t, s)
		o, e := s.CommitCargoTransfer(in)
		assertCommandError(t, o, e, CommandInvalid, CommandNotCommitted)
		if before != enrollmentSnapshot(t, s) {
			t.Fatal("maintenance wrote")
		}
	})
	t.Run("concurrent adapters", func(t *testing.T) {
		s, _, in := cargoStoreFixture(t, true)
		adapter := &Store{db: s.db}
		start := make(chan struct{})
		results := make(chan CommandOutcome, 2)
		var wg sync.WaitGroup
		for _, db := range []*Store{s, adapter} {
			wg.Add(1)
			go func(db *Store) {
				defer wg.Done()
				<-start
				o, e := db.CommitCargoTransfer(in)
				if e != nil {
					var ce *CommandError
					if !errors.As(e, &ce) || ce.Code != CommandStale {
						t.Error(e)
					}
				}
				results <- o
			}(db)
		}
		close(start)
		wg.Wait()
		close(results)
		commits := 0
		for o := range results {
			if o == CommandCommitted {
				commits++
			}
		}
		if commits != 1 {
			t.Fatal("double committed", commits)
		}
		assertCargoRecords(t, s, in)
	})
}

func TestCargoStoreCoherentAuthorityAndCrystalContracts(t *testing.T) {
	for _, which := range []string{"foreign selected", "wrong selected governor", "foreign resource gift", "foreign resource load", "foreign crystal load", "foreign crystal unload", "crystal load", "crystal unload", "crystal alias"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := cargoStoreFixture(t, true)
			a, b := in.Selected.Expected, in.Partner.Expected
			in.Selected.Proposed = new(model.Ship)
			*in.Selected.Proposed = *a
			in.Partner.Proposed = new(model.Ship)
			*in.Partner.Proposed = *b
			valid := false
			switch which {
			case "foreign selected":
				a.Owner = 2
				in.Selected.Proposed.Owner = 2
				// A resource gift would otherwise be legal across owners;
				// make the proposed result coherent so only caller authority rejects.
				in.Request.Unload = true
				in.Selected.Proposed.Resource = 8
				in.Selected.Proposed.Mass = 99.8
				in.Partner.Proposed.Resource = 32
			case "wrong selected governor":
				in.Governor = 1
				in.Context.Race.Governor[1].Active = true
				a.Governor = 2
				in.Selected.Proposed.Governor = 2
				in.Selected.Proposed.Resource = 12
				in.Selected.Proposed.Mass = 100.2
				in.Partner.Proposed.Resource = 28
			case "foreign resource gift":
				b.Owner = 2
				in.Partner.Proposed.Owner = 2
				in.Request.Amount = -2
				in.Selected.Proposed.Resource = 8
				in.Selected.Proposed.Mass = 99.8
				in.Partner.Proposed.Resource = 32
				valid = true
			case "foreign resource load":
				b.Owner = 2
				in.Partner.Proposed.Owner = 2
				in.Selected.Proposed.Resource = 12
				in.Selected.Proposed.Mass = 100.2
				in.Partner.Proposed.Resource = 28
			case "foreign crystal load", "foreign crystal unload":
				b.Owner = 2
				in.Partner.Proposed.Owner = 2
				in.Request.Commodity = 'x'
				if which == "foreign crystal load" {
					in.Selected.Proposed.Crystals = 12
					in.Partner.Proposed.Crystals = 28
				} else {
					in.Request.Unload = true
					in.Selected.Proposed.Crystals = 8
					in.Partner.Proposed.Crystals = 32
				}
			case "crystal load", "crystal alias":
				in.Request.Commodity = 'x'
				if which == "crystal alias" {
					in.Request.Commodity = '&'
				}
				in.Selected.Proposed.Crystals = 12
				in.Partner.Proposed.Crystals = 28
				valid = true
			case "crystal unload":
				in.Request.Commodity = 'x'
				in.Request.Unload = true
				in.Selected.Proposed.Crystals = 8
				in.Partner.Proposed.Crystals = 32
				valid = true
			}
			if e := s.PutShip(a); e != nil {
				t.Fatal(e)
			}
			if e := s.PutShip(b); e != nil {
				t.Fatal(e)
			}
			if e := s.PutRace(in.Context.Race); e != nil {
				t.Fatal(e)
			}
			before := enrollmentSnapshot(t, s)
			o, e := s.CommitCargoTransfer(in)
			if valid {
				if e != nil || o != CommandCommitted {
					t.Fatal("valid C cargo rejected", o, e)
				}
				assertCargoRecords(t, s, in)
			} else {
				assertCommandError(t, o, e, CommandInvalid, CommandNotCommitted)
				if before != enrollmentSnapshot(t, s) {
					t.Fatal("unauthorized write")
				}
			}
		})
	}
	t.Run("malformed maintenance", func(t *testing.T) {
		s, _, in := cargoStoreFixture(t, false)
		if e := s.PutMeta(KeyMaintenance, []byte("null")); e != nil {
			t.Fatal(e)
		}
		before := enrollmentSnapshot(t, s)
		o, e := s.CommitCargoTransfer(in)
		assertCommandError(t, o, e, CommandCorrupt, CommandNotCommitted)
		if before != enrollmentSnapshot(t, s) {
			t.Fatal("corrupt maintenance ignored")
		}
	})
}
