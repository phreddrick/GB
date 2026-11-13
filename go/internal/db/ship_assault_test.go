package db

import (
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
	"math"
	"reflect"
	"testing"
)

func shipAssaultStoreFixture(t *testing.T) (*Store, string, ShipGroundAssault) {
	t.Helper()
	s, path, ground := assaultStoreFixture(t)
	sh := &model.Ship{Number: 77, Type: model.StypeCargo, Owner: 1, Governor: 0, Alive: true, Active: true, Docked: true, WhatOrbits: model.LevelPlan, WhatDest: model.LevelPlan, LandX: 1, Popn: 200, MaxCrew: 400, Mass: 700, Armor: 1}
	ground.Input.Attacker.Mass = 2
	ground.Input.Planet.Ships = 77
	for _, r := range []*model.Race{ground.Input.Attacker, ground.Input.Defender} {
		if e := s.PutRace(r); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.PutPlanet(ground.Input.Planet); e != nil {
		t.Fatal(e)
	}
	if e := s.PutShip(sh); e != nil {
		t.Fatal(e)
	}
	in := ShipGroundAssault{Input: commandrules.ShipAssaultInput{Player: 1, Scope: ground.Input.Scope, Ship: sh, Planet: ground.Input.Planet, Star: ground.Input.Star, Attacker: ground.Input.Attacker, Defender: ground.Input.Defender, Ships: []*model.Ship{sh}, Request: commandrules.PopulationRequest{Commodity: 'c', Unload: true, Amount: 100}}, At: ground.At}
	for {
		c, e := commandrules.PlanShipAssault(in.Input, in.Draws)
		if e == nil {
			in.Proposed = c
			break
		}
		var need *commandrules.AssaultNeedDraw
		if !errors.As(e, &need) {
			t.Fatal(e)
		}
		v := need.Draw
		if v.Kind == "round" {
			v.Fraction = 0.999999
		} else {
			v.Integer = v.Bound
		}
		in.Draws = append(in.Draws, v)
	}
	return s, path, in
}
func assertShipAssaultStore(t *testing.T, s *Store, in ShipGroundAssault) {
	t.Helper()
	sh, se := s.GetShip(in.Input.Ship.Number)
	if se != nil || !reflect.DeepEqual(sh, in.Proposed.Ship) {
		t.Fatal("complete selected ship", se)
	}
	p, e := s.GetPlanet(in.Input.Planet.StarNum, in.Input.Planet.PlanetNum)
	if e != nil || !reflect.DeepEqual(p, in.Proposed.Planet) {
		t.Fatal("complete planet", e)
	}
	st, e := s.GetStar(in.Input.Star.Number)
	if e != nil || !reflect.DeepEqual(st, in.Input.Star) {
		t.Fatal("complete star", e)
	}
	for _, want := range []*model.Race{in.Proposed.Attacker, in.Proposed.Defender} {
		got, e := s.GetRace(want.PlayerNum)
		if e != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("complete race", e)
		}
	}
	news, tele, e := ShipAssaultMessages(in)
	if e != nil {
		t.Fatal(e)
	}
	lines, seq, e := s.ReadNewsFrom(model.NewsCombat, 0)
	if e != nil || seq != 1 || len(lines) != 1 || lines[0] != news.Text {
		t.Fatal("required one durable news", seq, len(lines), e)
	}
	e = s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketTelegrams)
		if b.Sequence() != 1 || string(b.Get(TelegramKeyPG(2, 0, 1))) != tele.Text {
			return errors.New("required durable telegram/sequence missing")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestShipAssaultStoreAtomicReopen(t *testing.T) {
	s, path, in := shipAssaultStoreFixture(t)
	meta, _ := s.GetMeta(KeyCommit)
	free, _ := s.FreeShipNums()
	out, e := s.CommitShipGroundAssault(in)
	if e != nil || out != CommandCommitted {
		t.Fatal(out, e)
	}
	assertShipAssaultStore(t, s, in)
	after, _ := s.GetMeta(KeyCommit)
	f, _ := s.FreeShipNums()
	if !reflect.DeepEqual(meta, after) || !reflect.DeepEqual(free, f) {
		t.Fatal("commit/allocator changed")
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	}()
	assertShipAssaultStore(t, s, in)
	before := assaultStoreSnapshot(t, s)
	out, e = s.CommitShipGroundAssault(in)
	if e == nil || out != CommandNotCommitted || before != assaultStoreSnapshot(t, s) {
		t.Fatal("stale retry must not duplicate effects", out, e)
	}
}
func TestShipAssaultStoreFailuresAndOutcomes(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"ship-assault.before", "ship-assault.check:1", "ship-assault.validated", "ship-assault.write:1", "ship-assault.write:2", "ship-assault.write:3", "ship-assault.write:4", "assault.news", "assault.telegram", "ship-assault.before-commit", "ship-assault.commit-error", "ship-assault.after-commit"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := shipAssaultStoreFixture(t)
				before := assaultStoreSnapshot(t, s)
				reached := false
				s.SetMetaFault(func(op string) error {
					if op == stage {
						reached = true
						if panics {
							panic("private assault fault")
						}
						return errors.New("private assault fault")
					}
					return nil
				})
				out, e := s.CommitShipGroundAssault(in)
				s.SetMetaFault(nil)
				if e == nil || !reached {
					t.Fatal("fault not exercised")
				}
				want := CommandNotCommitted
				switch stage {
				case "ship-assault.commit-error":
					want = CommandUncertain
				case "ship-assault.after-commit":
					want = CommandCommitted
				}
				if out != want {
					t.Fatal("outcome", out, want)
				}
				if want == CommandNotCommitted {
					if before != assaultStoreSnapshot(t, s) {
						t.Fatal("partial world/effects/sequence persisted")
					}
				} else {
					assertShipAssaultStore(t, s, in)
				}
				if want == CommandUncertain {
					var ce *CommandError
					if !errors.As(e, &ce) || ce.Readback != "proposal_visible" || !s.CommandWriteUncertain() {
						t.Fatal("uncertainty evidence", e)
					}
					out, e = s.CommitShipGroundAssault(in)
					if out != CommandUncertain || e == nil {
						t.Fatal("guard bypassed")
					}
				}
			})
		}
	}
}
func TestShipAssaultStoreStaleAndClosedContract(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"ship", "ship-extra", "missing-ship-witness", "planet", "attacker", "defender", "star", "extra-fields", "draws", "missing-news-bucket", "missing-tele-bucket", "news-overflow", "news-malformed", "tele-overflow", "tele-stale", "fence", "maintenance", "bad-maintenance", "readback"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := shipAssaultStoreFixture(t)
			switch kind {
			case "ship":
				sh := *in.Input.Ship
				sh.Resource++
				if e := s.PutShip(&sh); e != nil {
					t.Fatal(e)
				}
			case "ship-extra":
				in.Proposed.Ship.Resource++
			case "missing-ship-witness":
				in.Input.Ships = nil
			case "planet":
				v := *in.Input.Planet
				v.Info[0].Resource++
				if e := s.PutPlanet(&v); e != nil {
					t.Fatal(e)
				}
			case "attacker", "defender":
				v := *in.Input.Attacker
				if kind == "defender" {
					v = *in.Input.Defender
				}
				v.Motto = "newer"
				if e := s.PutRace(&v); e != nil {
					t.Fatal(e)
				}
			case "star":
				v := *in.Input.Star
				v.AP[0]--
				if e := s.PutStar(&v); e != nil {
					t.Fatal(e)
				}
			case "extra-fields":
				in.Proposed.Planet.Info[0].Resource++
			case "draws":
				in.Draws = append(in.Draws, commandrules.AssaultDraw{Kind: "integer", Bound: 1})
			case "fence":
				s.SetWriteFence(true)
			case "maintenance":
				if e := s.PutMaintenance(&MaintenanceRecord{SchemaVersion: 1, Quiesced: true, Reason: "test", SetAt: in.At}); e != nil {
					t.Fatal(e)
				}
			case "readback":
				s.SetMetaFault(func(op string) error {
					if op == "ship-assault.commit-error" || op == "command.readback" {
						return errors.New("unavailable")
					}
					return nil
				})
			default:
				e := s.db.Update(func(tx *bolt.Tx) error {
					switch kind {
					case "missing-news-bucket":
						return tx.DeleteBucket(BucketNews)
					case "missing-tele-bucket":
						return tx.DeleteBucket(BucketTelegrams)
					case "news-malformed":
						b := tx.Bucket(BucketNews)
						if e := b.Put(NewsKey(model.NewsCombat, 1), []byte("prior")); e != nil {
							return e
						}
						return b.Put(append(NewsKey(model.NewsCombat, 2), 0), []byte("malformed"))
					case "news-overflow":
						return tx.Bucket(BucketNews).Put(NewsKey(model.NewsCombat, math.MaxUint64), []byte("old"))
					case "tele-overflow":
						return tx.Bucket(BucketTelegrams).SetSequence(math.MaxUint64)
					case "tele-stale":
						return tx.Bucket(BucketTelegrams).Put(TelegramKeyPG(1, 0, 1), []byte("old"))
					case "bad-maintenance":
						return tx.Bucket(BucketMeta).Put(KeyMaintenance, []byte("null"))
					}
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
			}
			before := assaultStoreSnapshot(t, s)
			out, e := s.CommitShipGroundAssault(in)
			if e == nil {
				t.Fatal("unsafe accepted", kind)
			}
			if kind == "readback" {
				var ce *CommandError
				if out != CommandUncertain || !errors.As(e, &ce) || ce.Readback != "unavailable" {
					t.Fatal(out, e)
				}
				assertShipAssaultStore(t, s, in)
			} else if out != CommandNotCommitted || before != assaultStoreSnapshot(t, s) {
				t.Fatal("rejection changed data", out, e)
			}
		})
	}
}
func TestShipAssaultAllianceAndOrbitalWitness(t *testing.T) {
	for _, scenario := range []string{"allied permits", "stale diplomacy", "stale orbit"} {
		t.Run(scenario, func(t *testing.T) {
			stale := scenario != "allied permits"
			s, _, in := shipAssaultStoreFixture(t)
			r3 := &model.Race{PlayerNum: 3, Allied: 1}
			in.Input.Attacker.Allied = 4
			afv := &model.Ship{Number: 78, Owner: 3, Alive: true, Active: true, Docked: true, WhatOrbits: model.LevelPlan, WhatDest: model.LevelShip, DestShipNo: 79, LandX: 1, Popn: 1, Type: model.OtypeAFV, Guns: model.GunPrimary, Primary: 1, Retaliate: 1, Destruct: 100, NextShip: 79}
			peer := *afv
			peer.Number = 79
			peer.Type = model.StypeCargo
			peer.DestShipNo = 78
			peer.NextShip = 77
			in.Input.Planet.Ships = 78
			in.Input.Ships = append(in.Input.Ships, afv, &peer)
			in.Input.Races = []*model.Race{r3}
			if e := s.PutRace(in.Input.Attacker); e != nil {
				t.Fatal(e)
			}
			if e := s.PutRace(r3); e != nil {
				t.Fatal(e)
			}
			if e := s.PutShip(afv); e != nil {
				t.Fatal(e)
			}
			if e := s.PutShip(&peer); e != nil {
				t.Fatal(e)
			}
			if e := s.PutPlanet(in.Input.Planet); e != nil {
				t.Fatal(e)
			}
			var e error
			in.Proposed, e = commandrules.PlanShipAssault(in.Input, in.Draws)
			if e != nil {
				t.Fatal("allied mechanical witness rejected", e)
			}
			reached := false
			if stale {
				s.SetMetaFault(func(op string) error {
					if op == "ship-assault.before" {
						reached = true
						if scenario == "stale orbit" {
							v := peer
							v.NextShip = 0
							return s.PutShip(&v)
						}
						v := *r3
						v.Allied = 0
						return s.PutRace(&v)
					}
					return nil
				})
			}
			out, e := s.CommitShipGroundAssault(in)
			if stale {
				var ce *CommandError
				if !reached || out != CommandNotCommitted || !errors.As(e, &ce) || ce.Code != CommandStale {
					t.Fatal("changed allied defense not refused", out, e)
				}
				ship, se := s.GetShip(77)
				planet, pe := s.GetPlanet(0, 0)
				if se != nil || pe != nil || !reflect.DeepEqual(ship, in.Input.Ship) || !reflect.DeepEqual(planet, in.Input.Planet) {
					t.Fatal("stale witness partial write")
				}
				has, _ := s.HasTelegrams(2, 0)
				news, _, _ := s.ReadNewsFrom(model.NewsCombat, 0)
				if has || len(news) != 0 {
					t.Fatal("stale witness emitted report")
				}
			} else {
				if e != nil || out != CommandCommitted {
					t.Fatal(out, e)
				}
				assertShipAssaultStore(t, s, in)
				stored, e := s.GetShip(78)
				if e != nil || !reflect.DeepEqual(stored, afv) {
					t.Fatal("readonly peer altered", e)
				}
			}
		})
	}
}
