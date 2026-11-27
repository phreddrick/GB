package game

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

// Construct the exact expected store independently of the command planner.
func expectedDock(t *testing.T, f *factoryFixture, records map[string]string) map[uint32]*model.Ship {
	t.Helper()
	rows := map[uint32]*model.Ship{}
	for id, target := range map[uint32]uint32{77: 78, 78: 77} {
		sh := landingShip(t, f, id)
		sh.Docked, sh.WhatDest, sh.DestShipNo = true, model.LevelShip, target
		rows[id] = sh
		records[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(id))] = factoryBuildJSON(t, sh)
	}
	return rows
}

func TestCR17FriendlyDockDispatch(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"dock #77 #78", "dock 77 78", "dock #+77 +78 extra"} {
		t.Run(cmd, func(t *testing.T) {
			f := flyingPeerFixture(t)
			ptrs := map[uint32]*model.Ship{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				for _, n := range []uint32{77, 78} {
					sh := f.state.ships[n]
					ptrs[n] = sh
					sh.XPos, sh.YPos = 30, 56
					sh.Active = false // neither active status nor fuel is a Go docking requirement
					sh.Fuel = 0
					sh.HyperDrive.On, sh.Notified = true, true
					if n == 77 {
						sh.XPos += f.state.cfg.DistToDock
						sh.Governor = 2
					}
					if err := f.store.PutShip(sh); err != nil {
						t.Fatal(err)
					}
				}
			})
			want := factoryBuildRecords(t, f)
			rows := expectedDock(t, f, want)
			writes, notices := 0, 0
			f.store.SetMetaFault(func(op string) error {
				if strings.HasPrefix(op, "friendly-dock.write:") {
					writes++
				}
				return nil
			})
			resp := &probeLaunchResponder{at: func(msg string) {
				notices++
				if writes != 2 || msg != "c77 docked with c78.\n" {
					t.Error("notice before complete durable pair", msg, writes)
				}
				for id, sh := range rows {
					if ptrs[id] != f.state.ships[id] || !reflect.DeepEqual(ptrs[id], sh) || !reflect.DeepEqual(landingShip(t, f, id), sh) {
						t.Error("incomplete actor installation at notice", id)
					}
				}
			}}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 1, cmd, resp); e != nil {
				t.Fatal(e)
			}
			f.store.SetMetaFault(nil)
			if notices != 1 || f.state.Mode() != ModeServing || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal(notices, f.state.Mode())
			}
			// Race leader can release either ship under #96's independent authority rule.
			checkPeerRelease(t, f, 0, 78, 77, 78, 77)
		})
	}
}

