package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

func enrollmentFixture(t *testing.T) (*Store, string, Enrollment) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "enrollment.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	star := &model.Star{Name: "Sol", NumPlanets: 2}
	planet := &model.Planet{MaxX: 2, MaxY: 1, Sectors: []model.Sector{{Condition: model.SectorLand, Resource: 17}, {Condition: model.SectorLand}}, TotalResources: 41}
	if e = s.PutUniverse(&model.StarData{NumStars: 1}, []*model.Star{star}, []*model.Planet{planet, {PlanetNum: 1}}, &UniverseRecord{Stars: 1, Planets: 2, InitializedAt: time.Unix(100, 0).UTC()}); e != nil {
		t.Fatal(e)
	}
	if e = s.PutSchedule(&ScheduleRecord{NextUpdate: time.Unix(500, 0), NextSegment: time.Unix(300, 0), UpdateMinutes: 60, MovesPerUpdate: 3, SavedAt: time.Unix(100, 0)}); e != nil {
		t.Fatal(e)
	}
	ticket, e := s.EnrollmentShipCandidate()
	if e != nil {
		t.Fatal(e)
	}
	race := &model.Race{PlayerNum: 1, Name: "Fixture species", GovShip: ticket.Number, NumberSexes: 2, Password: "opaque-r"}
	race.Governor[0] = model.Governor{Password: "opaque-g", Active: true}
	newP := *planet
	newP.Sectors = append([]model.Sector(nil), planet.Sectors...)
	newP.Sectors[1].Owner = 1
	newP.Sectors[1].Race = 1
	newP.Sectors[1].Popn = 2
	newP.Sectors[1].Fert = 100
	newP.Sectors[1].Eff = 10
	newP.Popn = 2
	newP.MaxPopn = 1100
	newP.Info[0].NumSectsOwned = 1
	newP.Info[0].Explored = true
	newP.Ships = ticket.Number
	newS := *star
	newS.SetExplored(1)
	newS.SetInhabited(1)
	newS.AP[0] = 5
	sh := &model.Ship{Number: ticket.Number, Owner: 1, Race: 1, Alive: true, Active: true, WhatOrbits: model.LevelPlan, LandX: 1}
	return s, path, Enrollment{RequestID: "request-1", CallerID: "account-1", RequestHash: strings.Repeat("a", 64), CreatedAt: time.Unix(200, 0).UTC(), Race: race, Planet: &newP, Star: &newS, Ship: sh, ExpectedPlanet: planet, ExpectedStar: star, Ticket: ticket, CapitalX: 1}
}
func enrollmentSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	var out bytes.Buffer
	e := s.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			fmt.Fprintf(&out, "%x:%d\n", name, b.Sequence())
			return b.ForEach(func(k, v []byte) error { fmt.Fprintf(&out, "%x:%x\n", k, v); return nil })
		})
	})
	if e != nil {
		t.Fatal(e)
	}
	return out.String()
}
func assertEnrollmentError(t *testing.T, e error, code EnrollmentCode, outcome EnrollmentOutcome) {
	t.Helper()
	var ee *EnrollmentError
	if !errors.As(e, &ee) || ee.Code != code || ee.Outcome != outcome {
		t.Fatalf("error %v, want %s/%s", e, code, outcome)
	}
}
func assertEnrollmentWorld(t *testing.T, s *Store, in Enrollment) {
	t.Helper()
	r, e := s.GetRace(in.Race.PlayerNum)
	if e != nil || !reflect.DeepEqual(r, in.Race) {
		t.Fatal("race differs", e)
	}
	p, e := s.GetPlanet(in.Planet.StarNum, in.Planet.PlanetNum)
	if e != nil || !reflect.DeepEqual(p, in.Planet) {
		t.Fatal("planet differs", e)
	}
	st, e := s.GetStar(in.Star.Number)
	if e != nil || !reflect.DeepEqual(st, in.Star) {
		t.Fatal("star differs", e)
	}
	sh, e := s.GetShip(in.Ship.Number)
	if e != nil || !reflect.DeepEqual(sh, in.Ship) {
		t.Fatal("ship differs", e)
	}
}
func TestEnrollmentSuccessReopenAndReplay(t *testing.T) {
	s, path, in := enrollmentFixture(t)
	beforeMeta := map[string][]byte{}
	for _, k := range [][]byte{KeyCommit, KeySchedule, KeyUniverse, KeyMaintenance} {
		beforeMeta[string(k)], _ = s.GetMeta(k)
	}
	input, _ := json.Marshal(in)
	rec, e := s.PutEnrollment(in)
	if e != nil || rec == nil || !rec.valid() {
		t.Fatal(rec, e)
	}
	afterInput, _ := json.Marshal(in)
	if !bytes.Equal(input, afterInput) {
		t.Fatal("mutated input")
	}
	assertEnrollmentWorld(t, s, in)
	for k, v := range beforeMeta {
		after, _ := s.GetMeta([]byte(k))
		if !bytes.Equal(after, v) {
			t.Fatal("changed old metadata", k)
		}
	}
	before := enrollmentSnapshot(t, s)
	writes := s.db.Stats().TxStats.Write
	replay, e := s.PutEnrollment(in)
	if e != nil || !reflect.DeepEqual(rec, replay) || enrollmentSnapshot(t, s) != before || s.db.Stats().TxStats.Write != writes {
		t.Fatal("retry wrote or changed result", e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close() }()
	assertEnrollmentWorld(t, s, in)
	lookup, e := s.GetPlacement(in.CallerID, in.RequestID)
	if e != nil || !reflect.DeepEqual(lookup, rec) {
		t.Fatal("reopen lookup", e)
	}
	_, e = s.PutEnrollment(in)
	if e != nil {
		t.Fatal("reopen retry", e)
	}
	if _, e = s.GetCommitRecord(); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetSchedule(); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetMaintenance(); e != nil {
		t.Fatal(e)
	}
}
func TestEnrollmentIdempotencyConflicts(t *testing.T) {
	for _, kind := range []string{"payload", "proposal", "caller", "second-account-placement", "species-name-bypass"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := enrollmentFixture(t)
			if _, e := s.PutEnrollment(in); e != nil {
				t.Fatal(e)
			}
			before := enrollmentSnapshot(t, s)
			switch kind {
			case "payload":
				in.RequestHash = strings.Repeat("b", 64)
			case "proposal":
				in.Race.Motto = "changed"
			case "caller":
				in.CallerID = "account-2"
			case "second-account-placement":
				in.RequestID = "request-2"
			case "species-name-bypass":
				in.RequestID = "request-2"
				in.Race.Name = "Other species"
			}
			_, e := s.PutEnrollment(in)
			assertEnrollmentError(t, e, EnrollmentConflict, EnrollmentNotCommitted)
			if enrollmentSnapshot(t, s) != before {
				t.Fatal("conflict wrote")
			}
		})
	}
	s, _, in := enrollmentFixture(t)
	r, e := s.GetPlacement(in.CallerID, "absent")
	if r != nil || e != nil {
		t.Fatal("missing", e)
	}
	if _, e = s.PutEnrollment(in); e != nil {
		t.Fatal(e)
	}
	r, e = s.GetPlacement("other", in.RequestID)
	if r != nil {
		t.Fatal("unauthorized data")
	}
	assertEnrollmentError(t, e, EnrollmentConflict, EnrollmentNotCommitted)
}
func TestEnrollmentRollbackFaultStages(t *testing.T) {
	for _, stage := range []string{"before", "construct", "race", "planet", "star", "ship", "free", "metadata", "before-commit"} {
		for _, panicFault := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panicFault), func(t *testing.T) {
				s, _, in := enrollmentFixture(t)
				before := enrollmentSnapshot(t, s)
				hit := false
				s.SetMetaFault(func(op string) error {
					if op == "enrollment."+stage {
						hit = true
						if panicFault {
							panic("secret must not escape")
						}
						return errors.New("secret must not escape")
					}
					return nil
				})
				r, e := s.PutEnrollment(in)
				assertEnrollmentError(t, e, EnrollmentIO, EnrollmentNotCommitted)
				if !hit {
					t.Fatal("fault not reached")
				}
				if r != nil {
					t.Fatal("aborted result must be nil")
				}
				if strings.Contains(e.Error(), "secret") {
					t.Fatal("leaked hook")
				}
				if before != enrollmentSnapshot(t, s) {
					t.Fatal("partial transaction")
				}
				s.SetMetaFault(nil)
				if _, e = s.PutEnrollment(in); e != nil {
					t.Fatal("retry after rollback", e)
				}
			})
		}
	}
}
func TestEnrollmentCommitOutcomes(t *testing.T) {
	for _, stage := range []string{"after-commit", "commit-error"} {
		for _, panicFault := range []bool{false, true} {
			for _, readFail := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/panic=%t/readFail=%t", stage, panicFault, readFail), func(t *testing.T) {
					s, _, in := enrollmentFixture(t)
					hit := false
					readHit := false
					s.SetMetaFault(func(op string) error {
						if op == "enrollment."+stage {
							hit = true
							if panicFault {
								panic("postcommit")
							}
							return errors.New("postcommit")
						}
						if readFail && strings.HasPrefix(op, "meta.get:"+placementPrefix) {
							readHit = true
							return errors.New("read failed")
						}
						return nil
					})
					r, e := s.PutEnrollment(in)
					outcome := EnrollmentCommitted
					if stage == "commit-error" {
						outcome = EnrollmentUncertain
					}
					assertEnrollmentError(t, e, EnrollmentIO, outcome)
					if !hit {
						t.Fatal("fault not exercised")
					}
					if stage == "commit-error" && readFail && (!readHit || r != nil) {
						t.Fatal("failed readback not exercised")
					}
					assertEnrollmentWorld(t, s, in)
					before := enrollmentSnapshot(t, s)
					s.SetMetaFault(nil)
					_, e = s.PutEnrollment(in)
					if stage == "commit-error" {
						assertEnrollmentError(t, e, EnrollmentIO, EnrollmentUncertain)
					} else if e != nil {
						t.Fatal(e)
					}
					if before != enrollmentSnapshot(t, s) {
						t.Fatal("outcome retry wrote")
					}
				})
			}
		}
	}
}
func TestEnrollmentFence(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	before := enrollmentSnapshot(t, s)
	count := s.FencedWrites()
	s.SetWriteFence(true)
	_, e := s.PutEnrollment(in)
	assertEnrollmentError(t, e, EnrollmentFenced, EnrollmentNotCommitted)
	if !errors.Is(e, ErrWriteFenced) || s.FencedWrites() != count+1 || s.LastFencedWrite() != "PutEnrollment" || before != enrollmentSnapshot(t, s) {
		t.Fatal("fence violation")
	}
}
func TestEnrollmentStalePreimages(t *testing.T) {
	for _, which := range []string{"star", "planet", "capital", "head", "free-tail", "free-sequence", "race-slot"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := enrollmentFixture(t)
			switch which {
			case "star":
				old := *in.ExpectedStar
				old.Stability++
				if e := s.PutStar(&old); e != nil {
					t.Fatal(e)
				}
			case "planet", "capital", "head":
				old := *in.ExpectedPlanet
				old.Sectors = append([]model.Sector(nil), old.Sectors...)
				if which == "planet" {
					old.TotalResources++
				}
				if which == "capital" {
					old.Sectors[1].Owner = 2
				}
				if which == "head" {
					old.Ships = 19
				}
				if e := s.PutPlanet(&old); e != nil {
					t.Fatal(e)
				}
			case "free-tail":
				if e := s.db.Update(func(tx *bolt.Tx) error { return appendFreeNum(tx.Bucket(BucketShipFree), 19) }); e != nil {
					t.Fatal(e)
				}
			case "free-sequence":
				if e := s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(BucketShipFree).SetSequence(17) }); e != nil {
					t.Fatal(e)
				}
			case "race-slot":
				if e := s.PutRace(&model.Race{PlayerNum: 1, Name: "other"}); e != nil {
					t.Fatal(e)
				}
			}
			before := enrollmentSnapshot(t, s)
			_, e := s.PutEnrollment(in)
			code := EnrollmentStale
			if which == "race-slot" {
				code = EnrollmentConflict
			}
			assertEnrollmentError(t, e, code, EnrollmentNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("stale transaction wrote")
			}
		})
	}
}
func TestEnrollmentConcurrentSlotsAndShips(t *testing.T) {
	for _, which := range []string{"same-slot"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := enrollmentFixture(t)
			other := in
			other.RequestID = "request-2"
			other.CallerID = "account-2"
			r := *in.Race
			r.Name = "Other"
			other.Race = &r
			// Both contend for the same candidate and player: neither field may be
			// consumed independently. Separate stale free-ticket coverage checks the ID.
			var wg sync.WaitGroup
			errs := make([]error, 2)
			wg.Add(2)
			go func() { defer wg.Done(); _, errs[0] = s.PutEnrollment(in) }()
			go func() { defer wg.Done(); _, errs[1] = s.PutEnrollment(other) }()
			wg.Wait()
			if (errs[0] == nil) == (errs[1] == nil) {
				t.Fatal("expected one commit", errs)
			}
			var all map[string]*PlacementRecord
			if e := s.db.View(func(tx *bolt.Tx) error { var e error; all, e = placementsTx(tx); return e }); e != nil || len(all) != 1 {
				t.Fatal("not exactly one", e)
			}
		})
	}
}
func enrollmentSetShip(t *testing.T, s *Store, in *Enrollment, n uint32) {
	t.Helper()
	in.Ship.Number = n
	in.Race.GovShip = n
	in.Planet.Ships = n
	ticket, e := s.EnrollmentShipCandidate()
	if e != nil {
		t.Fatal(e)
	}
	in.Ticket = ticket
	if ticket.Number != n {
		t.Fatalf("ticket %d want %d", ticket.Number, n)
	}
}
func TestEnrollmentFreeListAndDeadRecords(t *testing.T) {
	t.Run("reuse absent free tail atomically", func(t *testing.T) {
		s, _, in := enrollmentFixture(t)
		if e := s.db.Update(func(tx *bolt.Tx) error { return appendFreeNum(tx.Bucket(BucketShipFree), 19) }); e != nil {
			t.Fatal(e)
		}
		enrollmentSetShip(t, s, &in, 19)
		before := enrollmentSnapshot(t, s)
		hit := false
		s.SetMetaFault(func(op string) error {
			if op == "enrollment.free" {
				hit = true
				return errors.New("abort free pop")
			}
			return nil
		})
		_, e := s.PutEnrollment(in)
		assertEnrollmentError(t, e, EnrollmentIO, EnrollmentNotCommitted)
		if !hit || before != enrollmentSnapshot(t, s) {
			t.Fatal("free pop did not roll back")
		}
		s.SetMetaFault(nil)
		if _, e = s.PutEnrollment(in); e != nil {
			t.Fatal(e)
		}
		free, e := s.FreeShipNums()
		if e != nil || len(free) != 0 {
			t.Fatal(free, e)
		}
	})
	for _, live := range []bool{false, true} {
		t.Run(fmt.Sprintf("occupied/live=%t", live), func(t *testing.T) {
			s, _, in := enrollmentFixture(t)
			if e := s.PutShip(&model.Ship{Number: 1, Alive: live}); e != nil {
				t.Fatal(e)
			}
			if e := s.db.Update(func(tx *bolt.Tx) error { return appendFreeNum(tx.Bucket(BucketShipFree), 1) }); e != nil {
				t.Fatal(e)
			}
			before := enrollmentSnapshot(t, s)
			_, e := s.EnrollmentShipCandidate()
			assertEnrollmentError(t, e, EnrollmentConflict, EnrollmentNotCommitted)
			_, e = s.PutEnrollment(in)
			assertEnrollmentError(t, e, EnrollmentConflict, EnrollmentNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("occupied overwrite")
			}
		})
	}
	for _, kind := range []string{"duplicate", "short-key", "short-value", "zero-number", "zero-key", "sequence-mismatch", "overflow"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := enrollmentFixture(t)
			e := s.db.Update(func(tx *bolt.Tx) error {
				b := tx.Bucket(BucketShipFree)
				switch kind {
				case "duplicate":
					if e := appendFreeNum(b, 19); e != nil {
						return e
					}
					return appendFreeNum(b, 19)
				case "short-key":
					return b.Put([]byte{1}, ShipKey(19))
				case "short-value":
					return b.Put(enrollmentFreeKey(1), []byte{1})
				case "zero-number":
					return appendFreeNum(b, 0)
				case "zero-key":
					return b.Put(enrollmentFreeKey(0), ShipKey(19))
				case "sequence-mismatch":
					return b.Put(enrollmentFreeKey(99), ShipKey(19))
				case "overflow":
					return tx.Bucket(BucketShips).Put(ShipKey(math.MaxUint32), []byte(fmt.Sprintf(`{"number":%d}`, uint64(math.MaxUint32))))
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			before := enrollmentSnapshot(t, s)
			_, e = s.PutEnrollment(in)
			code := EnrollmentCorrupt
			if kind == "overflow" {
				code = EnrollmentConflict
			}
			assertEnrollmentError(t, e, code, EnrollmentNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("corruption wrote")
			}
		})
	}
}
func TestEnrollmentDanglingReferences(t *testing.T) {
	for _, kind := range []string{"next", "cargo", "destination", "weapon-target", "protection", "mirror", "transport", "star", "planet", "race", "universe", "metadata"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := enrollmentFixture(t)
			if e := s.db.Update(func(tx *bolt.Tx) error { return appendFreeNum(tx.Bucket(BucketShipFree), 7) }); e != nil {
				t.Fatal(e)
			}
			sh := &model.Ship{Number: 2}
			switch kind {
			case "next":
				sh.NextShip = 7
			case "cargo":
				sh.Ships = 7
			case "destination":
				sh.DestShipNo = 7
			case "weapon-target":
				sh.Target = 7
			case "protection":
				sh.Protect.Ship = 7
			case "mirror":
				sh.Special.AimedAt = &model.AimedAtSpecial{ShipNo: 7}
			case "transport":
				sh.Special.Transport = &model.TransportSpecial{Target: 7}
			case "star":
				st := *in.ExpectedStar
				st.Ships = 7
				if e := s.PutStar(&st); e != nil {
					t.Fatal(e)
				}
			case "planet":
				p := *in.ExpectedPlanet
				p.Ships = 7
				if e := s.PutPlanet(&p); e != nil {
					t.Fatal(e)
				}
			case "race":
				if e := s.PutRace(&model.Race{PlayerNum: 2, GovShip: 7}); e != nil {
					t.Fatal(e)
				}
			case "universe":
				if e := s.PutStarData(&model.StarData{Ships: 7}); e != nil {
					t.Fatal(e)
				}
			case "metadata":
				rec := PlacementRecord{SchemaVersion: 1, RequestID: "prior", CallerID: "prior", RequestHash: in.RequestHash, ProposalHash: in.RequestHash, Player: 2, Width: 1, Height: 1, GovernmentShip: 7, CreatedAt: in.CreatedAt}
				rec.RecordHash = rec.checksum()
				raw, _ := json.Marshal(rec)
				if e := s.PutMeta([]byte(placementPrefix+"prior"), raw); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.PutShip(sh); e != nil {
				t.Fatal(e)
			}
			before := enrollmentSnapshot(t, s)
			_, e := s.EnrollmentShipCandidate()
			assertEnrollmentError(t, e, EnrollmentConflict, EnrollmentNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("candidate wrote")
			}
		})
	}
}
func TestEnrollmentMalformedMetadata(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	rec, e := s.PutEnrollment(in)
	if e != nil {
		t.Fatal(e)
	}
	base, _ := json.Marshal(rec)
	for _, kind := range []string{"null", "missing", "unknown", "case-variant", "null-field", "trailing", "duplicate", "schema", "player", "planet", "coordinate", "time", "key", "checksum", "caller", "request"} {
		t.Run(kind, func(t *testing.T) {
			raw := append([]byte(nil), base...)
			var obj map[string]any
			if e := json.Unmarshal(base, &obj); e != nil {
				t.Fatal(e)
			}
			key := placementPrefix + in.RequestID
			switch kind {
			case "null":
				raw = []byte("null")
			case "missing":
				delete(obj, "caller_id")
			case "unknown":
				obj["secret"] = "unexpected"
			case "case-variant":
				obj["PLAYER"] = 1
			case "null-field":
				obj["player"] = nil
			case "trailing":
				raw = append(raw, []byte(" {}")...)
			case "duplicate":
				raw = []byte(strings.Replace(string(base), `"player":1`, `"player":1,"player":1`, 1))
			case "schema":
				obj["schema_version"] = 2
			case "player":
				obj["player"] = 64
			case "planet":
				obj["planet"] = 10
			case "coordinate":
				obj["capital_x"] = 2
			case "time":
				obj["created_at"] = "invalid"
			case "key":
				key = placementPrefix + "different"
			case "checksum":
				obj["government_ship"] = 2
			case "caller":
				obj["caller_id"] = "bad/id"
			case "request":
				obj["request_id"] = "other"
			}
			if kind != "null" && kind != "trailing" && kind != "duplicate" {
				raw, _ = json.Marshal(obj)
			}
			if e := s.db.Update(func(tx *bolt.Tx) error {
				b := tx.Bucket(BucketMeta)
				if e := b.Delete([]byte(placementPrefix + in.RequestID)); e != nil {
					return e
				}
				return b.Put([]byte(key), raw)
			}); e != nil {
				t.Fatal(e)
			}
			r, e := s.GetPlacement(in.CallerID, in.RequestID)
			if r != nil {
				t.Fatal("corrupt returned")
			}
			assertEnrollmentError(t, e, EnrollmentCorrupt, EnrollmentNotCommitted)
			before := enrollmentSnapshot(t, s)
			_, e = s.PutEnrollment(in)
			assertEnrollmentError(t, e, EnrollmentCorrupt, EnrollmentNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("corruption wrote")
			}
			if e = s.db.Update(func(tx *bolt.Tx) error {
				b := tx.Bucket(BucketMeta)
				if e := b.Delete([]byte(key)); e != nil {
					return e
				}
				return b.Put([]byte(placementPrefix+in.RequestID), base)
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestEnrollmentInvalidInput(t *testing.T) {
	for _, kind := range []string{"request", "long-request", "caller", "hash", "time", "nil", "expected", "player-zero", "player-64", "ship-zero", "mismatch", "coordinates", "NaN", "positive-infinity", "negative-infinity", "mutation", "incomplete-chain"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := enrollmentFixture(t)
			switch kind {
			case "request":
				in.RequestID = "../../bad"
			case "long-request":
				in.RequestID = strings.Repeat("x", 65)
			case "caller":
				in.CallerID = ""
			case "hash":
				in.RequestHash = "browser"
			case "time":
				in.CreatedAt = time.Time{}
			case "nil":
				in.Race = nil
			case "expected":
				in.ExpectedPlanet = nil
			case "player-zero":
				in.Race.PlayerNum = 0
			case "player-64":
				in.Race.PlayerNum = 64
			case "ship-zero":
				in.Ship.Number = 0
			case "mismatch":
				in.Race.GovShip = 19
			case "coordinates":
				in.CapitalX = 2
			case "NaN":
				in.Race.Tech = math.NaN()
			case "positive-infinity":
				in.Race.Tech = math.Inf(1)
			case "negative-infinity":
				in.Race.Tech = math.Inf(-1)
			case "mutation":
				in.Planet.TotalResources++
			case "incomplete-chain":
				in.ExpectedChain = []*model.Ship{{Number: 19}}
			}
			before := enrollmentSnapshot(t, s)
			_, e := s.PutEnrollment(in)
			assertEnrollmentError(t, e, EnrollmentInvalid, EnrollmentNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("invalid wrote")
			}
		})
	}
}
func TestEnrollmentRaceAppendOnly(t *testing.T) {
	for _, n := range []int{1, 62, 63, 64} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s, _, in := enrollmentFixture(t)
			if e := s.PutRace(&model.Race{PlayerNum: n, Name: "existing"}); e != nil {
				t.Fatal(e)
			}
			before := enrollmentSnapshot(t, s)
			_, e := s.PutEnrollment(in)
			assertEnrollmentError(t, e, EnrollmentConflict, EnrollmentNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("append policy wrote")
			}
		})
	}
}
func TestEnrollmentClosedRead(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	r, e := s.GetPlacement(in.CallerID, in.RequestID)
	if r != nil {
		t.Fatal("closed read data")
	}
	assertEnrollmentError(t, e, EnrollmentIO, EnrollmentNotCommitted)
	_, e = s.PutEnrollment(in)
	assertEnrollmentError(t, e, EnrollmentIO, EnrollmentNotCommitted)
}
func TestEnrollmentTicketBindsFreeKey(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	if e := s.db.Update(func(tx *bolt.Tx) error { return appendFreeNum(tx.Bucket(BucketShipFree), 1) }); e != nil {
		t.Fatal(e)
	}
	ticket, e := s.EnrollmentShipCandidate()
	if e != nil {
		t.Fatal(e)
	}
	in.Ticket = ticket
	if e = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(BucketShipFree)
		if e := b.Delete(enrollmentFreeKey(ticket.FreeKey)); e != nil {
			return e
		}
		return appendFreeNum(b, 1)
	}); e != nil {
		t.Fatal(e)
	}
	before := enrollmentSnapshot(t, s)
	_, e = s.PutEnrollment(in)
	assertEnrollmentError(t, e, EnrollmentStale, EnrollmentNotCommitted)
	if before != enrollmentSnapshot(t, s) {
		t.Fatal("reinserted tail wrote")
	}
}

