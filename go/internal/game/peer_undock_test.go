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

// Three living ships in the same star exterior.
func flyingPeerFixture(t *testing.T) *factoryFixture {
	t.Helper()
	f := newCargoBuildFixture(t, false, model.LevelStar)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		st, r := f.state.stars[f.star], f.state.races[0]
		a := f.state.ships[77]
		a.Docked = false
		a.WhatDest = model.LevelUniv
		a.DestShipNo = 0
		a.Governor = 1
		r.Governors = 2
		r.Governor[1].Active = true
		r.Governor[2].Active = true
		st.AP[0] = 0
		for _, id := range []uint32{78, 79} {
			sh := factoryCopyShip(t, a)
			sh.Number = id
			sh.Governor = 2
			sh.NextShip = st.Ships
			st.Ships = id
			f.state.ships[id] = sh
			if err := f.store.PutShip(sh); err != nil {
				t.Fatal(err)
			}
		}
		for _, err := range []error{f.store.PutShip(a), f.store.PutStar(st), f.store.PutRace(r)} {
			if err != nil {
				t.Fatal(err)
			}
		}
	})
	return f
}

func peerUndockFixture(t *testing.T) *factoryFixture {
	t.Helper()
	f := flyingPeerFixture(t)
	if out := f.command(t, "dock #77 #78"); !strings.Contains(out, "docked with") {
		t.Fatal(out)
	}
	return f
}

// Independently change only the three dock fields in each expected writable row.
func undockedRecords(t *testing.T, f *factoryFixture, want map[string]string, ids ...uint32) map[uint32]*model.Ship {
	t.Helper()
	rows := map[uint32]*model.Ship{}
	for _, id := range ids {
		sh := landingShip(t, f, id)
		sh.Docked = false
		sh.WhatDest = model.LevelUniv
		sh.DestShipNo = 0
		want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(id))] = factoryBuildJSON(t, sh)
		rows[id] = sh
	}
	return rows
}

func TestCR17PeerUndockDispatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		spec           string
		gov, id, other int
	}{{"#77", 1, 77, 78}, {"78", 2, 78, 77}, {"#77", 0, 77, 78}} {
		t.Run(fmt.Sprintf("%s/g%d", tc.spec, tc.gov), func(t *testing.T) {
			f := peerUndockFixture(t)
			want := factoryBuildRecords(t, f)
			ships := undockedRecords(t, f, want, 77, 78)
			ptrs := map[uint32]*model.Ship{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				for id := range ships {
					ptrs[id] = f.state.ships[id]
				}
			})
			calls := 0
			r := &probeLaunchResponder{at: func(msg string) {
				calls++
				for id, sh := range ships {
					if !reflect.DeepEqual(landingShip(t, f, id), sh) || f.state.ships[id] != ptrs[id] || !reflect.DeepEqual(ptrs[id], sh) {
						t.Errorf("peer %d not durable and installed at notice %q", id, msg)
					}
				}
			}}
			if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, tc.gov, "launch "+tc.spec, r); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || r.text() != fmt.Sprintf("c%d undocked from c%d.\n", tc.id, tc.other) || f.state.Mode() != ModeServing || !reflect.DeepEqual(factoryBuildRecords(t, f), want) {
				t.Fatal("incorrect complete undock", r.text())
			}
		})
	}
}

