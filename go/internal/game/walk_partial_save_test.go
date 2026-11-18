package game

import (
	"bytes"
	"encoding/json"
	"github.com/phreddrick/GB/go/internal/db"
	bolt "go.etcd.io/bbolt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

func TestWalkAfterLegacyBuildPartialSave(t *testing.T) {
	for _, which := range []string{"planet-build", "ship-build", "actor-only-ship-probe"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				r := f.state.races[0]
				r.God = true
				pl := f.state.planets[f.star][f.planet]
				pl.Info[0].Resource = 10000
				if which == "ship-build" {
					sh := f.state.ships[f.factory]
					builder := factoryCopyShip(t, sh)
					builder.Number = 79
					builder.Type = model.StypeCargo
					builder.Docked = false
					builder.Resource = 10000
					builder.Damage = 0
					builder.NextShip = sh.NextShip
					sh.NextShip = 79
					f.state.ships[79] = builder
					f.state.dirs[0][0] = model.Directory{Level: model.LevelShip, ShipNo: 79, StarNum: f.star, PlanetNum: f.planet}
					for _, e := range []error{f.store.PutShip(sh), f.store.PutShip(builder)} {
						if e != nil {
							t.Fatal(e)
						}
					}
				}
				for _, e := range []error{f.store.PutRace(r), f.store.PutPlanet(pl)} {
					if e != nil {
						t.Fatal(e)
					}
				}
			})
			var child uint32
			if which == "actor-only-ship-probe" {
				// Deterministic valid actor-only record, not a claimed natural build result.
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					pl := f.state.planets[f.star][f.planet]
					sh := factoryCopyShip(t, f.state.ships[f.factory])
					sh.Number = 88
					sh.Type = model.StypeCargo
					sh.LandX = 0
					sh.NextShip = pl.Ships
					pl.Ships = sh.Number
					f.state.ships[sh.Number] = sh
					child = sh.Number
				})
			} else {
				cmd := "build c 0,0"
				if which == "ship-build" {
					cmd = "build c"
				}
				text := f.command(t, cmd)
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { child = f.state.planets[f.star][f.planet].Ships })
				if child == f.factory || child == 79 || child == 0 || f.state.Mode() != ModeServing {
					t.Fatal("real build did not create a planet-root child", text, child)
				}
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				pl, e := f.store.GetPlanet(f.star, f.planet)
				if e != nil {
					t.Fatal(e)
				}
				sh, e := f.store.GetShip(child)
				if e != nil {
					t.Fatal(e)
				}
				if pl.Ships == child || (which == "actor-only-ship-probe" && sh != nil) || (which != "actor-only-ship-probe" && sh == nil) {
					t.Fatal("partial-save setup not reproduced")
				}
				t.Logf("%s: actor root=%d stored root=%d child=%d persisted=%t", which, f.state.planets[f.star][f.planet].Ships, pl.Ships, child, sh != nil)
			})
			reg := &fakeRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			before := walkRecords(t, f)
			admitted := 0
			f.store.SetMetaFault(func(op string) error {
				if op == "walk.before" {
					admitted++
				}
				return nil
			})
			text := f.command(t, "walk #77 6")
			if f.state.Mode() != ModeServing || !strings.Contains(text, "retry after the next persisted turn") || admitted != 0 || reg.count() != 0 || !reflect.DeepEqual(before, walkRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("partial save must defer without HALT/writes/success", text)
			}
			if which == "actor-only-ship-probe" {
				// Existing turn start reloads ships. Complete this deliberately unsaved
				// probe through a fixture-only delayed save before the persisted boundary;
				// do not invent production recovery or claim unsaved ships survive reload.
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					if e := f.store.PutShip(f.state.ships[child]); e != nil {
						t.Fatal(e)
					}
				})
			}
			if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			prior := f.snapshot(t)
			text = f.command(t, "walk #77 6")
			after := f.snapshot(t)
			if f.state.Mode() != ModeServing || strings.Contains(text, "deferred") || admitted != 1 || after.StoredShip.LandX != 1 || after.StoredShip.Fuel != prior.StoredShip.Fuel-1 || after.Star.AP[0] != prior.Star.AP[0]-1 || !reflect.DeepEqual(after.ActorShip, after.StoredShip) {
				t.Fatal("manual retry after agreeing boundary did not commit", text)
			}
		})
	}
}

func TestWalkStoredRootPartialSaveDefers(t *testing.T) {
	for _, which := range []string{"empty-stored-head", "omitted-stored-prefix"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				pl := planetEdit(f.state.planets[f.star][f.planet])
				pl.Ships = 0
				if which == "omitted-stored-prefix" {
					pl.Ships = f.state.ships[f.factory].NextShip
				}
				if e := f.store.PutPlanet(pl); e != nil {
					t.Fatal(e)
				}
			})
			before := walkRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			text := f.command(t, "walk #77 6")
			if f.state.Mode() != ModeServing || !strings.Contains(text, "retry after") || !reflect.DeepEqual(before, walkRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("valid actor chain must explain pending stored membership", text)
			}
			if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			f.command(t, "walk #77 6")
			if f.state.Mode() != ModeServing || f.snapshot(t).StoredShip.LandX != 1 {
				t.Fatal("retry after persisted root failed")
			}
		})
	}
}

