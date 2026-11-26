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

// Replace the leaf of a real engine's three-member star list by an ordinary
// probe design. The test governor 2 controls the session, but not ship #77.
func scrapProbeFixture(t *testing.T, ap uint8) *factoryFixture {
	t.Helper()
	f := flyingPeerFixture(t)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		sh, st := f.state.ships[77], f.state.stars[f.star]
		next := sh.NextShip
		f.state.getshipDefaults(sh, model.OtypeProbe, f.state.races[0])
		sh.Number, sh.NextShip, sh.Owner, sh.Governor = 77, next, 1, 1
		sh.WhatOrbits, sh.StOrbits = model.LevelStar, f.star
		sh.Alive, sh.Notified = true, true
		sh.WhatDest, sh.DestShipNo = model.LevelShip, 78 // navigation, not a dock
		sh.Fuel = 7.125
		st.AP[0] = ap
		if err := f.store.PutShip(sh); err != nil {
			t.Fatal(err)
		}
		if err := f.store.PutStar(st); err != nil {
			t.Fatal(err)
		}
	})
	return f
}

func scrapStar(t *testing.T, f *factoryFixture) *model.Star {
	t.Helper()
	st, err := f.store.GetStar(f.star)
	if err != nil || st == nil {
		t.Fatal("missing fixture star", err)
	}
	return st
}

// Compute the expected complete database independently of the production planner.
func expectedScrap(t *testing.T, f *factoryFixture, records map[string]string) (*model.Ship, *model.Star) {
	t.Helper()
	sh, st := landingShip(t, f, 77), scrapStar(t, f)
	sh.Alive, sh.Notified, sh.WhoKilled = false, false, 1
	st.AP[0]--
	records[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(77))] = factoryBuildJSON(t, sh)
	records[fmt.Sprintf("%x/%x", db.BucketStars, db.StarKey(st.Number))] = factoryBuildJSON(t, st)
	return sh, st
}

func TestCR17ProbeScrapDispatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ap  uint8
		cmd string
	}{{0, "scrap #77"}, {1, "scrap #77"}, {5, "scrap 77"}, {255, "scrap #+077 ignored"}} {
		t.Run(fmt.Sprint(tc.ap), func(t *testing.T) {
			f := scrapProbeFixture(t, tc.ap)
			reg := &fakeRegistry{}
			var originalShip *model.Ship
			var originalStar *model.Star
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.SetRegistry(reg)
				originalShip = f.state.ships[77]
				originalStar = f.state.stars[f.star]
			})
			want := factoryBuildRecords(t, f)
			var sh *model.Ship
			var st *model.Star
			if tc.ap != 0 {
				sh, st = expectedScrap(t, f, want)
			}
			writes := 0
			f.store.SetMetaFault(func(op string) error {
				if strings.HasPrefix(op, "probe-scrap.write:") {
					writes++
				}
				return nil
			})
			r := &probeLaunchResponder{at: func(msg string) {
				if writes != 2 || originalShip != f.state.ships[77] || originalStar != f.state.stars[f.star] || !reflect.DeepEqual(originalShip, sh) || !reflect.DeepEqual(originalStar, st) || !reflect.DeepEqual(landingShip(t, f, 77), sh) || !reflect.DeepEqual(scrapStar(t, f), st) {
					t.Errorf("incomplete probe/AP at notice %q", msg)
				}
			}}
			if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 2, tc.cmd, r); err != nil {
				t.Fatal(err)
			}
			f.store.SetMetaFault(nil)
			if tc.ap == 0 {
				if writes != 0 || r.text() != "" || reg.all() != "gov:1/2:You don't have 1 action points there.\n" {
					t.Fatal("wrong AP refusal", writes, r.text(), reg.all())
				}
			} else {
				if writes != 2 || r.text() != ":77 is not landed or docked.\nNo resources can be reclaimed.\n\nDestroyed.\n" || reg.all() != "" {
					t.Fatal("wrong success", writes, r.text(), reg.all())
				}
				if out := f.command(t, "scrap #77"); out != "" {
					t.Fatal("duplicate scrap", out)
				}
			}
			if f.state.Mode() != ModeServing || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("wrong durable result")
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		})
	}
}

