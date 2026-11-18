package game

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

func TestWalkRetainsIrrelevantDestinations(t *testing.T) {
	for _, what := range []uint8{model.LevelUniv, model.LevelStar, model.LevelPlan, model.LevelShip} {
		t.Run(string(rune('0'+what)), func(t *testing.T) {
			f := newWalkFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[f.factory]
				sh.WhatDest = what
				sh.DestShipNo = 987
				sh.DestStar = 99
				sh.DestPNum = 99
				if e := f.store.PutShip(sh); e != nil {
					t.Fatal(e)
				}
			})
			before := f.snapshot(t)
			f.command(t, "walk #77 6")
			after := f.snapshot(t)
			want := factoryCopyShip(t, before.ActorShip)
			want.LandX = 1
			want.Fuel--
			want.Mass -= f.cfg.MassFuel
			if f.state.Mode() != ModeServing || !reflect.DeepEqual(after.ActorShip, want) || !reflect.DeepEqual(after.StoredShip, want) || after.Star.AP[0] != 49 {
				t.Fatal("destination fields changed walk or bypassed persistence")
			}
		})
	}
}

func TestWalkAfterRealCarrierLaunch(t *testing.T) {
	f := newWalkFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		sh := f.state.ships[f.factory]
		pl := f.state.planets[f.star][f.planet]
		carrier := factoryCopyShip(t, sh)
		carrier.Number = 70
		carrier.Type = model.StypeCargo
		carrier.NextShip = sh.NextShip
		carrier.Ships = sh.Number
		carrier.Mass = 200
		carrier.Hanger = sh.Size
		carrier.MaxHanger = 1000
		sh.NextShip = 0
		sh.WhatOrbits = model.LevelShip
		sh.WhatDest = model.LevelShip
		sh.DestShipNo = carrier.Number
		pl.Ships = carrier.Number
		f.state.ships[carrier.Number] = carrier
		for _, e := range []error{f.store.PutShip(sh), f.store.PutShip(carrier), f.store.PutPlanet(pl)} {
			if e != nil {
				t.Fatal(e)
			}
		}
	})
	text := f.command(t, "launch #77")
	before := f.snapshot(t)
	if !landed(before.ActorShip) || before.ActorShip.DestShipNo != 70 || !strings.Contains(text, "Landed on") {
		t.Fatal("real carrier launch not reached", text)
	}
	f.command(t, "walk #77 6")
	after := f.snapshot(t)
	if f.state.Mode() != ModeServing || after.StoredShip.LandX != 1 || after.StoredShip.Fuel != before.StoredShip.Fuel-1 || after.StoredShip.DestShipNo != 70 || !reflect.DeepEqual(after.ActorShip, after.StoredShip) || after.Star.AP[0] != 49 {
		t.Fatal("carrier launch walk lost ship/AP durability")
	}
}

// AFVs cannot land directly from orbit (AbilCanLand=0). Keep that real
// dispatcher refusal, rather than weakening unrelated landing rules to invent
// an otherwise unreachable positive walk fixture.
func TestWalkAFVDirectLandingRefusal(t *testing.T) {
	f := newWalkFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		sh := f.state.ships[f.factory]
		sh.Docked = false
		sh.WhatDest = model.LevelUniv
		sh.DestShipNo = 70
		sh.MaxFuel = 100
		sh.MaxCrew = 10
		sh.Resource = 0
		sh.Destruct = 0
		sh.Troops = 0
		if e := f.store.PutShip(sh); e != nil {
			t.Fatal(e)
		}
	})
	before := walkRecords(t, f)
	actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	if text := f.command(t, "land #77 0,0"); text != "This ship is not equipped to land.\n" {
		t.Fatal("AFV landing capability changed", text)
	}
	if text := f.command(t, "walk #77 6"); text != "This ship is not landed on a planet.\n" {
		t.Fatal("orbiting AFV entered surface walk", text)
	}
	if f.state.Mode() != ModeServing || !reflect.DeepEqual(before, walkRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("unsupported landing/walk mutated")
	}
}

