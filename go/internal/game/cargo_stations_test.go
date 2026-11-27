package game

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

func stationFixture(t *testing.T, budgets uint16, autoload bool, free ...uint32) *factoryFixture {
	t.Helper()
	return starCargoFixture(t, budgets*427+23, autoload, free...)
}

func starCargoFixture(t *testing.T, resources uint16, autoload bool, free ...uint32) *factoryFixture {
	t.Helper()
	f := newCargoBuildFixture(t, false, model.LevelStar)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		r, b := f.state.races[0], f.state.ships[77]
		r.Governor[0].Toggle.AutoLoad = autoload
		b.Resource = resources
		b.MaxResource = max(b.MaxResource, resources)
		b.Popn, b.Fuel = 58, 899.5
		b.DestStar, b.DestPNum, b.LandX, b.LandY = 2, 1, 3, 4
		for _, e := range []error{f.store.PutRace(r), f.store.PutShip(b)} {
			if e != nil {
				t.Error(e)
			}
		}
	})
	for _, id := range free {
		sh := &model.Ship{Number: id}
		if err := f.store.PutShip(sh); err != nil {
			t.Fatal(err)
		}
		if err := f.store.DeleteShip(id); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// Separate legacy design, loading, and initialization helpers provide an
// independent oracle. This also checks all fields not named in the assertions
// and all database records the command must leave alone, including AP.
func stationExpected(t *testing.T, f *factoryFixture, before map[string]string, ids ...uint32) map[string]string {
	t.Helper()
	return starCargoExpected(t, f, before, model.StypeStation, 427, ids)
}

func starCargoExpected(t *testing.T, f *factoryFixture, before map[string]string, kind model.ShipType, cost uint16, ids []uint32) map[string]string {
	t.Helper()
	expected := maps.Clone(before)
	if len(ids) == 0 {
		return expected
	}
	b := landingShip(t, f, 77)
	st := scrapStar(t, f)
	r, err := f.store.GetRace(1)
	if err != nil || r == nil {
		t.Fatal(r, err)
	}
	legacy := &State{cfg: f.cfg}
	put := func(bucket, key []byte, v any) { expected[fmt.Sprintf("%x/%x", bucket, key)] = factoryBuildJSON(t, v) }
	for _, id := range ids {
		var child model.Ship
		legacy.getshipDefaults(&child, kind, r)
		if child.BuildCost != cost {
			t.Fatal("fixture cost changed", child.BuildCost)
		}
		child.Number, child.Tech, child.Class = id, r.Tech, "Standard"
		child.WhatOrbits, child.StOrbits, child.PNumOrbits = model.LevelStar, b.StOrbits, b.PNumOrbits
		child.DestStar, child.DestPNum = b.DestStar, b.DestPNum
		child.XPos, child.YPos, child.LandX, child.LandY = b.XPos, b.YPos, b.LandX, b.LandY
		child.NextShip, st.Ships = st.Ships, id
		b.Resource -= child.BuildCost
		var crew int
		var fuel float64
		if r.Governor[0].Toggle.AutoLoad {
			autoloadAtShip(1, &child, b, &crew, &fuel)
		}
		legacy.initializeNewShip(1, 0, r, &child, fuel, crew)
		put(db.BucketShips, db.ShipKey(id), &child)
		for k, v := range expected {
			if strings.HasPrefix(k, fmt.Sprintf("%x/", db.BucketShipFree)) && v == string(db.ShipKey(id)) {
				delete(expected, k)
			}
		}
	}
	put(db.BucketShips, db.ShipKey(b.Number), b)
	put(db.BucketStars, db.StarKey(st.Number), st)
	return expected
}

func TestCR17CargoStationDispatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, command string
		budget        uint16
		free, ids     []uint32
		short         bool
	}{
		{"default", "build S", 2, nil, []uint32{78}, false},
		{"explicit one reused", "build S 1", 2, []uint32{41, 55}, []uint32{55}, false},
		{"two fresh", "build S 2", 2, nil, []uint32{78, 79}, false},
		{"two reused", "build S 2", 2, []uint32{41, 55}, []uint32{55, 41}, false},
		{"mixed lower", "build S 2", 2, []uint32{41}, []uint32{41, 78}, false},
		{"mixed higher", "build S 2", 2, []uint32{88}, []uint32{88, 89}, false},
		{"affordable prefix", "build S 2", 1, []uint32{40, 55}, []uint32{55}, true},
		{"unaffordable", "build S 2", 0, []uint32{55}, nil, true},
	} {
		for _, load := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/autoload=%t", tc.name, load), func(t *testing.T) {
				f := stationFixture(t, tc.budget, load, tc.free...)
				want := stationExpected(t, f, factoryBuildRecords(t, f), tc.ids...)
				var b *model.Ship
				var st *model.Star
				var messages []string
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					b, st = f.state.ships[77], f.state.stars[f.star]
					f.state.registry = &factoryBuildRegistry{at: func(msg string) {
						if strings.Contains(msg, "built at a cost") {
							if !reflect.DeepEqual(want, commandFixtureRecords(t, stringMustReadFactoryBuild(t, f.path))) ||
								b != f.state.ships[77] || st != f.state.stars[f.star] || !reflect.DeepEqual(b, landingShip(t, f, 77)) ||
								!reflect.DeepEqual(st, scrapStar(t, f)) {
								t.Error("incomplete commit or installation at success", msg)
							}
							for _, id := range tc.ids {
								if !reflect.DeepEqual(f.state.ships[id], landingShip(t, f, id)) {
									t.Error("child not installed at first success", id)
								}
							}
						}
						messages = append(messages, msg)
					}}
				})
				resp := &probeLaunchResponder{at: func(msg string) { messages = append(messages, msg) }}
				if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, tc.command, resp); err != nil {
					t.Fatal(err)
				}
				var expected []string
				for i, id := range tc.ids {
					crew, fuel := 0, 0.0
					if load {
						crew, fuel = 50, 899.5
						if i == 1 {
							crew, fuel = 8, 0
						}
					}
					expected = append(expected,
						fmt.Sprintf("S%d built at a cost of 427 resources.\n", id),
						"Technology 1000.0.\n", "Warning: This ship is constructed with a 75% damage level.\n",
						"This ship does not need resources to repair.\n", fmt.Sprintf("Loaded with %d crew and %.1f fuel.\n", crew, fuel))
				}
				if tc.short {
					expected = append(expected, "You need 427r to construct the ship.\n")
				}
				if !reflect.DeepEqual(messages, expected) || f.state.Mode() != ModeServing || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatalf("wrong messages or whole-store delta in %v: %q vs %q", f.state.Mode(), messages, expected)
				}
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			})
		}
	}
}

