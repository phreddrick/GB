package game

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

func newProbeFixture(t *testing.T, free bool) *factoryFixture {
	t.Helper()
	f := newCargoBuildFixture(t, free, model.LevelPlan)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		b := f.state.ships[77]
		r := f.state.races[0]
		previous := *b
		f.state.getshipDefaults(b, model.StypeCarrier, r)
		b.Number, b.Owner, b.Alive, b.Active = 77, 1, true, true
		b.Resource, b.Popn, b.Fuel = 500, 30, 250
		b.Tech, b.WhatOrbits = r.Tech, model.LevelPlan
		b.StOrbits, b.PNumOrbits = previous.StOrbits, previous.PNumOrbits
		b.XPos, b.YPos, b.NextShip = previous.XPos, previous.YPos, previous.NextShip
		old := &model.Ship{}
		f.state.getshipDefaults(old, model.StypeShuttle, r)
		old.Number, old.Owner, old.Popn = 76, 1, 1
		old.Alive, old.Active, old.Docked = true, true, true
		old.WhatOrbits, old.WhatDest, old.DestShipNo = model.LevelShip, model.LevelShip, b.Number
		old.StOrbits, old.PNumOrbits = b.StOrbits, b.PNumOrbits
		if f.state.ships[76] != nil {
			t.Fatal("fixture needs unused #76")
		}
		f.state.ships[76] = old
		b.Ships = old.Number
		b.Hanger = old.Size
		r.Governor[0].Toggle.AutoLoad = false
		for _, e := range []error{f.store.PutShip(b), f.store.PutShip(old), f.store.PutRace(r)} {
			if e != nil {
				t.Fatal(e)
			}
		}
	})
	return f
}

// Compute expected records using the legacy design/initialization helpers, independently of the transaction planner.
func expectedProbe(t *testing.T, f *factoryFixture, id uint32) (model.Ship, model.Ship) {
	t.Helper()
	var probe, carrier model.Ship
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		r := f.state.races[0]
		carrier = *f.state.ships[77]
		legacy := &State{cfg: f.cfg}
		legacy.getshipDefaults(&probe, model.OtypeProbe, r)
		legacy.initializeNewShip(1, 0, r, &probe, 0, 0)
		probe.Number = id
		probe.Class = "Standard"
		probe.Tech = r.Tech
		probe.NextShip = carrier.Ships
		probe.WhatOrbits = model.LevelShip
		probe.WhatDest = model.LevelShip
		probe.Docked = true
		probe.DestShipNo = carrier.Number
		probe.XPos, probe.YPos = carrier.XPos, carrier.YPos
		probe.LandX, probe.LandY = carrier.LandX, carrier.LandY
		probe.StOrbits, probe.PNumOrbits = carrier.StOrbits, carrier.PNumOrbits
		probe.DestStar, probe.DestPNum = carrier.DestStar, carrier.DestPNum
		carrier.Ships = id
		carrier.Hanger += probe.Size
		carrier.Resource -= probe.BuildCost
	})
	return probe, carrier
}
func expectProbeRecords(t *testing.T, records map[string]string, probe, carrier model.Ship, free bool) {
	t.Helper()
	for _, sh := range []model.Ship{probe, carrier} {
		records[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(sh.Number))] = factoryBuildJSON(t, &sh)
	}
	if free {
		delete(records, fmt.Sprintf("%x/%016x", db.BucketShipFree, 1))
	}
}
func TestCarrierProbeDispatch(t *testing.T) {
	t.Parallel()
	for _, free := range []bool{false, true} {
		t.Run(fmt.Sprintf("free=%v", free), func(t *testing.T) {
			f := newProbeFixture(t, free)
			id := uint32(78)
			if free {
				id = 70
			}
			probe, carrier := expectedProbe(t, f, id)
			want := factoryBuildRecords(t, f)
			expectProbeRecords(t, want, probe, carrier, free)
			var ptr *model.Ship
			reg := &factoryBuildRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				ptr = f.state.ships[77]
				reg.at = func(string) {
					b, e := f.store.GetShip(77)
					c, ce := f.store.GetShip(id)
					if e != nil || ce != nil || !reflect.DeepEqual(b, &carrier) || !reflect.DeepEqual(c, &probe) || !reflect.DeepEqual(ptr, b) || !reflect.DeepEqual(f.state.ships[id], c) || ptr != f.state.ships[77] {
						t.Error("notice before durable actor and store installation")
					}
				}
				f.state.SetRegistry(reg)
			})
			msg := f.command(t, "build :")
			b, e := f.store.GetShip(77)
			c, ce := f.store.GetShip(id)
			if e != nil || ce != nil || b == nil || c == nil {
				t.Fatalf("missing construction: %q, %v %v", msg, e, ce)
			}
			if !reflect.DeepEqual(b, &carrier) || !reflect.DeepEqual(c, &probe) || !reflect.DeepEqual(factoryBuildRecords(t, f), want) {
				t.Fatalf("stored child #%d, carrier resource/hangar/head=%d/%d/%d, want %d/%d/%d", c.Number, b.Resource, b.Hanger, b.Ships, carrier.Resource, carrier.Hanger, carrier.Ships)
			}
			wantNotices := strings.Join([]string{
				fmt.Sprintf("gov:1/0::%d built at a cost of 24 resources.\n", id),
				"gov:1/0:Technology 1000.0.\n",
				"gov:1/0:This ship is robotic, and may not repair itself.\n",
				"gov:1/0:Loaded with 0 crew and 0.0 fuel.\n",
			}, "\n")
			if reg.all() != wantNotices || msg != "" {
				t.Fatalf("unexpected notices or response: %q %q", reg.all(), msg)
			}
		})
	}
}