func TestCR17ProbeScrapAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reply string
		mode        Mode
	}{
		{"pending", "deferred", ModeServing}, {"historic hold", "unsettled", ModeServing}, {"cycle", "halted", ModeHalted},
		{"interior tail", "unsettled", ModeServing}, {"wrong physical root", "halted", ModeHalted}, {"broken stored tail", "halted", ModeHalted}, {"revoked", "not authorized", ModeServing}, {"wide alias", "Invalid ship number", ModeServing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := scrapProbeFixture(t, 10)
			g, cmd := 0, "scrap #77"
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				a, b, c := f.state.ships[77], f.state.ships[78], f.state.ships[79]
				switch tc.name {
				case "pending":
					f.state.stars[f.star].AP[0]--
					return // legacy AP in actor only
				case "historic hold":
					b.NextShip = 0
					c.Ships = a.Number
				case "interior tail":
					b.NextShip = 0
					a.NextShip = b.Number // bypasses the real head
				case "wrong physical root":
					b.NextShip = 0
					a.NextShip = c.Number
					pl := f.state.planet(int(f.star), int(f.planet))
					pl.Ships = a.Number
					if e := f.store.PutPlanet(pl); e != nil {
						t.Fatal(e)
					}
				case "cycle":
					a.NextShip = c.Number
				case "broken stored tail":
					row := *b
					row.NextShip = 9999
					if err := f.store.PutShip(&row); err != nil {
						t.Fatal(err)
					}
					return
				case "revoked":
					g = 3
				case "wide alias":
					cmd = "scrap #4294967373"
				}
				for _, sh := range []*model.Ship{a, b, c} {
					if err := f.store.PutShip(sh); err != nil {
						t.Fatal(err)
					}
				}
			})
			before, actor := factoryBuildRecords(t, f), actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			r := &recordingResponder{}
			if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, cmd, r); err != nil {
				t.Fatal(err)
			}
			if f.state.Mode() != tc.mode || !strings.Contains(r.text(), tc.reply) || strings.Contains(r.text(), "reclaimed") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
				t.Fatal("wrong admission", f.state.Mode(), r.text())
			}
			if tc.name == "pending" {
				scheduledBuildTurnsAndRestart(t, f, func() { assertActorStoreAgreement(t, f.actorEnrollmentFixture) })
				if out := f.command(t, cmd); !strings.Contains(out, "Destroyed.") {
					t.Fatal("manual retry failed", out)
				}
			}
		})
	}
}

