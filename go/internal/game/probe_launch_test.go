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

// Both forms start from actual build dispatch. A second build places a real
// sibling (#79) before #78, making its NextShip a fourth writable record.
func probeLaunchFixture(t *testing.T, sibling, fueled bool) *factoryFixture {
	t.Helper()
	f := newProbeFixture(t, false)
	if fueled {
		enableProbeAutoload(t, f, 7.125)
	}
	f.command(t, "build :")
	if sibling {
		f.command(t, "build :")
	}
	return f
}

type launchWorld struct {
	planet *model.Planet
	ships  map[uint32]*model.Ship
}

func launchExpected(t *testing.T, f *factoryFixture, nonHead bool) launchWorld {
	t.Helper()
	pl, err := f.store.GetPlanet(f.star, f.planet)
	if err != nil || pl == nil {
		t.Fatal(err)
	}
	want := launchWorld{pl, map[uint32]*model.Ship{}}
	for _, id := range []uint32{77, 78, 79} {
		if id == 79 && !nonHead {
			continue
		}
		sh, e := f.store.GetShip(id)
		if e != nil || sh == nil {
			t.Fatalf("fixture missing #%d: %v", id, e)
		}
		want.ships[id] = sh
	}
	b, c := want.ships[77], want.ships[78]
	if c.DestShipNo != 77 || !c.Docked || c.WhatOrbits != model.LevelShip || c.NextShip != 76 || c.Size != 1 {
		t.Fatal("invalid initial probe")
	}
	if nonHead {
		if b.Ships != 79 || want.ships[79].NextShip != 78 {
			t.Fatal("invalid sibling fixture")
		}
		want.ships[79].NextShip = 76
	} else {
		if b.Ships != 78 {
			t.Fatal("invalid head fixture")
		}
		b.Ships = 76
	}
	b.Hanger--
	b.Mass -= c.Mass
	c.XPos, c.YPos = b.XPos, b.YPos
	c.WhatOrbits, c.WhatDest, c.Docked = model.LevelPlan, model.LevelUniv, false
	c.StOrbits, c.PNumOrbits = b.StOrbits, b.PNumOrbits
	c.NextShip, pl.Ships = pl.Ships, 78
	return want
}
func (w launchWorld) updateRecords(t *testing.T, records map[string]string) {
	t.Helper()
	for id, sh := range w.ships {
		records[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(id))] = factoryBuildJSON(t, sh)
	}
	records[fmt.Sprintf("%x/%x", db.BucketPlanets, db.PlanetKey(w.planet.StarNum, w.planet.PlanetNum))] = factoryBuildJSON(t, w.planet)
}

type probeLaunchResponder struct {
	recordingResponder
	at func(string)
}

func (r *probeLaunchResponder) Send(msg string) error {
	r.at(msg)
	return r.recordingResponder.Send(msg)
}