func TestCarrierProbeScheduledPersistence(t *testing.T) {
	t.Parallel()
	f := newProbeFixture(t, false)
	probe, carrier := expectedProbe(t, f, 78)
	f.command(t, "build :")
	check := func() {
		t.Helper()
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		b, e := f.store.GetShip(77)
		c, ce := f.store.GetShip(78)
		old, oe := f.store.GetShip(76)
		if e != nil || ce != nil || oe != nil || b == nil || c == nil || old == nil || b.Resource != carrier.Resource || b.Hanger != carrier.Hanger || c.WhatOrbits != model.LevelShip || !c.Docked || c.DestShipNo != 77 || c.Size != probe.Size {
			t.Fatal("lost Carrier debit, probe or original child chain", b, c, old)
		}
		// Turn processing rebuilds lists in ship-number order. Either order
		// must reach precisely both children, once, with the exact accounting.
		hold := map[uint32]bool{}
		for n := b.Ships; n != 0; {
			sh, e := f.store.GetShip(n)
			if e != nil || sh == nil || hold[n] || sh.WhatOrbits != model.LevelShip || sh.DestShipNo != 77 {
				t.Fatal("bad hangar membership", n, e)
			}
			hold[n] = true
			n = sh.NextShip
		}
		if len(hold) != 2 || !hold[78] || !hold[76] {
			t.Fatal("wrong hangar contents", hold)
		}
		pl, e := f.store.GetPlanet(f.star, f.planet)
		if e != nil || pl == nil {
			t.Fatal("missing planet", e)
		}
		seen := map[uint32]bool{}
		for n := pl.Ships; n != 0; {
			sh, e := f.store.GetShip(n)
			if e != nil || sh == nil || seen[n] || n == 78 || n == 76 {
				t.Fatal("bad exterior orbit", n, e)
			}
			seen[n] = true
			n = sh.NextShip
		}
		if !seen[77] {
			t.Fatal("Carrier unreachable")
		}
	}
	check()
	scheduledBuildTurnsAndRestart(t, f, check)
}

func TestCarrierProbeRealProcessCrash(t *testing.T) {
	t.Parallel()
	for _, free := range []bool{false, true} {
		for _, stage := range []string{"before", "validated", "write:1", "write:2", "write:3", "before-commit", "after-commit", "preinstall", "installed"} {
			t.Run(fmt.Sprintf("free=%t/%s", free, stage), func(t *testing.T) {
				f := newProbeFixture(t, free)
				id := uint32(78)
				if free {
					id = 70
				}
				c, b := expectedProbe(t, f, id)
				if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
				sched, e := f.store.GetSchedule()
				if e != nil || sched == nil {
					t.Fatal(e)
				}
				want := factoryBuildRecords(t, f)
				if stage == "after-commit" || stage == "preinstall" || stage == "installed" {
					expectProbeRecords(t, want, c, b, free)
				}
				killConstructionAt(t, f, "probe-build."+stage, sched.SavedAt)
				if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatal("crash persisted incorrect world or allocation")
				}
			})
		}
	}
}