func TestCR17FriendlyDockAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reply string
		mode        Mode
	}{
		{"self", "yourself", ModeServing}, {"occupied", "already docked", ModeServing}, {"source occupied", "already docked", ModeServing},
		{"range", "must be within", ModeServing}, {"inactive governor", "not authorized", ModeServing},
		{"wide source", "Invalid ship number", ModeServing}, {"wide target", "Invalid ship number", ModeServing}, {"negative target alias", "Invalid ship number", ModeServing},
		{"pending", "deferred", ModeServing}, {"held source", "unsettled", ModeServing}, {"held target", "unsettled", ModeServing},
		{"detached interior join", "unsettled", ModeServing}, {"missing linked row", "halted", ModeHalted}, {"broken stored", "halted", ModeHalted}, {"wrong star", "halted", ModeHalted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := flyingPeerFixture(t)
			cmd := "dock #77 #78"
			g := 0
			if tc.name == "occupied" || tc.name == "source occupied" {
				if out := f.command(t, "dock #79 #78"); !strings.Contains(out, "docked with") {
					t.Fatal(out)
				}
				if tc.name == "source occupied" {
					cmd = "dock #79 #77"
				}
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				a, b, c := f.state.ships[77], f.state.ships[78], f.state.ships[79]
				switch tc.name {
				case "self":
					cmd = "dock #77 #77"
				case "inactive governor":
					g = 3
				case "wide source":
					cmd = "dock #4294967373 #78"
				case "wide target":
					cmd = "dock #77 #4294967374"
				case "negative target alias":
					cmd = "dock #77 #-4294967218"
				case "range":
					a.XPos = b.XPos + f.state.cfg.DistToDock + 1
				case "pending":
					c.Name += " pending"
					return
				case "detached interior join":
					c.NextShip = a.Number // b still points at a, a current member
				case "missing linked row":
					c.NextShip = 9999
				case "held source":
					b.NextShip = 0
					c.Ships = 77
					a.NextShip = 0
				case "held target":
					c.NextShip = 77
					c.Ships = 78
					b.NextShip = 0
				case "wrong star":
					b.StOrbits ^= 1
				case "broken stored":
					raw := *c
					raw.NextShip = 9999
					if e := f.store.PutShip(&raw); e != nil {
						t.Fatal(e)
					}
					return
				}
				for _, sh := range []*model.Ship{a, b, c} {
					if e := f.store.PutShip(sh); e != nil {
						t.Fatal(e)
					}
				}
			})
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			want := factoryBuildRecords(t, f)
			hit := false
			f.store.SetMetaFault(func(op string) error {
				if op == "friendly-dock.before" {
					hit = true
				}
				return nil
			})
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, cmd, r); e != nil {
				t.Fatal(e)
			}
			f.store.SetMetaFault(nil)
			if hit || f.state.Mode() != tc.mode || !strings.Contains(r.text(), tc.reply) || strings.Contains(r.text(), "docked with") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("wrong admission", hit, f.state.Mode(), r.text())
			}
			if tc.name == "pending" {
				scheduledBuildTurnsAndRestart(t, f, func() { assertActorStoreAgreement(t, f.actorEnrollmentFixture) })
				if out := f.command(t, cmd); !strings.Contains(out, "docked with") {
					t.Fatal("retry", out)
				}
			}
		})
	}
}

func TestCR17FriendlyDockFaults(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"source", "target", "sibling", "race", "star", "write:1", "write:2", "before-commit", "commit-error", "after-commit", "lost install"} {
		t.Run(stage, func(t *testing.T) {
			f := flyingPeerFixture(t)
			want := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			point := "friendly-dock." + stage
			fault := func() error { return errors.New("injected") }
			switch stage {
			case "source", "target", "sibling":
				id := map[string]uint32{"source": 77, "target": 78, "sibling": 79}[stage]
				sh := landingShip(t, f, id)
				sh.XPos++
				point = "friendly-dock.before"
				fault = func() error { return f.store.PutShip(sh) }
				want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(id))] = factoryBuildJSON(t, sh)
			case "race":
				r, e := f.store.GetRace(1)
				if e != nil {
					t.Fatal(e)
				}
				r.Governor[0].Active = false
				point = "friendly-dock.before"
				fault = func() error { return f.store.PutRace(r) }
				want[fmt.Sprintf("%x/%x", db.BucketRaces, db.RaceKey(1))] = factoryBuildJSON(t, r)
			case "star":
				st, e := f.store.GetStar(f.star)
				if e != nil {
					t.Fatal(e)
				}
				st.AP[0]++
				point = "friendly-dock.before"
				fault = func() error { return f.store.PutStar(st) }
				want[fmt.Sprintf("%x/%x", db.BucketStars, db.StarKey(f.star))] = factoryBuildJSON(t, st)
			case "after-commit", "commit-error", "lost install":
				expectedDock(t, f, want)
			}
			hit := false
			if stage == "lost install" {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.testHooks.beforeCommandInstall = func() { hit = true; sh := *f.state.ships[78]; f.state.ships[78] = &sh }
				})
			}
			f.store.SetMetaFault(func(op string) error {
				if op == point {
					hit = true
					return fault()
				}
				return nil
			})
			out := f.command(t, "dock #77 #78")
			f.store.SetMetaFault(nil)
			if !hit || f.state.Mode() != ModeHalted || strings.Contains(out, "docked with") || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) || f.store.CommandWriteUncertain() != (stage == "commit-error") {
				t.Fatal("invalid fault outcome", out, stage)
			}
			if stage == "after-commit" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("unconfirmed installation", stage)
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("stale flush", e)
			}
		})
	}
}