func TestCR17PeerUndockAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		mode       Mode
	}{
		{"wrong governor", "not authorized", ModeServing}, {"inactive", "not authorized", ModeServing}, {"dissolved", "not authorized", ModeServing},
		{"pending star", "deferred", ModeServing}, {"pending sibling", "deferred", ModeServing}, {"stored-only tail", "deferred", ModeServing},
		{"historical star hold", "unsettled", ModeServing}, {"overlap", "halted", ModeHalted}, {"broken hold", "halted", ModeHalted}, {"stored-only broken tail", "halted", ModeHalted},
		{"cycle", "halted", ModeHalted}, {"coherent detached member", "unsettled", ModeServing}, {"missing peer row", "halted", ModeHalted}, {"wrong orbit", "halted", ModeHalted}, {"wrong partner star", "halted", ModeHalted},
		{"stored exterior overlap", "halted", ModeHalted}, {"stored cycle", "halted", ModeHalted}, {"stored dangling", "halted", ModeHalted}, {"wide selector", "", ModeServing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := peerUndockFixture(t)
			g, cmd := 0, "launch #77"
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				a, b, c, st, r := f.state.ships[77], f.state.ships[78], f.state.ships[79], f.state.stars[f.star], f.state.races[0]
				switch tc.name {
				case "wrong governor":
					g = 2
				case "inactive":
					g = 1
					r.Governor[1].Active = false
				case "dissolved":
					r.Dissolved = true
				case "pending star":
					st.AP[0]++
					return
				case "pending sibling":
					c.Name = "pending"
					return
				case "stored-only tail":
					st.Ships = b.Number
					return
				case "historical star hold", "broken hold":
					b.NextShip = a.NextShip
					c.Ships = a.Number
					a.NextShip = 0
					if tc.name == "broken hold" {
						a.NextShip = a.Number
					}
				case "overlap":
					c.Ships = a.Number
				case "stored-only broken tail":
					st.Ships = b.Number
					stored := *c
					stored.NextShip = c.Number
					if e := f.store.PutShip(&stored); e != nil {
						t.Fatal(e)
					}
					return
				case "cycle":
					a.NextShip = c.Number
				case "wrong partner star":
					b.StOrbits++
				case "wrong orbit":
					c.WhatOrbits = model.LevelPlan
				case "coherent detached member":
					c.NextShip = a.Number
				case "missing peer row":
					a.DestShipNo = 1234567
				case "stored exterior overlap":
					stored := *c
					stored.Ships = b.Number
					if e := f.store.PutShip(&stored); e != nil {
						t.Fatal(e)
					}
					return
				case "stored cycle", "stored dangling":
					stored := *a
					stored.NextShip = c.Number
					if tc.name == "stored dangling" {
						stored.NextShip = 1234567
					}
					if e := f.store.PutShip(&stored); e != nil {
						t.Fatal(e)
					}
					return
				case "wide selector":
					cmd = "launch #4294967373"
				}
				for _, e := range []error{f.store.PutShip(a), f.store.PutShip(b), f.store.PutShip(c), f.store.PutStar(st), f.store.PutRace(r)} {
					if e != nil {
						t.Fatal(e)
					}
				}
			})
			want := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			reply := &recordingResponder{}
			entered := false
			f.store.SetMetaFault(func(op string) error {
				if op == "peer-undock.before" {
					entered = true
				}
				return nil
			})
			if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, cmd, reply); err != nil {
				t.Fatal(err)
			}
			f.store.SetMetaFault(nil)
			if entered || !strings.Contains(reply.text(), tc.want) || strings.Contains(reply.text(), "undocked from") || f.state.Mode() != tc.mode || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("admission changed state or gave wrong disposition", reply.text(), f.state.Mode())
			}
			if tc.name == "pending star" {
				scheduledBuildTurnsAndRestart(t, f, func() { assertActorStoreAgreement(t, f.actorEnrollmentFixture) })
				if msg := f.command(t, cmd); !strings.Contains(msg, "undocked from") {
					t.Fatal("manual retry", msg)
				}
			}
		})
	}
}

func TestCR17PeerUndockConflicts(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"selected", "partner", "sibling", "star", "race", "write:1", "write:2", "before-commit", "commit-error", "after-commit"} {
		t.Run(stage, func(t *testing.T) {
			f := peerUndockFixture(t)
			want := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			point := "peer-undock.before"
			var fault func() error
			switch stage {
			case "selected", "partner", "sibling":
				id := map[string]uint32{"selected": 77, "partner": 78, "sibling": 79}[stage]
				sh := landingShip(t, f, id)
				sh.Name += " (late)"
				want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(id))] = factoryBuildJSON(t, sh)
				fault = func() error { return f.store.PutShip(sh) }
			case "star":
				st, e := f.store.GetStar(f.star)
				if e != nil {
					t.Fatal(e)
				}
				st.AP[0]++
				want[fmt.Sprintf("%x/%x", db.BucketStars, db.StarKey(f.star))] = factoryBuildJSON(t, st)
				fault = func() error { return f.store.PutStar(st) }
			case "race":
				r, e := f.store.GetRace(1)
				if e != nil {
					t.Fatal(e)
				}
				r.Governor[0].Active = false
				want[fmt.Sprintf("%x/%x", db.BucketRaces, db.RaceKey(1))] = factoryBuildJSON(t, r)
				fault = func() error { return f.store.PutRace(r) }
			default:
				point = "peer-undock." + stage
				fault = func() error { return errors.New("injected undock failure") }
				if stage == "after-commit" || stage == "commit-error" {
					undockedRecords(t, f, want, 77, 78)
				}
			}
			hit := false
			f.store.SetMetaFault(func(op string) error {
				if op == point {
					hit = true
					return fault()
				}
				return nil
			})
			msg := f.command(t, "launch #77")
			f.store.SetMetaFault(nil)
			if !hit || !strings.Contains(msg, "halted") || strings.Contains(msg, "undocked from") || f.state.Mode() != ModeHalted || f.store.CommandWriteUncertain() != (stage == "commit-error") {
				t.Fatalf("%s: failed to halt without success: %q", stage, msg)
			}
			if stage == "after-commit" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("unconfirmed proposal installed")
			}
			if err := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("partial write or stale shutdown flush", err)
			}
		})
	}
}