func TestCarrierProbeFailures(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"carrier writer", "child writer", "orbit writer", "race writer", "write:1", "write:2", "write:3", "commit-error", "after-commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newProbeFixture(t, true)
			want := factoryBuildRecords(t, f)
			c, b := expectedProbe(t, f, 70)
			point := "probe-build." + stage
			var external func() error
			if stage == "carrier writer" || stage == "child writer" || stage == "orbit writer" {
				id := uint32(77)
				if stage == "child writer" {
					id = 76
				}
				if stage == "orbit writer" {
					id = b.NextShip
					if id == 0 || id == 77 || id == 76 {
						t.Fatal("fixture needs a distinct exterior witness")
					}
				}
				sh, e := f.store.GetShip(id)
				if e != nil || sh == nil {
					t.Fatal(e)
				}
				sh.Name += " changed"
				external = func() error { return f.store.PutShip(sh) }
				want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(id))] = factoryBuildJSON(t, sh)
				point = "probe-build.before"
			}
			if stage == "race writer" {
				r, err := f.store.GetRace(1)
				if err != nil || r == nil {
					t.Fatal("missing race", err)
				}
				r.Governor[0].Active = false
				external = func() error { return f.store.PutRace(r) }
				want[fmt.Sprintf("%x/%x", db.BucketRaces, db.RaceKey(1))] = factoryBuildJSON(t, r)
				point = "probe-build.before"
			}
			if stage == "commit-error" || stage == "after-commit" {
				expectProbeRecords(t, want, c, b, true)
			}
			reg := &fakeRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
			before := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			hit := false
			f.store.SetMetaFault(func(op string) error {
				if op != point {
					return nil
				}
				hit = true
				if external != nil {
					return external()
				}
				return errors.New("fault injection")
			})
			msg := f.command(t, "build :")
			f.store.SetMetaFault(nil)
			if !hit || f.state.Mode() != ModeHalted || strings.Contains(reg.all(), "built at a cost") || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) || f.store.CommandWriteUncertain() != (stage == "commit-error") {
				t.Fatalf("incorrect failure classification or partial save: %q", msg)
			}
			if stage == "after-commit" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != before {
				t.Fatal("installed unconfirmed proposal")
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("stale shutdown flush", e)
			}
		})
	}
}

func TestCarrierProbeAdmission(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"full", "resources", "governor", "pending", "wrong parent", "stored tail"} {
		t.Run(which, func(t *testing.T) {
			f := newProbeFixture(t, true)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				b := f.state.ships[77]
				child := f.state.ships[76]
				switch which {
				case "full":
					b.MaxHanger = b.Hanger
				case "resources":
					b.Resource = 0
				case "governor":
					f.state.races[0].Governor[0].Active = false
				case "pending":
					child.Name += " pending"
					return
				case "wrong parent":
					child.DestShipNo = 78
				case "stored tail":
					old := *b
					old.Ships = 12345
					if e := f.store.PutShip(&old); e != nil {
						t.Fatal(e)
					}
					return
				}
				for _, e := range []error{f.store.PutShip(b), f.store.PutShip(child), f.store.PutRace(f.state.races[0])} {
					if e != nil {
						t.Fatal(e)
					}
				}
			})
			before := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			msg := f.command(t, "build :")
			halt := which == "wrong parent" || which == "stored tail"
			if halt != (f.state.Mode() == ModeHalted) || msg == "" || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("incorrect admission or mutation", msg)
			}
			switch which {
			case "full":
				if msg != "Not enough hanger space.\n" {
					t.Fatal(msg)
				}
			case "resources":
				if !strings.Contains(msg, "24r") {
					t.Fatal(msg)
				}
			case "pending":
				if !strings.Contains(msg, "retry after the next persisted turn") {
					t.Fatal(msg)
				}
			}
		})
	}
}

func TestCarrierProbeStoreContract(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"omit child", "omit carrier", "forged probe", "autoload", "God", "wrong governor", "insufficient capacity", "allocator changed", "star changed", "fence"} {
		t.Run(which, func(t *testing.T) {
			f := newProbeFixture(t, true)
			var in db.CarrierProbeBuild
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				var e error
				var pending bool
				_, in, pending, e = f.state.probeBuildStage(1, 0)
				if e != nil || pending {
					t.Fatal("valid starting proposal", e, pending)
				}
			})
			switch which {
			case "omit child":
				in.Input.Children = nil
			case "omit carrier":
				in.Input.Orbit = in.Input.Orbit[1:]
			case "forged probe":
				in.Child.Resource = 123
			case "autoload":
				in.Input.Race.Governor[0].Toggle.AutoLoad = true
			case "God":
				in.Input.Race.God = true
			case "wrong governor":
				in.Input.Governor = 1
				in.Input.Race.Governor[1].Active = true
			case "insufficient capacity":
				in.Input.Orbit[0].MaxHanger = in.Input.Orbit[0].Hanger
			case "allocator changed":
				if e := f.store.DeleteShip(65); e != nil {
					t.Fatal(e)
				}
			case "star changed":
				st := *in.Input.Star
				st.AP[0]++
				if e := f.store.PutStar(&st); e != nil {
					t.Fatal(e)
				}
			case "fence":
				f.store.SetWriteFence(true)
				defer f.store.SetWriteFence(false)
			}
			before := factoryBuildRecords(t, f)
			outcome, e := f.store.CommitCarrierProbe(in)
			var failure *db.CommandError
			code := db.CommandInvalid
			if which == "allocator changed" || which == "star changed" {
				code = db.CommandStale
			}
			if which == "fence" {
				code = db.CommandFenced
			}
			if outcome != db.CommandNotCommitted || !errors.As(e, &failure) || failure.Code != code || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
				t.Fatalf("unexpected outcome %s, error %v", outcome, e)
			}
		})
	}
}

