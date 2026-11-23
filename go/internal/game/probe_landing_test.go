package game

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

// Build and launch through dispatch first. Place the same exterior probe at
// three list positions and offset it by a 3-4-5 triangle for a fractional burn.
func probeLandingFixture(t *testing.T, layout string) *factoryFixture {
	t.Helper()
	f := probeLaunchFixture(t, false, true)
	if msg := f.command(t, "launch #78"); !strings.Contains(msg, "launched from") {
		t.Fatal(msg)
	}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		pl := f.state.planets[f.star][f.planet]
		c, b := f.state.ships[78], f.state.ships[77]
		other := f.state.ships[b.NextShip]
		if pl.Ships != 78 || c.NextShip != 77 || other == nil {
			t.Fatal("unexpected exterior fixture")
		}
		c.XPos += 3
		c.YPos += 4
		switch layout {
		case "head":
		case "carrier predecessor":
			pl.Ships = 77
			c.NextShip, b.NextShip = b.NextShip, 78
		case "other predecessor":
			pl.Ships = 77
			c.NextShip, other.NextShip = other.NextShip, 78
		default:
			t.Fatal("unknown layout")
		}
		for _, err := range []error{f.store.PutShip(c), f.store.PutShip(b), f.store.PutShip(other), f.store.PutPlanet(pl)} {
			if err != nil {
				t.Fatal(err)
			}
		}
	})
	return f
}

func landingShip(t *testing.T, f *factoryFixture, id uint32) *model.Ship {
	t.Helper()
	sh, err := f.store.GetShip(id)
	if err != nil || sh == nil {
		t.Fatalf("ship %d: %v", id, err)
	}
	return sh
}

func landingExpected(t *testing.T, f *factoryFixture, layout string) (launchWorld, float64) {
	t.Helper()
	pl, err := f.store.GetPlanet(f.star, f.planet)
	if err != nil || pl == nil {
		t.Fatal(err)
	}
	c, b := landingShip(t, f, 78), landingShip(t, f, 77)
	w := launchWorld{pl, map[uint32]*model.Ship{78: c, 77: b}}
	switch layout {
	case "head":
		pl.Ships = c.NextShip
	case "carrier predecessor":
		b.NextShip = c.NextShip
	case "other predecessor":
		pred := landingShip(t, f, b.NextShip)
		pred.NextShip = c.NextShip
		w.ships[pred.Number] = pred
	}
	fuel := 0.05 + 0.125*math.Sqrt(c.Mass)
	c.Fuel -= fuel
	c.Mass -= f.state.cfg.MassFuel * fuel
	b.Mass += c.Mass
	b.Hanger += c.Size
	c.NextShip, b.Ships = b.Ships, c.Number
	c.Docked, c.WhatOrbits, c.WhatDest, c.DestShipNo = true, model.LevelShip, model.LevelShip, b.Number
	return w, fuel
}

func TestCR17ProbeLandingDispatch(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"head", "carrier predecessor", "other predecessor"} {
		t.Run(layout, func(t *testing.T) {
			f := probeLandingFixture(t, layout)
			g := 0
			if layout == "carrier predecessor" {
				g = 1
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					c, b, r := f.state.ships[78], f.state.ships[77], f.state.races[0]
					c.Governor, b.Governor = 1, 1
					r.Governor[1].Active = true
					for _, err := range []error{f.store.PutShip(c), f.store.PutShip(b), f.store.PutRace(r)} {
						if err != nil {
							t.Fatal(err)
						}
					}
				})
			}
			w, burn := landingExpected(t, f, layout)
			want := factoryBuildRecords(t, f)
			w.updateRecords(t, want)
			ptrs := map[uint32]*model.Ship{}
			var planet *model.Planet
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				for id := range w.ships {
					ptrs[id] = f.state.ships[id]
				}
				planet = f.state.planets[f.star][f.planet]
			})
			atNotice := false
			r := &probeLaunchResponder{at: func(msg string) {
				atNotice = true
				pl, err := f.store.GetPlanet(f.star, f.planet)
				if err != nil || !reflect.DeepEqual(pl, w.planet) || planet != f.state.planets[f.star][f.planet] || !reflect.DeepEqual(planet, pl) {
					t.Error("planet at notice", msg)
				}
				for id, expected := range w.ships {
					stored, err := f.store.GetShip(id)
					if err != nil || !reflect.DeepEqual(stored, expected) || ptrs[id] != f.state.ships[id] || !reflect.DeepEqual(ptrs[id], stored) {
						t.Errorf("ship #%d not durable and installed at notice", id)
					}
				}
			}}
			if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, "land #78 #77", r); err != nil {
				t.Fatal(err)
			}
			if !atNotice || r.text() != fmt.Sprintf(":78 landed on X77 using %.1f fuel.\n", burn) || f.state.Mode() != ModeServing || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("incorrect landing result", r.text())
			}
		})
	}
}