func TestWalkDeadLinkedWitnesses(t *testing.T) {
	for _, which := range []string{"landed", "orbiting", "missing", "cycle", "wrong-orbit"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			enrollActor(t, f.actorEnrollmentFixture, actorEnrollmentRequest("account-2"))
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				pl := f.state.planets[f.star][f.planet]
				dead := factoryCopyShip(t, f.state.ships[f.factory])
				dead.Number = 78
				dead.Alive = false
				dead.Owner = 2
				dead.Guns = model.GunPrimary
				dead.Primary = 1
				dead.Retaliate = 1
				dead.Destruct = 1
				dead.LandX = 1
				dead.NextShip = pl.Ships
				if which == "orbiting" {
					dead.Docked = false
				}
				if which == "wrong-orbit" {
					dead.WhatOrbits = model.LevelStar
				}
				if which == "cycle" {
					dead.NextShip = 78
				}
				pl.Ships = 78
				if which != "missing" {
					f.state.ships[78] = dead
					if e := f.store.PutShip(dead); e != nil {
						t.Fatal(e)
					}
				}
				if e := f.store.PutPlanet(pl); e != nil {
					t.Fatal(e)
				}
			})
			before := walkRecords(t, f)
			f.command(t, "walk #77 6")
			if which == "landed" || which == "orbiting" {
				after := f.snapshot(t)
				if f.state.Mode() != ModeServing || after.StoredShip.LandX != 1 || !reflect.DeepEqual(after.ActorShip, after.StoredShip) {
					t.Fatal("dead witness rejected")
				}
				got := walkRecords(t, f)
				key := "7368697073/0000004e"
				if _, ok := before[key]; !ok {
					t.Fatal("dead witness missing from snapshot")
				}
				if got[key] != before[key] {
					t.Fatal("dead witness rewritten")
				}
			} else if f.state.Mode() != ModeHalted || !reflect.DeepEqual(before, walkRecords(t, f)) {
				t.Fatal("corrupt chain admitted")
			}
		})
	}
}

func TestWalkPendingLegacyAPAndRetry(t *testing.T) {
	for _, which := range []string{"pending", "external-star", "actor-star", "external-ship", "late-writer"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			f.command(t, "tech 1") // real legacy writer: persisted planet, actor-only star AP
			if f.snapshot(t).Star.AP[0] != 49 {
				t.Fatal("legacy AP spender did not run")
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				switch which {
				case "external-star":
					st, e := f.store.GetStar(f.star)
					if e != nil {
						t.Fatal(e)
					}
					st.AP[0] -= 2
					if e = f.store.PutStar(st); e != nil {
						t.Fatal(e)
					}
				case "actor-star":
					f.state.stars[f.star].AP[0]--
				case "external-ship":
					sh, e := f.store.GetShip(f.factory)
					if e != nil {
						t.Fatal(e)
					}
					sh.Fuel++
					if e = f.store.PutShip(sh); e != nil {
						t.Fatal(e)
					}
				}
			})
			if which == "late-writer" {
				if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
				f.store.SetMetaFault(func(op string) error {
					if op == "walk.before" {
						q, e := f.store.GetStar(f.star)
						if e != nil {
							return e
						}
						q.AP[0]--
						return f.store.PutStar(q)
					}
					return nil
				})
			}
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			before := walkRecords(t, f)
			text := f.command(t, "walk #77 6")
			if which == "late-writer" {
				if f.state.Mode() != ModeHalted || strings.Contains(text, "retry after") {
					t.Fatal("in-transaction staleness incorrectly deferred", text)
				}
				if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("stale admission installed actor changes")
				}
				sh, e := f.store.GetShip(f.factory)
				if e != nil || sh.LandX != 0 || sh.Fuel != 10 {
					t.Fatal("in-transaction CAS allowed a partial ship write", e)
				}
				return
			}
			if f.state.Mode() != ModeServing || !strings.Contains(text, "retry after") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || !reflect.DeepEqual(before, walkRecords(t, f)) {
				t.Fatal("pending legacy AP must defer without effects", text)
			}
			if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			prior := f.snapshot(t)
			f.command(t, "walk #77 6")
			after := f.snapshot(t)
			st, e := f.store.GetStar(f.star)
			if e != nil || f.state.Mode() != ModeServing || after.StoredShip.LandX != 1 || after.Star.AP[0] != prior.Star.AP[0]-1 || st.AP[0] != after.Star.AP[0] || after.StoredShip.Fuel != prior.StoredShip.Fuel-1 || !reflect.DeepEqual(after.ActorShip, after.StoredShip) {
				t.Fatal("persisted boundary retry failed", e)
			}
		})
	}
}

