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

type exteriorCargoDesign struct {
	letter       string
	kind         model.ShipType
	cost, crew   uint16
	fuelCap      float64
	requiredTech float64
}

// Keep these expectations independent of the atomic design planner.
var exteriorCargoDesigns = []exteriorCargoDesign{
	{"H", model.StypeHabitat, 592, 2000, 2000, 100},
	{"O", model.StypeOAP, 423, 200, 2000, 200},
	{"M", model.StypeMirror, 115, 5, 20, 100},
}

func TestCR17StarCargoExteriorDispatch(t *testing.T) {
	t.Parallel()
	for _, d := range exteriorCargoDesigns {
		t.Run(d.letter, func(t *testing.T) {
			for _, tc := range []struct {
				name, count string
				budget      uint16
				free, ids   []uint32
				short       bool
			}{
				{"default", "", 2, nil, []uint32{78}, false},
				{"explicit one", " 1", 2, []uint32{41, 55}, []uint32{55}, false},
				{"two fresh", " 2", 2, nil, []uint32{78, 79}, false},
				{"two LIFO reuses", " 2", 2, []uint32{41, 55}, []uint32{55, 41}, false},
				{"mixed reused high", " 2", 2, []uint32{88}, []uint32{88, 89}, false},
				{"affordable prefix", " 2", 1, []uint32{40, 55}, []uint32{55}, true},
				{"unaffordable", " 2", 0, []uint32{55}, nil, true},
			} {
				for _, load := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/autoload=%t", tc.name, load), func(t *testing.T) {
						f := starCargoFixture(t, tc.budget*d.cost+23, load, tc.free...)
						addStarCargoSibling(t, f)
						want := starCargoExpected(t, f, factoryBuildRecords(t, f), d.kind, d.cost, tc.ids)
						var output []string
						onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
							s := f.state
							builder, star := s.ships[77], s.stars[f.star]
							s.registry = &factoryBuildRegistry{at: func(msg string) {
								output = append(output, msg)
								if !strings.Contains(msg, "built at a cost") {
									return
								}
								got := commandFixtureRecords(t, stringMustReadFactoryBuild(t, f.path))
								if !reflect.DeepEqual(want, got) ||
									builder != s.ships[77] || star != s.stars[f.star] ||
									!reflect.DeepEqual(builder, landingShip(t, f, 77)) || !reflect.DeepEqual(star, scrapStar(t, f)) {
									t.Error("not completely committed and installed at output", msg)
								}
								for _, id := range tc.ids {
									if !reflect.DeepEqual(s.ships[id], landingShip(t, f, id)) {
										t.Errorf("child %d not installed before output", id)
									}
								}
							}}
						})
						resp := &probeLaunchResponder{at: func(msg string) { output = append(output, msg) }}
						cmd := "build " + d.letter + tc.count
						if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, cmd, resp); err != nil {
							t.Fatal(err)
						}
						wantOutput := d.notices(tc.ids, load, tc.short)
						if f.state.Mode() != ModeServing || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) || !reflect.DeepEqual(output, wantOutput) {
							t.Fatalf("mode %v, actual output %q, expected %q (and exact durable records)", f.state.Mode(), output, wantOutput)
						}
						assertActorStoreAgreement(t, f.actorEnrollmentFixture)
					})
				}
			}
		})
	}
}

// Start from a nonempty list whose Cargo builder is not the head.
func addStarCargoSibling(t *testing.T, f *factoryFixture) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		s := f.state
		st := s.stars[f.star]
		sibling := &model.Ship{Number: 76, Type: model.StypeCargo, Owner: 1, Alive: true, Active: true, WhatOrbits: model.LevelStar, StOrbits: f.star, NextShip: st.Ships}
		st.Ships, s.ships[sibling.Number] = sibling.Number, sibling
		for _, e := range []error{f.store.PutShip(sibling), f.store.PutStar(st)} {
			if e != nil {
				t.Error(e)
			}
		}
	})
}