func TestCR17FriendlyDockCrash(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"before", "validated", "write:1", "write:2", "before-commit", "after-commit", "preinstall", "installed"} {
		t.Run(stage, func(t *testing.T) {
			f := flyingPeerFixture(t)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			sched, e := f.store.GetSchedule()
			if e != nil || sched == nil {
				t.Fatal(e)
			}
			want := factoryBuildRecords(t, f)
			if stage == "after-commit" || stage == "preinstall" || stage == "installed" {
				expectedDock(t, f, want)
			}
			killConstructionAt(t, f, "friendly-dock."+stage, sched.SavedAt)
			if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("wrong or partial pair immediately on reopen")
			}
		})
	}
}

func TestCR17FriendlyDockScheduled(t *testing.T) {
	t.Parallel()
	f := peerUndockFixture(t) // dispatched dock
	checkRecoveryBoundary(t, f, map[uint32]uint32{77: 78, 78: 77, 79: 0}, 0)
	checkPeerRelease(t, f, 2, 78, 77, 78, 77)
}

func TestCR17FriendlyDockStorageContract(t *testing.T) {
	t.Parallel()
	f := flyingPeerFixture(t)
	st, e := f.store.GetStar(f.star)
	if e != nil {
		t.Fatal(e)
	}
	r, e := f.store.GetRace(1)
	if e != nil {
		t.Fatal(e)
	}
	before := factoryBuildRecords(t, f)
	for _, variant := range []string{"foreign", "dead", "occupied", "self", "absent", "cycle", "truncated", "far", "revoked"} {
		t.Run(variant, func(t *testing.T) {
			in := commandrules.FriendlyDock{Player: 1, Governor: 0, Source: 77, Target: 78, Star: st, Race: r, DistToDock: 10}
			for n := st.Ships; n != 0; {
				sh := landingShip(t, f, n)
				in.Orbit = append(in.Orbit, sh)
				n = sh.NextShip
			}
			b := in.Orbit[1]
			if b.Number != 78 {
				t.Fatal("fixture")
			}
			switch variant {
			case "foreign":
				b.Owner = 2
			case "dead":
				b.Alive = false
			case "occupied":
				b.Docked = true
			case "self":
				in.Target = in.Source
			case "absent":
				in.Target = 80
			case "cycle":
				in.Orbit[2].NextShip = in.Orbit[0].Number
			case "truncated":
				in.Orbit = in.Orbit[:2]
			case "far":
				b.YPos += 100
			case "revoked":
				rc := *r
				rc.Dissolved = true
				in.Race = &rc
			}
			out, e := f.store.CommitFriendlyDock(in)
			var ce *db.CommandError
			if out != db.CommandNotCommitted || !errors.As(e, &ce) || ce.Code != db.CommandInvalid || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
				t.Fatal("invalid proposal reached compare or write", out, e)
			}
		})
	}
}

func TestCR17FriendlyDockLegacyRouting(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"cross-star", "planet", "universe", "foreign", "one type match", "wildcard"} {
		t.Run(variant, func(t *testing.T) {
			f := flyingPeerFixture(t)
			cmd := "dock #77 #78"
			if variant == "cross-star" {
				moveNamedUndockShip(t, f, "other star")
			}
			var writes []uint32
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				a, b, c := f.state.ships[77], f.state.ships[78], f.state.ships[79]
				switch variant {
				case "foreign":
					b.Owner, b.Type = 2, model.OtypeVN // deterministic legacy assault refusal
				case "planet", "universe":
					c.NextShip = 0
					if variant == "planet" {
						a.WhatOrbits, b.WhatOrbits = model.LevelPlan, model.LevelPlan
						pl := f.state.planet(int(f.star), int(f.planet))
						a.PNumOrbits, b.PNumOrbits = f.planet, f.planet
						pl.Ships = b.Number
						if e := f.store.PutPlanet(pl); e != nil {
							t.Fatal(e)
						}
					} else {
						a.WhatOrbits, b.WhatOrbits = model.LevelUniv, model.LevelUniv
						f.state.sdata.Ships = b.Number
						if e := f.store.PutStarData(f.state.sdata); e != nil {
							t.Fatal(e)
						}
					}
				case "one type match", "wildcard":
					f.state.dirs[0][0].Level, f.state.dirs[0][0].StarNum = model.LevelStar, f.star
					b.Type, c.Type = model.StypeCarrier, model.StypeCarrier
					cmd = "dock c #78"
					if variant == "wildcard" {
						cmd = "dock * #78"
					}
				}
				for _, sh := range []*model.Ship{a, b, c} {
					if e := f.store.PutShip(sh); e != nil {
						t.Fatal(e)
					}
				}
				f.state.testHooks.afterLegacyShipWrite = func(id uint32) { writes = append(writes, id) }
			})
			entered := false
			f.store.SetMetaFault(func(op string) error {
				if op == "friendly-dock.before" {
					entered = true
				}
				return nil
			})
			msg := f.command(t, cmd)
			f.store.SetMetaFault(nil)
			if entered || f.state.Mode() != ModeServing {
				t.Fatal("changed legacy routing", msg)
			}
			if variant == "foreign" {
				if len(writes) != 0 || !strings.Contains(msg, "can't assault Von Neumann") {
					t.Fatal(msg, writes)
				}
			} else if len(writes) < 2 || !strings.Contains(msg, "docked with") {
				t.Fatal("missing legacy dock", msg, writes)
			}
		})
	}
}