func TestCR17ProbeScrapLegacyRouting(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"type", "wildcard", "government", "other ship", "child", "docked", "planet", "foreign", "dead", "crewed design"} {
		t.Run(variant, func(t *testing.T) {
			f := scrapProbeFixture(t, 9)
			cmd := "scrap #77"
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				s := f.state
				sh, b, st := s.ships[77], s.ships[78], s.stars[f.star]
				switch variant {
				case "type", "wildcard":
					s.dirs[0][0] = model.Directory{Level: model.LevelStar, StarNum: f.star}
					cmd = "scrap :"
					if variant == "wildcard" {
						cmd = "scrap *"
					}
				case "government":
					s.races[0].GovShip = 77
				case "other ship":
					sh.Type = model.StypeCargo
				case "child":
					b.NextShip = sh.NextShip
					sh.NextShip = 0
					st.Ships = 77
					sh.Ships = 79
					s.ships[79].NextShip = 78
					for _, child := range []*model.Ship{b, s.ships[79]} {
						child.WhatOrbits = model.LevelShip
						child.Docked = true
						child.WhatDest = model.LevelShip
						child.DestShipNo = 77
					}
				case "docked":
					sh.Docked = true
					b.WhatDest, b.DestShipNo, b.Docked = model.LevelShip, 77, true
				case "planet":
					b.NextShip = sh.NextShip
					sh.WhatOrbits = model.LevelPlan
					sh.PNumOrbits = f.planet
					pl := s.planet(int(f.star), int(f.planet))
					sh.NextShip = pl.Ships
					pl.Ships = 77
					if e := f.store.PutPlanet(pl); e != nil {
						t.Fatal(e)
					}
				case "foreign":
					sh.Owner = 2
				case "dead":
					sh.Alive = false
				case "crewed design":
					sh.MaxCrew = 1
				}
				for _, row := range []*model.Ship{sh, b, s.ships[79]} {
					if e := f.store.PutShip(row); e != nil {
						t.Fatal(e)
					}
				}
				if e := f.store.PutRace(s.races[0]); e != nil {
					t.Fatal(e)
				}
				if e := f.store.PutStar(st); e != nil {
					t.Fatal(e)
				}
			})
			var legacy, atomic bool
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.testHooks.afterLegacyShipWrite = func(n uint32) { legacy = legacy || n == 77 } })
			f.store.SetMetaFault(func(op string) error {
				if op == "probe-scrap.before" {
					atomic = true
				}
				return nil
			})
			out := f.command(t, cmd)
			f.store.SetMetaFault(nil)
			expectedWrite := variant != "dead" && variant != "foreign" && variant != "crewed design"
			if atomic || legacy != expectedWrite || f.state.Mode() != ModeServing || strings.Contains(out, "Destroyed.") != expectedWrite {
				t.Fatal("changed legacy dispatch", variant, out, legacy, atomic)
			}
		})
	}
}

func TestCR17ProbeScrapScheduled(t *testing.T) {
	t.Parallel()
	f := scrapProbeFixture(t, 5)
	// A stale incoming exterior claim does not make the probe docked. Its
	// owner can release it under #96 even after the dead probe is off-list.
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		b := f.state.ships[78]
		b.Docked, b.WhatDest, b.DestShipNo = true, model.LevelShip, 77
		if err := f.store.PutShip(b); err != nil {
			t.Fatal(err)
		}
	})
	// The same database killed after commit must survive the next two
	// scheduled turns. Inspect it before the restarted actor can rebuild.
	if err := f.state.Stop(ctxT(t, 10*time.Second)); err != nil {
		t.Fatal(err)
	}
	sched, err := f.store.GetSchedule()
	if err != nil || sched == nil {
		t.Fatal(err)
	}
	wantCommitted := factoryBuildRecords(t, f)
	expectedScrap(t, f, wantCommitted)
	killConstructionAt(t, f, "probe-scrap.preinstall", sched.SavedAt)
	if !reflect.DeepEqual(wantCommitted, factoryBuildRecords(t, f)) {
		t.Fatal("wrong reopened scrap or incoming claim")
	}
	f.open(t)
	step := 0
	var updateAP uint8
	scheduledBuildTurnsAndRestart(t, f, func() {
		step++
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		st := scrapStar(t, f)
		dead := landingShip(t, f, 77)
		if dead.Alive || dead.WhoKilled != 1 || dead.NextShip != 0 {
			t.Fatal("resurrected or still linked", dead.Number)
		}
		seen := map[uint32]bool{}
		for id := st.Ships; id != 0; {
			sh := landingShip(t, f, id)
			if seen[id] || !sh.Alive || sh.WhatOrbits != model.LevelStar || sh.StOrbits != f.star {
				t.Fatal("invalid settled exterior", id)
			}
			seen[id] = true
			id = sh.NextShip
		}
		if !seen[78] || !seen[79] || seen[77] {
			t.Fatal("incorrect rebuilt membership", seen)
		}
		switch step {
		case 1:
			if st.AP[0] != 4 {
				t.Fatal("segment AP", st.AP[0])
			}
		case 2:
			updateAP = st.AP[0]
			if updateAP < 4 {
				t.Fatal("second AP debit on update", updateAP)
			}
		case 3:
			if st.AP[0] != updateAP {
				t.Fatal("restart AP", st.AP[0], updateAP)
			}
		}
		records := factoryBuildRecords(t, f)
		if out := f.command(t, "scrap #77"); out != "" || !reflect.DeepEqual(records, factoryBuildRecords(t, f)) {
			t.Fatal("duplicate on retry", out)
		}
	})
	want := factoryBuildRecords(t, f)
	undockedRecords(t, f, want, 78)
	if msg := f.command(t, "launch #78"); msg != "c78 undocked from :77.\n" || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) || f.state.Mode() != ModeServing {
		t.Fatal("stale claimant could not release its own link", msg)
	}
}