func TestEnrollmentUnlinkedShipAndChangedChain(t *testing.T) {
	for _, which := range []string{"unlinked", "changed-link", "changed-ship"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := enrollmentFixture(t)
			if which == "unlinked" {
				if e := s.PutShip(&model.Ship{Number: 19, Alive: true, WhatOrbits: model.LevelPlan}); e != nil {
					t.Fatal(e)
				}
			} else {
				head := &model.Ship{Number: 7, Alive: true, WhatOrbits: model.LevelPlan}
				if e := s.PutShip(head); e != nil {
					t.Fatal(e)
				}
				in.ExpectedPlanet.Ships = 7
				in.Ship.NextShip = 7
				if e := s.PutPlanet(in.ExpectedPlanet); e != nil {
					t.Fatal(e)
				}
				in.ExpectedChain = []*model.Ship{head}
				ticket, e := s.EnrollmentShipCandidate()
				if e != nil {
					t.Fatal(e)
				}
				in.Ticket = ticket
				in.Ship.Number = ticket.Number
				in.Race.GovShip = ticket.Number
				in.Planet.Ships = ticket.Number
				changed := *head
				if which == "changed-link" {
					changed.NextShip = 29
				} else {
					changed.Fuel = 17
				}
				if e = s.PutShip(&changed); e != nil {
					t.Fatal(e)
				}
			}
			before := enrollmentSnapshot(t, s)
			_, e := s.PutEnrollment(in)
			assertEnrollmentError(t, e, EnrollmentStale, EnrollmentNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("changed chain wrote")
			}
		})
	}
}
func TestEnrollmentReadbackPanicStaysUncertain(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	hit := false
	s.SetMetaFault(func(op string) error {
		if op == "enrollment.commit-error" {
			return errors.New("uncertain")
		}
		if strings.HasPrefix(op, "meta.get:"+placementPrefix) {
			hit = true
			panic("readback")
		}
		return nil
	})
	r, e := s.PutEnrollment(in)
	assertEnrollmentError(t, e, EnrollmentIO, EnrollmentUncertain)
	if !hit || r != nil {
		t.Fatal("readback panic leaked a result")
	}
	assertEnrollmentWorld(t, s, in)
}
func TestEnrollmentMetadataAllRequiredFields(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	r, e := s.PutEnrollment(in)
	if e != nil {
		t.Fatal(e)
	}
	base, _ := json.Marshal(r)
	for _, field := range placementFields.required {
		for _, null := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/null=%t", field, null), func(t *testing.T) {
				var obj map[string]json.RawMessage
				if e := json.Unmarshal(base, &obj); e != nil {
					t.Fatal(e)
				}
				if null {
					obj[field] = json.RawMessage("null")
				} else {
					delete(obj, field)
				}
				raw, _ := json.Marshal(obj)
				if e := s.PutMeta([]byte(placementPrefix+in.RequestID), raw); e != nil {
					t.Fatal(e)
				}
				_, e := s.GetPlacement(in.CallerID, in.RequestID)
				assertEnrollmentError(t, e, EnrollmentCorrupt, EnrollmentNotCommitted)
			})
		}
	}
}