func TestWalkPendingLegacyCombat(t *testing.T) {
	f := newWalkFixture(t)
	enrollActor(t, f.actorEnrollmentFixture, actorEnrollmentRequest("account-2"))
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		pl := f.state.planets[f.star][f.planet]
		enemy := factoryCopyShip(t, f.state.ships[f.factory])
		enemy.Number = 78
		enemy.Owner = 2
		enemy.LandX = 1
		enemy.Guns = model.GunPrimary
		enemy.Primary = 1
		enemy.Destruct = 1
		enemy.Retaliate = 1
		enemy.NextShip = pl.Ships
		pl.Ships = 78
		f.state.ships[78] = enemy
		for _, e := range []error{f.store.PutShip(enemy), f.store.PutPlanet(pl)} {
			if e != nil {
				t.Fatal(e)
			}
		}
	})
	f.command(t, "walk #77 6") // legacy mechanical classification; unarmed attacker = no volleys
	after := f.snapshot(t)
	if f.state.Mode() != ModeServing || after.ActorShip.LandX != 1 || after.StoredShip.LandX != 0 || after.Star.AP[0] != 49 {
		t.Fatal("legacy path not exercised")
	}
	before := walkRecords(t, f)
	actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	text := f.command(t, "walk #77 4") // peaceful destination after legacy actor-only movement
	if f.state.Mode() != ModeServing || !strings.Contains(text, "retry after") || !reflect.DeepEqual(before, walkRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("legacy combat pending state halted or wrote", text)
	}
	if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
		t.Fatal(e)
	}
	// Legacy combat movement itself remains subject to CR-11 reload; this slice
	// must not flush/repair it. A peaceful retry after the boundary is durable.
	f.command(t, "walk #77 2")
	after = f.snapshot(t)
	if f.state.Mode() != ModeServing || after.StoredShip.LandY != 1 || !reflect.DeepEqual(after.ActorShip, after.StoredShip) {
		t.Fatal("post-boundary peaceful retry failed")
	}
}

func TestWalkLegacyRefusalAndRecipientDelivery(t *testing.T) {
	for _, which := range []string{"pole", "direction", "AP", "recipient-bound"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			reg := &fakeRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.SetRegistry(reg)
				if which == "AP" {
					f.state.stars[f.star].AP[0] = 0
					if e := f.store.PutStar(f.state.stars[f.star]); e != nil {
						t.Fatal(e)
					}
				}
				if which == "recipient-bound" {
					f.state.races = f.state.races[:1]
					pl := f.state.planets[f.star][f.planet]
					pl.Info[1].NumSectsOwned = 1
					f.state.stars[f.star].Governor[1] = 255
					for _, e := range []error{f.store.PutPlanet(pl), f.store.PutStar(f.state.stars[f.star])} {
						if e != nil {
							t.Fatal(e)
						}
					}
				}
			})
			cmd := "walk #77 6"
			if which == "pole" {
				cmd = "walk #77 8"
			}
			if which == "direction" {
				cmd = "walk #77 q"
			}
			text := f.command(t, cmd)
			if f.state.Mode() != ModeServing {
				t.Fatal("ordinary refusal or recipient bound halted")
			}
			switch which {
			case "pole":
				if text != "Illegal coordinates 0,-1.\n" {
					t.Fatal("pole text", text)
				}
			case "direction":
				if text != "Illegal move.\n" {
					t.Fatal("direction text", text)
				}
			case "AP":
				if text != "" || reg.all() != "gov:1/0:You don't have 1 action points there.\n" {
					t.Fatal("AP refusal delivery", text, reg.all())
				}
			case "recipient-bound":
				if reg.count() != 0 || f.snapshot(t).StoredShip.LandX != 1 {
					t.Fatal("legacy recipient bound changed")
				}
			}
		})
	}
}

func TestWalkAdmissionAfterLegacyEffectsAndProbes(t *testing.T) {
	for _, which := range []string{"real-legacy-name-known-fenced-write", "real-legacy-toggle-known-fenced-write", "actor-race-deterministic-probe", "stored-race-deterministic-probe"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			switch which {
			case "real-legacy-name-known-fenced-write", "real-legacy-toggle-known-fenced-write":
				// Deliberate, confirmed write-fence rejection of an existing ignored legacy
				// save: real dispatch, not a claim that an ordinary successful save diverges.
				f.store.SetWriteFence(true)
				t.Cleanup(func() { f.store.SetWriteFence(false) })
				cmd := "name #77 pending-name"
				if which == "real-legacy-toggle-known-fenced-write" {
					cmd = "toggle color"
				}
				f.command(t, cmd)
				f.store.SetWriteFence(false)
				if f.state.Mode() != ModeServing {
					t.Fatal("legacy setup unexpectedly halted")
				}
			case "actor-race-deterministic-probe":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.races[0].Morale++ })
			case "stored-race-deterministic-probe":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					q := *f.state.races[0]
					q.Morale++
					if e := f.store.PutRace(&q); e != nil {
						t.Fatal(e)
					}
				})

			}
			reg := &fakeRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
			before := walkRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			admitted := 0
			f.store.SetMetaFault(func(op string) error {
				if op == "walk.before" {
					admitted++
				}
				return nil
			})
			text := f.command(t, "walk #77 6")
			if f.state.Mode() != ModeServing || !strings.Contains(text, "retry after the next persisted turn") || admitted != 0 || reg.count() != 0 || !reflect.DeepEqual(before, walkRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("valid pretransaction divergence must only defer", text, admitted, reg.all())
			}
			if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			prior := f.snapshot(t)
			text = f.command(t, "walk #77 6")
			after := f.snapshot(t)
			if f.state.Mode() != ModeServing || admitted != 1 || strings.Contains(text, "deferred") || after.StoredShip.LandX != 1 || after.StoredShip.Fuel != prior.StoredShip.Fuel-1 || after.Star.AP[0] != prior.Star.AP[0]-1 || !reflect.DeepEqual(after.ActorShip, after.StoredShip) {
				t.Fatal("manual retry after agreeing persisted boundary failed", text)
			}
		})
	}
}