func TestCR17ProbeScrapFaults(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"probe", "sibling", "star", "AP", "caller", "write:1", "write:2", "before-commit", "commit-error", "after-commit", "lost install"} {
		t.Run(which, func(t *testing.T) {
			f := scrapProbeFixture(t, 5)
			want := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			point := "probe-scrap." + which
			fault := func() error { return errors.New("injected") }
			switch which {
			case "probe", "sibling":
				id := uint32(77)
				if which == "sibling" {
					id = 79
				}
				row := landingShip(t, f, id)
				row.Name += " external"
				want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(id))] = factoryBuildJSON(t, row)
				fault = func() error { return f.store.PutShip(row) }
				point = "probe-scrap.before"
			case "star", "AP":
				row := scrapStar(t, f)
				if which == "star" {
					row.Name += " external"
				} else {
					row.AP[0]--
				}
				want[fmt.Sprintf("%x/%x", db.BucketStars, db.StarKey(f.star))] = factoryBuildJSON(t, row)
				fault = func() error { return f.store.PutStar(row) }
				point = "probe-scrap.before"
			case "caller":
				row, e := f.store.GetRace(1)
				if e != nil {
					t.Fatal(e)
				}
				row.Governor[0].Active = false
				want[fmt.Sprintf("%x/%x", db.BucketRaces, db.RaceKey(1))] = factoryBuildJSON(t, row)
				fault = func() error { return f.store.PutRace(row) }
				point = "probe-scrap.before"
			case "commit-error", "after-commit", "lost install":
				expectedScrap(t, f, want)
			}
			hit := false
			if which == "lost install" {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.testHooks.beforeCommandInstall = func() { hit = true; st := *f.state.stars[f.star]; f.state.stars[f.star] = &st }
				})
			}
			f.store.SetMetaFault(func(op string) error {
				if op == point {
					hit = true
					return fault()
				}
				return nil
			})
			out := f.command(t, "scrap #77")
			f.store.SetMetaFault(nil)
			if !hit || f.state.Mode() != ModeHalted || strings.Contains(out, "reclaimed") || strings.Contains(out, "Destroyed.") || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) || f.store.CommandWriteUncertain() != (which == "commit-error") {
				t.Fatal("wrong abort or commit outcome", which, out)
			}
			if which == "after-commit" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("installed unconfirmed state")
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("stale shutdown flush", e)
			}
		})
	}
}

func TestCR17ProbeScrapCrash(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"before", "validated", "write:1", "write:2", "before-commit", "after-commit", "preinstall", "installed"} {
		t.Run(stage, func(t *testing.T) {
			f := scrapProbeFixture(t, 5)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
				t.Fatal(e)
			}
			sched, e := f.store.GetSchedule()
			if e != nil || sched == nil {
				t.Fatal(e)
			}
			want := factoryBuildRecords(t, f)
			if stage == "after-commit" || stage == "preinstall" || stage == "installed" {
				expectedScrap(t, f, want)
			}
			killConstructionAt(t, f, "probe-scrap."+stage, sched.SavedAt)
			if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("partial or wrong scrap immediately on reopen")
			}
		})
	}
}