func TestCR17ProbeLandingCrash(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"head", "carrier predecessor", "other predecessor"} {
		for _, stage := range []string{"before", "validated", "write:1", "write:2", "write:3", "before-commit", "after-commit", "preinstall", "installed"} {
			if layout == "carrier predecessor" && stage == "write:3" {
				continue
			}
			t.Run(layout+"/"+stage, func(t *testing.T) {
				f := probeLandingFixture(t, layout)
				if err := f.state.Stop(ctxT(t, 10*time.Second)); err != nil {
					t.Fatal(err)
				}
				sched, err := f.store.GetSchedule()
				if err != nil || sched == nil {
					t.Fatal(err)
				}
				want := factoryBuildRecords(t, f)
				if stage == "after-commit" || stage == "preinstall" || stage == "installed" {
					w, _ := landingExpected(t, f, layout)
					w.updateRecords(t, want)
				}
				killConstructionAt(t, f, "probe-land."+stage, sched.SavedAt)
				if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatal("incorrect reopened graph or accounting")
				}
			})
		}
	}
}

func TestCR17ProbeLandingScheduled(t *testing.T) {
	t.Parallel()
	f := probeLandingFixture(t, "other predecessor")
	want, _ := landingExpected(t, f, "other predecessor")
	if msg := f.command(t, "land #78 #77"); !strings.Contains(msg, "landed on") {
		t.Fatal(msg)
	}
	check := func() {
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		b, c := landingShip(t, f, 77), landingShip(t, f, 78)
		if b.Hanger != want.ships[77].Hanger || b.Mass != want.ships[77].Mass || c.Mass != want.ships[78].Mass || c.Fuel != want.ships[78].Fuel || !c.Docked || c.DestShipNo != 77 {
			t.Fatal("turn reverted landing accounting", b.Mass, c.Fuel)
		}
		pl, err := f.store.GetPlanet(f.star, f.planet)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[uint32]bool{}
		for n := pl.Ships; n != 0; {
			sh := landingShip(t, f, n)
			if seen[n] || sh.WhatOrbits != model.LevelPlan {
				t.Fatal("invalid exterior", n)
			}
			seen[n] = true
			n = sh.NextShip
		}
		if !seen[77] || seen[78] || seen[76] {
			t.Fatal("wrong exterior membership", seen)
		}
		for n := b.Ships; n != 0; {
			sh := landingShip(t, f, n)
			if seen[n] || sh.WhatOrbits != model.LevelShip || sh.DestShipNo != 77 {
				t.Fatal("invalid hold", n)
			}
			seen[n] = true
			n = sh.NextShip
		}
		if !seen[78] || !seen[76] {
			t.Fatal("missing hold member", seen)
		}
	}
	check()
	scheduledBuildTurnsAndRestart(t, f, check)
}

