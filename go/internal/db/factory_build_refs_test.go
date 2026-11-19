package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

func factoryReferenceFixture(t *testing.T, free bool) (*Store, PlanetFactoryBuild, *model.Ship) {
	t.Helper()
	s, _, in := planetFactoryFixture(t, free)
	sh := &model.Ship{Number: 79, Owner: 2, Type: model.StypeCruiser, WhatOrbits: model.LevelUniv}
	if e := s.PutShip(sh); e != nil {
		t.Fatal(e)
	}
	if e := s.PutRace(&model.Race{PlayerNum: 2}); e != nil {
		t.Fatal(e)
	}
	ticket, e := s.PlanetFactoryCandidate()
	if e != nil {
		t.Fatal(e)
	}
	in.Allocation = ticket
	in.Input.Number = ticket.Number
	in.Proposed, e = commandrules.PlanPlanetFactory(in.Input)
	if e != nil {
		t.Fatal(e)
	}
	return s, in, sh
}

func TestPlanetFactoryAllocationAdvisoryVersusEnrollment(t *testing.T) {
	for _, free := range []bool{false, true} {
		for _, which := range []string{"protect-on", "protect-off", "target", "aim", "transport", "destination", "old-destination", "old-landed-destination"} {
			t.Run(fmt.Sprintf("free=%t/%s", free, which), func(t *testing.T) {
				s, in, sh := factoryReferenceFixture(t, free)
				small := uint16(80)
				if free {
					small = 70
				}
				if in.Input.Number != uint32(small) {
					t.Fatal("fixture candidate changed")
				}
				switch which {
				case "protect-on", "protect-off":
					sh.Protect.Ship = small
					sh.Protect.On = which == "protect-on"
				case "target":
					sh.Target = small
				case "aim":
					sh.Special.AimedAt = &model.AimedAtSpecial{Level: model.LevelShip, ShipNo: small}
				case "transport":
					sh.Special.Transport = &model.TransportSpecial{Target: small}
					sh.On = true
				case "destination", "old-destination", "old-landed-destination":
					sh.DestShipNo = in.Input.Number
					sh.WhatDest = model.LevelShip
					if which == "old-destination" {
						sh.WhatDest = model.LevelUniv
					}
					if which == "old-landed-destination" {
						sh.WhatOrbits = model.LevelPlan
						sh.WhatDest = model.LevelPlan
						sh.Docked = true
					}
				}
				if e := s.PutShip(sh); e != nil {
					t.Fatal(e)
				}
				before := enrollmentSnapshot(t, s)
				ticket, e := s.PlanetFactoryCandidate()
				if e != nil || ticket != in.Allocation || before != enrollmentSnapshot(t, s) {
					t.Fatal("advisory reference reserved ID", ticket, e)
				}
				// Enrollment intentionally retains its stronger cross-empire identity policy.
				if _, e := s.EnrollmentShipCandidate(); e == nil {
					t.Fatal("enrollment reference guard weakened")
				}
				out, e := s.CommitPlanetFactory(in)
				if e != nil || out != CommandCommitted {
					t.Fatal("transaction still uses enrollment admission", out, e)
				}
				after, e := s.GetShip(sh.Number)
				if e != nil || !reflect.DeepEqual(after, sh) {
					t.Fatal("advisory record rewritten")
				}
				assertPlanetFactory(t, s, in, true)
			})
		}
	}
}

func factoryStructuralReference(t *testing.T, s *Store, in PlanetFactoryBuild, sh *model.Ship, which string) {
	t.Helper()
	n := in.Input.Number
	switch which {
	case "next":
		sh.NextShip = n
	case "children":
		sh.Ships = n
	case "contained-parent":
		sh.WhatOrbits = model.LevelShip
		sh.DestShipNo = n
	case "star":
		if e := s.PutStar(&model.Star{Number: 1, Ships: n}); e != nil {
			t.Fatal(e)
		}
	case "planet":
		if e := s.PutPlanet(&model.Planet{StarNum: 0, PlanetNum: 1, Ships: n}); e != nil {
			t.Fatal(e)
		}
	case "universe":
		data, e := s.GetStarData()
		if e != nil {
			t.Fatal(e)
		}
		data.Ships = n
		if e := s.PutStarData(data); e != nil {
			t.Fatal(e)
		}
	case "government":
		if e := s.PutRace(&model.Race{PlayerNum: 2, GovShip: n}); e != nil {
			t.Fatal(e)
		}
	case "placement":
		rec := &PlacementRecord{SchemaVersion: 1, RequestID: "historical-placement", CallerID: "account-2", RequestHash: strings.Repeat("a", 64), ProposalHash: strings.Repeat("b", 64), Player: 2, Width: 1, Height: 1, GovernmentShip: n, CreatedAt: time.Unix(200, 0).UTC()}
		rec.RecordHash = rec.checksum()
		if !rec.valid() {
			t.Fatal("invalid fixture placement")
		}
		raw, e := json.Marshal(rec)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.db.Update(func(tx *bolt.Tx) error {
			return tx.Bucket(BucketMeta).Put([]byte(placementPrefix+rec.RequestID), raw)
		}); e != nil {
			t.Fatal(e)
		}
	}
	if which == "next" || which == "children" || which == "contained-parent" {
		if e := s.PutShip(sh); e != nil {
			t.Fatal(e)
		}
	}
}