func TestCR17CargoStationAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, reply string
		mode        Mode
		edit        func(*testing.T, *factoryFixture)
	}{
		{"pending legacy link", "deferred", ModeServing, func(t *testing.T, f *factoryFixture) {
			resp := &recordingResponder{}
			f.state.buildFromShip(1, 0, f.state.races[0], []string{"M"}, resp)
			if resp.text() != "" {
				t.Error("legacy fixture did not produce a pending build", resp.text())
			}
		}},
		{"pending builder", "deferred", ModeServing, func(_ *testing.T, f *factoryFixture) { f.state.ships[77].Resource-- }},
		{"actor-only allocated id", "deferred", ModeServing, func(_ *testing.T, f *factoryFixture) { f.state.ships[89] = &model.Ship{Number: 89} }},
		{"broken actor chain", "halted", ModeHalted, func(_ *testing.T, f *factoryFixture) { f.state.ships[77].NextShip = 77 }},
		{"broken durable chain", "halted", ModeHalted, func(t *testing.T, f *factoryFixture) {
			st := scrapStar(t, f)
			st.Ships = 109
			if err := f.store.PutStar(st); err != nil {
				t.Error(err)
			}
		}},
		{"unqualified technology", "not advanced enough", ModeServing, func(t *testing.T, f *factoryFixture) {
			r := f.state.races[0]
			r.Tech = 0
			if err := f.store.PutRace(r); err != nil {
				t.Error(err)
			}
		}},
		{"uncrewed", "no crew", ModeServing, func(_ *testing.T, f *factoryFixture) { f.state.ships[77].Popn = 0 }},
		{"damaged", "damaged and cannot build", ModeServing, func(_ *testing.T, f *factoryFixture) { f.state.ships[77].Damage = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := stationFixture(t, 2, true, 88)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				tc.edit(t, f)
				f.state.registry = &factoryBuildRegistry{at: func(msg string) { t.Errorf("unexpected notification: %q", msg) }}
			})
			before, actor := factoryBuildRecords(t, f), actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			f.store.SetMetaFault(func(op string) error {
				if strings.HasPrefix(op, "star-cargo-build.") {
					t.Errorf("unexpected write boundary: %s", op)
				}
				return nil
			})
			if msg := f.command(t, "build S 2"); !strings.Contains(msg, tc.reply) || f.state.Mode() != tc.mode ||
				!reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatalf("wrong admission outcome %v %q", f.state.Mode(), msg)
			}
			if tc.mode == ModeHalted {
				assertStationNoFlush(t, f, before)
			}
		})
	}
}