func TestCarrierProbeEmptyHold(t *testing.T) {
	f := newProbeFixture(t, false)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		b := f.state.ships[77]
		// Move the old child into the exterior orbit instead of orphaning it.
		old := f.state.ships[76]
		old.WhatOrbits, old.WhatDest, old.Docked, old.DestShipNo = model.LevelPlan, model.LevelUniv, false, 0
		old.NextShip, b.NextShip = b.NextShip, old.Number
		b.Ships, b.Hanger, b.MaxHanger = 0, 0, 1
		for _, e := range []error{f.store.PutShip(b), f.store.PutShip(old)} {
			if e != nil {
				t.Fatal(e)
			}
		}
	})
	probe, carrier := expectedProbe(t, f, 78)
	want := factoryBuildRecords(t, f)
	expectProbeRecords(t, want, probe, carrier, false)
	msg := f.command(t, "build : 1")
	if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) || f.state.Mode() != ModeServing {
		t.Fatal("empty hold / exact capacity", msg)
	}
}

func TestCarrierProbeGovernorDispatch(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"assigned", "unassigned", "foreign owner"} {
		t.Run(which, func(t *testing.T) {
			f := newProbeFixture(t, false)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				r := f.state.races[0]
				r.Governor[1].Active = true
				r.Governor[1].Toggle.AutoLoad = false
				b := f.state.ships[77]
				b.Governor = 1
				if which == "unassigned" {
					b.Governor = 0
				}
				if which == "foreign owner" {
					b.Owner = 2
				}
				f.state.dirs[0][1] = f.state.dirs[0][0]
				for _, e := range []error{f.store.PutRace(r), f.store.PutShip(b)} {
					if e != nil {
						t.Fatal(e)
					}
				}
			})
			before := factoryBuildRecords(t, f)
			reply := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 1, "build :", reply); e != nil {
				t.Fatal(e)
			}
			c, e := f.store.GetShip(78)
			if e != nil || f.state.Mode() != ModeServing {
				t.Fatal("authority admission", e, reply.text())
			}
			if which == "assigned" {
				if c == nil || c.Owner != 1 || c.Governor != 1 {
					t.Fatal("authorized governor build refused", reply.text())
				}
			} else if c != nil || reply.text() == "" || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
				t.Fatal("unauthorized construction", reply.text())
			}
		})
	}
}