// Create the detached graph through ordinary construction, then reopen the
// durable rows without flushing the builder's pending exterior list.
func detachedDockBuild(t *testing.T, builderType model.ShipType, autoload bool, commands []string, types ...model.ShipType) *factoryFixture {
	t.Helper()
	f := pendingDockBuild(t, builderType, autoload, commands, types...)
	reopenUnflushedBuild(t, f)
	return f
}

func pendingDockBuild(t *testing.T, builderType model.ShipType, autoload bool, commands []string, types ...model.ShipType) *factoryFixture {
	t.Helper()
	f := newCargoBuildFixture(t, false, model.LevelStar)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		r := f.state.races[0]
		if r.God {
			t.Error("expected ordinary race")
		}
		r.Governor[0].Toggle.AutoLoad = autoload
		b := f.state.ships[77]
		if builderType != b.Type {
			sh := &model.Ship{}
			f.state.getshipDefaults(sh, builderType, r)
			sh.Number, sh.Owner, sh.Alive, sh.Active = b.Number, b.Owner, true, true
			sh.WhatOrbits, sh.StOrbits, sh.NextShip = b.WhatOrbits, b.StOrbits, b.NextShip
			sh.XPos, sh.YPos = b.XPos, b.YPos
			b = sh
			f.state.ships[77] = b
		}
		// Stock this fixture for exactly the requested designs.
		b.Resource = 0
		for _, design := range types {
			template := &model.Ship{}
			f.state.getshipDefaults(template, design, r)
			b.Resource += template.BuildCost
		}
		if b.MaxResource < b.Resource {
			b.MaxResource = b.Resource
		}
		b.Popn, b.Fuel = b.MaxCrew, float64(b.MaxFuel)
		b.Mass = b.BaseMass + float64(b.Popn)*r.Mass + b.Fuel*f.state.cfg.MassFuel + float64(b.Resource)*f.state.cfg.MassResource
		for _, err := range []error{f.store.PutShip(b), f.store.PutRace(r)} {
			if err != nil {
				t.Error(err)
			}
		}
	})
	for _, cmd := range commands {
		if builderType == model.StypeCargo && (cmd == "build S" || cmd == "build S 2") {
			// Recreate the old producer for #43 compatibility tests. Only the
			// producer bypasses the new router; dock and launch still dispatch.
			resp := &recordingResponder{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.buildFromShip(1, 0, f.state.races[0], strings.Fields(cmd)[1:], resp)
			})
			if msg := resp.text(); msg != "" {
				t.Fatalf("%s (legacy fixture): %s", cmd, msg)
			}
			continue
		}
		if msg := f.command(t, cmd); msg != "" {
			t.Fatalf("%s: %s", cmd, msg)
		}
	}
	for i, design := range types {
		row := landingShip(t, f, uint32(78+i))
		if row.Type != design || !row.Alive || row.Docked || row.Ships != 0 || row.NextShip != uint32(77+i) || row.WhatOrbits != model.LevelStar || row.StOrbits != f.star {
			t.Fatalf("unexpected built row #%d: type %v next %d", row.Number, row.Type, row.NextShip)
		}
	}
	if scrapStar(t, f).Ships != 77 {
		t.Fatal("expected unsaved star head")
	}
	expectDetachedAdmission(t, f, "dock #77 #78", "deferred", ModeServing)
	return f
}