func TestCR17ProbeLandingConflicts(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"hold", "orbit", "carrier", "probe", "planet", "star", "governor", "write:2", "commit-error", "after-commit"} {
		t.Run(stage, func(t *testing.T) {
			layout := "head"
			if stage == "planet" {
				layout = "carrier predecessor"
			}
			f := probeLandingFixture(t, layout)
			want := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			point := "probe-land.before"
			var fault func() error
			switch stage {
			case "hold", "orbit", "carrier", "probe":
				id := map[string]uint32{"hold": 76, "orbit": landingShip(t, f, 77).NextShip, "carrier": 77, "probe": 78}[stage]
				sh := landingShip(t, f, id)
				sh.Name += " late"
				want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(id))] = factoryBuildJSON(t, sh)
				fault = func() error { return f.store.PutShip(sh) }
			case "planet":
				pl, e := f.store.GetPlanet(f.star, f.planet)
				if e != nil {
					t.Fatal(e)
				}
				pl.Explored = !pl.Explored
				want[fmt.Sprintf("%x/%x", db.BucketPlanets, db.PlanetKey(f.star, f.planet))] = factoryBuildJSON(t, pl)
				fault = func() error { return f.store.PutPlanet(pl) }
			case "star":
				st, e := f.store.GetStar(f.star)
				if e != nil {
					t.Fatal(e)
				}
				st.AP[0] ^= 1
				want[fmt.Sprintf("%x/%x", db.BucketStars, db.StarKey(f.star))] = factoryBuildJSON(t, st)
				fault = func() error { return f.store.PutStar(st) }
			case "governor":
				r, e := f.store.GetRace(1)
				if e != nil {
					t.Fatal(e)
				}
				r.Governor[0].Active = false
				want[fmt.Sprintf("%x/%x", db.BucketRaces, db.RaceKey(1))] = factoryBuildJSON(t, r)
				fault = func() error { return f.store.PutRace(r) }
			default:
				point = "probe-land." + stage
				fault = func() error { return errors.New("injected landing fault") }
				if stage != "write:2" {
					w, _ := landingExpected(t, f, "head")
					w.updateRecords(t, want)
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
			msg := f.command(t, "land #78 #77")
			f.store.SetMetaFault(nil)
			if !hit || f.state.Mode() != ModeHalted || strings.Contains(msg, "landed on") || f.store.CommandWriteUncertain() != (stage == "commit-error") {
				t.Fatal("incorrect fault outcome", msg)
			}
			if stage == "after-commit" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("installed unconfirmed landing")
			}
			if err := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("partial write or stale flush", err)
			}
		})
	}
}