func TestEnrollmentAppendAtOrdinaryLimit(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	if e := s.PutRace(&model.Race{PlayerNum: 62, Name: "preceding"}); e != nil {
		t.Fatal(e)
	}
	in.Race.PlayerNum = 63
	in.Ship.Owner = 63
	in.Ship.Race = 63
	in.Planet.Sectors[1].Owner = 63
	in.Planet.Sectors[1].Race = 63
	in.Planet.Info[0] = model.PlInfo{}
	in.Planet.Info[62] = model.PlInfo{NumSectsOwned: 1, Explored: true}
	in.Star.Explored = uint64(1) << 62
	in.Star.Inhabited = uint64(1) << 62
	in.Star.AP[0] = 0
	in.Star.AP[62] = 5
	r, e := s.PutEnrollment(in)
	if e != nil || r.Player != 63 {
		t.Fatal("append63", e)
	}
	assertEnrollmentWorld(t, s, in)
}
func TestEnrollmentUncertainCannotChangeID(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	s.SetMetaFault(func(op string) error {
		if op == "enrollment.commit-error" {
			return errors.New("ambiguous")
		}
		return nil
	})
	_, e := s.PutEnrollment(in)
	assertEnrollmentError(t, e, EnrollmentIO, EnrollmentUncertain)
	before := enrollmentSnapshot(t, s)
	s.SetMetaFault(nil)
	in.RequestID = "another-id"
	in.CallerID = "another-account"
	_, e = s.PutEnrollment(in)
	assertEnrollmentError(t, e, EnrollmentIO, EnrollmentUncertain)
	if before != enrollmentSnapshot(t, s) {
		t.Fatal("uncertain bypass")
	}
}