func TestCR17ProbeLaunchDispatch(t *testing.T) {
	t.Parallel()
	for _, sibling := range []bool{false, true} {
		for _, fueled := range []bool{false, true} {
			t.Run(fmt.Sprintf("sibling=%v/fueled=%v", sibling, fueled), func(t *testing.T) {
				f := probeLaunchFixture(t, sibling, fueled)
				// Control of the selected probe suffices, regardless of Carrier owner.
				g := 0
				if sibling {
					g = 1
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
						b := f.state.ships[77]
						b.Owner = 2
						probe, race := f.state.ships[78], f.state.races[0]
						probe.Governor = 1
						race.Governor[1].Active = true
						for _, err := range []error{f.store.PutShip(b), f.store.PutShip(probe), f.store.PutRace(race)} {
							if err != nil {
								t.Fatal(err)
							}
						}
					})
				}
				w := launchExpected(t, f, sibling)
				want := factoryBuildRecords(t, f)
				w.updateRecords(t, want)
				var planetPtr *model.Planet
				ptrs := map[uint32]*model.Ship{}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					planetPtr = f.state.planets[f.star][f.planet]
					for id := range w.ships {
						ptrs[id] = f.state.ships[id]
					}
				})
				r := &probeLaunchResponder{at: func(msg string) {
					pl, e := f.store.GetPlanet(f.star, f.planet)
					if e != nil || planetPtr != f.state.planets[f.star][f.planet] || !reflect.DeepEqual(planetPtr, w.planet) || !reflect.DeepEqual(pl, w.planet) {
						t.Errorf("planet not installed/durable at %q", msg)
					}
					for id, sh := range w.ships {
						stored, e := f.store.GetShip(id)
						if e != nil || ptrs[id] != f.state.ships[id] || !reflect.DeepEqual(sh, ptrs[id]) || !reflect.DeepEqual(sh, stored) {
							t.Errorf("ship #%d not installed/durable at %q", id, msg)
						}
					}
				}}
				if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, "launch #78", r); err != nil {
					t.Fatal(err)
				}
				expected := fmt.Sprintf("%s launched from %s.\nOrbiting %s/%s.\n", shipDisplayName(w.ships[78]), shipDisplayName(w.ships[77]), f.state.starName(int(f.star)), f.state.planetName(int(f.star), int(f.planet)))
				if r.text() != expected || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatal("wrong launch text, accounting, or unrelated writes", r.text())
				}
			})
		}
	}
}

func TestCR17ProbeLaunchScheduled(t *testing.T) {
	t.Parallel()
	f := probeLaunchFixture(t, true, true)
	w := launchExpected(t, f, true)
	msg := f.command(t, "launch #78")
	if !strings.Contains(msg, "launched from") {
		t.Fatal(msg)
	}
	check := func() {
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		pl, err := f.store.GetPlanet(f.star, f.planet)
		if err != nil {
			t.Fatal(err)
		}
		get := func(id uint32) *model.Ship {
			sh, e := f.store.GetShip(id)
			if e != nil || sh == nil {
				t.Fatalf("missing #%d", id)
			}
			return sh
		}
		b := get(77)
		seen := map[uint32]bool{}
		for n := pl.Ships; n != 0; {
			sh := get(n)
			if seen[n] || sh.WhatOrbits != model.LevelPlan || sh.StOrbits != f.star || sh.PNumOrbits != f.planet {
				t.Fatal("invalid exterior orbit", n)
			}
			seen[n] = true
			n = sh.NextShip
		}
		if !seen[78] || !seen[77] || seen[79] || seen[76] {
			t.Fatal("incorrect exterior membership", seen)
		}
		inside := map[uint32]bool{}
		for n := b.Ships; n != 0; {
			sh := get(n)
			if seen[n] || inside[n] || sh.WhatOrbits != model.LevelShip || sh.DestShipNo != 77 {
				t.Fatal("invalid hold", n)
			}
			inside[n] = true
			n = sh.NextShip
		}
		if !inside[79] || !inside[76] || len(inside) != 2 || b.Hanger != w.ships[77].Hanger || get(78).Docked {
			t.Fatal("launch reverted", inside)
		}
	}
	check()
	scheduledBuildTurnsAndRestart(t, f, check)
}

func TestCR17ProbeLaunchRealProcessCrash(t *testing.T) {
	t.Parallel()
	for _, sibling := range []bool{false, true} {
		for _, stage := range []string{"before", "validated", "write:1", "write:2", "write:3", "write:4", "before-commit", "after-commit", "preinstall", "installed"} {
			if !sibling && stage == "write:4" {
				continue
			}
			t.Run(fmt.Sprintf("sibling=%v/%s", sibling, stage), func(t *testing.T) {
				f := probeLaunchFixture(t, sibling, true)
				if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
				sched, e := f.store.GetSchedule()
				if e != nil || sched == nil {
					t.Fatal(e)
				}
				want := factoryBuildRecords(t, f)
				next := launchExpected(t, f, sibling)
				if stage == "after-commit" || stage == "preinstall" || stage == "installed" {
					next.updateRecords(t, want)
				}
				killConstructionAt(t, f, "probe-launch."+stage, sched.SavedAt)
				if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatal("partial or incorrect launch immediately after reopen")
				}
			})
		}
	}
}