func TestCR17FriendlyDockLegacyExteriorBuild(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		builder  model.ShipType
		autoload bool
		commands []string
		types    []model.ShipType
	}{
		{"Cargo Station", model.StypeCargo, true, []string{"build S"}, []model.ShipType{model.StypeStation}},
		{"Cargo Habitat", model.StypeCargo, true, []string{"build H"}, []model.ShipType{model.StypeHabitat}},
		{"Cargo Mirror", model.StypeCargo, true, []string{"build M"}, []model.ShipType{model.StypeMirror}},
		{"Cargo Platform then Station", model.StypeCargo, false, []string{"build O", "build S"}, []model.ShipType{model.StypeOAP, model.StypeStation}},
		{"Cargo two Carriers", model.StypeCargo, false, []string{"build X 2"}, []model.ShipType{model.StypeCarrier, model.StypeCarrier}},
		{"Shuttle Carrier", model.StypeShuttle, true, []string{"build X"}, []model.ShipType{model.StypeCarrier}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := detachedDockBuild(t, tc.builder, tc.autoload, tc.commands, tc.types...)
			expectDetachedAdmission(t, f, "dock #77 #78", "unsettled", ModeServing)
			expectDetachedAdmission(t, f, "dock #78 #77", "unsettled", ModeServing)
			if len(tc.types) > 1 {
				expectDetachedAdmission(t, f, "dock #78 #79", "unsettled", ModeServing)
				expectDetachedAdmission(t, f, "dock #79 #78", "unsettled", ModeServing)
			}
			scheduledBuildTurnsAndRestart(t, f, func() { assertActorStoreAgreement(t, f.actorEnrollmentFixture) })
			if msg := f.command(t, "dock #78 #77"); !strings.Contains(msg, "docked with") {
				t.Fatal("manual retry after rebuild", msg)
			}
			a, b := landingShip(t, f, 78), landingShip(t, f, 77)
			if !a.Docked || !b.Docked || a.DestShipNo != 77 || b.DestShipNo != 78 || f.state.Mode() != ModeServing {
				t.Fatal("retry did not persist reciprocal pair")
			}
		})
	}
}

func TestCR17FriendlyDockDetachedBuildCorruption(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"wrong star", "planet overlap", "dangling", "cycle", "invalid owner", "children"} {
		t.Run(fault, func(t *testing.T) {
			f := detachedDockBuild(t, model.StypeCargo, false, []string{"build M", "build S"}, model.StypeMirror, model.StypeStation)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				row := f.state.ships[78] // Interior of the unlinked Station -> Mirror -> builder run.
				switch fault {
				case "wrong star":
					row.StOrbits ^= 1
				case "planet overlap":
					pl := f.state.planets[f.star][f.planet]
					pl.Ships = row.Number
					if err := f.store.PutPlanet(pl); err != nil {
						t.Error(err)
					}
				case "dangling":
					row.NextShip = 9999
				case "cycle":
					row.NextShip = 79
				case "invalid owner":
					row.Owner = 0
				case "children":
					row.Ships = 9999
				}
				if err := f.store.PutShip(row); err != nil {
					t.Error(err)
				}
			})
			expectDetachedAdmission(t, f, "dock #77 #79", "halted", ModeHalted)
		})
	}
}

// Only the source type selector walks the star root. The numeric target can
// still name one of the ships the legacy build saved ahead of that root.
func dockReopenedBuild(t *testing.T, f *factoryFixture) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		f.state.dirs[0][0] = model.Directory{Level: model.LevelStar, StarNum: f.star}
	})
	if msg := f.command(t, "dock c #78"); !strings.Contains(msg, "c77 docked with") {
		t.Fatal("legacy type-selector dock", msg)
	}
	a, b := landingShip(t, f, 77), landingShip(t, f, 78)
	if !a.Docked || !b.Docked || a.WhatDest != model.LevelShip || b.WhatDest != model.LevelShip ||
		a.DestShipNo != 78 || b.DestShipNo != 77 || scrapStar(t, f).Ships != 77 || b.NextShip != 77 || landingShip(t, f, 79).NextShip != 78 {
		t.Fatal("expected persisted peer claims with the star still missing 79 -> 78")
	}
}