func (d exteriorCargoDesign) notices(ids []uint32, load, short bool) []string {
	var output []string
	var remainingCrew uint16 = 58
	remainingFuel := 899.5
	for _, id := range ids {
		var crew uint16
		var fuel float64
		if load {
			crew, fuel = min(d.crew, remainingCrew), min(d.fuelCap, remainingFuel)
			remainingCrew, remainingFuel = remainingCrew-crew, remainingFuel-fuel
		}
		output = append(output,
			fmt.Sprintf("%s%d built at a cost of %d resources.\n", d.letter, id, d.cost),
			"Technology 1000.0.\n", "Warning: This ship is constructed with a 75% damage level.\n",
			"This ship does not need resources to repair.\n", fmt.Sprintf("Loaded with %d crew and %.1f fuel.\n", crew, fuel))
	}
	if short {
		output = append(output, fmt.Sprintf("You need %dr to construct the ship.\n", d.cost))
	}
	return output
}

func TestCR17StarCargoExteriorCrash(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		design                 exteriorCargoDesign
		budget                 uint16
		free, ids, unspentFree []uint32
	}{
		{exteriorCargoDesigns[0], 1, []uint32{40, 55}, []uint32{55}, []uint32{40}},
		{exteriorCargoDesigns[1], 2, []uint32{88}, []uint32{88, 89}, nil},
		{exteriorCargoDesigns[2], 2, []uint32{41, 55}, []uint32{55, 41}, nil},
	} {
		t.Run(tc.design.letter, func(t *testing.T) {
			stages := []string{"before", "validated", "before-commit", "after-commit", "preinstall", "installed"}
			for i := 1; i <= 2+2*len(tc.ids); i++ {
				stages = append(stages, fmt.Sprintf("write:%d", i))
			}
			for _, stage := range stages {
				t.Run(stage, func(t *testing.T) {
					d := tc.design
					f := starCargoFixture(t, tc.budget*d.cost+23, true, tc.free...)
					checkStarCargoCrash(t, f, stage, "build "+d.letter+" 2", d.kind, d.cost, tc.ids)
					if stage == "after-commit" {
						checkStarCargoTurnsAndNavigation(t, f, d.kind, tc.unspentFree, tc.ids)
					}
				})
			}
		})
	}
}

func TestCR17StarCargoExteriorTechnology(t *testing.T) {
	t.Parallel()
	for _, d := range exteriorCargoDesigns {
		t.Run(d.letter, func(t *testing.T) {
			f := starCargoFixture(t, d.cost, false)
			setTech := func(value float64) {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					r := f.state.races[0]
					r.Tech = value
					if err := f.store.PutRace(r); err != nil {
						t.Error(err)
					}
				})
			}
			setTech(d.requiredTech - 0.5)
			want := factoryBuildRecords(t, f)
			msg := f.command(t, "build "+d.letter)
			if msg != fmt.Sprintf("You are not advanced enough to build this ship.\n%.1f engineering technology needed. You have %.1f.\n", d.requiredTech, d.requiredTech-0.5) ||
				f.state.Mode() != ModeServing || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("tech refusal changed or wrote records", msg)
			}
			setTech(d.requiredTech)
			want = starCargoExpected(t, f, factoryBuildRecords(t, f), d.kind, d.cost, []uint32{78})
			if msg = f.command(t, "build "+d.letter+" 1"); msg != "" || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("exact tech boundary did not permit build", msg)
			}
		})
	}
}

