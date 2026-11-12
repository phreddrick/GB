package game

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

type cargoFixture struct {
	*factoryFixture
	partner uint32
}

func newCargoFixture(t *testing.T, ships bool) *cargoFixture {
	t.Helper()
	f := &cargoFixture{factoryFixture: newFactoryFixture(t)}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		sh := f.state.ships[f.factory]
		sh.Type = model.StypeCargo
		sh.MaxResource = 50
		sh.Resource = 10
		sh.Crystals = 1
		sh.Mount = 127
		sh.Mass = 100
		pl := f.state.planets[f.star][f.planet]
		pl.Info[0].Resource = 100
		pl.Info[0].Crystals = 20
		if ships {
			f.partner = 70
			other := factoryCopyShip(t, sh)
			other.Number = f.partner
			other.Resource = 30
			other.Crystals = 10
			other.Mass = 200
			other.MaxHanger = 100
			other.Hanger = sh.Size
			other.Docked = false
			other.WhatDest = model.LevelUniv
			other.NextShip = sh.NextShip
			other.Ships = sh.Number
			sh.NextShip = 0
			sh.WhatOrbits = model.LevelShip
			sh.WhatDest = model.LevelShip
			sh.DestShipNo = other.Number
			pl.Ships = other.Number
			f.state.ships[other.Number] = other
			if e := f.store.PutShip(other); e != nil {
				t.Error(e)
			}
		}
		if e := f.store.PutPlanet(pl); e != nil {
			t.Error(e)
		}
		if e := f.store.PutShip(sh); e != nil {
			t.Error(e)
		}
	})
	return f
}

// Before migration, transfer output is a real boundary after PutPlanet or
// before the independent partner/source saves. A responder raises the existing
// write fence there; it is not a simulated handler or a production fault route.
type cargoBoundaryResponder struct {
	recordingResponder
	at func()
}

func (r *cargoBoundaryResponder) Send(s string) error {
	if e := r.recordingResponder.Send(s); e != nil {
		return e
	}
	if strings.Contains(s, "transferred.") && r.at != nil {
		r.at()
		r.at = nil
	}
	return nil
}
func TestCargoAtomicTransfer(t *testing.T) {
	for _, ships := range []bool{false, true} {
		for _, commodity := range []string{"r", "x", "&"} {
			for _, verb := range []string{"load", "unload"} {
				t.Run(fmt.Sprintf("ships=%t/%s/%s", ships, commodity, verb), func(t *testing.T) {
					f := newCargoFixture(t, ships)
					before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
					evidence := map[string]cargoEvidence{}
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() { evidence["before"] = captureCargoEvidence(t, f) })
					reached := false
					r := &cargoBoundaryResponder{at: func() {
						reached = true
						if !ships {
							evidence["first-write"] = captureCargoEvidence(t, f)
							f.store.SetWriteFence(true)
						}
					}}
					if ships {
						onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
							f.state.testHooks.afterLegacyShipWrite = func(n uint32) {
								if n == f.partner {
									evidence["first-write"] = captureCargoEvidence(t, f)
									f.store.SetWriteFence(true)
								}
							}
						})
					}
					cmd := fmt.Sprintf("%s #%d %s 1", verb, f.factory, commodity)
					if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, cmd, r); e != nil {
						t.Fatal(e)
					}
					f.store.SetWriteFence(false)
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
						f.state.testHooks.afterLegacyShipWrite = nil
						evidence["after-command"] = captureCargoEvidence(t, f)
					})
					if !reached {
						t.Fatal("transfer boundary not exercised", r.text())
					}
					// The future staged operation releases output after all participants commit,
					// so this fence cannot interrupt it. Check cargo conservation and coherence.
					sh, e := f.store.GetShip(f.factory)
					if e != nil {
						t.Fatal(e)
					}
					var selected, partner int
					if commodity == "r" {
						selected = int(sh.Resource)
					} else {
						selected = int(sh.Crystals)
					}
					if ships {
						other, e := f.store.GetShip(f.partner)
						if e != nil {
							t.Fatal(e)
						}
						if commodity == "r" {
							partner = int(other.Resource)
						} else {
							partner = int(other.Crystals)
						}
					} else {
						pl, e := f.store.GetPlanet(f.star, f.planet)
						if e != nil {
							t.Fatal(e)
						}
						if commodity == "r" {
							partner = int(pl.Info[0].Resource)
						} else {
							partner = int(pl.Info[0].Crystals)
						}
					}
					wantTotal := 110
					if ships {
						wantTotal = 40
					}
					if commodity != "r" {
						wantTotal = 21
						if ships {
							wantTotal = 11
						}
					}
					if selected+partner != wantTotal {
						t.Errorf("partial cargo after output boundary: selected=%d partner=%d total=%d want=%d", selected, partner, selected+partner, wantTotal)
					}
					if before == enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
						t.Error("no durable transfer despite success output")
					}
					assertActorStoreAgreementCargo(t, f)
					for _, stage := range []string{"segment", "update", "restart"} {
						switch stage {
						case "segment":
							if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
								t.Fatal(e)
							}
						case "update":
							if e := f.state.TriggerUpdate(ctxT(t, 10*time.Second)); e != nil {
								t.Fatal(e)
							}
						case "restart":
							f.restart(t)
						}
						onEnrollmentActor(t, f.actorEnrollmentFixture, func() { evidence[stage] = captureCargoEvidence(t, f) })
						assertActorStoreAgreementCargo(t, f)
						x := evidence[stage]
						got := x.SelectedStore.Resource
						want := uint16(11)
						if verb == "unload" {
							want = 9
						}
						if commodity != "r" {
							got = x.SelectedStore.Crystals
							want = 2
							if verb == "unload" {
								want = 0
							}
						}
						if got != want {
							t.Error(stage, "transfer lost", got, want)
						}
					}

				})
			}
		}
	}
}

type cargoEvidence struct {
	SelectedActor, SelectedStore, PartnerActor, PartnerStore *model.Ship
	PlanetActor, PlanetStore                                 *model.Planet
	StarActor, StarStore                                     *model.Star
}

func captureCargoEvidence(t *testing.T, f *cargoFixture) cargoEvidence {
	t.Helper()
	out := cargoEvidence{SelectedActor: factoryCopyShip(t, f.state.ships[f.factory]), PlanetActor: planetEdit(f.state.planets[f.star][f.planet])}
	var e error
	out.SelectedStore, e = f.store.GetShip(f.factory)
	if e != nil {
		t.Fatal(e)
	}
	out.PlanetStore, e = f.store.GetPlanet(f.star, f.planet)
	if e != nil {
		t.Fatal(e)
	}
	star := *f.state.stars[f.star]
	out.StarActor = &star
	out.StarStore, e = f.store.GetStar(f.star)
	if e != nil {
		t.Fatal(e)
	}
	if f.partner != 0 {
		out.PartnerActor = factoryCopyShip(t, f.state.ships[f.partner])
		out.PartnerStore, e = f.store.GetShip(f.partner)
		if e != nil {
			t.Fatal(e)
		}
	}
	return out
}
func assertActorStoreAgreementCargo(t *testing.T, f *cargoFixture) {
	t.Helper()
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}