func TestCarrierProbeHistoricalHold(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"agreed", "actor pending", "store pending", "corrupt actor tail", "corrupt stored tail", "undocked child", "changed child owner", "double membership", "double membership in store"} {
		t.Run(which, func(t *testing.T) {
			f := newProbeFixture(t, false)
			addArrivingProbe(t, f)
			if msg := f.command(t, "land #60 #77"); !strings.Contains(msg, "landed on") {
				t.Fatalf("land refused: %q", msg)
			}
			reg := &fakeRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				b, sh := f.state.ships[77], f.state.ships[60]
				// Recreate the persisted pre-fix landing on top of a real landing.
				sh.WhatOrbits, sh.WhatDest, sh.DestShipNo = model.LevelPlan, model.LevelUniv, 0
				b.Hanger += sh.Size
				if err := f.store.PutShip(sh); err != nil {
					t.Fatal(err)
				}
				if err := f.store.PutShip(b); err != nil {
					t.Fatal(err)
				}
				stored, err := f.store.GetShip(60)
				if err != nil || !reflect.DeepEqual(sh, stored) || !sh.Docked || sh.WhatOrbits != model.LevelPlan || sh.DestShipNo == b.Number || b.Ships != 60 || sh.NextShip != 76 {
					t.Fatal("historical fixture did not persist the recorded hold", err)
				}
				pl := f.state.planets[f.star][f.planet]
				storedPlanet, pe := f.store.GetPlanet(f.star, f.planet)
				storedCarrier, ce := f.store.GetShip(b.Number)
				if pe != nil || ce != nil || pl.Ships != b.Number || !reflect.DeepEqual(pl, storedPlanet) || !reflect.DeepEqual(b, storedCarrier) {
					t.Fatal("historical fixture has different planet or Carrier preimages", pe, ce)
				}
				// Exercise one-sided changes and corruption against that historical image.
				switch which {
				case "actor pending":
					sh.WhatOrbits, sh.DestShipNo = model.LevelShip, b.Number
				case "store pending":
					stored.WhatOrbits, stored.DestShipNo = model.LevelShip, b.Number
					if err := f.store.PutShip(stored); err != nil {
						t.Fatal(err)
					}
				case "corrupt actor tail":
					f.state.ships[76].DestShipNo = 78
				case "corrupt stored tail":
					tail := *f.state.ships[76]
					tail.DestShipNo = 78
					if err := f.store.PutShip(&tail); err != nil {
						t.Fatal(err)
					}
				case "changed child owner":
					// Capture may change ownership of a legacy child that still
					// appears landed to the old navigation helpers.
					sh.Owner = 2
					if err := f.store.PutShip(sh); err != nil {
						t.Fatal(err)
					}
				case "undocked child":
					sh.Docked = false
					if err := f.store.PutShip(sh); err != nil {
						t.Fatal(err)
					}
				case "double membership", "double membership in store":
					// Keep the exterior and hold individually traversable, sharing their tail.
					carrier := *b
					stored.NextShip, carrier.NextShip = 0, sh.Number
					if which == "double membership" {
						sh.NextShip, b.NextShip = stored.NextShip, carrier.NextShip
					}
					for _, record := range []*model.Ship{&carrier, stored} {
						if err := f.store.PutShip(record); err != nil {
							t.Fatal(err)
						}
					}
				}
				f.state.SetRegistry(reg)
			})
			before := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			f.store.SetMetaFault(func(point string) error {
				if point == "probe-build.before" {
					t.Error("refused build reached the write transaction")
				}
				return nil
			})
			msg := f.command(t, "build :")
			f.store.SetMetaFault(nil)
			wantMode := ModeServing
			switch which {
			case "agreed", "undocked child", "changed child owner":
				if !strings.Contains(msg, "hangar records are unsettled") {
					t.Errorf("expected specific hold refusal, got %q", msg)
				}
			case "actor pending", "store pending":
				if !strings.Contains(msg, "retry after the next persisted turn") {
					t.Errorf("expected pending-state refusal, got %q", msg)
				}
			default:
				wantMode = ModeHalted
			}
			if mode := f.state.Mode(); mode != wantMode || strings.Contains(reg.all(), "built at a cost") || (wantMode == ModeServing && reg.all() != "") || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatalf("mode=%v want=%v, notices=%q, response=%q; expected unchanged records and actor", mode, wantMode, reg.all(), msg)
			}
		})
	}
}