func TestWalkStoredStructuralFailuresStillHalt(t *testing.T) {
	for _, which := range []string{"missing-ship", "cycle", "missing-link", "unlinked-cycle", "unlinked-missing-link", "wrong-orbit", "planet-shape", "negative-fuel", "negative-mass", "missing-race", "missing-alternate-parent"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := factoryCopyShip(t, f.state.ships[f.factory])
				pl := planetEdit(f.state.planets[f.star][f.planet])
				switch which {
				case "missing-ship":
					if e := f.store.DeleteShip(sh.Number); e != nil {
						t.Fatal(e)
					}
					return
				case "cycle":
					sh.NextShip = sh.Number
				case "missing-link":
					sh.NextShip = 999
				case "unlinked-cycle":
					pl.Ships = sh.NextShip
					sh.NextShip = sh.Number
				case "unlinked-missing-link":
					pl.Ships = sh.NextShip
					sh.NextShip = 999
				case "wrong-orbit":
					sh.WhatOrbits = model.LevelStar
				case "planet-shape":
					pl.MaxX++
				case "negative-fuel":
					sh.Fuel = -1
				case "negative-mass":
					sh.Mass = -1
				case "missing-race":
					sh.Owner = 64
				case "missing-alternate-parent":
					sh.WhatOrbits = model.LevelShip
					sh.DestShipNo = 999
					pl.Ships = sh.NextShip
					sh.NextShip = 0
				}
				for _, e := range []error{f.store.PutShip(sh), f.store.PutPlanet(pl)} {
					if e != nil {
						t.Fatal(e)
					}
				}
			})
			before := walkRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			text := f.command(t, "walk #77 6")
			if f.state.Mode() != ModeHalted || strings.Contains(text, "retry after") || !reflect.DeepEqual(before, walkRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("structural failure was deferred or mutated", text)
			}
		})
	}
}

func TestWalkLateWriterStillHalts(t *testing.T) {
	for _, which := range []string{"ship", "star", "race", "planet"} {
		t.Run(which, func(t *testing.T) {
			f := newWalkFixture(t)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			var expected map[string]string
			f.store.SetMetaFault(func(op string) error {
				if op != "walk.before" {
					return nil
				}
				switch which {
				case "ship":
					q, e := f.store.GetShip(f.factory)
					if e != nil {
						return e
					}
					q.Fuel++
					if e = f.store.PutShip(q); e != nil {
						return e
					}
				case "star":
					q, e := f.store.GetStar(f.star)
					if e != nil {
						return e
					}
					q.AP[0]--
					if e = f.store.PutStar(q); e != nil {
						return e
					}
				case "race":
					q, e := f.store.GetRace(1)
					if e != nil {
						return e
					}
					q.Morale++
					if e = f.store.PutRace(q); e != nil {
						return e
					}
				case "planet":
					q, e := f.store.GetPlanet(f.star, f.planet)
					if e != nil {
						return e
					}
					q.Info[0].Resource++
					if e = f.store.PutPlanet(q); e != nil {
						return e
					}
				}
				// Sole fixture writer is paused here after its synchronous Put returns,
				// before Update admission. Same disposable-byte reader as commandFixtureImage,
				// without recursively submitting another actor request from its callback.
				image, e := os.ReadFile(f.path)
				if e != nil {
					return e
				}
				expected = commandFixtureRecords(t, string(image))
				return nil
			})
			text := f.command(t, "walk #77 6")
			if expected == nil || f.state.Mode() != ModeHalted || strings.Contains(text, "retry after") || !reflect.DeepEqual(expected, walkRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("late writer escaped transaction CAS or caused partial commit", text)
			}
		})
	}
}