func TestCR17ProbeLaunchConflicts(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"sibling", "exterior", "star AP", "caller governor", "planet", "write:2", "commit-error", "after-commit"} {
		t.Run(stage, func(t *testing.T) {
			f := probeLaunchFixture(t, true, true)
			want := factoryBuildRecords(t, f)
			next := launchExpected(t, f, true)
			if stage == "commit-error" || stage == "after-commit" {
				next.updateRecords(t, want)
			}
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			point := "probe-launch." + stage
			fault := func() error { return errors.New("injected launch failure") }
			switch stage {
			case "sibling", "exterior":
				// #76 is a read-only tail, beyond both selected probe and predecessor.
				id := uint32(76)
				if stage == "exterior" {
					id = next.ships[77].NextShip
					if id == 0 || id == 76 || next.ships[id] != nil {
						t.Fatal("expected a distinct read-only exterior witness", id)
					}
				}
				sh, e := f.store.GetShip(id)
				if e != nil || sh == nil {
					t.Fatal(e)
				}
				sh.Name += "late"
				want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(id))] = factoryBuildJSON(t, sh)
				fault = func() error { return f.store.PutShip(sh) }
				point = "probe-launch.before"
			case "star AP":
				st, err := f.store.GetStar(f.star)
				if err != nil || st == nil {
					t.Fatal("missing star", err)
				}
				st.AP[0] ^= 1
				want[fmt.Sprintf("%x/%x", db.BucketStars, db.StarKey(f.star))] = factoryBuildJSON(t, st)
				fault = func() error { return f.store.PutStar(st) }
				point = "probe-launch.before"
			case "caller governor":
				r, err := f.store.GetRace(1)
				if err != nil || r == nil || !r.Governor[0].Active {
					t.Fatal("expected active caller", err)
				}
				r.Governor[0].Active = false
				want[fmt.Sprintf("%x/%x", db.BucketRaces, db.RaceKey(1))] = factoryBuildJSON(t, r)
				fault = func() error { return f.store.PutRace(r) }
				point = "probe-launch.before"
			case "planet":
				pl, e := f.store.GetPlanet(f.star, f.planet)
				if e != nil {
					t.Fatal(e)
				}
				pl.Explored = !pl.Explored
				want[fmt.Sprintf("%x/%x", db.BucketPlanets, db.PlanetKey(f.star, f.planet))] = factoryBuildJSON(t, pl)
				fault = func() error { return f.store.PutPlanet(pl) }
				point = "probe-launch.before"
			}
			hit := false
			f.store.SetMetaFault(func(op string) error {
				if op == point {
					hit = true
					return fault()
				}
				return nil
			})
			msg := f.command(t, "launch #78")
			f.store.SetMetaFault(nil)
			if !hit || strings.Contains(msg, "launched from") || strings.Contains(msg, "Orbiting") || f.state.Mode() != ModeHalted || f.store.CommandWriteUncertain() != (stage == "commit-error") {
				t.Fatal("incorrect outcome", msg)
			}
			if stage == "after-commit" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("installed unconfirmed launch")
			}
			if err := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("partial write or stale flush", err)
			}
		})
	}
}

