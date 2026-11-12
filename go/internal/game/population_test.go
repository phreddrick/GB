package game

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

func newPopulationFixture(t *testing.T, ships bool) *cargoFixture {
	t.Helper()
	f := newCargoFixture(t, ships)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		a := f.state.ships[f.factory]
		a.Popn = 10
		a.Troops = 10
		a.MaxCrew = 50
		a.Mass = 100
		if ships {
			b := f.state.ships[f.partner]
			b.Popn = 30
			b.Troops = 20
			b.MaxCrew = 100
			b.Mass = 200
		}
		pl := f.state.planets[f.star][f.planet]
		sec := pl.SectorAt(int(a.LandX), int(a.LandY))
		sec.Popn = 30
		sec.Troops = 20
		sec.Owner = 1
		sec.Mobilization = 7
		sec.Fert = 100
		populationTotals(pl)
		f.state.races[0].BirthRate = 0
		persistCargo(t, f)
	})
	return f
}

// Fixture accounting, independently summed by sector owner, not transfer code.
func populationTotals(p *model.Planet) {
	p.Popn = 0
	p.Troops = 0
	for i := range p.Info {
		p.Info[i].Popn = 0
		p.Info[i].Troops = 0
		p.Info[i].NumSectsOwned = 0
		p.Info[i].MobPoints = 0
	}
	for _, sec := range p.Sectors {
		p.Popn += uint64(sec.Popn)
		p.Troops += uint64(sec.Troops)
		if sec.Owner > 0 {
			i := int(sec.Owner) - 1
			p.Info[i].Popn += uint32(sec.Popn)
			p.Info[i].Troops += uint32(sec.Troops)
			p.Info[i].NumSectsOwned++
			p.Info[i].MobPoints += uint64(sec.Mobilization)
		}
	}
}
func TestPopulationAtomicTransfer(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	for _, ships := range []bool{false, true} {
		for _, commodity := range []string{"c", "m"} {
			for _, verb := range []string{"load", "unload"} {
				t.Run(fmt.Sprintf("ships=%t/%s/%s", ships, commodity, verb), func(t *testing.T) {
					f := newPopulationFixture(t, ships)
					evidence := map[string]cargoEvidence{}
					capture := func(stage string) { evidence[stage] = captureCargoEvidence(t, f) }
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
						capture("before")
						if ships {
							f.state.testHooks.afterLegacyShipWrite = func(n uint32) {
								if n == f.partner {
									capture("first-write")
									f.store.SetWriteFence(true)
								}
							}
						} else {
							f.state.testHooks.afterLegacyPlanetWrite = func(*model.Planet) { capture("first-write"); f.store.SetWriteFence(true) }
						}
					})
					out := f.command(t, fmt.Sprintf("%s #77 %s 2", verb, commodity))
					f.store.SetWriteFence(false)
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
						f.state.testHooks.afterLegacyShipWrite = nil
						f.state.testHooks.afterLegacyPlanetWrite = nil
						capture("after-command")
						assertPopulationRetained(t, f, commodity, verb, evidence["after-command"], "after-command")
					})
					if !strings.Contains(out, "complement") {
						t.Error("actual handler did not transfer", out)
					}
					a, e := f.store.GetShip(f.factory)
					if e != nil {
						t.Fatal(e)
					}
					selected, other := int(a.Popn), 0
					if commodity == "m" {
						selected = int(a.Troops)
					}
					if ships {
						b, e := f.store.GetShip(f.partner)
						if e != nil {
							t.Fatal(e)
						}
						other = int(b.Popn)
						if commodity == "m" {
							other = int(b.Troops)
						}
					} else {
						p, e := f.store.GetPlanet(f.star, f.planet)
						if e != nil {
							t.Fatal(e)
						}
						sec := p.SectorAt(int(a.LandX), int(a.LandY))
						other = int(sec.Popn)
						if commodity == "m" {
							other = int(sec.Troops)
						}
					}
					total := 40
					if commodity == "m" {
						total = 30
					}
					if selected+other != total {
						t.Errorf("partial population selected=%d recipient=%d total=%d want=%d", selected, other, selected+other, total)
					}
					for _, kind := range []string{"segment", "update"} {
						var e error
						if kind == "segment" {
							e = f.state.TriggerSegment(ctxT(t, 10*time.Second))
						} else {
							e = f.state.TriggerUpdate(ctxT(t, 10*time.Second))
						}
						if e != nil {
							t.Fatal(e)
						}
						onEnrollmentActor(t, f.actorEnrollmentFixture, func() { capture(kind); assertPopulationRetained(t, f, commodity, verb, evidence[kind], kind) })
					}
					f.restart(t)
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
						capture("restart")
						assertPopulationRetained(t, f, commodity, verb, evidence["restart"], "restart")
					})
					if t.Failed() {
						b, e := json.Marshal(evidence)
						if e != nil {
							t.Fatal(e)
						}
						t.Log("POPULATION_SNAPSHOT:" + string(b))
					} else {
						assertActorStoreAgreementCargo(t, f)
					}
				})
			}
		}
	}
}
func TestPopulationOwnershipEdgeCases(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	for _, kind := range []string{"neutral zero", "owned empty deposit"} {
		t.Run(kind, func(t *testing.T) {
			f := newPopulationFixture(t, false)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				a := f.state.ships[f.factory]
				p := f.state.planets[f.star][f.planet]
				sec := p.SectorAt(int(a.LandX), int(a.LandY))
				sec.Popn = 0
				sec.Troops = 0
				if kind == "neutral zero" {
					sec.Owner = 0
				}
				populationTotals(p)
				persistCargo(t, f)
			})
			before := captureOnPopulationActor(t, f)
			command := "load #77 c 0"
			if kind == "owned empty deposit" {
				command = "unload #77 c 2"
			}
			out := f.command(t, command)
			after := captureOnPopulationActor(t, f)
			want := before.PlanetStore.Info[0].NumSectsOwned
			if after.PlanetStore.Info[0].NumSectsOwned != want {
				t.Errorf("wrong ownership count before=%d after=%d", want, after.PlanetStore.Info[0].NumSectsOwned)
				b, _ := json.Marshal(map[string]cargoEvidence{"before": before, "after": after})
				t.Log("POPULATION_SNAPSHOT:" + string(b))
				t.Log(out)
			}
		})
	}
}
func captureOnPopulationActor(t *testing.T, f *cargoFixture) cargoEvidence {
	t.Helper()
	var v cargoEvidence
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { v = captureCargoEvidence(t, f) })
	return v
}