func TestCR17ProbeScrapStoreContract(t *testing.T) {
	t.Parallel()
	f := scrapProbeFixture(t, 1)
	want := factoryBuildRecords(t, f)
	for _, variant := range []string{"AP", "docked", "child", "crewed", "troops", "passenger", "other type", "govship", "foreign", "dead", "authority", "outside", "truncated"} {
		t.Run(variant, func(t *testing.T) {
			st := scrapStar(t, f)
			r, e := f.store.GetRace(1)
			if e != nil {
				t.Fatal(e)
			}
			in := commandrules.ProbeScrap{Player: 1, Selected: 77, Star: st, Race: r}
			var probe *model.Ship
			for id := st.Ships; id != 0; {
				sh := landingShip(t, f, id)
				in.Orbit = append(in.Orbit, sh)
				if id == 77 {
					probe = sh
				}
				id = sh.NextShip
			}
			switch variant {
			case "AP":
				st.AP[0] = 0
			case "docked":
				probe.Docked = true
			case "child":
				probe.Ships = 999
			case "crewed":
				probe.MaxCrew = 1
			case "troops":
				probe.Troops = 1
			case "passenger":
				probe.Popn = 1
			case "other type":
				probe.Type = model.StypeCargo
			case "govship":
				r.GovShip = 77
			case "foreign":
				probe.Owner = 2
			case "dead":
				probe.Alive = false
			case "authority":
				r.Dissolved = true
			case "outside":
				in.Selected = 100
			case "truncated":
				in.Orbit = in.Orbit[:2]
			}
			out, e := f.store.CommitProbeScrap(in)
			var ce *db.CommandError
			if out != db.CommandNotCommitted || !errors.As(e, &ce) || ce.Code != db.CommandInvalid || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("illegal proposal admitted", variant, out, e)
			}
		})
	}
}

// Real legacy construction puts the probes but leaves the star head pending.
// Drop only this fixture's actor without flushing, as at process loss, then
// cold-open its saved records. Test both members of the omitted prefix.
func detachedBuildFixture(t *testing.T, setup string) *factoryFixture {
	t.Helper()
	f := newCargoBuildFixture(t, false, model.LevelStar)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		r, st := f.state.races[0], f.state.stars[f.star]
		r.God = true // allows exterior probe construction by a cargo ship
		st.AP[0] = 5
		if setup == "carrier launch" {
			// A settled star-orbit Carrier with one probe available to launch.
			// Both numbers precede the builder so normal allocation still starts at 78.
			carrier, child := &model.Ship{}, &model.Ship{}
			f.state.getshipDefaults(carrier, model.StypeCarrier, r)
			f.state.getshipDefaults(child, model.OtypeProbe, r)
			carrier.Number, child.Number = 75, 76
			carrier.WhatOrbits = model.LevelStar
			child.Docked, child.WhatOrbits, child.WhatDest, child.DestShipNo = true, model.LevelShip, model.LevelShip, carrier.Number
			builder := f.state.ships[77]
			carrier.NextShip, builder.NextShip = builder.NextShip, carrier.Number
			carrier.Ships, carrier.Hanger = child.Number, child.Size
			carrier.Mass += child.Mass
			for _, sh := range []*model.Ship{carrier, child} {
				sh.Alive, sh.Active, sh.Owner, sh.StOrbits = true, true, 1, f.star
				sh.XPos, sh.YPos = builder.XPos, builder.YPos
				f.state.ships[sh.Number] = sh
			}
			for _, sh := range []*model.Ship{builder, carrier, child} {
				if err := f.store.PutShip(sh); err != nil {
					t.Error(err)
				}
			}
		}
		if e := f.store.PutRace(r); e != nil {
			t.Fatal(e)
		}
		if e := f.store.PutStar(st); e != nil {
			t.Fatal(e)
		}
	})
	if setup == "mixed build" {
		f.command(t, "build c 1")
		f.command(t, "build : 1")
		if landingShip(t, f, 78).Type != model.StypeCargo {
			t.Fatal("expected a cargo ship before the probe")
		}
	} else {
		f.command(t, "build : 2")
	}
	if st := scrapStar(t, f); st.Ships != 77 || landingShip(t, f, 78).NextShip != 77 || landingShip(t, f, 79).NextShip != 78 {
		t.Fatalf("legacy build graph: root=%d, 79.next=%d, 78.next=%d", st.Ships, landingShip(t, f, 79).NextShip, landingShip(t, f, 78).NextShip)
	}
	for _, cmd := range []string{"scrap #79", "dock #79 #77"} {
		expectDetachedAdmission(t, f, cmd, "deferred", ModeServing)
	}
	reopenUnflushedBuild(t, f)
	return f
}