// Install a probe behind a ship other than the Carrier. Landing it must save
// that predecessor as well as the planet, the probe and the Carrier.
func addArrivingProbe(t *testing.T, f *factoryFixture) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		b := f.state.ships[77]
		prev := f.state.ships[b.NextShip]
		if prev == nil || f.state.ships[60] != nil {
			t.Fatal("fixture requires an exterior predecessor and unused #60")
		}
		sh := &model.Ship{}
		f.state.getshipDefaults(sh, model.OtypeProbe, f.state.races[0])
		sh.Number, sh.Owner, sh.Alive, sh.Active = 60, 1, true, true
		sh.Fuel = float64(sh.MaxFuel)
		sh.WhatOrbits, sh.StOrbits, sh.PNumOrbits = model.LevelPlan, b.StOrbits, b.PNumOrbits
		sh.XPos, sh.YPos, sh.NextShip = b.XPos, b.YPos, prev.NextShip
		prev.NextShip, f.state.ships[60] = sh.Number, sh
		st := f.state.stars[f.star]
		st.AP[0] = 50
		for _, err := range []error{f.store.PutShip(sh), f.store.PutShip(prev), f.store.PutStar(st)} {
			if err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestCarrierProbeHangarCommands(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		land  bool
		next  string
		scrap bool
	}{
		{name: "land", land: true},
		{name: "land launch", land: true, next: "launch #60"},
		{name: "land launch sibling", land: true, next: "launch #76"},
		{name: "launch", next: "launch #76"},
		{name: "land scrap sibling", land: true, next: "scrap #76", scrap: true},
		{name: "scrap", next: "scrap #76", scrap: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProbeFixture(t, false)
			addArrivingProbe(t, f)
			shuttle, err := f.store.GetShip(76)
			if err != nil || shuttle == nil {
				t.Fatal(err)
			}
			occupied := shuttle.Size
			if tc.land {
				if msg := f.command(t, "land #60 #77"); !strings.Contains(msg, "landed on") {
					t.Fatal(msg)
				}
				sh, err := f.store.GetShip(60)
				b, be := f.store.GetShip(77)
				if err != nil || be != nil || sh == nil || b == nil || sh.Size != 1 || !sh.Docked || sh.WhatDest != model.LevelShip || sh.WhatOrbits != model.LevelShip || sh.DestShipNo != 77 || b.Ships != 60 || sh.NextShip != 76 || b.Hanger != occupied+sh.Size {
					t.Fatal("incorrect hold after landing", b, sh, err, be)
				}
				occupied += sh.Size
			}
			if tc.next != "" {
				msg := f.command(t, tc.next)
				if tc.scrap {
					if !strings.Contains(msg, "Destroyed.") {
						t.Fatal(msg)
					}
					occupied -= shuttle.Size
				} else {
					if !strings.Contains(msg, "launched from") {
						t.Fatal(msg)
					}
					id := uint32(76)
					if tc.next == "launch #60" {
						id = 60
					}
					sh, err := f.store.GetShip(id)
					pl, pe := f.store.GetPlanet(f.star, f.planet)
					if err != nil || pe != nil || sh == nil || pl == nil || pl.Ships != id || sh.Docked || sh.WhatOrbits != model.LevelPlan || sh.WhatDest != model.LevelUniv {
						t.Fatal("incorrect exterior membership after launch", sh, pl, err, pe)
					}
					occupied -= sh.Size
				}
			}
			b, err := f.store.GetShip(77)
			if err != nil || b == nil || b.Hanger != occupied {
				t.Fatal("incorrect occupied space", b, err)
			}

			if tc.scrap {
				// The legacy AP charge is still volatile. Then model a
				// persisted AP boundary while the corpse remains linked.
				checkRefusedProbe(t, f, "retry after the next persisted turn", ModeServing)
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					if err := f.store.PutStar(f.state.stars[f.star]); err != nil {
						t.Fatal(err)
					}
				})
				checkRefusedProbe(t, f, "hangar records are unsettled", ModeServing)
				scheduledBuildTurnsAndRestart(t, f, func() {
					b, e := f.store.GetShip(77)
					if e != nil || b == nil || b.Hanger != occupied || b.Ships == 76 {
						t.Fatal("turn retained scrapped occupancy or head", b, e)
					}
				})
				f.command(t, "cs #77") // The session directory is volatile across restart.
				// A turn may order live children differently. Check the
				// exact new insertion against the post-turn Carrier.
			}
			child, carrier := expectedProbe(t, f, 78)
			want := factoryBuildRecords(t, f)
			expectProbeRecords(t, want, child, carrier, false)
			msg := f.command(t, "build :")
			if f.state.Mode() != ModeServing || msg != "" || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatalf("expected a complete successful build after %s, got %q (%v)", tc.name, msg, f.state.Mode())
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		})
	}
}

func checkRefusedProbe(t *testing.T, f *factoryFixture, text string, mode Mode) {
	t.Helper()
	before := factoryBuildRecords(t, f)
	actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	reg := &fakeRegistry{}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
	msg := f.command(t, "build :")
	if !strings.Contains(msg, text) || f.state.Mode() != mode || strings.Contains(reg.all(), "gov:") || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatalf("refusal %q (%v) wanted %q (%v) without proposal, write or notice", msg, f.state.Mode(), text, mode)
	}
}

func TestCarrierProbeHoldValidation(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"excess occupancy", "short occupancy", "dead linked", "cycle", "absent linked record", "invalid number"} {
		t.Run(which, func(t *testing.T) {
			f := newProbeFixture(t, true)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				b, c := f.state.ships[77], f.state.ships[76]
				switch which {
				case "excess occupancy":
					b.Hanger++
				case "short occupancy":
					b.Hanger--
				case "dead linked":
					c.Alive = false
				case "cycle":
					c.NextShip = c.Number
				case "absent linked record":
					b.Ships = 666
				case "invalid number":
					c.Number = 75
				}
				if err := f.store.PutShip(b); err != nil {
					t.Fatal(err)
				}
				if err := f.store.PutShip(c); err != nil {
					t.Fatal(err)
				}
			})
			switch which {
			case "cycle", "absent linked record", "invalid number":
				checkRefusedProbe(t, f, "halted", ModeHalted)
			default:
				checkRefusedProbe(t, f, "hangar records are unsettled", ModeServing)
			}
		})
	}
}