func TestPlanetFactoryAllocationStructuralConflicts(t *testing.T) {
	for _, free := range []bool{false, true} {
		for _, late := range []bool{false, true} {
			for _, which := range []string{"next", "children", "contained-parent", "star", "planet", "universe", "government", "placement"} {
				t.Run(fmt.Sprintf("free=%t/late=%t/%s", free, late, which), func(t *testing.T) {
					s, in, sh := factoryReferenceFixture(t, free)
					var afterWriter string
					if late {
						s.SetMetaFault(func(op string) error {
							if op == "factory-build.before" {
								factoryStructuralReference(t, s, in, sh, which)
								afterWriter = enrollmentSnapshot(t, s)
							}
							return nil
						})
					} else {
						factoryStructuralReference(t, s, in, sh, which)
						afterWriter = enrollmentSnapshot(t, s)
						if _, e := s.PlanetFactoryCandidate(); e == nil {
							t.Fatal("structural candidate admitted")
						}
					}
					out, e := s.CommitPlanetFactory(in)
					var ce *CommandError
					if out != CommandNotCommitted || !errors.As(e, &ce) || ce.Code != CommandStale || afterWriter != enrollmentSnapshot(t, s) {
						t.Fatal("structural reference escaped transaction", out, e)
					}
				})
			}
		}
	}
}

func TestPlanetFactoryAllocationMalformedReferenceRecord(t *testing.T) {
	for _, which := range []string{"ship-json", "ship-identity", "planet-json", "race-json", "placement-json", "closed-store"} {
		t.Run(which, func(t *testing.T) {
			s, in, sh := factoryReferenceFixture(t, true)
			if which == "closed-store" {
				if e := s.Close(); e != nil {
					t.Fatal(e)
				}
			} else if e := s.db.Update(func(tx *bolt.Tx) error {
				switch which {
				case "ship-json":
					return tx.Bucket(BucketShips).Put(ShipKey(sh.Number), []byte("null"))
				case "ship-identity":
					q := *sh
					q.Number++
					raw, e := json.Marshal(q)
					if e != nil {
						return e
					}
					return tx.Bucket(BucketShips).Put(ShipKey(sh.Number), raw)
				case "planet-json":
					return tx.Bucket(BucketPlanets).Put(PlanetKey(0, 1), []byte("null"))
				case "race-json":
					return tx.Bucket(BucketRaces).Put(RaceKey(2), []byte("null"))
				case "placement-json":
					return tx.Bucket(BucketMeta).Put([]byte(placementPrefix+"bad"), []byte("null"))
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			_, e := s.PlanetFactoryCandidate()
			var ce *CommandError
			if !errors.As(e, &ce) || (ce.Code != CommandCorrupt && which != "closed-store") {
				t.Fatal("missing corrupt/IO classification", e)
			}
			out, e := s.CommitPlanetFactory(in)
			if out != CommandNotCommitted || e == nil {
				t.Fatal("corrupt reference committed")
			}
		})
	}
}

// An advisory target does not reserve an absent identity, but every field of a
// participating chain witness still belongs to its immutable CAS preimage.
func TestPlanetFactoryAllocationLateAdvisoryWitness(t *testing.T) {
	for _, free := range []bool{false, true} {
		t.Run(fmt.Sprint(free), func(t *testing.T) {
			s, in, sh := factoryReferenceFixture(t, free)
			sh.WhatOrbits = model.LevelPlan
			sh.StOrbits = in.Input.Planet.StarNum
			sh.PNumOrbits = in.Input.Planet.PlanetNum
			in.Input.Planet.Ships = sh.Number
			in.Input.Ships = []*model.Ship{sh}
			for _, e := range []error{s.PutShip(sh), s.PutPlanet(in.Input.Planet)} {
				if e != nil {
					t.Fatal(e)
				}
			}
			var e error
			in.Proposed, e = commandrules.PlanPlanetFactory(in.Input)
			if e != nil {
				t.Fatal(e)
			}
			var afterWriter string
			s.SetMetaFault(func(op string) error {
				if op == "factory-build.before" {
					q := *sh
					q.Protect.Ship = 80
					if free {
						q.Protect.Ship = 70
					}
					if e := s.PutShip(&q); e != nil {
						return e
					}
					afterWriter = enrollmentSnapshot(t, s)
				}
				return nil
			})
			out, e := s.CommitPlanetFactory(in)
			var ce *CommandError
			if out != CommandNotCommitted || !errors.As(e, &ce) || ce.Code != CommandStale || afterWriter != enrollmentSnapshot(t, s) {
				t.Fatal("advisory witness change escaped CAS", out, e)
			}
		})
	}
}