func TestCR17ProbeLandingAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		mode       Mode
	}{
		{"full", "hanger space", ModeServing}, {"no fuel", "Not enough fuel", ModeServing},
		{"too far", "or closer", ModeServing}, {"overloaded", "overloaded", ModeServing},
		{"probe governor", "not authorized", ModeServing}, {"carrier governor", "not authorized", ModeServing},
		{"foreign carrier", "not authorized", ModeServing}, {"inactive governor", "not authorized", ModeServing},
		{"pending", "deferred", ModeServing}, {"old occupancy", "unsettled", ModeServing},
		{"legacy held probe", "unsettled", ModeServing}, {"other historical hold", "another hold", ModeServing},
		{"historically held carrier", "another hold", ModeServing},
		{"dead sibling", "unsettled", ModeServing}, {"cycle", "halted", ModeHalted}, {"broken stored hold", "halted", ModeHalted},
		{"historical hold overlap", "halted", ModeHalted}, {"containment cycle", "halted", ModeHalted},
		{"wide source", "Invalid ship number", ModeServing}, {"wide target", "Invalid ship number", ModeServing},
		{"double membership", "halted", ModeHalted}, {"missing probe", "halted", ModeHalted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := probeLandingFixture(t, "head")
			g := 0
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				b, c, tail := f.state.ships[77], f.state.ships[78], f.state.ships[76]
				pl, r := f.state.planets[f.star][f.planet], f.state.races[0]
				switch tc.name {
				case "full":
					b.MaxHanger = b.Hanger
				case "no fuel":
					c.Fuel = 0
				case "too far":
					c.XPos = b.XPos + f.state.cfg.DistToDock + 2
				case "overloaded":
					c.Resource = c.MaxResource + 1
				case "probe governor":
					g = 1
					r.Governor[1].Active = true
					b.Governor = 1
				case "carrier governor":
					g = 1
					r.Governor[1].Active = true
					c.Governor = 1
				case "foreign carrier":
					b.Owner = 2
				case "inactive governor":
					g = 1
					b.Governor, c.Governor = 1, 1
					r.Governor[1].Active = false
				case "pending":
					b.Name += " pending"
					return
				case "old occupancy":
					b.Hanger++
				case "dead sibling":
					tail.Alive = false
				case "legacy held probe":
					pl.Ships = c.NextShip
					c.NextShip, b.Ships = b.Ships, c.Number
					b.Hanger += c.Size
				case "historical hold overlap":
					// Both holds reach #76 through legacy planet-orbit fields.
					other := f.state.ships[b.NextShip]
					pl.Ships = c.NextShip
					c.NextShip = tail.Number
					other.Ships = c.Number
					tail.WhatOrbits = model.LevelPlan
					if e := f.store.PutShip(other); e != nil {
						t.Fatal(e)
					}
				case "containment cycle":
					other := f.state.ships[b.NextShip]
					c.NextShip, b.NextShip = 0, 0
					other.NextShip, b.Ships = b.Ships, other.Number
					other.Ships = b.Number
					if e := f.store.PutShip(other); e != nil {
						t.Fatal(e)
					}
				case "historically held carrier":
					// The target still declares planet orbit, yet is held by the
					// exterior sibling. Its own shuttle remains correctly linked.
					other := f.state.ships[b.NextShip]
					c.NextShip = b.NextShip
					b.NextShip, other.Ships = other.Ships, b.Number
					if e := f.store.PutShip(other); e != nil {
						t.Fatal(e)
					}
				case "other historical hold":
					other := f.state.ships[b.NextShip]
					pl.Ships = c.NextShip
					c.NextShip, other.Ships = other.Ships, c.Number
					if e := f.store.PutShip(other); e != nil {
						t.Fatal(e)
					}
				case "cycle":
					c.NextShip = c.Number
				case "broken stored hold":
					stored := *tail
					stored.NextShip = 99999
					if e := f.store.PutShip(&stored); e != nil {
						t.Fatal(e)
					}
					return
				case "double membership":
					tail.WhatOrbits = model.LevelPlan
					other := f.state.ships[b.NextShip]
					other.NextShip = tail.Number
					if e := f.store.PutShip(other); e != nil {
						t.Fatal(e)
					}
				case "missing probe":
					pl.Ships = c.NextShip
					c.NextShip = 0
				}
				for _, e := range []error{f.store.PutShip(b), f.store.PutShip(c), f.store.PutShip(tail), f.store.PutPlanet(pl), f.store.PutRace(r)} {
					if e != nil {
						t.Fatal(e)
					}
				}
			})
			want := factoryBuildRecords(t, f)
			actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			reply := &recordingResponder{}
			cmd := "land #78 #77"
			switch tc.name {
			case "wide source":
				cmd = "land #4294967374 #77"
			case "wide target":
				cmd = "land #78 #4294967373"
			}
			if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, cmd, reply); err != nil {
				t.Fatal(err)
			}
			msg := reply.text()
			if !strings.Contains(msg, tc.want) || strings.Contains(msg, "landed on") || f.state.Mode() != tc.mode ||
				actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("wrong admission", f.state.Mode(), msg)
			}
			if tc.name == "pending" {
				scheduledBuildTurnsAndRestart(t, f, func() { assertActorStoreAgreement(t, f.actorEnrollmentFixture) })
				if msg := f.command(t, "land #78 #77"); !strings.Contains(msg, "landed on") {
					t.Fatal("manual retry", msg)
				}
			}
		})
	}
}