func TestCarrierProbeHistoricalTurnRebuild(t *testing.T) {
	t.Parallel()
	f := newProbeFixture(t, false)
	addArrivingProbe(t, f)
	f.command(t, "land #60 #77")
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		// An old land followed by a planet launch left an undocked child
		// in the hold and charged its size twice.
		sh, b := f.state.ships[60], f.state.ships[77]
		sh.Docked, sh.WhatOrbits, sh.WhatDest, sh.DestShipNo = false, model.LevelPlan, model.LevelUniv, 0
		b.Hanger += sh.Size
		for _, e := range []error{f.store.PutShip(sh), f.store.PutShip(b)} {
			if e != nil {
				t.Fatal(e)
			}
		}
	})
	checkRefusedProbe(t, f, "hangar records are unsettled", ModeServing)
	scheduledBuildTurnsAndRestart(t, f, func() {
		b, e := f.store.GetShip(77)
		sh, se := f.store.GetShip(76)
		if e != nil || se != nil || b == nil || sh == nil || b.Ships != 76 || sh.NextShip != 0 || b.Hanger != sh.Size {
			t.Fatal("rebuilt hold or occupancy", e, se, b, sh)
		}
	})
	f.command(t, "cs #77")
	c, b := expectedProbe(t, f, 78)
	want := factoryBuildRecords(t, f)
	expectProbeRecords(t, want, c, b, false)
	if msg := f.command(t, "build :"); msg != "" || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
		t.Fatalf("build after legacy turn rebuild: %q", msg)
	}
}

func TestCarrierSurfaceLoadingAndLegacyInteriorBuild(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"surface", "legacy build"} {
		t.Run(which, func(t *testing.T) {
			f := newProbeFixture(t, false)
			addArrivingProbe(t, f)
			if which == "surface" {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					for _, n := range []uint32{77, 60} {
						sh := f.state.ships[n]
						sh.Docked, sh.WhatDest = true, model.LevelPlan
						sh.LandX, sh.LandY = 0, 0
						if e := f.store.PutShip(sh); e != nil {
							t.Fatal(e)
						}
					}
				})
				if msg := f.command(t, "land #60 #77"); !strings.Contains(msg, "loaded onto") {
					t.Fatal(msg)
				}
				b, e := f.store.GetShip(77)
				sh, se := f.store.GetShip(60)
				old, oe := f.store.GetShip(76)
				if e != nil || se != nil || oe != nil || sh == nil || old == nil || b == nil || b.Ships != 60 || sh.NextShip != 76 || b.Hanger != sh.Size+old.Size || !sh.Docked || sh.WhatOrbits != model.LevelShip || sh.WhatDest != model.LevelShip || sh.DestShipNo != 77 {
					t.Fatal("surface load accounting", b, sh, e, se, oe)
				}
				if msg := f.command(t, "launch #60"); !strings.Contains(msg, "Landed on") {
					t.Fatal(msg)
				}
				b, e = f.store.GetShip(77)
				sh, se = f.store.GetShip(60)
				if e != nil || se != nil || sh == nil || b == nil || b.Ships != 76 || b.Hanger != old.Size || !landed(sh) || sh.WhatDest != model.LevelPlan {
					t.Fatal("surface unload accounting", b, sh, e, se)
				}
			} else {
				f.command(t, "build : 2")
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					b, c, d := f.state.ships[77], f.state.ships[78], f.state.ships[79]
					if c == nil || d == nil || b.Ships != 79 || d.NextShip != 78 || c.NextShip != 76 || b.Hanger != f.state.ships[76].Size+c.Size+d.Size {
						t.Fatal("legacy multi-build hold", b, c, d)
					}
					for _, sh := range []*model.Ship{c, d} {
						if !sh.Docked || sh.WhatDest != model.LevelShip || sh.WhatOrbits != model.LevelShip || sh.DestShipNo != 77 {
							t.Fatal("legacy build containment", sh)
						}
					}
				})
				checkRefusedProbe(t, f, "retry after the next persisted turn", ModeServing)
			}
		})
	}
}

