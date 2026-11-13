package game

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

func TestPopulationGameplayMatrix(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	for _, location := range []string{"planet", "contained", "peer"} {
		for _, commodity := range []string{"c", "m"} {
			for _, verb := range []string{"load", "unload"} {
				for _, amount := range []string{"", "0", "2", "-2"} {
					t.Run(location+"/"+commodity+"/"+verb+"/amount="+amount, func(t *testing.T) {
						f := newPopulationFixture(t, location != "planet")
						if location == "peer" {
							cargoPeer(t, f, model.LevelPlan)
						}
						onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
							f.state.races[0].Mass = 1.25
							f.state.stars[f.star].AP[0] = 0
							f.state.sdata.AP[0] = 0
							persistCargo(t, f)
						})
						before := cargoStoredWorld(t, f)
						source := before.Ships[f.factory]
						var actorSource, actorOther *model.Ship
						var actorPlanet *model.Planet
						onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
							actorSource = f.state.ships[f.factory]
							actorOther = f.state.ships[f.partner]
							actorPlanet = f.state.planets[f.star][f.planet]
						})
						delta := int64(2)
						if amount == "-2" {
							delta = -2
						}
						if verb == "unload" {
							delta = -delta
						}
						if amount == "" || amount == "0" {
							if verb == "unload" {
								delta = -10
							} else if commodity == "c" {
								delta = 30
							} else {
								delta = 20
							}
						}
						if commodity == "c" {
							source.Popn = cargoTestWord(t, 10+delta)
						} else {
							source.Troops = cargoTestWord(t, 10+delta)
						}
						source.Mass += float64(delta) * 1.25
						if f.partner != 0 {
							other := before.Ships[f.partner]
							if commodity == "c" {
								other.Popn = cargoTestWord(t, 30-delta)
							} else {
								other.Troops = cargoTestWord(t, 20-delta)
							}
							if location == "peer" {
								other.Mass -= float64(delta) * 1.25
							}
						} else {
							p := before.Planets[f.star][f.planet]
							sec := p.SectorAt(int(source.LandX), int(source.LandY))
							if commodity == "c" {
								sec.Popn = cargoTestWord(t, 30-delta)
								p.Popn = uint64(sec.Popn)
								p.Info[0].Popn = uint32(sec.Popn)
							} else {
								sec.Troops = cargoTestWord(t, 20-delta)
								p.Troops = uint64(sec.Troops)
								p.Info[0].Troops = uint32(sec.Troops)
							}
						}
						command := fmt.Sprintf("%s #77 %s", verb, commodity)
						if amount != "" {
							command += " " + amount
						}
						if out := f.command(t, command); !strings.Contains(out, "complement") {
							t.Fatal("valid C transfer rejected", out)
						}
						if !reflect.DeepEqual(cargoStoredWorld(t, f), before) {
							t.Fatal("complete world or immutable witnesses differ")
						}
						onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
							if f.state.ships[f.factory] != actorSource || f.state.ships[f.partner] != actorOther || f.state.planets[f.star][f.planet] != actorPlanet {
								t.Error("actor pointers replaced")
							}
						})
						assertActorStoreAgreementCargo(t, f)
					})
				}
			}
		}
	}
}
func TestPopulationSectorTransitions(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	for _, commodity := range []string{"c", "m"} {
		for _, scenario := range []string{"neutral deposit", "negative neutral deposit", "owned empty deposit", "neutral empty zero", "owned empty zero", "last resident", "other resident survives", "wide residents", "wide no-op", "empty stock", "planet default load"} {
			t.Run(commodity+"/"+scenario, func(t *testing.T) {
				f := newPopulationFixture(t, false)
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					a := f.state.ships[f.factory]
					p := f.state.planets[f.star][f.planet]
					sec := p.SectorAt(int(a.LandX), int(a.LandY))
					sec.Race = 2
					switch scenario {
					case "neutral deposit", "negative neutral deposit", "neutral empty zero":
						sec.Owner = 0
						sec.Popn = 0
						sec.Troops = 0
					case "owned empty deposit", "owned empty zero", "empty stock":
						sec.Popn = 0
						sec.Troops = 0
					case "last resident", "planet default load":
						if commodity == "c" {
							sec.Troops = 0
						} else {
							sec.Popn = 0
						}
					case "wide no-op":
						sec.Popn = 65535
						sec.Troops = 1
						a.Popn = 0
						a.Troops = 0
						a.MaxCrew = 0
					case "wide residents":
						sec.Popn = 65535
						sec.Troops = 1
						a.Popn = 0
						a.Troops = 0
						a.MaxCrew = 65535
					}
					populationTotals(p)
					persistCargo(t, f)
				})
				want := cargoStoredWorld(t, f)
				a := want.Ships[f.factory]
				p := want.Planets[f.star][f.planet]
				sec := p.SectorAt(int(a.LandX), int(a.LandY))
				command := "unload #77 " + commodity + " 2"
				delta := int64(-2)
				switch scenario {
				case "negative neutral deposit":
					command = "load #77 " + commodity + " -2"
				case "neutral empty zero", "owned empty zero", "empty stock", "wide no-op":
					command = "load #77 " + commodity + " 0"
					delta = 0
				case "last resident", "planet default load":
					command = "load #77 " + commodity
					delta = int64(sec.Popn)
					if commodity == "m" {
						delta = int64(sec.Troops)
					}
				case "wide residents":
					command = "load #77 " + commodity
					delta = int64(65535)
					if commodity == "m" {
						delta = 1
					}
				}
				if commodity == "c" {
					a.Popn = cargoTestWord(t, int64(a.Popn)+delta)
					sec.Popn = cargoTestWord(t, int64(sec.Popn)-delta)
				} else {
					a.Troops = cargoTestWord(t, int64(a.Troops)+delta)
					sec.Troops = cargoTestWord(t, int64(sec.Troops)-delta)
				}
				a.Mass += float64(delta) * want.Races[0].Mass
				claim := sec.Owner == 0 && delta < 0
				evac := sec.Owner == 1 && sec.Popn == 0 && sec.Troops == 0
				if claim {
					sec.Owner = 1
				}
				if evac {
					sec.Owner = 0
				}
				populationTotals(p)
				out := f.command(t, command)
				if !strings.Contains(out, "complement") {
					t.Fatal(out)
				}
				if strings.Contains(out, "COLONIZED") || strings.Contains(out, "OCCUPIED") {
					if !claim {
						t.Fatal("false colonization notice")
					}
				} else if claim {
					t.Fatal("missing claim notice")
				}
				if strings.Contains(out, "evacuated") != evac {
					t.Fatal("incorrect evacuation notice", out)
				}
				if !reflect.DeepEqual(cargoStoredWorld(t, f), want) {
					t.Fatal("sector/owner/counter/unrelated record mismatch")
				}
				assertActorStoreAgreementCargo(t, f)
				f.restart(t)
				assertActorStoreAgreementCargo(t, f)
			})
		}
	}
}
func TestPopulationAuthorityForeignAndAssaultBoundary(t *testing.T) {
	// Private actor/store fixtures permit parallel groups within the unchanged package timeout.
	t.Parallel()
	for _, commodity := range []string{"c", "m"} {
		for _, scenario := range []string{"foreign give", "foreign take", "foreign zero", "selected owner", "selected governor", "partner governor", "ship Race differs", "hostile unload", "hostile load", "hostile zero", "star", "universe"} {
			t.Run(commodity+"/"+scenario, func(t *testing.T) {
				ships := !strings.HasPrefix(scenario, "hostile")
				f := newPopulationFixture(t, ships)
				enrollActor(t, f.actorEnrollmentFixture, actorEnrollmentRequest("account-2"))
				g := 0
				command := "load #77 " + commodity + " 2"
				success := false
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					a := f.state.ships[f.factory]
					p := f.state.planets[f.star][f.planet]
					if ships {
						b := f.state.ships[f.partner]
						switch scenario {
						case "foreign give":
							b.Owner = 2
							command = "unload #77 " + commodity + " 2"
						case "foreign take":
							b.Owner = 2
						case "foreign zero":
							b.Owner = 2
							command = "load #77 " + commodity + " 0"
							success = true
						case "selected owner":
							a.Owner = 2
							b.Owner = 2
						case "selected governor":
							g = 1
							f.state.races[0].Governor[1].Active = true
							f.state.dirs[0][1] = f.state.dirs[0][0]
							a.Governor = 2
						case "partner governor":
							b.Governor = 2
							success = true
						case "ship Race differs":
							a.Race = 2
							b.Race = 2
							success = true
						case "star", "universe":
							success = true
						}
					} else {
						sec := p.SectorAt(int(a.LandX), int(a.LandY))
						sec.Owner = 2
						populationTotals(p)
						if scenario == "hostile unload" {
							command = "unload #77 " + commodity + " 2"
						}
						if scenario == "hostile zero" {
							command = "load #77 " + commodity + " 0"
						}
					}
					persistCargo(t, f)
				})
				if scenario == "star" || scenario == "universe" {
					level := uint8(model.LevelStar)
					if scenario == "universe" {
						level = model.LevelUniv
					}
					cargoPeer(t, f, level)
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.ships[f.factory].PNumOrbits = 1; persistCargo(t, f) })
				}
				// Hostile positive unload is now an approved atomic operation.
				// Keep all prior rejection cases; verify the newly enabled case
				// against its COMPLETE durable proposal, not just response text.
				var assault db.ShipGroundAssault
				var records map[string]string
				if scenario == "hostile unload" {
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
						// This fixture now prepares and dispatches a durable
						// assault: keep its message timestamp deterministic too.
						now := f.state.now()
						f.state.now = func() time.Time { return now }
						f.state.assaultRandom = highAssaultDraw
					})
					assault = shipAssaultPrepared(t, f.factoryFixture, commodity == "m", 2)
					records = assaultRecords(t, f.factoryFixture)
				}
				before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
				r := &recordingResponder{}
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, command, r); e != nil {
					t.Fatal(e)
				}
				if scenario == "hostile unload" {
					if !strings.Contains(r.text(), "Casualties:") {
						t.Fatal("approved hostile unload did not execute", r.text())
					}
					assertShipAssaultActor(t, f.factoryFixture, assault)
					if !reflect.DeepEqual(assaultRecords(t, f.factoryFixture), shipAssaultExpectedRecords(t, records, assault)) {
						t.Fatal("incomplete newly enabled hostile operation")
					}
				} else if success {
					if !strings.Contains(r.text(), "complement") {
						t.Fatal(r.text())
					}
					assertActorStoreAgreementCargo(t, f)
				} else if strings.Contains(r.text(), "complement") || strings.Contains(r.text(), "VICTORY") || before != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("unauthorized/deferred effects escaped", r.text())
				}
				if !ships {
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
						if f.state.groundAssaults[0][1][f.star] != 0 {
							t.Error("assault accounting mutated")
						}
					})
				}
			})
		}
	}
}