func TestCR17FriendlyDockDockedBuild(t *testing.T) {
	t.Parallel()
	f := detachedDockBuild(t, model.StypeCargo, false, []string{"build S 2"}, model.StypeStation, model.StypeStation)
	dockReopenedBuild(t, f)
	for _, cmd := range []string{"dock #79 #77", "dock #77 #79", "dock #79 #78", "dock #78 #79", "dock #78 #77"} {
		expectDetachedAdmission(t, f, cmd, "unsettled", ModeServing)
	}
	scheduledBuildTurnsAndRestart(t, f, func() { assertActorStoreAgreement(t, f.actorEnrollmentFixture) })
	// The turn repairs membership but preserves the existing dock. Release it
	// through the ordinary repaired graph, then manually retry the same dock.
	expectDetachedAdmission(t, f, "dock #79 #77", "already docked", ModeServing)
	if msg := f.command(t, "launch #78"); !strings.Contains(msg, "undocked from") {
		t.Fatal("release repaired pair", msg)
	}
	if msg := f.command(t, "dock #79 #77"); !strings.Contains(msg, "docked with") ||
		landingShip(t, f, 79).DestShipNo != 77 || landingShip(t, f, 77).DestShipNo != 79 || landingShip(t, f, 78).Docked {
		t.Fatal("manual retry after repair", msg)
	}
}

func TestCR17FriendlyDockMalformedDetachedClaim(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"wrong destination kind", "missing named ship", "invalid named owner", "overlapping planet"} {
		t.Run(fault, func(t *testing.T) {
			f := detachedDockBuild(t, model.StypeCargo, false, []string{"build S 2"}, model.StypeStation, model.StypeStation)
			dockReopenedBuild(t, f)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				b := f.state.ships[78]
				switch fault {
				case "wrong destination kind":
					b.WhatDest = model.LevelPlan // still names the real, linked Cargo
				case "missing named ship":
					b.DestShipNo = 9999
				case "invalid named owner":
					b.DestShipNo = 79 // outside the traversed 78 -> 77 suffix
					peer := f.state.ships[79]
					peer.Owner = 0
					if err := f.store.PutShip(peer); err != nil {
						t.Error(err)
					}
				case "overlapping planet":
					pl := f.state.planets[f.star][f.planet]
					pl.Ships = 78
					if err := f.store.PutPlanet(pl); err != nil {
						t.Error(err)
					}
				}
				if err := f.store.PutShip(b); err != nil {
					t.Error(err)
				}
			})
			expectDetachedAdmission(t, f, "dock #77 #78", "halted", ModeHalted)
		})
	}
}

// A type selection before the unsaved root is lost can pair two new ships
// or dock a new ship with itself. Neither peer pointer is a NextShip cycle.
func TestCR17FriendlyDockPeerClaimBeforeReopen(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		command string
		designs []model.ShipType
		build   string
		peer    uint32
	}{
		{"dock S #78", []model.ShipType{model.StypeStation}, "build S", 78},
		{"dock S #79", []model.ShipType{model.StypeStation, model.StypeStation}, "build S 2", 79},
	} {
		t.Run(tc.command, func(t *testing.T) {
			f := pendingDockBuild(t, model.StypeCargo, false, []string{tc.build}, tc.designs...)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.dirs[0][0] = model.Directory{Level: model.LevelStar, StarNum: f.star}
			})
			if out := f.command(t, tc.command); !strings.Contains(out, "docked with") {
				t.Fatal("legacy dock before reopen", out)
			}
			if sh := landingShip(t, f, 78); !sh.Docked || sh.DestShipNo != tc.peer || landingShip(t, f, tc.peer).DestShipNo != 78 {
				t.Fatal("missing saved peer claim")
			}
			reopenUnflushedBuild(t, f)
			expectDetachedAdmission(t, f, "dock #78 #77", "unsettled", ModeServing)
			// In the two-Station case #79 is outside both the rooted list
			// and the detached suffix starting at #78. It is read for its
			// identity but disagreement about that row must still defer.
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.ships[tc.peer].Name = "pending rename" })
			expectDetachedAdmission(t, f, "dock #78 #77", "deferred", ModeServing)
		})
	}
}
