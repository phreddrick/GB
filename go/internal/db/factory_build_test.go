package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

func planetFactoryFixture(t *testing.T, free bool) (*Store, string, PlanetFactoryBuild) {
	t.Helper()
	s, path, en := enrollmentFixture(t)
	p, st := en.ExpectedPlanet, en.ExpectedStar
	p.Ships = 0
	p.Info[0].Resource = 10000
	p.SectorAt(0, 0).Owner = 1
	p.SectorAt(0, 0).Popn = 20
	r := &model.Race{PlayerNum: 1, Tech: 1000}
	r.Governor[0].Active = true
	for _, e := range []error{s.PutPlanet(p), s.PutStar(st), s.PutRace(r)} {
		if e != nil {
			t.Fatal(e)
		}
	}
	if free {
		if e := s.DeleteShip(70); e != nil {
			t.Fatal(e)
		}
	}
	ticket, e := s.PlanetFactoryCandidate()
	if e != nil {
		t.Fatal(e)
	}
	in := PlanetFactoryBuild{Allocation: ticket, Input: commandrules.PlanetFactoryInput{Player: 1, Number: ticket.Number, Scope: model.Directory{Level: model.LevelPlan, StarNum: p.StarNum, PlanetNum: p.PlanetNum}, Race: r, Planet: p, Star: st, SizeCrew: .01, SizeResource: .01, MassSize: 1}}
	in.Proposed, e = commandrules.PlanPlanetFactory(in.Input)
	if e != nil {
		t.Fatal(e)
	}
	return s, path, in
}
func assertPlanetFactory(t *testing.T, s *Store, in PlanetFactoryBuild, committed bool) {
	t.Helper()
	p, e := s.GetPlanet(in.Input.Planet.StarNum, in.Input.Planet.PlanetNum)
	if e != nil {
		t.Fatal(e)
	}
	f, e := s.GetShip(in.Input.Number)
	if e != nil {
		t.Fatal(e)
	}
	wp, wf := in.Input.Planet, (*model.Ship)(nil)
	if committed {
		wp, wf = in.Proposed.Planet, in.Proposed.Ship
	}
	if !reflect.DeepEqual(p, wp) || !reflect.DeepEqual(f, wf) {
		t.Fatal("partial world commit")
	}
	st, e := s.GetStar(in.Input.Star.Number)
	if e != nil || !reflect.DeepEqual(st, in.Input.Star) {
		t.Fatal("zero AP witness changed")
	}
	r, e := s.GetRace(1)
	if e != nil || !reflect.DeepEqual(r, in.Input.Race) {
		t.Fatal("race witness changed")
	}
	free, e := s.FreeShipNums()
	if e != nil {
		t.Fatal(e)
	}
	if committed && len(free) != 0 {
		t.Fatal("allocated free ID remains free")
	}
}
func TestPlanetFactoryContractCommitReopen(t *testing.T) {
	for _, free := range []bool{false, true} {
		t.Run(fmt.Sprint(free), func(t *testing.T) {
			s, path, in := planetFactoryFixture(t, free)
			out, e := s.CommitPlanetFactory(in)
			if e != nil || out != CommandCommitted {
				t.Fatal(out, e)
			}
			assertPlanetFactory(t, s, in, true)
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			r, e := Open(path)
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				if e := r.Close(); e != nil {
					t.Error(e)
				}
			}()
			assertPlanetFactory(t, r, in, true)
		})
	}
}
func TestPlanetFactoryContractRollback(t *testing.T) {
	for _, stage := range []string{"factory-build.before", "factory-build.check:1", "factory-build.check:2", "factory-build.check:3", "factory-build.check:4", "factory-build.check:5", "factory-build.validated", "factory-build.write:1", "factory-build.write:2", "factory-build.write:3", "factory-build.before-commit"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := planetFactoryFixture(t, true)
				before := enrollmentSnapshot(t, s)
				hit := false
				s.SetMetaFault(func(op string) error {
					if op == stage {
						hit = true
						if panics {
							panic("injected")
						}
						return errors.New("injected")
					}
					return nil
				})
				out, e := s.CommitPlanetFactory(in)
				if !hit || out != CommandNotCommitted || e == nil || before != enrollmentSnapshot(t, s) {
					t.Fatal("not complete rollback", out, e)
				}
			})
		}
	}
}
func TestPlanetFactoryContractProposalRestrictions(t *testing.T) {
	for _, which := range []string{"planet", "sector", "ship", "special", "AP", "authority", "autoload", "God", "wrong-ID", "scope", "missing-witness"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := planetFactoryFixture(t, false)
			switch which {
			case "planet":
				in.Proposed.Planet.Info[1].Resource++
			case "sector":
				in.Proposed.Planet.Sectors[0].Fert++
			case "ship":
				in.Proposed.Ship.Popn++
			case "special":
				in.Proposed.Ship.Special.Transport = &model.TransportSpecial{Target: 1}
			case "AP":
				in.Input.Star.AP[0]++
			case "authority":
				in.Input.Governor = 1
			case "autoload":
				in.Input.Race.Governor[0].Toggle.AutoLoad = true
			case "God":
				in.Input.Race.God = true
			case "wrong-ID":
				in.Proposed.Ship.Number++
			case "scope":
				in.Input.Scope.Level = model.LevelShip
			case "missing-witness":
				in.Input.Planet.Ships = 99
			}
			before := enrollmentSnapshot(t, s)
			out, e := s.CommitPlanetFactory(in)
			if out != CommandNotCommitted || e == nil || before != enrollmentSnapshot(t, s) {
				t.Fatal("unsupported proposal accepted", which, out, e)
			}
		})
	}
}
func TestPlanetFactoryContractLateWriters(t *testing.T) {
	for _, which := range []string{"planet", "race", "star", "candidate", "free-list", "free-ABA"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := planetFactoryFixture(t, true)
			var afterWriter string
			s.SetMetaFault(func(op string) error {
				if op != "factory-build.before" {
					return nil
				}
				var e error
				switch which {
				case "planet":
					q := *in.Input.Planet
					q.Info[2].Resource++
					e = s.PutPlanet(&q)
				case "race":
					q := *in.Input.Race
					q.Morale++
					e = s.PutRace(&q)
				case "star":
					q := *in.Input.Star
					q.AP[2]++
					e = s.PutStar(&q)
				case "candidate":
					q := *in.Proposed.Ship
					e = s.PutShip(&q)
				case "free-list":
					e = s.DeleteShip(71)
				case "free-ABA":
					_, e = s.AllocShipNum()
					if e == nil {
						e = s.DeleteShip(in.Input.Number)
					}
				}
				afterWriter = enrollmentSnapshot(t, s)
				return e
			})
			out, e := s.CommitPlanetFactory(in)
			var ce *CommandError
			if out != CommandNotCommitted || !errors.As(e, &ce) || ce.Code != CommandStale || afterWriter != enrollmentSnapshot(t, s) {
				t.Fatal("late writer escaped CAS", out, e)
			}
		})
	}
}