// A second storage adapter may race even though one Store serializes its own
// calls. First writer commits player1/ship1 on star1; the second proposal is
// otherwise valid for the now-next player2 on untouched star0 but has the old
// ship1 ticket. This forces the loser through allocator validation, independently
// of race-slot and stale-homeworld checks.
func TestEnrollmentConcurrentShipConflict(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	st := *in.ExpectedStar
	st.Number = 1
	p := *in.ExpectedPlanet
	p.StarNum = 1
	p.Sectors = append([]model.Sector(nil), p.Sectors...)
	if e := s.PutStar(&st); e != nil {
		t.Fatal(e)
	}
	if e := s.PutPlanet(&p); e != nil {
		t.Fatal(e)
	}
	first := in
	first.RequestID = "first"
	first.CallerID = "first"
	first.ExpectedStar = &st
	first.ExpectedPlanet = &p
	firstStar := *in.Star
	firstStar.Number = 1
	first.Star = &firstStar
	firstP := *in.Planet
	firstP.Sectors = append([]model.Sector(nil), in.Planet.Sectors...)
	firstP.StarNum = 1
	first.Planet = &firstP
	firstShip := *in.Ship
	firstShip.StOrbits = 1
	first.Ship = &firstShip
	in.Race.PlayerNum = 2
	in.Race.Name = "second"
	in.Ship.Owner = 2
	in.Ship.Race = 2
	in.Planet.Sectors[1].Owner = 2
	in.Planet.Sectors[1].Race = 2
	in.Planet.Info[0] = model.PlInfo{}
	in.Planet.Info[1] = model.PlInfo{NumSectsOwned: 1, Explored: true}
	in.Star.Explored = 2
	in.Star.Inhabited = 2
	in.Star.AP[0] = 0
	in.Star.AP[1] = 5
	// Deep-clone the first proposal's shared race before the second mutates it.
	r := *in.Race
	r.PlayerNum = 1
	r.Name = "first"
	first.Race = &r
	adapter := &Store{db: s.db}
	ready := make(chan struct{})
	proceed := make(chan struct{})
	adapter.SetMetaFault(func(op string) error {
		if op == "enrollment.before" {
			close(ready)
			<-proceed
		}
		return nil
	})
	done := make(chan error, 1)
	go func() { _, e := adapter.PutEnrollment(in); done <- e }()
	<-ready
	if _, e := s.PutEnrollment(first); e != nil {
		close(proceed)
		t.Fatal(e)
	}
	before := enrollmentSnapshot(t, s)
	close(proceed)
	e := <-done
	assertEnrollmentError(t, e, EnrollmentStale, EnrollmentNotCommitted)
	if before != enrollmentSnapshot(t, s) {
		t.Fatal("ship conflict changed database")
	}
}

