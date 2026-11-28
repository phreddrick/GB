package game

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

const carrierBatchCost = 354

func TestCR17CargoCarrierBatchDispatch(t *testing.T) {
	t.Parallel()
	design := exteriorCargoDesign{"X", model.StypeCarrier, carrierBatchCost, 30, 1000, 250}
	for _, tc := range []struct {
		name             string
		resources        uint16
		free, ships      []uint32
		issuer, assigned uint8
		short            bool
	}{
		{"fresh leader", 708, nil, []uint32{78, 79}, 0, 2, false},
		{"LIFO assigned governor", 708, []uint32{41, 50}, []uint32{50, 41}, 1, 1, false},
		{"mixed nonassigned governor", 708, []uint32{88}, []uint32{88, 89}, 1, 2, false},
		{"partial nonassigned governor", 707, []uint32{41, 50}, []uint32{50}, 1, 2, true},
		{"zero affordable", 353, []uint32{50}, nil, 1, 2, true},
	} {
		for _, load := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/autoload=%t", tc.name, load), func(t *testing.T) {
				f := starCargoFixture(t, tc.resources, load, tc.free...)
				addStarCargoSibling(t, f)
				g := int(tc.issuer)
				configureCarrierBatchGovernor(t, f, tc.issuer, tc.assigned, load)
				before := factoryBuildRecords(t, f)
				if g == 1 && tc.assigned != tc.issuer && tc.resources >= carrierBatchCost {
					beforeActor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
					for _, cmd := range []string{"build X", "build X 1"} {
						resp := &recordingResponder{}
						if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, cmd, resp); err != nil || resp.text() != "You can't build here.\n" {
							t.Fatalf("single-Carrier route changed for %s: %v %q", cmd, err, resp.text())
						}
					}
					if beforeActor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
						t.Fatal("single-Carrier refusal changed the actor or store")
					}
				}
				want := starCargoExpected(t, f, before, design.kind, design.cost, g, tc.ships)
				var output []string
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					s := f.state
					b, st := s.ships[77], s.stars[f.star]
					s.registry = &factoryBuildRegistry{at: func(msg string) {
						output = append(output, msg)
						if !strings.Contains(msg, "built at a cost") {
							return
						}
						got := commandFixtureRecords(t, stringMustReadFactoryBuild(t, f.path))
						if !reflect.DeepEqual(want, got) ||
							b != s.ships[77] || st != s.stars[f.star] ||
							!reflect.DeepEqual(b, landingShip(t, f, 77)) || !reflect.DeepEqual(st, scrapStar(t, f)) {
							t.Error("batch builder/root or durable image incomplete at first success", msg)
						}
						for _, id := range tc.ships {
							if !reflect.DeepEqual(s.ships[id], landingShip(t, f, id)) {
								t.Errorf("child %d was not installed before success", id)
							}
						}
					}}
				})
				var emptyImage string
				if len(tc.ships) == 0 {
					emptyImage = commandFixtureImage(t, f.actorEnrollmentFixture)
					f.store.SetMetaFault(func(op string) error {
						if strings.HasPrefix(op, "star-cargo-build.") {
							t.Error("unaffordable batch entered the writer", op)
						}
						return nil
					})
				}
				resp := &probeLaunchResponder{at: func(msg string) { output = append(output, msg) }}
				if err := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, "build X 2", resp); err != nil {
					t.Fatal(err)
				}
				if wantOutput := design.notices(tc.ships, load, tc.short, 50); !reflect.DeepEqual(output, wantOutput) {
					t.Fatalf("batch notices = %q, want %q", output, wantOutput)
				}
				if f.state.Mode() != ModeServing || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatal("batch changed the wrong durable records", f.state.Mode())
				}
				if len(tc.ships) == 0 && emptyImage != commandFixtureImage(t, f.actorEnrollmentFixture) {
					t.Fatal("unaffordable batch wrote to bbolt")
				}
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			})
		}
	}
}

func configureCarrierBatchGovernor(t *testing.T, f *factoryFixture, issuer, assigned uint8, load bool) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		b, r := f.state.ships[77], f.state.races[0]
		b.Governor = assigned
		r.Governor[0].Toggle.AutoLoad = !load
		r.Governor[assigned].Active = true
		r.Governor[assigned].Toggle.AutoLoad = !load
		r.Governor[issuer].Active = true
		r.Governor[issuer].Toggle.AutoLoad = load
		f.state.dirs[0][issuer] = f.state.dirs[0][0]
		st := f.state.stars[f.star]
		if load {
			st.AP[0] = 7
		}
		for _, err := range []error{f.store.PutShip(b), f.store.PutRace(r), f.store.PutStar(st)} {
			if err != nil {
				t.Error(err)
			}
		}
	})
}