func TestCR17StarCargoExteriorLateWitnesses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, cmd string
		edit      func(*testing.T, *factoryFixture) error
	}{
		{"H orbit sibling", "build H 2", func(t *testing.T, f *factoryFixture) error {
			other := landingShip(t, f, 76)
			other.Resource++
			return f.store.PutShip(other)
		}},
		{"O rebuilt allocator state", "build O 2", func(_ *testing.T, f *factoryFixture) error {
			if err := f.store.DeleteShip(42); err != nil {
				return err
			}
			_, err := f.store.AllocShipNum()
			return err
		}},
		{"M second candidate appeared", "build M 2", func(_ *testing.T, f *factoryFixture) error {
			return f.store.PutShip(&model.Ship{Number: 89})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := starCargoFixture(t, 1220, true, 88)
			addStarCargoSibling(t, f)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				f.state.registry = &factoryBuildRegistry{at: func(msg string) { t.Error("late witness emitted success", msg) }}
			})
			var after map[string]string
			f.store.SetMetaFault(func(op string) error {
				if op != "star-cargo-build.before" {
					return nil
				}
				if err := tc.edit(t, f); err != nil {
					t.Error(err)
					return err
				}
				after = commandFixtureRecords(t, stringMustReadFactoryBuild(t, f.path))
				return nil
			})
			if msg := f.command(t, tc.cmd); after == nil || f.state.Mode() != ModeHalted || !strings.Contains(msg, "halted") ||
				!reflect.DeepEqual(after, factoryBuildRecords(t, f)) {
				t.Fatal("late conflict was ignored or retried on the legacy path", msg)
			}
			f.store.SetMetaFault(nil)
			assertStationNoFlush(t, f, after)
		})
	}
}

func TestCR17StarCargoExteriorAdmission(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, cmd, reply string
		mode             Mode
		edit             func(*factoryFixture)
	}{
		{"H valid pending debit", "build H", "deferred", ModeServing, func(f *factoryFixture) {
			f.state.ships[77].Resource--
		}},
		{"O uncrewed", "build O", "This ship has no crew.\n", ModeServing, func(f *factoryFixture) {
			f.state.ships[77].Popn = 0
		}},
		{"M cycle in actor orbit", "build M", "halted", ModeHalted, func(f *factoryFixture) {
			f.state.ships[77].NextShip = 77
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := starCargoFixture(t, 700, true)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				tc.edit(f)
				f.state.registry = &factoryBuildRegistry{at: func(msg string) { t.Error("unexpected early output", msg) }}
			})
			before, actor := factoryBuildRecords(t, f), actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			f.store.SetMetaFault(func(op string) error {
				if strings.HasPrefix(op, "star-cargo-build.") {
					t.Error("attempted write for read-only refusal", op)
				}
				return nil
			})
			if msg := f.command(t, tc.cmd); !strings.Contains(msg, tc.reply) || f.state.Mode() != tc.mode ||
				!reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("wrong admission outcome", msg, f.state.Mode())
			}
			if tc.mode == ModeHalted {
				assertStationNoFlush(t, f, before)
			}
		})
	}
}