func assertStationNoFlush(t *testing.T, f *factoryFixture, want map[string]string) {
	t.Helper()
	if err := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(err, ErrHaltedNotFlushed) ||
		!reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
		t.Fatal("halted shutdown should not flush", err)
	}
}

func TestCR17CargoStationLateChanges(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"builder", "star", "race", "second ID", "second structural reference", "late free append", "free sequence"} {
		t.Run(which, func(t *testing.T) {
			f := stationFixture(t, 2, true, 88)
			var after map[string]string
			f.store.SetMetaFault(func(op string) error {
				if op != "star-cargo-build.before" {
					return nil
				}
				var err error
				switch which {
				case "builder":
					b := landingShip(t, f, 77)
					b.Resource--
					err = f.store.PutShip(b)
				case "star":
					st := scrapStar(t, f)
					st.Governor[0]++
					err = f.store.PutStar(st)
				case "race":
					r, e := f.store.GetRace(1)
					if e != nil {
						return e
					}
					r.Governor[0].Toggle.AutoLoad = false
					err = f.store.PutRace(r)
				case "second ID":
					err = f.store.PutShip(&model.Ship{Number: 89})
				case "second structural reference":
					pl, e := f.store.GetPlanet(f.star, f.planet)
					if e != nil {
						return e
					}
					pl.Ships = 89
					err = f.store.PutPlanet(pl)
				case "late free append":
					// A new LIFO entry displaces both optimistic candidates.
					err = f.store.DeleteShip(42)
				case "free sequence":
					if err = f.store.DeleteShip(42); err == nil {
						_, err = f.store.AllocShipNum()
					}
				}
				if err != nil {
					t.Errorf("setup late writer: %v", err)
					return err
				}
				after = commandFixtureRecords(t, stringMustReadFactoryBuild(t, f.path))
				return nil
			})
			msg := f.command(t, "build S 2")
			f.store.SetMetaFault(nil)
			if f.state.Mode() != ModeHalted || after == nil || !strings.Contains(msg, "halted") || strings.Contains(msg, "built") ||
				!reflect.DeepEqual(after, factoryBuildRecords(t, f)) {
				t.Fatal("late writer was clobbered or legacy fallback ran", msg)
			}
			assertStationNoFlush(t, f, after)
		})
	}
}

func TestCR17CargoStationFailures(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"before", "write:1", "write:2", "write:3", "write:4", "write:5", "write:6", "before-commit", "commit-error", "after-commit", "lost actor slot", "fence", "maintenance", "malformed maintenance"} {
		t.Run(stage, func(t *testing.T) {
			f := stationFixture(t, 2, true, 41, 55)
			want := factoryBuildRecords(t, f)
			actorBefore := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			switch stage {
			case "commit-error", "after-commit", "lost actor slot":
				want = stationExpected(t, f, want, 55, 41)
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.registry = &factoryBuildRegistry{at: func(msg string) { t.Errorf("unexpected success notice: %s", msg) }}
				if stage == "lost actor slot" {
					f.state.testHooks = &testHooks{beforeCommandInstall: func() {
						f.state.ships[41] = &model.Ship{Number: 41}
					}}
				}
			})
			reached := stage == "lost actor slot"
			f.store.SetMetaFault(func(op string) error {
				if stage == "fence" && op == "star-cargo-build.before" {
					f.store.SetWriteFence(true)
					reached = true
				}
				if op == "star-cargo-build.before" {
					switch stage {
					case "maintenance":
						reached = true
						return f.store.PutMaintenance(&db.MaintenanceRecord{
							SchemaVersion: db.MaintenanceSchemaVersion, Quiesced: true,
							SetAt: time.Now(), Reason: "review test",
						})
					case "malformed maintenance":
						reached = true
						return f.store.PutMeta(db.KeyMaintenance, []byte("null"))
					}
				}
				if op == "star-cargo-build."+stage {
					reached = true
					return errors.New("injected")
				}
				return nil
			})
			msg := f.command(t, "build S 2")
			f.store.SetMetaFault(nil)
			f.store.SetWriteFence(false)
			if stage == "maintenance" || stage == "malformed maintenance" {
				actual := factoryBuildRecords(t, f)
				key := fmt.Sprintf("%x/%x", db.BucketMeta, db.KeyMaintenance)
				if actual[key] == "" {
					t.Fatal("missing late maintenance write")
				}
				want[key] = actual[key]
			}
			if !reached || f.state.Mode() != ModeHalted || !strings.Contains(msg, "halted") ||
				f.store.CommandWriteUncertain() != (stage == "commit-error") ||
				!reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatalf("unexpected construction failure at %s: %s", stage, msg)
			}
			if stage != "after-commit" && stage != "lost actor slot" &&
				actorBefore != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("uncommitted or uncertain state was installed")
			}
			if stage == "after-commit" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			}
			assertStationNoFlush(t, f, want)
		})
	}
}