func TestCarrierProbeOtherPlanetLanding(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"ordinary", "historical", "historical double link"} {
		t.Run(which, func(t *testing.T) {
			f := newProbeFixture(t, false)
			addArrivingProbe(t, f)
			var origin *model.Planet
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				current := f.state.planets[f.star][f.planet]
				sh := f.state.ships[60]
				if !f.state.removeShipPlan(current, sh) {
					t.Fatal("fixture lacks arriving ship")
				}
				// Choose deterministically a different valid planet, leaving
				// the two ships close enough to dock in absolute coordinates.
				for _, st := range f.state.stars {
					if st == nil {
						continue
					}
					for n := uint8(0); n < st.NumPlanets; n++ {
						pl := f.state.planet(int(st.Number), int(n))
						if pl != nil && pl != current && pl.Ships == 0 {
							origin = pl
							break
						}
					}
					if origin != nil {
						break
					}
				}
				if origin == nil {
					t.Fatal("fixture lacks another planet")
				}
				sh.StOrbits, sh.PNumOrbits = origin.StarNum, origin.PlanetNum
				f.state.insertShipPlan(origin, sh)
				for _, e := range []error{f.store.PutPlanet(current), f.store.PutPlanet(origin), f.store.PutShip(sh)} {
					if e != nil {
						t.Fatal(e)
					}
				}
			})
			if msg := f.command(t, "land #60 #77"); !strings.Contains(msg, "landed on") {
				t.Fatal(msg)
			}
			if which == "ordinary" {
				child, carrier := expectedProbe(t, f, 78)
				want := factoryBuildRecords(t, f)
				expectProbeRecords(t, want, child, carrier, false)
				if msg := f.command(t, "build :"); msg != "" || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatalf("build after cross-orbit landing: %q", msg)
				}
				return
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[60]
				sh.WhatOrbits, sh.WhatDest, sh.DestShipNo = model.LevelPlan, model.LevelUniv, 0
				if e := f.store.PutShip(sh); e != nil {
					t.Fatal(e)
				}
				if which == "historical double link" {
					other := *origin
					other.Ships = 60
					if e := f.store.PutPlanet(&other); e != nil {
						t.Fatal(e)
					}
				}
			})
			if which == "historical" {
				checkRefusedProbe(t, f, "hangar records are unsettled", ModeServing)
			} else {
				checkRefusedProbe(t, f, "halted", ModeHalted)
			}
		})
	}
}

func TestCarrierStarHangarRoundTrip(t *testing.T) {
	f := newProbeFixture(t, false)
	addArrivingProbe(t, f)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		pl, st := f.state.planets[f.star][f.planet], f.state.stars[f.star]
		for _, n := range []uint32{60, 77} {
			sh := f.state.ships[n]
			if !f.state.removeShipPlan(pl, sh) {
				t.Fatal("missing planet fixture ship")
			}
			sh.WhatOrbits = model.LevelStar
			f.state.insertShipStar(int(f.star), sh)
			if e := f.store.PutShip(sh); e != nil {
				t.Fatal(e)
			}
		}
		for _, e := range []error{f.store.PutPlanet(pl), f.store.PutStar(st)} {
			if e != nil {
				t.Fatal(e)
			}
		}
	})
	before := factoryBuildRecords(t, f)
	if msg := f.command(t, "land #77 #77"); !strings.Contains(msg, "Cannot land") || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
		t.Fatal("self landing", msg)
	}
	if msg := f.command(t, "land #60 #77"); !strings.Contains(msg, "landed on") {
		t.Fatal(msg)
	}
	old, oe := f.store.GetShip(76)
	b, e := f.store.GetShip(77)
	sh, se := f.store.GetShip(60)
	if e != nil || se != nil || oe != nil || old == nil || sh == nil || b == nil || sh.WhatOrbits != model.LevelShip || sh.WhatDest != model.LevelShip || sh.DestShipNo != 77 || b.Hanger != old.Size+sh.Size || b.Ships != 60 {
		t.Fatal("star landing", b, sh, e, se)
	}
	if msg := f.command(t, "launch #60"); !strings.Contains(msg, "launched from") {
		t.Fatal(msg)
	}
	b, e = f.store.GetShip(77)
	sh, se = f.store.GetShip(60)
	st, ste := f.store.GetStar(f.star)
	if e != nil || se != nil || ste != nil || sh == nil || b == nil || st == nil || sh.Docked || sh.WhatOrbits != model.LevelStar || sh.WhatDest != model.LevelUniv || b.Hanger != old.Size || b.Ships != 76 || st.Ships != 60 {
		t.Fatal("star launch", b, sh, e, se, ste)
	}
}