func TestEnrollmentDuplicateMetadataIdentities(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	r, e := s.PutEnrollment(in)
	if e != nil {
		t.Fatal(e)
	}
	for _, which := range []string{"account", "player", "ship", "home"} {
		t.Run(which, func(t *testing.T) {
			other := *r
			other.RequestID = "other"
			other.CallerID = "other"
			other.Player = 2
			other.GovernmentShip = 2
			other.Star = 1
			switch which {
			case "account":
				other.CallerID = r.CallerID
			case "player":
				other.Player = r.Player
			case "ship":
				other.GovernmentShip = r.GovernmentShip
			case "home":
				other.Star = r.Star
			}
			other.RecordHash = other.checksum()
			raw, _ := json.Marshal(other)
			if e := s.PutMeta([]byte(placementPrefix+other.RequestID), raw); e != nil {
				t.Fatal(e)
			}
			before := enrollmentSnapshot(t, s)
			_, e := s.GetPlacement(in.CallerID, in.RequestID)
			assertEnrollmentError(t, e, EnrollmentCorrupt, EnrollmentNotCommitted)
			_, e = s.PutEnrollment(in)
			assertEnrollmentError(t, e, EnrollmentCorrupt, EnrollmentNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("corrupt identity wrote")
			}
			if e := s.db.Update(func(tx *bolt.Tx) error {
				return tx.Bucket(BucketMeta).Delete([]byte(placementPrefix + other.RequestID))
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestEnrollmentExactlyOneWriteTransaction(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	id := func() int {
		n := 0
		if e := s.db.View(func(tx *bolt.Tx) error { n = tx.ID(); return nil }); e != nil {
			t.Fatal(e)
		}
		return n
	}
	before := id()
	if _, e := s.PutEnrollment(in); e != nil {
		t.Fatal(e)
	}
	if id() != before+1 {
		t.Fatal("enrollment did not use exactly one write transaction")
	}
	before = id()
	if _, e := s.PutEnrollment(in); e != nil {
		t.Fatal(e)
	}
	if id() != before {
		t.Fatal("replay opened a write transaction")
	}
}
func TestEnrollmentConcurrentReplay(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	adapter := &Store{db: s.db}
	hit := false
	adapter.SetMetaFault(func(op string) error {
		switch op {
		case "enrollment.before":
			hit = true
			_, e := s.PutEnrollment(in)
			return e
		case "enrollment.commit-error", "enrollment.after-commit":
			t.Fatal("new-commit hook reached for replay")
		}
		return nil
	})
	r, e := adapter.PutEnrollment(in)
	if e != nil || r == nil || !hit {
		t.Fatal("transaction race replay", e)
	}
	assertEnrollmentWorld(t, s, in)
}
func TestEnrollmentFenceWhileWaitingToWrite(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	hit := false
	s.SetMetaFault(func(op string) error {
		if op == "enrollment.before" {
			hit = true
			s.SetWriteFence(true)
		}
		return nil
	})
	before := enrollmentSnapshot(t, s)
	_, e := s.PutEnrollment(in)
	assertEnrollmentError(t, e, EnrollmentFenced, EnrollmentNotCommitted)
	if !hit || s.FencedWrites() != 1 || before != enrollmentSnapshot(t, s) {
		t.Fatal("second fence check bypassed")
	}
}

func TestEnrollmentHistoricalPlayerIsNotReused(t *testing.T) {
	s, _, in := enrollmentFixture(t)
	r := PlacementRecord{SchemaVersion: 1, RequestID: "historical", CallerID: "historical", RequestHash: in.RequestHash, ProposalHash: in.RequestHash, Player: 5, Star: 1, Width: 1, Height: 1, GovernmentShip: 99, CreatedAt: in.CreatedAt}
	r.RecordHash = r.checksum()
	raw, _ := json.Marshal(r)
	if e := s.PutMeta([]byte(placementPrefix+r.RequestID), raw); e != nil {
		t.Fatal(e)
	}
	before := enrollmentSnapshot(t, s)
	_, e := s.PutEnrollment(in)
	assertEnrollmentError(t, e, EnrollmentConflict, EnrollmentNotCommitted)
	if before != enrollmentSnapshot(t, s) {
		t.Fatal("historical allocation reused")
	}
}
func TestEnrollmentOrphanPlayerOwnership(t *testing.T) {
	for _, which := range []string{"ship-owner", "ship-race", "star", "sector-owner", "sector-race", "planet-info"} {
		t.Run(which, func(t *testing.T) {
			s, _, in := enrollmentFixture(t)
			switch which {
			case "ship-owner", "ship-race":
				sh := &model.Ship{Number: 19}
				if which == "ship-owner" {
					sh.Owner = 1
				} else {
					sh.Race = 1
				}
				if e := s.PutShip(sh); e != nil {
					t.Fatal(e)
				}
			case "star":
				if e := s.PutStar(&model.Star{Number: 1, Inhabited: 1}); e != nil {
					t.Fatal(e)
				}
			default:
				p := &model.Planet{PlanetNum: 1, Sectors: []model.Sector{{}}}
				if which == "sector-owner" {
					p.Sectors[0].Owner = 1
				}
				if which == "sector-race" {
					p.Sectors[0].Race = 1
				}
				if which == "planet-info" {
					p.Info[0].NumSectsOwned = 1
				}
				if e := s.PutPlanet(p); e != nil {
					t.Fatal(e)
				}
			}
			before := enrollmentSnapshot(t, s)
			_, e := s.PutEnrollment(in)
			assertEnrollmentError(t, e, EnrollmentConflict, EnrollmentNotCommitted)
			if before != enrollmentSnapshot(t, s) {
				t.Fatal("orphan ownership changed")
			}
		})
	}
}