func assertPopulationRetained(t *testing.T, f *cargoFixture, commodity, verb string, v cargoEvidence, stage string) {
	t.Helper()
	delta := 2
	if verb == "unload" {
		delta = -2
	}
	a, b := v.SelectedStore, v.PartnerStore
	selected := int(a.Popn)
	otherStock := 30
	if commodity == "m" {
		selected = int(a.Troops)
		otherStock = 20
	}
	other := 0
	if b != nil {
		other = int(b.Popn)
		if commodity == "m" {
			other = int(b.Troops)
		}
	} else {
		sec := v.PlanetStore.SectorAt(int(a.LandX), int(a.LandY))
		other = int(sec.Popn)
		if commodity == "m" {
			other = int(sec.Troops)
		}
	}
	if selected != 10+delta || other != otherStock-delta {
		t.Errorf("%s command lost or duplicated people: selected=%d other=%d want=%d/%d", stage, selected, other, 10+delta, otherStock-delta)
	}
	if !reflect.DeepEqual(v.SelectedActor, v.SelectedStore) || !reflect.DeepEqual(v.PartnerActor, v.PartnerStore) || !reflect.DeepEqual(v.PlanetActor, v.PlanetStore) {
		t.Error(stage, "actor/store records disagree")
	}
	if math.IsNaN(a.Mass) || math.IsInf(a.Mass, 0) || a.Mass < 0 {
		t.Error(stage, "invalid source mass")
	}
	p := v.PlanetStore
	expected := planetEdit(p)
	populationTotals(expected)
	if p.Popn != expected.Popn || p.Troops != expected.Troops || p.Info[0].Popn != expected.Info[0].Popn || p.Info[0].Troops != expected.Info[0].Troops || p.Info[0].NumSectsOwned != expected.Info[0].NumSectsOwned {
		t.Error(stage, "planet/sector/player accounting disagreement")
	}
}