func reopenUnflushedBuild(t *testing.T, f *factoryFixture) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.halt("discard disposable actor without flushing", "") })
	if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) {
		t.Fatal(e)
	}
	f.open(t)
}

func expectDetachedAdmission(t *testing.T, f *factoryFixture, cmd, reply string, mode Mode) {
	t.Helper()
	before, actor := factoryBuildRecords(t, f), actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		f.state.testHooks.afterLegacyShipWrite = func(id uint32) { t.Errorf("%s wrote legacy ship %d", cmd, id) }
	})
	f.store.SetMetaFault(func(op string) error {
		if strings.HasPrefix(op, "probe-scrap.") || strings.HasPrefix(op, "friendly-dock.") {
			t.Errorf("%s entered write operation %s", cmd, op)
		}
		return nil
	})
	out := f.command(t, cmd)
	f.store.SetMetaFault(nil)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.testHooks.afterLegacyShipWrite = nil })
	if f.state.Mode() != mode || !strings.Contains(out, reply) || strings.Contains(out, "Destroyed.") || strings.Contains(out, "docked with") ||
		!reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatalf("%s: got %q in %v, want %q in %v with unchanged actor and store", cmd, out, f.state.Mode(), reply, mode)
	}
}

func TestCR17ProbeScrapLegacyBuild(t *testing.T) {
	t.Parallel()
	for _, prepend := range []string{"none", "legacy build", "carrier launch"} {
		t.Run(prepend, func(t *testing.T) {
			f := detachedBuildFixture(t, prepend)
			reply := "unsettled"
			switch prepend {
			case "legacy build":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.dirs[0][0] = model.Directory{Level: model.LevelShip, ShipNo: 77, StarNum: f.star, PlanetNum: f.planet}
				})
				f.command(t, "build : 1")
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					if f.state.stars[f.star].Ships != 80 || scrapStar(t, f).Ships != 77 || landingShip(t, f, 80).NextShip != 77 {
						t.Error("missing actor-only prepend")
					}
				})
				reply = "deferred"
			case "carrier launch":
				out := f.command(t, "launch #76")
				if !strings.Contains(out, "launched from") || scrapStar(t, f).Ships != 76 || landingShip(t, f, 76).NextShip != 77 {
					t.Fatal("missing durable prepend", out)
				}
			}
			for _, cmd := range []string{"scrap #79", "scrap #78", "dock #79 #78", "dock #77 #79", "dock #79 #77"} {
				expectDetachedAdmission(t, f, cmd, reply, ModeServing)
			}
			scheduledBuildTurnsAndRestart(t, f, func() { assertActorStoreAgreement(t, f.actorEnrollmentFixture) })
			if out := f.command(t, "dock #79 #78"); !strings.Contains(out, "docked with") {
				t.Fatal("dock after rebuild", out)
			}
			if out := f.command(t, "launch #78"); !strings.Contains(out, "undocked from") {
				t.Fatal("undock after rebuild", out)
			}
			ap := scrapStar(t, f).AP[0]
			if out := f.command(t, "scrap #79"); !strings.Contains(out, "Destroyed.") || landingShip(t, f, 79).Alive || !landingShip(t, f, 78).Alive || scrapStar(t, f).AP[0] != ap-1 {
				t.Fatal("scrap after rebuild", out)
			}
		})
	}
}