func TestCR17CargoStationCrash(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		budget uint16
		free   []uint32
		ids    []uint32
	}{
		{"fresh", 2, nil, []uint32{78, 79}},
		{"mixed", 2, []uint32{88}, []uint32{88, 89}},
		{"reused", 2, []uint32{41, 55}, []uint32{55, 41}},
		{"affordable prefix", 1, []uint32{40, 55}, []uint32{55}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stages := []string{"before", "check:1", "validated", "before-commit", "after-commit", "preinstall", "installed"}
			for i := 1; i <= 2+2*len(tc.ids); i++ {
				stages = append(stages, fmt.Sprintf("write:%d", i))
			}
			for _, stage := range stages {
				t.Run(stage, func(t *testing.T) {
					f := stationFixture(t, tc.budget, true, tc.free...)
					checkStarCargoCrash(t, f, stage, "build S 2", model.StypeStation, 427, tc.ids)
					if stage == "after-commit" && (tc.name == "mixed" || tc.budget == 1) {
						checkStationTurnsAndNavigation(t, f, tc.budget, tc.ids)
					}
				})
			}
		})
	}
}

func checkStarCargoCrash(t *testing.T, f *factoryFixture, stage, command string, kind model.ShipType, cost uint16, ids []uint32) {
	t.Helper()
	if err := f.state.Stop(ctxT(t, 10*time.Second)); err != nil {
		t.Fatal(err)
	}
	schedule, err := f.store.GetSchedule()
	if err != nil || schedule == nil {
		t.Fatal(schedule, err)
	}
	before := factoryBuildRecords(t, f)
	want := before
	switch stage {
	case "after-commit", "preinstall", "installed":
		want = starCargoExpected(t, f, before, kind, cost, ids)
	}
	killConstructionCommandAt(t, f, "star-cargo-build."+stage, schedule.SavedAt, command)
	// Compare the newly opened bbolt image before any engine startup or turn.
	if got := factoryBuildRecords(t, f); !reflect.DeepEqual(got, want) {
		t.Fatal("partial construction or unrelated records after process death")
	}
	f.open(t)
	if f.state.Mode() != ModeServing {
		t.Fatal(f.state.Mode())
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestCR17CargoStationTurnsAndNavigation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		budget uint16
		free   []uint32
		ids    []uint32
	}{
		{"mixed", 2, []uint32{88}, []uint32{88, 89}},
		{"affordable prefix", 1, []uint32{40, 55}, []uint32{55}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := stationFixture(t, tc.budget, true, tc.free...)
			before := factoryBuildRecords(t, f)
			want := stationExpected(t, f, before, tc.ids...)
			if msg := f.command(t, "build S 2"); tc.budget == 1 && !strings.Contains(msg, "You need 427r") || tc.budget == 2 && msg != "" {
				t.Fatal(msg)
			}
			if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("build did not commit the expected accounting")
			}
			checkStationTurnsAndNavigation(t, f, tc.budget, tc.ids)
		})
	}
}

func checkStationTurnsAndNavigation(t *testing.T, f *factoryFixture, budget uint16, ids []uint32) {
	t.Helper()
	var free []uint32
	if budget == 1 {
		free = []uint32{40}
	}
	checkStarCargoTurnsAndNavigation(t, f, model.StypeStation, free, ids)
}

func checkStarCargoTurnsAndNavigation(t *testing.T, f *factoryFixture, kind model.ShipType, wantFree, ids []uint32) {
	t.Helper()
	check := func() {
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		seen := map[uint32]bool{}
		for n := scrapStar(t, f).Ships; n != 0; {
			row := landingShip(t, f, n)
			if seen[n] || row.WhatOrbits != model.LevelStar || row.StOrbits != f.star {
				t.Fatal("invalid exterior membership", n)
			}
			seen[n], n = true, row.NextShip
		}
		b := landingShip(t, f, 77)
		if !seen[77] || b.Resource != 23 {
			t.Fatal("Cargo debit or membership changed across turn/restart")
		}
		for _, id := range ids {
			if !seen[id] || landingShip(t, f, id).Type != kind {
				t.Fatal("Station detached or lost", id)
			}
		}
		free, err := f.store.FreeShipNums()
		if err != nil || !slices.Equal(free, wantFree) {
			t.Fatal("turn/restart changed allocator consumption", free, err)
		}
	}
	check()
	scheduledBuildTurnsAndRestart(t, f, check)
	first := ids[0]
	dock := f.command(t, fmt.Sprintf("dock #77 #%d", first))
	launch := f.command(t, fmt.Sprintf("launch #%d", first))
	if !strings.Contains(dock, "docked with") || !strings.Contains(launch, "undocked from") ||
		landingShip(t, f, 77).Docked || landingShip(t, f, first).Docked || f.state.Mode() != ModeServing {
		t.Fatal("peer movement after restart failed", dock, launch, f.state.Mode())
	}
	check()
}