func TestCR17PeerUndockCrash(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"before", "validated", "write:1", "write:2", "before-commit", "after-commit", "preinstall", "installed"} {
		t.Run(stage, func(t *testing.T) {
			f := peerUndockFixture(t)
			checkPeerUndockKill(t, f, stage, 77, 78)
		})
	}
}

func checkPeerUndockKill(t *testing.T, f *factoryFixture, stage string, changed ...uint32) {
	t.Helper()
	if err := f.state.Stop(ctxT(t, 10*time.Second)); err != nil {
		t.Fatal(err)
	}
	sched, err := f.store.GetSchedule()
	if err != nil || sched == nil {
		t.Fatal(err)
	}
	want := factoryBuildRecords(t, f)
	if stage == "after-commit" || stage == "preinstall" || stage == "installed" {
		undockedRecords(t, f, want, changed...)
	}
	killConstructionAt(t, f, "peer-undock."+stage, sched.SavedAt)
	if !reflect.DeepEqual(factoryBuildRecords(t, f), want) {
		t.Fatal("reopened world contains partial or wrong undock")
	}
}

func TestCR17PeerUndockScheduled(t *testing.T) {
	t.Parallel()
	f := peerUndockFixture(t)
	if msg := f.command(t, "launch #77"); msg != "c77 undocked from c78.\n" {
		t.Fatal(msg)
	}
	check := func() {
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		st, e := f.store.GetStar(f.star)
		if e != nil {
			t.Fatal(e)
		}
		seen := map[uint32]bool{}
		for n := st.Ships; n != 0; {
			sh := landingShip(t, f, n)
			if seen[n] || sh.WhatOrbits != model.LevelStar || sh.StOrbits != st.Number {
				t.Fatalf("invalid exterior at #%d", n)
			}
			seen[n] = true
			if n == 77 || n == 78 {
				if !sh.Alive || sh.Docked || sh.WhatDest != model.LevelUniv || sh.DestShipNo != 0 {
					t.Fatalf("turn reverted peer #%d", n)
				}
			}
			n = sh.NextShip
		}
		if !seen[77] || !seen[78] || !seen[79] {
			t.Fatal("star lost exterior member", seen)
		}
	}
	check()
	scheduledBuildTurnsAndRestart(t, f, check)
}