// Only the private disposable fixture is paused. Rewrite raw bytes with no
// engine/store reader or writer overlapping, then restore its sole handle.
// This is test corruption setup, never a live database inspection or API.
func walkFixtureShipBytes(t *testing.T, f *cargoFixture, data []byte) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		if e := f.store.Close(); e != nil {
			t.Fatal(e)
		}
		raw, e := bolt.Open(f.path, 0600, nil)
		if e != nil {
			t.Fatal(e)
		}
		e = raw.Update(func(tx *bolt.Tx) error {
			if data == nil {
				return tx.Bucket(db.BucketShips).Delete(db.ShipKey(f.factory))
			}
			return tx.Bucket(db.BucketShips).Put(db.ShipKey(f.factory), data)
		})
		closeErr := raw.Close()
		if e != nil || closeErr != nil {
			t.Fatal(e, closeErr)
		}
		f.store, e = db.Open(f.path)
		if e != nil {
			t.Fatal(e)
		}
		f.state.store = f.store
	})
}

func TestWalkAbsentRecordVersusReadDecodeErrors(t *testing.T) {
	for _, which := range []string{"absent-selected-probe", "malformed-json", "null-record", "wrong-identity", "closed-store", "freed-actor-only", "invalid-actor-only-link"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			switch which {
			case "absent-selected-probe":
				walkFixtureShipBytes(t, f, nil)
			case "malformed-json":
				walkFixtureShipBytes(t, f, []byte("{invalid"))
			case "null-record":
				walkFixtureShipBytes(t, f, []byte("null"))
			case "wrong-identity":
				q := f.snapshot(t).ActorShip
				q.Number = 999
				b, e := json.Marshal(q)
				if e != nil {
					t.Fatal(e)
				}
				walkFixtureShipBytes(t, f, b)
			case "closed-store":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					if e := f.store.Close(); e != nil {
						t.Fatal(e)
					}
				})
			case "freed-actor-only", "invalid-actor-only-link":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					pl := f.state.planets[f.star][f.planet]
					q := factoryCopyShip(t, f.state.ships[f.factory])
					q.Number = 88
					q.NextShip = pl.Ships
					pl.Ships = 88
					f.state.ships[88] = q
					if which == "freed-actor-only" {
						if e := f.store.DeleteShip(88); e != nil {
							t.Fatal(e)
						}
					} else {
						q.NextShip = 999
					}
				})
			}
			// Read bytes, not JSON model records, so even intentionally corrupt JSON is
			// compared exactly after a no-write admission. Sole disposable actor paused.
			var before []byte
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				var e error
				before, e = os.ReadFile(f.path)
				if e != nil {
					t.Fatal(e)
				}
			})
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			text := f.command(t, "walk #77 6")
			var after []byte
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				var e error
				after, e = os.ReadFile(f.path)
				if e != nil {
					t.Fatal(e)
				}
			})
			if !bytes.Equal(before, after) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("admission wrote despite absence/corruption")
			}
			if which == "absent-selected-probe" {
				if f.state.Mode() != ModeServing || !strings.Contains(text, "retry after") {
					t.Fatal("absence with valid actor witness should defer", text)
				}
				// Explicit fixture completion of the missing legacy save, not production
				// auto-recovery. The subsequent segment is the agreeing persisted boundary.
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					if e := f.store.PutShip(f.state.ships[f.factory]); e != nil {
						t.Fatal(e)
					}
				})
				if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
				f.command(t, "walk #77 6")
				if f.state.Mode() != ModeServing || f.snapshot(t).StoredShip.LandX != 1 {
					t.Fatal("post-boundary manual retry failed")
				}
			} else if f.state.Mode() != ModeHalted || strings.Contains(text, "retry after") {
				t.Fatal("read/decode/corruption must not masquerade as missing partial save", text)
			}
		})
	}
}

// Deterministic stored-side topology probe, not a claimed natural build path.
// The extra record is complete and its compatible tail is checked read-only;
// caching it must not randomly promote it to an actor-witness requirement.
func TestWalkExtraStoredTailClassification(t *testing.T) {
	f := newWalkFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		sh := factoryCopyShip(t, f.state.ships[f.factory])
		extra := factoryCopyShip(t, sh)
		extra.Number = 99
		extra.Type = model.StypeCargo
		extra.NextShip = 0
		pl := planetEdit(f.state.planets[f.star][f.planet])
		pl.Ships = sh.NextShip
		sh.NextShip = extra.Number
		for _, e := range []error{f.store.PutShip(extra), f.store.PutShip(sh), f.store.PutPlanet(pl)} {
			if e != nil {
				t.Fatal(e)
			}
		}
	})
	before := walkRecords(t, f)
	actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	for i := 0; i < 4; i++ {
		text := f.command(t, "walk #77 6")
		if f.state.Mode() != ModeServing || !strings.Contains(text, "retry after") || !reflect.DeepEqual(before, walkRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
			t.Fatal("valid cached stored tail changed classification or world", text)
		}
	}
}