func TestPlanetFactoryContractAppendMaxConflict(t *testing.T) {
	s, _, in := planetFactoryFixture(t, false)
	var afterWriter string
	s.SetMetaFault(func(op string) error {
		if op != "factory-build.before" {
			return nil
		}
		e := s.PutShip(&model.Ship{Number: 99, Owner: 1})
		afterWriter = enrollmentSnapshot(t, s)
		return e
	})
	out, e := s.CommitPlanetFactory(in)
	var ce *CommandError
	if out != CommandNotCommitted || !errors.As(e, &ce) || ce.Code != CommandStale || afterWriter != enrollmentSnapshot(t, s) {
		t.Fatal("append maximum CAS failed", out, e)
	}
}
func TestPlanetFactoryContractUncertaintyReportingFence(t *testing.T) {
	for _, stage := range []string{"factory-build.after-commit", "factory-build.commit-error", "fence", "maintenance"} {
		t.Run(stage, func(t *testing.T) {
			s, _, in := planetFactoryFixture(t, true)
			if stage == "fence" {
				s.SetWriteFence(true)
			}
			if stage == "maintenance" {
				if e := s.PutMaintenance(&MaintenanceRecord{SchemaVersion: 1, Quiesced: true, SetAt: time.Now().UTC(), Reason: "disposable factory test"}); e != nil {
					t.Fatal(e)
				}
			}
			before := enrollmentSnapshot(t, s)
			s.SetMetaFault(func(op string) error {
				if op == stage || op == "command.readback" {
					return errors.New("injected")
				}
				return nil
			})
			out, e := s.CommitPlanetFactory(in)
			if stage == "fence" || stage == "maintenance" {
				if out != CommandNotCommitted || e == nil || before != enrollmentSnapshot(t, s) {
					t.Fatal(out, e)
				}
				s.SetWriteFence(false)
				return
			}
			assertPlanetFactory(t, s, in, true)
			if stage == "factory-build.after-commit" {
				if out != CommandCommitted || e == nil {
					t.Fatal(out, e)
				}
			} else {
				if out != CommandUncertain || e == nil || !s.CommandWriteUncertain() {
					t.Fatal(out, e)
				}
				s.SetMetaFault(nil)
				if out, _ := s.CommitPlanetFactory(in); out != CommandUncertain {
					t.Fatal("guard lost")
				}
			}
		})
	}
}
func TestPlanetFactoryAllocationSafety(t *testing.T) {
	for _, which := range []string{"malformed", "duplicate", "occupied", "referenced", "overflow"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := planetFactoryFixture(t, true)
			if e := s.db.Update(func(tx *bolt.Tx) error {
				b := tx.Bucket(BucketShipFree)
				switch which {
				case "malformed":
					return b.Put(enrollmentFreeKey(1), []byte{1})
				case "duplicate":
					return appendFreeNum(b, in.Input.Number)
				case "occupied":
					raw, e := json.Marshal(in.Proposed.Ship)
					if e != nil {
						return e
					}
					return tx.Bucket(BucketShips).Put(ShipKey(in.Input.Number), raw)
				case "referenced":
					p := *in.Input.Planet
					p.Ships = in.Input.Number
					raw, e := json.Marshal(&p)
					if e != nil {
						return e
					}
					return tx.Bucket(BucketPlanets).Put(PlanetKey(p.StarNum, p.PlanetNum), raw)
				case "overflow":
					if e := b.Delete(enrollmentFreeKey(in.Allocation.FreeKey)); e != nil {
						return e
					}
					return tx.Bucket(BucketShips).Put(ShipKey(^uint32(0)), []byte("{}"))
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			before := enrollmentSnapshot(t, s)
			_, e := s.PlanetFactoryCandidate()
			if e == nil || before != enrollmentSnapshot(t, s) {
				t.Fatal("unsafe allocator candidate", which, e)
			}
		})
	}
}
func TestPlanetFactoryContractConcurrentSameTicket(t *testing.T) {
	s, _, in := planetFactoryFixture(t, true)
	var wg sync.WaitGroup
	ch := make(chan CommandOutcome, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); out, _ := s.CommitPlanetFactory(in); ch <- out }()
	}
	wg.Wait()
	close(ch)
	counts := map[CommandOutcome]int{}
	for out := range ch {
		counts[out]++
	}
	if counts[CommandCommitted] != 1 || counts[CommandNotCommitted] != 1 {
		t.Fatal(counts)
	}
	assertPlanetFactory(t, s, in, true)
}