// Exercise the store's own derivation independently of actor-side classification.
func TestCR17PeerUndockStorageContract(t *testing.T) {
	f := peerUndockFixture(t)
	st, err := f.store.GetStar(f.star)
	if err != nil {
		t.Fatal(err)
	}
	race, err := f.store.GetRace(1)
	if err != nil {
		t.Fatal(err)
	}
	orbit := []*model.Ship{}
	for n := st.Ships; n != 0; {
		sh := landingShip(t, f, n)
		orbit = append(orbit, sh)
		n = sh.NextShip
	}
	want := factoryBuildRecords(t, f)
	for _, bad := range []string{"inactive", "wrong governor", "truncated", "wrong star", "foreign", "absent", "extraneous wreck", "wrong wreck identity", "linked wreck", "occupied wreck", "held wreck", "live detached target"} {
		t.Run(bad, func(t *testing.T) {
			in := commandrules.StarPeerUndock{Player: 1, Governor: 1, Selected: 77, Race: race, Star: st}
			for _, sh := range orbit {
				in.Orbit = append(in.Orbit, factoryCopyShip(t, sh))
			}
			switch bad {
			case "inactive":
				in.Governor = 3
			case "wrong governor":
				in.Governor = 2
			case "truncated":
				in.Orbit = in.Orbit[:1]
			case "wrong star":
				in.Orbit[0].StOrbits++
			case "foreign":
				in.Orbit[1].Owner = 2
			case "absent":
				in.Selected = 1234567
			case "extraneous wreck":
				in.DetachedWreck = in.Orbit[1]
			case "wrong wreck identity", "linked wreck", "occupied wreck", "held wreck", "live detached target":
				wreck := *in.Orbit[1]
				wreck.Alive, wreck.NextShip, wreck.Ships = false, 0, 0
				in.Orbit[0].NextShip = in.Selected
				in.Orbit = append(in.Orbit[:1], in.Orbit[2:]...)
				in.DetachedWreck = &wreck
				switch bad {
				case "wrong wreck identity":
					wreck.Number++
				case "linked wreck":
					wreck.NextShip = in.Selected
				case "held wreck":
					in.Orbit[0].Ships = wreck.Number
				case "occupied wreck":
					wreck.Ships = in.Selected
				case "live detached target":
					wreck.Alive = true
				}
			}
			outcome, e := f.store.CommitStarPeerUndock(in)
			var ce *db.CommandError
			if outcome != db.CommandNotCommitted || !errors.As(e, &ce) || ce.Code != db.CommandInvalid || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("invalid proposal reached storage CAS or writes", outcome, e)
			}
		})
	}
}

func TestCR17PeerUndockLegacyRouting(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"foreign", "different stars", "universe", "planet", "wildcard"} {
		t.Run(variant, func(t *testing.T) {
			f := peerUndockFixture(t)
			cmd := "launch #77"
			var writes []uint32
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				a, b, c := f.state.ships[77], f.state.ships[78], f.state.ships[79]
				st := f.state.stars[f.star]
				switch variant {
				case "foreign":
					b.Owner = 2
				case "different stars":
					n := f.star ^ 1
					dst := f.state.stars[n]
					if dst == nil {
						dst = &model.Star{Number: n}
						f.state.stars[n] = dst
					}
					c.NextShip = b.NextShip
					b.StOrbits, b.NextShip, dst.Ships = n, dst.Ships, b.Number
					if err := f.store.PutStar(dst); err != nil {
						t.Fatal(err)
					}
				case "universe", "planet":
					st.Ships = c.Number
					c.NextShip = 0
					if variant == "universe" {
						a.WhatOrbits, b.WhatOrbits = model.LevelUniv, model.LevelUniv
						f.state.sdata.Ships = b.Number
					} else {
						pl := f.state.planet(int(f.star), int(f.planet))
						a.WhatOrbits, b.WhatOrbits = model.LevelPlan, model.LevelPlan
						pl.Ships, st.AP[0] = b.Number, 10
						if err := f.store.PutPlanet(pl); err != nil {
							t.Fatal(err)
						}
					}
				case "wildcard":
					f.state.dirs[0][0].Level, f.state.dirs[0][0].StarNum = model.LevelStar, f.star
					cmd = "launch *"
				}
				for _, err := range []error{f.store.PutStar(st), f.store.PutShip(a), f.store.PutShip(b), f.store.PutShip(c)} {
					if err != nil {
						t.Fatal(err)
					}
				}
				f.state.testHooks.afterLegacyShipWrite = func(n uint32) { writes = append(writes, n) }
			})
			msg := f.command(t, cmd)
			if len(writes) != 2 || writes[0] == writes[1] || !strings.Contains(msg, " undocked from ") || f.state.Mode() != ModeServing {
				t.Fatalf("legacy route %v: %q", writes, msg)
			}
		})
	}
}

func TestCR17PeerUndockLostInstallation(t *testing.T) {
	t.Parallel()
	f := peerUndockFixture(t)
	want := factoryBuildRecords(t, f)
	undockedRecords(t, f, want, 77, 78)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		f.state.testHooks.beforeCommandInstall = func() {
			other := *f.state.ships[78]
			f.state.ships[78] = &other
		}
	})
	msg := f.command(t, "launch #77")
	if strings.Contains(msg, "undocked from") || !strings.Contains(msg, "halted") || f.state.Mode() != ModeHalted {
		t.Fatal("lost workspace was acknowledged", msg)
	}
	if err := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
		t.Fatal("confirmed commit overwritten", err)
	}
}