func TestCR17CargoCarrierBatchTechnology(t *testing.T) {
	t.Parallel()
	f := starCargoFixture(t, 708, false)
	setTech := func(value float64) {
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			r := f.state.races[0]
			r.Tech = value
			if err := f.store.PutRace(r); err != nil {
				t.Error(err)
			}
		})
	}
	setTech(249.5)
	before, actor := factoryBuildRecords(t, f), actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	const refusal = "You are not advanced enough to build this ship.\n250.0 engineering technology needed. You have 249.5.\n"
	if msg := f.command(t, "build X 2"); msg != refusal || f.state.Mode() != ModeServing ||
		!reflect.DeepEqual(before, factoryBuildRecords(t, f)) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("technology refusal changed", msg)
	}
	setTech(250)
	want := starCargoExpected(t, f, factoryBuildRecords(t, f), model.StypeCarrier, carrierBatchCost, 0, []uint32{78, 79})
	if msg := f.command(t, "build X 2"); msg != "" || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
		t.Fatal("build failed at the exact Carrier technology threshold", msg)
	}
}

func TestCR17CargoCarrierBatchUncertain(t *testing.T) {
	t.Parallel()
	f := starCargoFixture(t, 708, true, 88)
	want := starCargoExpected(t, f, factoryBuildRecords(t, f), model.StypeCarrier, carrierBatchCost, 0, []uint32{88, 89})
	before := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		f.state.registry = &factoryBuildRegistry{at: func(msg string) { t.Errorf("uncertain build reported success: %q", msg) }}
	})
	f.store.SetMetaFault(func(op string) error {
		if op == "star-cargo-build.commit-error" {
			return errors.New("injected uncertain commit result")
		}
		return nil
	})
	out := f.command(t, "build X 2")
	f.store.SetMetaFault(nil)
	if !strings.Contains(out, "UNCERTAIN") || f.state.Mode() != ModeHalted || !f.store.CommandWriteUncertain() ||
		!reflect.DeepEqual(want, factoryBuildRecords(t, f)) || before != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("uncertain batch did not preserve the observed durable outcome and halt without installation", out)
	}
	assertStationNoFlush(t, f, want)
}

func TestCR17CargoCarrierExplicitOneUsesSingleWriter(t *testing.T) {
	t.Parallel()
	f := starCargoFixture(t, 2*carrierBatchCost, true, 55)
	want := starCargoExpected(t, f, factoryBuildRecords(t, f), model.StypeCarrier, carrierBatchCost, 0, []uint32{55})
	single := false
	f.store.SetMetaFault(func(op string) error {
		if op == "cargo-build.before" {
			single = true
		}
		if strings.HasPrefix(op, "star-cargo-build.") {
			t.Error("explicit one entered the batch writer", op)
		}
		return nil
	})
	if msg := f.command(t, "build X 1"); msg != "" || !single || f.state.Mode() != ModeServing ||
		!reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
		t.Fatal("explicit one did not retain its checked single-Carrier route", msg)
	}
}

func TestCR17CargoCarrierLargerCountRemainsLegacy(t *testing.T) {
	t.Parallel()
	f := starCargoFixture(t, 3*carrierBatchCost, false)
	f.store.SetMetaFault(func(op string) error {
		if strings.HasPrefix(op, "star-cargo-build.") {
			t.Error("three-Carrier request entered the batch writer", op)
		}
		return nil
	})
	if msg := f.command(t, "build X 3"); msg != "" || f.state.Mode() != ModeServing {
		t.Fatal("three-Carrier routing changed", msg)
	}
	if landingShip(t, f, 80).Type != model.StypeCarrier || landingShip(t, f, 77).Resource != 3*carrierBatchCost || scrapStar(t, f).Ships != 77 {
		t.Fatal("three-Carrier request did not keep its existing path")
	}
}

func TestCR17CargoCarrierBatchCrash(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                   string
		load                   bool
		budget                 uint16
		free, ids, unspentFree []uint32
	}{
		{"fresh", false, 2*carrierBatchCost + 23, nil, []uint32{78, 79}, nil},
		{"mixed", true, 2*carrierBatchCost + 23, []uint32{88}, []uint32{88, 89}, nil},
		{"affordable prefix", true, carrierBatchCost + 23, []uint32{40, 55}, []uint32{55}, []uint32{40}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stages := []string{"before", "validated", "before-commit", "after-commit", "preinstall", "installed"}
			for i := 1; i <= 2+2*len(tc.ids); i++ {
				stages = append(stages, fmt.Sprintf("write:%d", i))
			}
			for _, stage := range stages {
				t.Run(stage, func(t *testing.T) {
					f := starCargoFixture(t, tc.budget, tc.load, tc.free...)
					checkStarCargoCrash(t, f, stage, "build X 2", model.StypeCarrier, carrierBatchCost, tc.ids)
					if stage == "after-commit" {
						checkStarCargoTurnsAndNavigation(t, f, model.StypeCarrier, tc.unspentFree, tc.ids)
					}
				})
			}
		})
	}
}
