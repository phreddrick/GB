package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func populationStoreFixture(t *testing.T, ships bool) (*Store, string, PopulationTransfer) {
	t.Helper()
	s, path, base := cargoStoreFixture(t, ships)
	a := base.Selected.Expected
	a.Popn = 10
	a.Troops = 10
	a.MaxCrew = 50
	var other *model.Ship
	if ships {
		other = base.Partner.Expected
		other.Popn = 30
		other.Troops = 20
		other.MaxCrew = 100
	}
	p := base.Context.Planets[0]
	p.Popn = 30
	p.Troops = 20
	p.Info[0].Popn = 30
	p.Info[0].Troops = 20
	p.Info[0].NumSectsOwned = 1
	p.Info[0].MobPoints = 7
	for i := range p.Sectors {
		p.Sectors[i].Owner = 0
		p.Sectors[i].Popn = 0
		p.Sectors[i].Troops = 0
	}
	sec := p.SectorAt(int(a.LandX), int(a.LandY))
	sec.Owner = 1
	sec.Popn = 30
	sec.Troops = 20
	sec.Mobilization = 7
	base.Context.Race.Mass = 1.25
	for _, sh := range []*model.Ship{a, other} {
		if sh != nil {
			if e := s.PutShip(sh); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := s.PutRace(base.Context.Race); e != nil {
		t.Fatal(e)
	}
	if e := s.PutPlanet(p); e != nil {
		t.Fatal(e)
	}
	next := *a
	next.Popn = 12
	next.Mass = 102.5
	in := PopulationTransfer{Player: 1, Selected: CommandShip{a, &next}, Context: base.Context, Request: commandrules.PopulationRequest{Commodity: 'c', Amount: 2}}
	if ships {
		n := *other
		n.Popn = 28
		in.Partner = &CommandShip{other, &n}
	} else {
		n := *p
		n.Sectors = append([]model.Sector(nil), p.Sectors...)
		n.SectorAt(int(a.LandX), int(a.LandY)).Popn = 28
		n.Popn = 28
		n.Info[0].Popn = 28
		in.Planet = &CommandPlanet{p, &n}
	}
	return s, path, in
}
func assertPopulationRecords(t *testing.T, s *Store, in PopulationTransfer) {
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
func TestPopulationStoreAtomicReopenAndInputs(t *testing.T) {
	for _, ships := range []bool{false, true} {
		t.Run(fmt.Sprint(ships), func(t *testing.T) {
			s, path, in := populationStoreFixture(t, ships)
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
			out, e := s.CommitPopulationTransfer(in)
			if out != CommandCommitted || e != nil {
				t.Fatal(out, e)
			}
			assertPopulationRecords(t, s, in)
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
			assertPopulationRecords(t, s, in)
		})
	}
}
func TestPopulationStoreFailuresAndOutcomes(t *testing.T) {
	for _, ships := range []bool{false, true} {
		for _, stage := range []string{"population.before", "population.check:1", "population.validated", "population.write:1", "population.write:2", "population.before-commit", "population.commit-error", "population.after-commit"} {
			for _, panics := range []bool{false, true} {
				t.Run(fmt.Sprintf("ships=%t/%s/panic=%t", ships, stage, panics), func(t *testing.T) {
					s, _, in := populationStoreFixture(t, ships)
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
					out, e := s.CommitPopulationTransfer(in)
					want := CommandNotCommitted
					if stage == "population.commit-error" {
						want = CommandUncertain
					}
					if stage == "population.after-commit" {
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
						assertPopulationRecords(t, s, in)
					}
					if want == CommandUncertain {
						if !s.CommandWriteUncertain() {
							t.Fatal("uncertainty guard absent")
						}
						again, e := s.CommitPopulationTransfer(in)
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
		s, _, in := populationStoreFixture(t, false)
		s.SetMetaFault(func(op string) error {
			if op == "population.commit-error" || op == "command.readback" {
				return errors.New("private failure")
			}
			return nil
		})
		o, e := s.CommitPopulationTransfer(in)
		var ce *CommandError
		if o != CommandUncertain || !errors.As(e, &ce) || ce.Readback != "unavailable" {
			t.Fatal(o, e)
		}
		assertPopulationRecords(t, s, in)
	})
}
func TestPopulationStoreClosedContract(t *testing.T) {
	cases := map[string]func(*PopulationTransfer){
		"nil source": func(i *PopulationTransfer) { i.Selected.Expected = nil }, "nil proposal": func(i *PopulationTransfer) { i.Selected.Proposed = nil }, "no recipient": func(i *PopulationTransfer) { i.Planet = nil }, "both recipients": func(i *PopulationTransfer) { i.Partner = &i.Selected },
		"invalid player": func(i *PopulationTransfer) { i.Player = 0 }, "invalid governor": func(i *PopulationTransfer) { i.Governor = model.MaxGovernors + 1 }, "inactive governor": func(i *PopulationTransfer) { i.Governor = 1 }, "foreign source": func(i *PopulationTransfer) { i.Selected.Expected.Owner = 2 }, "dead": func(i *PopulationTransfer) { i.Selected.Expected.Alive = false }, "inactive": func(i *PopulationTransfer) { i.Selected.Expected.Active = false }, "undocked": func(i *PopulationTransfer) { i.Selected.Expected.Docked = false },
		"wrong commodity": func(i *PopulationTransfer) { i.Request.Commodity = 'r' }, "overflow amount": func(i *PopulationTransfer) { i.Request.Amount = math.MaxInt64 }, "nonfinite rate": func(i *PopulationTransfer) { i.Context.Race.Mass = math.NaN() },
		"extra source field": func(i *PopulationTransfer) { i.Selected.Proposed.On = !i.Selected.Proposed.On }, "wrong cargo": func(i *PopulationTransfer) { i.Selected.Proposed.Resource++ }, "wrong mass": func(i *PopulationTransfer) { i.Selected.Proposed.Mass++ }, "other planet player": func(i *PopulationTransfer) { i.Planet.Proposed.Info[1].Resource++ }, "sector unrelated race": func(i *PopulationTransfer) {
			i.Planet.Proposed.Sectors = append([]model.Sector(nil), i.Planet.Proposed.Sectors...)
			i.Planet.Proposed.Sectors[0].Race = 2
		},
		"missing chain": func(i *PopulationTransfer) { i.Context.Ships = nil }, "missing star": func(i *PopulationTransfer) { i.Context.Stars = nil }, "duplicate ship": func(i *PopulationTransfer) { i.Context.Ships = append(i.Context.Ships, i.Context.Ships[0]) }, "bad scope": func(i *PopulationTransfer) { i.Scope.PlanetNum++ },
	}
	for n, edit := range cases {
		t.Run(n, func(t *testing.T) {
			s, _, in := populationStoreFixture(t, false)
			before := enrollmentSnapshot(t, s)
			edit(&in)
			o, e := s.CommitPopulationTransfer(in)
			assertCommandError(t, o, e, CommandInvalid, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("invalid request wrote records")
			}
		})
	}
}
func TestPopulationStoreStalePreimagesFenceAndMaintenance(t *testing.T) {
	for _, which := range []string{"selected", "recipient", "star AP", "race", "witness", "after preparation", "missing"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := populationStoreFixture(t, true)
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
					if op == "population.before" {
						change()
					}
					return nil
				})
			} else {
				change()
			}
			o, e := s.CommitPopulationTransfer(in)
			assertCommandError(t, o, e, CommandStale, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("stale records overwritten")
			}
		})
	}
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("fence/%t", late), func(t *testing.T) {
			s, _, in := populationStoreFixture(t, true)
			before := enrollmentSnapshot(t, s)
			if late {
				s.SetMetaFault(func(op string) error {
					if op == "population.before" {
						s.SetWriteFence(true)
					}
					return nil
				})
			} else {
				s.SetWriteFence(true)
			}
			o, e := s.CommitPopulationTransfer(in)
			assertCommandError(t, o, e, CommandFenced, CommandNotCommitted)
			if before != enrollmentSnapshot(t, s) || s.FencedWrites() != 1 || s.LastFencedWrite() != "CommitPopulationTransfer" {
				t.Fatal("fence bypass/accounting")
			}
		})
	}
	t.Run("maintenance", func(t *testing.T) {
		s, _, in := populationStoreFixture(t, false)
		if e := s.PutMaintenance(&MaintenanceRecord{SchemaVersion: MaintenanceSchemaVersion, Quiesced: true, SetAt: time.Now().UTC(), Reason: "test"}); e != nil {
			t.Fatal(e)
		}
		before := enrollmentSnapshot(t, s)
		o, e := s.CommitPopulationTransfer(in)
		assertCommandError(t, o, e, CommandInvalid, CommandNotCommitted)
		if before != enrollmentSnapshot(t, s) {
			t.Fatal("maintenance wrote")
		}
	})
	t.Run("concurrent adapters", func(t *testing.T) {
		s, _, in := populationStoreFixture(t, true)
		adapter := &Store{db: s.db}
		start := make(chan struct{})
		results := make(chan CommandOutcome, 2)
		var wg sync.WaitGroup
		for _, db := range []*Store{s, adapter} {
			wg.Add(1)
			go func(db *Store) {
				defer wg.Done()
				<-start
				o, e := db.CommitPopulationTransfer(in)
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
		assertPopulationRecords(t, s, in)
	})
}

func TestPopulationStoreFullMatrix(t *testing.T) {
	for _, ships := range []bool{false, true} {
		for _, commodity := range []byte{'c', 'm'} {
			for _, unload := range []bool{false, true} {
				for _, amount := range []int64{-2, 0, 2} {
					t.Run(fmt.Sprintf("ships=%t/%c/unload=%t/amount=%d", ships, commodity, unload, amount), func(t *testing.T) {
						s, _, in := populationStoreFixture(t, ships)
						in.Request = commandrules.PopulationRequest{Commodity: commodity, Amount: amount, Unload: unload}
						delta := amount
						if unload {
							delta = -delta
						}
						if amount == 0 {
							if unload {
								delta = -10
							} else if commodity == 'c' {
								delta = 30
							} else {
								delta = 20
							}
						}
						a := *in.Selected.Expected
						a.Mass += float64(delta) * 1.25
						if commodity == 'c' {
							a.Popn = populationTestWord(t, 10+delta)
						} else {
							a.Troops = populationTestWord(t, 10+delta)
						}
						in.Selected.Proposed = &a
						if ships {
							b := *in.Partner.Expected
							if commodity == 'c' {
								b.Popn = populationTestWord(t, 30-delta)
							} else {
								b.Troops = populationTestWord(t, 20-delta)
							}
							in.Partner.Proposed = &b
						} else {
							p := *in.Planet.Expected
							p.Sectors = append([]model.Sector(nil), p.Sectors...)
							sec := p.SectorAt(int(a.LandX), int(a.LandY))
							if commodity == 'c' {
								sec.Popn = populationTestWord(t, 30-delta)
								p.Popn = uint64(sec.Popn)
								p.Info[0].Popn = uint32(sec.Popn)
							} else {
								sec.Troops = populationTestWord(t, 20-delta)
								p.Troops = uint64(sec.Troops)
								p.Info[0].Troops = uint32(sec.Troops)
							}
							in.Planet.Proposed = &p
						}
						before, _ := json.Marshal(in)
						o, e := s.CommitPopulationTransfer(in)
						if e != nil || o != CommandCommitted {
							t.Fatal(o, e)
						}
						assertPopulationRecords(t, s, in)
						after, _ := json.Marshal(in)
						if !bytes.Equal(before, after) {
							t.Fatal("borrowed proposal mutated")
						}
					})
				}
			}
		}
	}
}
func populationTestWord(t *testing.T, n int64) uint16 {
	t.Helper()
	if n < 0 || n > math.MaxUint16 {
		t.Fatal("invalid expected quantity")
		return 0
	}
	return uint16(n)
}
func TestPopulationStoreCoherentAuthorityAndWhitelist(t *testing.T) {
	for _, which := range []string{"source owner", "source governor", "foreign partner", "planet total", "sector owner", "sector Race", "sector troop", "ownership count", "mobilization", "other player", "race mass stale", "partner authority not needed"} {
		t.Run(which, func(t *testing.T) {
			ships := !strings.HasPrefix(which, "sector") && which != "planet total" && which != "ownership count" && which != "mobilization" && which != "other player"
			s, _, in := populationStoreFixture(t, ships)
			switch which {
			case "source owner":
				in.Selected.Expected.Owner = 2
				in.Selected.Proposed.Owner = 2
				in.Partner.Expected.Owner = 2
				in.Partner.Proposed.Owner = 2
			case "source governor":
				in.Governor = 1
				in.Context.Race.Governor[1].Active = true
				in.Selected.Expected.Governor = 2
				in.Selected.Proposed.Governor = 2
			case "foreign partner":
				in.Partner.Expected.Owner = 2
				in.Partner.Proposed.Owner = 2
			case "planet total":
				in.Planet.Proposed.Popn++
			case "sector owner":
				in.Planet.Proposed.Sectors[0].Owner = 2
			case "sector Race":
				in.Planet.Proposed.Sectors[0].Race = 2
			case "sector troop":
				in.Planet.Proposed.Sectors[0].Troops++
			case "ownership count":
				in.Planet.Proposed.Info[0].NumSectsOwned++
			case "mobilization":
				in.Planet.Proposed.Info[0].MobPoints++
			case "other player":
				in.Planet.Proposed.Info[1].Popn++
			case "race mass stale":
				r := *in.Context.Race
				r.Mass = 2
				if e := s.PutRace(&r); e != nil {
					t.Fatal(e)
				}
			case "partner authority not needed":
				in.Partner.Expected.Governor = 2
				in.Partner.Proposed.Governor = 2
			}
			if which != "race mass stale" {
				if e := s.PutRace(in.Context.Race); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.PutShip(in.Selected.Expected); e != nil {
				t.Fatal(e)
			}
			if ships {
				if e := s.PutShip(in.Partner.Expected); e != nil {
					t.Fatal(e)
				}
			}
			before := enrollmentSnapshot(t, s)
			o, e := s.CommitPopulationTransfer(in)
			if which == "partner authority not needed" {
				if e != nil || o != CommandCommitted {
					t.Fatal("invented recipient governor restriction", o, e)
				}
				assertPopulationRecords(t, s, in)
			} else if e == nil || o != CommandNotCommitted || before != enrollmentSnapshot(t, s) {
				t.Fatal("invalid permission/whitelist wrote", o, e)
			}
		})
	}
}