func TestCR17ProbeLaunchAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reason string
		halted       bool
	}{
		{"legacy surface", "unsettled", false},
		{"legacy other planet", "unsettled", false},
		{"dead sibling", "unsettled", false},
		{"occupancy", "unsettled", false},
		{"pending", "deferred", false},
		{"mass", "Cannot launch", false},
		{"wrong parent", "halted", true},
		{"unlinked selected", "halted", true},
		{"cycle", "halted", true},
		{"double membership", "halted", true},
		{"broken stored tail", "halted", true},
		{"wrong governor", "not authorized", false},
		{"overflow", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := probeLaunchFixture(t, true, false)
			g := 0
			spec := "launch #78"
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				b, c, tail := f.state.ships[77], f.state.ships[78], f.state.ships[76]
				switch tc.name {
				case "legacy surface", "legacy other planet", "double membership":
					c.WhatOrbits, c.WhatDest, c.DestShipNo = model.LevelPlan, model.LevelPlan, 0
					if tc.name == "legacy other planet" {
						for star, planets := range f.state.planets {
							for n, pl := range planets {
								if pl != nil && (star != f.star || n != f.planet) {
									c.StOrbits, c.PNumOrbits = star, n
									break
								}
							}
							if c.StOrbits != f.star || c.PNumOrbits != f.planet {
								break
							}
						}
						if c.StOrbits == f.star && c.PNumOrbits == f.planet {
							t.Fatal("fixture has no other planet")
						}
					}
					if tc.name == "double membership" {
						pl := f.state.planets[f.star][f.planet]
						c.NextShip = pl.Ships
						pl.Ships = c.Number
						if e := f.store.PutPlanet(pl); e != nil {
							t.Fatal(e)
						}
					}
				case "dead sibling":
					tail.Alive = false
					b.Hanger -= tail.Size
				case "occupancy":
					b.Hanger++
				case "pending":
					c.Name = "volatile"
					return
				case "mass":
					b.Mass = c.Mass / 2
				case "wrong parent":
					c.DestShipNo = 76
				case "unlinked selected":
					pred := f.state.ships[79]
					pred.NextShip = 76
					b.Hanger -= c.Size
					if e := f.store.PutShip(pred); e != nil {
						t.Fatal(e)
					}
				case "cycle":
					tail.NextShip = 78
				case "broken stored tail":
					other := *tail
					other.NextShip = 3456
					if err := f.store.PutShip(&other); err != nil {
						t.Fatal(err)
					}
					return
				case "wrong governor":
					g = 1
				case "overflow":
					spec = "launch #4294967374"
				}
				for _, s := range []*model.Ship{b, c, tail} {
					if err := f.store.PutShip(s); err != nil {
						t.Fatal(err)
					}
				}
			})
			before := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, spec, r); e != nil {
				t.Fatal(e)
			}
			mode := ModeServing
			if tc.halted {
				mode = ModeHalted
			}
			msg := r.text()
			if f.state.Mode() != mode || !strings.Contains(msg, tc.reason) || strings.Contains(msg, "launched from") || strings.Contains(msg, "Orbiting") || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("incorrect mode, refusal or world state", f.state.Mode(), msg)
			}
			if tc.name == "pending" {
				scheduledBuildTurnsAndRestart(t, f, func() { assertActorStoreAgreement(t, f.actorEnrollmentFixture) })
				if msg := f.command(t, "launch #78"); !strings.Contains(msg, "launched from") {
					t.Fatal("manual retry failed", msg)
				}
			}
		})
	}
}