func TestCR17StarCargoExteriorReferencedFreeIDDefers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cmd   string
		count int
	}{
		{"build H", 1},
		{"build O 2", 2},
		{"build M 2", 2},
		{"build S 2", 2},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			const freed = 50
			f := starCargoFixture(t, 1280, true, 41, freed)
			tickets, err := f.store.StarCargoBuildCandidates(tc.count)
			if err != nil || len(tickets) != tc.count || tickets[0].Number != freed || tickets[0].FreeKey == 0 {
				t.Fatal("fixture did not seed the next reusable ID", tickets, err)
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				// A dead ship awaiting cleanup is outside the live exterior
				// list, but its persisted NextShip still reserves the ID.
				wreck := &model.Ship{Number: 49, Owner: 1, NextShip: freed}
				f.state.ships[wreck.Number] = wreck
				if err := f.store.PutShip(wreck); err != nil {
					t.Error(err)
				}
				f.state.registry = &factoryBuildRegistry{at: func(msg string) { t.Errorf("unexpected build output: %q", msg) }}
			})
			var stale *db.CommandError
			if _, err = f.store.StarCargoBuildCandidates(tc.count); !errors.As(err, &stale) || stale.Code != db.CommandStale {
				t.Fatal("the structural reference must make the allocation stale", err)
			}
			wantDB := commandFixtureImage(t, f.actorEnrollmentFixture)
			wantActor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			f.store.SetMetaFault(func(op string) error {
				if strings.HasPrefix(op, "star-cargo-build.") {
					t.Errorf("deferred build entered the commit path: %s", op)
				}
				return nil
			})
			out := f.command(t, tc.cmd)
			f.store.SetMetaFault(nil)
			const deferred = "Construction deferred: actor and stored records differ or allocation is pending; retry after the next persisted turn.\n"
			if mode := f.state.Mode(); mode != ModeServing || out != deferred {
				t.Fatalf("wanted allocation deferral in SERVING, got mode %v and reply %q", mode, out)
			}
			if wantDB != commandFixtureImage(t, f.actorEnrollmentFixture) || wantActor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("deferral changed the durable image or actor state")
			}
			free, err := f.store.FreeShipNums()
			if err != nil || !reflect.DeepEqual(free, []uint32{freed, 41}) {
				t.Fatal("deferral changed the free list", free, err)
			}
		})
	}
}

func TestCR17StarCargoExteriorIssuingGovernor(t *testing.T) {
	t.Parallel()
	f := starCargoFixture(t, 115, false)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		b, r := f.state.ships[77], f.state.races[0]
		b.Governor = 2
		r.Governor[1].Active = true
		r.Governor[1].Toggle.AutoLoad = true
		f.state.dirs[0][1] = f.state.dirs[0][0]
		st := f.state.stars[f.star]
		st.AP[0] = 7
		for _, err := range []error{f.store.PutShip(b), f.store.PutRace(r), f.store.PutStar(st)} {
			if err != nil {
				t.Error(err)
			}
		}
	})
	resp := &recordingResponder{}
	if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 1, "build M", resp); err != nil || resp.text() != "" {
		t.Fatal("legacy race-level construction authority changed", err, resp.text())
	}
	b, child := landingShip(t, f, 77), landingShip(t, f, 78)
	if b.Governor != 2 || b.Resource != 0 || child.Governor != 1 || child.Popn != 5 || child.Fuel != 20 ||
		f.state.Mode() != ModeServing || scrapStar(t, f).AP[0] != 7 {
		t.Fatal("owner, issuing governor's autoload, or existing AP changed")
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestCR17StarCargoExteriorLegacyRoutes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, cmd string
		level     uint8
		shuttle   bool
		god       bool
	}{
		{"other count", "build M 3", model.LevelStar, false, false},
		{"type word", "build Habitat", model.LevelStar, false, false},
		{"noncanonical count", "build M +1", model.LevelStar, false, false},
		{"trailing argument", "build O 1 extra", model.LevelStar, false, false},
		{"Shuttle", "build M", model.LevelStar, true, false},
		{"God", "build H", model.LevelStar, false, true},
		{"planet orbit", "build H 1", model.LevelPlan, false, false},
		{"universe", "build O 2", model.LevelUniv, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCargoBuildFixture(t, false, tc.level)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				if tc.shuttle {
					f.state.ships[77].Type = model.StypeShuttle
				}
				f.state.races[0].God = tc.god
			})
			f.store.SetMetaFault(func(op string) error {
				if strings.HasPrefix(op, "star-cargo-build.") {
					t.Error("out-of-scope command entered new writer", op)
				}
				return nil
			})
			if msg := f.command(t, tc.cmd); msg != "" || f.state.Mode() != ModeServing || landingShip(t, f, 78).WhatOrbits != tc.level {
				t.Fatal("legacy routing changed", msg)
			}
			if landingShip(t, f, 77).Resource != 1000 {
				t.Fatal("out-of-scope command changed its persistence behavior")
			}
		})
	}
}
