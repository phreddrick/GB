package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

func assaultStoreFixture(t *testing.T) (*Store, string, GroundAssault) {
	t.Helper()
	s, path, _ := fixtureStore(t)
	a := &model.Race{PlayerNum: 1, Name: "attacker", Fighters: 10}
	a.Governor[0].Active = true
	a.Likes[1] = 1
	d := &model.Race{PlayerNum: 2, Name: "defender", Fighters: 1}
	d.Governor[0].Active = true
	d.Likes[1] = 1
	p := &model.Planet{MaxX: 3, MaxY: 1, Popn: 1001, Sectors: []model.Sector{{Condition: 1, Type: 1, Owner: 1, Race: 1, Popn: 1000, Mobilization: 3}, {Condition: 1, Type: 1, Owner: 2, Race: 2, Popn: 1, Mobilization: 7}, {Condition: 1, Type: 1, Resource: 99, Crystals: 7}}}
	p.Info[0] = model.PlInfo{Popn: 1000, NumSectsOwned: 1, MobPoints: 3}
	p.Info[1] = model.PlInfo{Popn: 1, NumSectsOwned: 1, MobPoints: 7}
	st := &model.Star{Name: "Home", NumPlanets: 2}
	st.AP[0] = 100
	st.PlanetNames[0] = "World"
	for _, r := range []*model.Race{a, d} {
		if e := s.PutRace(r); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.PutPlanet(p); e != nil {
		t.Fatal(e)
	}
	if e := s.PutStar(st); e != nil {
		t.Fatal(e)
	}
	in := GroundAssault{Input: commandrules.AssaultInput{Player: 1, Scope: model.Directory{Level: model.LevelPlan}, Planet: p, Star: st, Attacker: a, Defender: d, Request: commandrules.AssaultRequest{Direction: '6', Quantity: 1000, MoveFactor: 1}}, At: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)}
	for {
		c, e := commandrules.PlanAssault(in.Input, in.Draws)
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
func assaultStoreSnapshot(t *testing.T, s *Store) string {
	t.Helper()
	rows := map[string]string{}
	e := s.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			rows[fmt.Sprintf("bucket:%x", name)] = fmt.Sprint(b.Sequence())
			return b.ForEach(func(k, v []byte) error { rows[fmt.Sprintf("%x/%x", name, k)] = string(v); return nil })
		})
	})
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(rows)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func assertAssaultStore(t *testing.T, s *Store, in GroundAssault) {
	t.Helper()
	p, e := s.GetPlanet(in.Input.Planet.StarNum, in.Input.Planet.PlanetNum)
	if e != nil || !reflect.DeepEqual(p, in.Proposed.Planet) {
		t.Fatal("complete planet", e)
	}
	st, e := s.GetStar(in.Input.Star.Number)
	if e != nil || !reflect.DeepEqual(st, in.Proposed.Star) {
		t.Fatal("complete star", e)
	}
	for _, want := range []*model.Race{in.Proposed.Attacker, in.Proposed.Defender} {
		got, e := s.GetRace(want.PlayerNum)
		if e != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("complete race", e)
		}
	}
	news, tele, e := AssaultMessages(in)
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
func TestAssaultStoreAtomicReopen(t *testing.T) {
	s, path, in := assaultStoreFixture(t)
	meta, _ := s.GetMeta(KeyCommit)
	free, _ := s.FreeShipNums()
	out, e := s.CommitGroundAssault(in)
	if e != nil || out != CommandCommitted {
		t.Fatal(out, e)
	}
	assertAssaultStore(t, s, in)
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
	assertAssaultStore(t, s, in)
	before := assaultStoreSnapshot(t, s)
	out, e = s.CommitGroundAssault(in)
	if e == nil || out != CommandNotCommitted || before != assaultStoreSnapshot(t, s) {
		t.Fatal("stale retry must not duplicate effects", out, e)
	}
}
func TestAssaultStoreFailuresAndOutcomes(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"assault.before", "assault.check:1", "assault.validated", "assault.write:1", "assault.write:2", "assault.write:3", "assault.write:4", "assault.news", "assault.telegram", "assault.before-commit", "assault.commit-error", "assault.after-commit"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				s, _, in := assaultStoreFixture(t)
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
				out, e := s.CommitGroundAssault(in)
				s.SetMetaFault(nil)
				if e == nil || !reached {
					t.Fatal("fault not exercised")
				}
				want := CommandNotCommitted
				switch stage {
				case "assault.commit-error":
					want = CommandUncertain
				case "assault.after-commit":
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
					assertAssaultStore(t, s, in)
				}
				if want == CommandUncertain {
					var ce *CommandError
					if !errors.As(e, &ce) || ce.Readback != "proposal_visible" || !s.CommandWriteUncertain() {
						t.Fatal("uncertainty evidence", e)
					}
					out, e = s.CommitGroundAssault(in)
					if out != CommandUncertain || e == nil {
						t.Fatal("guard bypassed")
					}
				}
			})
		}
	}
}
func TestAssaultStoreStaleAndClosedContract(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"planet", "attacker", "defender", "star", "extra-fields", "draws", "missing-news-bucket", "missing-tele-bucket", "news-overflow", "news-malformed", "tele-overflow", "tele-stale", "fence", "maintenance", "bad-maintenance", "readback"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := assaultStoreFixture(t)
			switch kind {
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
					if op == "assault.commit-error" || op == "command.readback" {
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
			out, e := s.CommitGroundAssault(in)
			if e == nil {
				t.Fatal("unsafe accepted", kind)
			}
			if kind == "readback" {
				var ce *CommandError
				if out != CommandUncertain || !errors.As(e, &ce) || ce.Readback != "unavailable" {
					t.Fatal(out, e)
				}
				assertAssaultStore(t, s, in)
			} else if out != CommandNotCommitted || before != assaultStoreSnapshot(t, s) {
				t.Fatal("rejection changed data", out, e)
			}
		})
	}
}