// A real scrap leaves the probe alive. A real turn keeps it in its dead
// parent's hold, even though only living ships re-enter the planet's orbit.
func TestCR17ProbeLaunchDestroyedCarrier(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		turn string
		mode Mode
		want string
	}{
		{"pending scrap", "", ModeServing, "deferred"},
		{"linked wreck", "", ModeServing, "Cannot launch"},
		{"after segment", "segment", ModeServing, "Cannot launch"},
		{"after update", "update", ModeServing, "Cannot launch"},
		{"pending after update", "update", ModeServing, "deferred"},
		{"live parent absent", "segment", ModeHalted, "halted"},
		{"double membership", "segment", ModeHalted, "halted"},
		{"stored live parent linked", "segment", ModeServing, "deferred"},
		{"stored dead parent linked", "segment", ModeServing, "deferred"},
		{"stored parent missing", "segment", ModeHalted, "halted"},
		{"stored live parent absent", "segment", ModeHalted, "halted"},
		{"stored detached tail", "segment", ModeHalted, "halted"},
		{"actor detached tail", "segment", ModeHalted, "halted"},
		{"actor shared sibling", "segment", ModeHalted, "halted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := probeLaunchFixture(t, false, false)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				st := f.state.stars[f.star]
				st.AP[0] = 5
				if err := f.store.PutStar(st); err != nil {
					t.Fatal(err)
				}
			})
			if msg := f.command(t, "scrap #77"); !strings.Contains(msg, "Destroyed.") {
				t.Fatalf("scrap: %q", msg)
			}
			switch tc.turn {
			case "segment":
				if err := f.state.TriggerSegment(ctxT(t, 10*time.Second)); err != nil {
					t.Fatal(err)
				}
			case "update":
				if err := f.state.TriggerUpdate(ctxT(t, 10*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				pl := f.state.planets[f.star][f.planet]
				b, c := f.state.ships[77], f.state.ships[78]
				_, linked := f.state.shipPredecessor(pl.Ships, 77)
				_, held := f.state.shipPredecessor(b.Ships, 78)
				if b.Alive || !c.Alive || !c.Docked || c.WhatOrbits != model.LevelShip || c.DestShipNo != 77 || !held || linked != (tc.turn == "") {
					t.Fatal("fixture did not produce expected dead-parent topology")
				}
				switch tc.name {
				case "linked wreck":
					// Settle scrap's volatile AP and morale without rebuilding the lists.
					if err := f.store.PutStar(f.state.stars[f.star]); err != nil {
						t.Fatal(err)
					}
					if err := f.store.PutRace(f.state.races[0]); err != nil {
						t.Fatal(err)
					}
				case "actor detached tail", "actor shared sibling":
					// Keep the stored graph valid to isolate the actor-side guards.
					if pl.Ships == 0 || b.NextShip != 0 || c.NextShip != 0 {
						t.Fatal("expected detached wreck and probe at end of hold")
					}
					if tc.name == "actor detached tail" {
						b.NextShip = pl.Ships
					} else {
						// The first exterior ship now also follows the selected probe.
						c.NextShip = pl.Ships
					}
				case "pending after update":
					c.Name += " pending"
				case "stored live parent linked", "stored dead parent linked":
					// A different, valid pre-rebuild exterior can still explain a deferral.
					stored, exterior := *b, *pl
					stored.Alive = tc.name == "stored live parent linked"
					stored.NextShip, exterior.Ships = exterior.Ships, stored.Number
					if err := f.store.PutShip(&stored); err != nil {
						t.Fatal(err)
					}
					if err := f.store.PutPlanet(&exterior); err != nil {
						t.Fatal(err)
					}
				case "stored parent missing":
					if err := f.store.DeleteShip(b.Number); err != nil {
						t.Fatal(err)
					}
				case "stored live parent absent", "stored detached tail":
					stored := *b
					if tc.name == "stored live parent absent" {
						stored.Alive = true
					} else {
						stored.NextShip = 12345
					}
					if e := f.store.PutShip(&stored); e != nil {
						t.Fatal(e)
					}
				case "live parent absent":
					b.Alive = true
					if err := f.store.PutShip(b); err != nil {
						t.Fatal(err)
					}
				case "double membership":
					// Both chains traverse to completion, but share the same tail.
					if c.NextShip != 0 {
						t.Fatal("expected probe at end of rebuilt hold")
					}
					tail := f.state.ships[pl.Ships]
					for tail.NextShip != 0 {
						tail = f.state.ships[tail.NextShip]
					}
					tail.NextShip = c.Number
					c.WhatOrbits, c.WhatDest = model.LevelPlan, model.LevelPlan
					for _, sh := range []*model.Ship{tail, c} {
						if err := f.store.PutShip(sh); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
			before := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			f.store.SetMetaFault(func(op string) error {
				if op == "probe-launch.before" {
					t.Error("unexpected entry to the launch writer")
					return errors.New("wreck launch must be read-only")
				}
				return nil
			})
			msg := f.command(t, "launch #78")
			f.store.SetMetaFault(nil)
			if f.state.Mode() != tc.mode || !strings.Contains(msg, tc.want) || strings.Contains(msg, "launched from") || strings.Contains(msg, "Orbiting") {
				t.Fatalf("launch response %q, mode %s; want %q in %s", msg, f.state.Mode(), tc.want, tc.mode)
			}
			if !reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("launch altered stored or actor world")
			}
		})
	}
}