func TestCR17CargoStationOtherRouting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		cmd  string
		edit func(*factoryFixture)
	}{
		{"other type", "build X 2", func(*factoryFixture) {}},
		{"beyond two", "build S 3", func(*factoryFixture) {}},
		{"God", "build S", func(f *factoryFixture) { f.state.races[0].God = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := stationFixture(t, 2, true)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { tc.edit(f) })
			f.store.SetMetaFault(func(op string) error {
				if strings.HasPrefix(op, "star-cargo-build.") {
					t.Errorf("Station path intercepted %s", tc.name)
				}
				return nil
			})
			f.command(t, tc.cmd)
			if scrapStar(t, f).Ships != 77 || landingShip(t, f, 78) == nil || f.state.Mode() != ModeServing {
				t.Fatal("the other construction route changed")
			}
		})
	}
}

func TestCR17CargoStationStorageContract(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"duplicate IDs", "incomplete prefix", "changed placement", "changed cost", "wrong ticket order", "invalid free digest", "unsupported design", "wrong child type"} {
		t.Run(which, func(t *testing.T) {
			f := stationFixture(t, 2, true, 41, 55)
			b, st := landingShip(t, f, 77), scrapStar(t, f)
			r, err := f.store.GetRace(1)
			if err != nil || r == nil {
				t.Fatal(r, err)
			}
			tickets, err := f.store.StarCargoBuildCandidates(2)
			if err != nil || len(tickets) != 2 {
				t.Fatal(tickets, err)
			}
			ctx := commandrules.StarCargoBuild{
				CargoBuild: commandrules.CargoBuild{
					Player: 1, Scope: model.Directory{Level: model.LevelShip, ShipNo: 77},
					Race: r, Star: st, Ships: []*model.Ship{b},
				},
				Kind:  model.StypeStation,
				Count: 2,
			}
			change, err := commandrules.PlanStarCargoBuild(ctx, []uint32{tickets[0].Number, tickets[1].Number}, f.cfg)
			if err != nil {
				t.Fatal(err)
			}
			in := db.StarCargoBuild{Input: ctx, Allocations: tickets, Ships: change.Ships, Config: f.cfg}
			switch which {
			case "duplicate IDs":
				in.Allocations[1] = in.Allocations[0]
			case "incomplete prefix":
				in.Allocations, in.Ships = in.Allocations[:1], in.Ships[:1]
			case "changed placement":
				in.Ships[1].NextShip = 77
			case "changed cost":
				in.Ships[1].BuildCost--
			case "wrong ticket order":
				tickets[0], tickets[1] = tickets[1], tickets[0]
				change, err = commandrules.PlanStarCargoBuild(ctx, []uint32{tickets[0].Number, tickets[1].Number}, f.cfg)
				if err != nil {
					t.Fatal(err)
				}
				in.Ships = change.Ships
			case "invalid free digest":
				in.Allocations[1].FreeDigest = ""
			case "unsupported design":
				in.Input.Kind = model.StypeCarrier
				if _, err = commandrules.PlanStarCargoBuild(in.Input, []uint32{tickets[0].Number, tickets[1].Number}, f.cfg); !errors.Is(err, commandrules.ErrFactoryBuildInput) {
					t.Fatal("planner accepted an otherwise eligible Carrier outside the allowlist", err)
				}
			case "wrong child type":
				in.Ships[1].Type = model.StypeHabitat
			}
			before := factoryBuildRecords(t, f)
			outcome, err := f.store.CommitStarCargoBuild(in)
			var ce *db.CommandError
			code := db.CommandInvalid
			if which == "wrong ticket order" || which == "invalid free digest" {
				code = db.CommandStale
			}
			if outcome != db.CommandNotCommitted || !errors.As(err, &ce) || ce.Code != code ||
				!reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
				t.Fatal("typed store accepted a forged proposal", outcome, err)
			}
		})
	}
}