func TestCR17ProbeScrapDetachedTopology(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"zero tail", "cycle", "dangling", "wrong star", "invalid identity", "docked interior", "planet overlap", "hold overlap", "dock source overlap", "dock target overlap", "stored dangling"} {
		t.Run(kind, func(t *testing.T) {
			f := detachedBuildFixture(t, "")
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				b := f.state.ships[78] // the interior detached member of 79 -> 78 -> 77
				switch kind {
				case "zero tail":
					b.NextShip = 0
				case "cycle":
					b.NextShip = 79
				case "dangling":
					b.NextShip = 999
				case "wrong star":
					b.StOrbits ^= 1
				case "invalid identity":
					b.Number = 999 // contradicts actor lookup key
					return
				case "docked interior":
					b.Docked = true
				case "planet overlap":
					pl := f.state.planets[f.star][f.planet]
					pl.Ships = 78
					if err := f.store.PutPlanet(pl); err != nil {
						t.Error(err)
					}
				case "hold overlap", "dock source overlap", "dock target overlap":
					// Even a disjoint exterior suffix (zero) cannot explain an
					// interior probe that already belongs to the builder's hold.
					b.NextShip = 0
					builder := f.state.ships[77]
					builder.Ships = b.Number
					if err := f.store.PutShip(builder); err != nil {
						t.Error(err)
					}
				case "stored dangling":
					row := *b
					row.NextShip = 999
					if err := f.store.PutShip(&row); err != nil {
						t.Error(err)
					}
					return
				}
				if err := f.store.PutShip(b); err != nil {
					t.Error(err)
				}
			})
			switch kind {
			case "zero tail":
				expectDetachedAdmission(t, f, "scrap #79", "unsettled", ModeServing)
				expectDetachedAdmission(t, f, "dock #77 #79", "unsettled", ModeServing)
			case "dock source overlap":
				expectDetachedAdmission(t, f, "dock #79 #77", "halted", ModeHalted)
			case "dock target overlap":
				expectDetachedAdmission(t, f, "dock #77 #79", "halted", ModeHalted)
			default:
				expectDetachedAdmission(t, f, "scrap #79", "halted", ModeHalted)
			}
		})
	}
}

// An earlier legal exterior build can be a different design. This prefix was
// already refused safely by scrap before support for prepends and docking.
func TestCR17ProbeScrapMixedBuildPrefix(t *testing.T) {
	t.Parallel()
	f := detachedBuildFixture(t, "mixed build")
	expectDetachedAdmission(t, f, "scrap #79", "unsettled", ModeServing)
	for _, cmd := range []string{"dock #77 #79", "dock #78 #79", "dock #79 #78"} {
		expectDetachedAdmission(t, f, cmd, "unsettled", ModeServing)
	}
}

func TestCR17ProbeScrapDockedBuildSibling(t *testing.T) {
	t.Parallel()
	f := detachedBuildFixture(t, "carrier launch")
	dockReopenedBuild(t, f)
	// The selected probe is undocked; its detached sibling is a peer of #77.
	expectDetachedAdmission(t, f, "scrap #79", "unsettled", ModeServing)
	// Legacy type selection can overwrite the Cargo's claim. The sibling's
	// outgoing claim remains meaningful even after the Cargo is released.
	if out := f.command(t, "dock X #77"); !strings.Contains(out, "docked with") ||
		landingShip(t, f, 77).DestShipNo != 75 || landingShip(t, f, 78).DestShipNo != 77 {
		t.Fatal("legacy overwrite", out)
	}
	expectDetachedAdmission(t, f, "scrap #79", "unsettled", ModeServing)
	if out := f.command(t, "launch X"); !strings.Contains(out, "undocked from") ||
		landingShip(t, f, 77).Docked || !landingShip(t, f, 78).Docked {
		t.Fatal("legacy release left an unexpected graph", out)
	}
	expectDetachedAdmission(t, f, "scrap #79", "unsettled", ModeServing)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.ships[78].Name = "pending rename" })
	expectDetachedAdmission(t, f, "scrap #79", "deferred", ModeServing)
	scheduledBuildTurnsAndRestart(t, f, func() { assertActorStoreAgreement(t, f.actorEnrollmentFixture) })
	ap := scrapStar(t, f).AP[0]
	if out := f.command(t, "scrap #79"); !strings.Contains(out, "Destroyed.") || ap == 0 ||
		scrapStar(t, f).AP[0] != ap-1 || landingShip(t, f, 79).Alive || !landingShip(t, f, 78).Alive {
		t.Fatal("manual scrap after repair", out)
	}
}
