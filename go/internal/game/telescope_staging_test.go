package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type telescopeFixture struct {
	*factoryFixture
	target uint8
}

func newTelescopeFixture(t *testing.T, kind model.ShipType) *telescopeFixture {
	t.Helper()
	f := &telescopeFixture{factoryFixture: newFactoryFixture(t)}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		sh := f.state.ships[f.factory]
		sh.Type = kind
		sh.Fuel = 10
		sh.Mass = 100
		sh.Tech = 0
		sh.Special.AimedAt = &model.AimedAtSpecial{Intensity: 37}
		sh.Notified = true
		for _, st := range f.state.stars {
			if st.Number != f.star && st.NumPlanets > 0 {
				f.target = st.Number
				break
			}
		}
		st := f.state.stars[f.target]
		st.Name = "AimTarget"
		st.PlanetNames[0] = "Sight"
		st.XPos = sh.XPos
		st.YPos = sh.YPos
		st.Explored &^= 1
		pl := f.state.planets[f.target][0]
		pl.XPos = 0
		pl.YPos = 0
		pl.Info[0].Explored = false
		if e := f.store.PutShip(sh); e != nil {
			t.Error(e)
		}
		if e := f.store.PutStar(st); e != nil {
			t.Error(e)
		}
		if e := f.store.PutPlanet(pl); e != nil {
			t.Error(e)
		}
	})
	return f
}

type telescopeWorld struct {
	ActorShip, StoredShip     *model.Ship
	ActorStar, StoredStar     *model.Star
	ActorPlanet, StoredPlanet *model.Planet
}

func (f *telescopeFixture) capture(t *testing.T) telescopeWorld {
	t.Helper()
	sh, e := f.store.GetShip(f.factory)
	if e != nil {
		t.Error(e)
	}
	st, e := f.store.GetStar(f.target)
	if e != nil {
		t.Error(e)
	}
	pl, e := f.store.GetPlanet(f.target, 0)
	if e != nil {
		t.Error(e)
	}
	a := *f.state.stars[f.target]
	return telescopeWorld{factoryCopyShip(t, f.state.ships[f.factory]), sh, &a, st, planetEdit(f.state.planets[f.target][0]), pl}
}
func (f *telescopeFixture) snapshotAim(t *testing.T) telescopeWorld {
	t.Helper()
	var x telescopeWorld
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { x = f.capture(t) })
	return x
}
func (f *telescopeFixture) aim(t *testing.T, target string) string {
	t.Helper()
	return f.command(t, fmt.Sprintf("order #%d aim %s", f.factory, target))
}
func (f *telescopeFixture) stopAim(t *testing.T) {
	t.Helper()
	if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
		t.Fatal(e)
	}
}

func telescopeExpected(t *testing.T, b telescopeWorld, level uint8, explore bool, gravity bool) telescopeWorld {
	t.Helper()
	x := telescopeWorld{}
	x.ActorShip = factoryCopyShip(t, b.ActorShip)
	x.StoredShip = factoryCopyShip(t, b.StoredShip)
	st := *b.ActorStar
	x.ActorStar = &st
	st2 := *b.StoredStar
	x.StoredStar = &st2
	x.ActorPlanet = planetEdit(b.ActorPlanet)
	x.StoredPlanet = planetEdit(b.StoredPlanet)
	for _, sh := range []*model.Ship{x.ActorShip, x.StoredShip} {
		aa := *sh.Special.AimedAt
		aa.Level = level
		aa.StarNum = b.ActorStar.Number
		aa.PlanetNum = 0
		aa.ShipNo = 0
		sh.Special.AimedAt = &aa
		sh.Notified = false
		if !gravity {
			sh.Fuel -= .3
			sh.Mass -= .3 * .05
		}
	}
	if explore {
		x.ActorStar.SetExplored(1)
		x.StoredStar.SetExplored(1)
		if level == model.LevelPlan {
			x.ActorPlanet.Info[0].Explored = true
			x.StoredPlanet.Info[0].Explored = true
		}
	}
	return x
}
func assertTelescopeComplete(t *testing.T, f *telescopeFixture, want telescopeWorld) {
	t.Helper()
	got := f.snapshotAim(t)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("complete actor/store telescope,star,planet result differs")
	}
}
func TestTelescopeActorCompleteSemanticsAndBoundaries(t *testing.T) {
	for _, kind := range []model.ShipType{model.OtypeStele, model.OtypeGtele} {
		for _, level := range []uint8{model.LevelStar, model.LevelPlan} {
			for _, where := range []string{"inside", "boundary", "outside"} {
				t.Run(fmt.Sprintf("%d/%d/%s", kind, level, where), func(t *testing.T) {
					f := newTelescopeFixture(t, kind)
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
						sh := f.state.ships[f.factory]
						sh.XPos = 0
						sh.YPos = 0
						st := f.state.stars[f.target]
						st.XPos = teleRange(kind, sh.Tech)
						st.YPos = 0
						pl := f.state.planets[f.target][0]
						pl.XPos = 0
						pl.YPos = 0
						switch where {
						case "inside":
							st.XPos = 0
						case "outside":
							st.XPos = math.Nextafter(st.XPos, math.Inf(1))
						}
						if level == model.LevelPlan && where == "inside" {
							pl.XPos = 3
							pl.YPos = 4
						}
						for _, e := range []error{f.store.PutShip(sh), f.store.PutStar(st), f.store.PutPlanet(pl)} {
							if e != nil {
								t.Error(e)
							}
						}
					})
					before := f.snapshotAim(t)
					want := telescopeExpected(t, before, level, where != "outside", kind == model.OtypeGtele)
					oldWorld := telescopeStoredWorld(t, f)
					var shipPtr *model.Ship
					var starPtr *model.Star
					var planetPtr *model.Planet
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
						shipPtr = f.state.ships[f.factory]
						starPtr = f.state.stars[f.target]
						planetPtr = f.state.planets[f.target][0]
					})
					target := "/AimTarget"
					if level == model.LevelPlan {
						target += "/Sight"
					}
					out := f.aim(t, target)
					if !strings.Contains(out, "Aimed at ") || strings.Contains(out, "Surveyed") != (where != "outside") || strings.Contains(out, "Too far") != (where == "outside") {
						t.Fatal("target result/output semantics changed")
					}
					assertTelescopeComplete(t, f, want)
					expectedWorld := oldWorld
					expectedWorld.Ships[f.factory] = want.StoredShip
					expectedWorld.Stars[f.target] = want.StoredStar
					expectedWorld.Planets[f.target][0] = want.StoredPlanet
					if !reflect.DeepEqual(expectedWorld, telescopeStoredWorld(t, f)) {
						t.Fatal("unrelated ships,planets,stars or orbit roots changed")
					}
					onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
						if f.state.ships[f.factory] != shipPtr || f.state.stars[f.target] != starPtr || f.state.planets[f.target][0] != planetPtr {
							t.Error("outer actor object identity replaced")
						}
					})
					for _, phase := range []string{"segment", "update", "restart"} {
						var e error
						switch phase {
						case "segment":
							e = f.state.TriggerSegment(ctxT(t, 10*time.Second))
						case "update":
							e = f.state.TriggerUpdate(ctxT(t, 10*time.Second))
						case "restart":
							f.restart(t)
						}
						if e != nil {
							t.Fatal(e)
						}
						assertActorStoreAgreement(t, f.actorEnrollmentFixture)
						x := f.snapshotAim(t)
						if !reflect.DeepEqual(x.StoredShip.Special.AimedAt, want.StoredShip.Special.AimedAt) || x.StoredStar.IsExplored(1) != want.StoredStar.IsExplored(1) || x.StoredPlanet.Info[0].Explored != want.StoredPlanet.Info[0].Explored {
							t.Fatal(phase, "successful aim/exploration erased")
						}
						assertFactoryOrbit(t, f.factoryFixture)
					}
				})
			}
		}
	}
}
func TestTelescopeActorFailureAndHalt(t *testing.T) {
	for _, stage := range []string{"telescope.before", "telescope.validated", "telescope.write:1", "telescope.write:2", "telescope.write:3", "telescope.before-commit", "telescope.commit-error", "telescope.after-commit", "fence"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", stage, panics), func(t *testing.T) {
				f := newTelescopeFixture(t, model.OtypeStele)
				before := f.snapshotAim(t)
				actor := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
				disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				hit := false
				if stage == "fence" {
					f.store.SetWriteFence(true)
					hit = true
				} else {
					f.store.SetMetaFault(func(op string) error {
						if op == stage {
							hit = true
							if panics {
								panic("private telescope fault")
							}
							return errors.New("private telescope fault")
						}
						return nil
					})
				}
				out := f.aim(t, "/AimTarget/Sight")
				f.store.SetWriteFence(false)
				if !hit || f.state.Mode() != ModeHalted || strings.Contains(out, "Aimed at") || strings.Contains(out, "Surveyed") || strings.Contains(f.log.text(), "private telescope") {
					t.Fatal("fault not reached or unsafe effects/diagnostics")
				}
				visible := stage == "telescope.commit-error" || stage == "telescope.after-commit"
				committed := stage == "telescope.after-commit"
				if visible {
					want := telescopeExpected(t, before, model.LevelPlan, true, false)
					x := f.snapshotAim(t)
					if !reflect.DeepEqual(x.StoredShip, want.StoredShip) || !reflect.DeepEqual(x.StoredStar, want.StoredStar) || !reflect.DeepEqual(x.StoredPlanet, want.StoredPlanet) {
						t.Fatal("visible transaction partial")
					}
				} else if disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("confirmed abort changed persistent records/allocator/meta")
				}
				if committed {
					assertActorStoreAgreement(t, f.actorEnrollmentFixture)
				} else if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("unconfirmed actor mutation installed")
				}
				after := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				if e := f.state.TriggerUpdate(ctxT(t, time.Second)); !errors.Is(e, ErrNotServing) {
					t.Fatal("halted turn admitted", e)
				}
				if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || after != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("halted stale shutdown flush", e)
				}
				if stage == "telescope.commit-error" {
					f.store.SetMetaFault(nil)
					f.open(t)
					if f.state.Mode() != ModeHalted {
						t.Fatal("uncertain handle reused as serving")
					}
				}
			})
		}
	}
}
func TestTelescopeActorValidationSelectorsAndAuthority(t *testing.T) {
	for _, text := range []string{"order #77 aim", "order #77 aim /AimTarget extra", "order * aim /AimTarget", "order = aim /AimTarget", "order #4294967373 aim /AimTarget", "order #0 aim /AimTarget", "order #77 aim /Missing", "order #77 aim #65536", "order #77 aim #4294967297", "order #77 aim #999"} {
		t.Run(text, func(t *testing.T) {
			f := newTelescopeFixture(t, model.OtypeStele)
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, text)
			if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || strings.Contains(out, "Aimed at") || f.state.Mode() != ModeServing {
				t.Fatal("invalid selector/target mutated world or claimed success")
			}
		})
	}
	for _, kind := range []string{"owner", "governor", "inactivegovernor", "inactive", "crew", "fuel"} {
		t.Run(kind, func(t *testing.T) {
			f := newTelescopeFixture(t, model.OtypeStele)
			g := 0
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[f.factory]
				switch kind {
				case "owner":
					sh.Owner = 2
				case "governor":
					g = 1
					f.state.races[0].Governor[1].Active = true
					sh.Governor = 2
				case "inactivegovernor":
					g = 1
				case "inactive":
					sh.Active = false
				case "crew":
					sh.Popn = 0
				case "fuel":
					sh.Fuel = math.Nextafter(f.state.cfg.FuelManeuver, 0)
				}
				if e := f.store.PutShip(sh); e != nil {
					t.Error(e)
				}
				if e := f.store.PutRace(f.state.races[0]); e != nil {
					t.Error(e)
				}
			})
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, "order #77 aim /AimTarget/Sight", r); e != nil {
				t.Fatal(e)
			}
			if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || strings.Contains(r.text(), "Aimed at") {
				t.Fatal("validation/authority rejection leaked changes")
			}
		})
	}
}
func TestTelescopeActorIdentityAndStalePreimages(t *testing.T) {
	for _, kind := range []string{"mapalias", "sourceorphan", "targetplanet", "targetstar", "sourceplanet", "racedrift", "orbithead"} {
		t.Run(kind, func(t *testing.T) {
			f := newTelescopeFixture(t, model.OtypeStele)
			switch kind {
			case "mapalias":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					copy := factoryCopyShip(t, f.state.ships[f.factory])
					copy.Number = 78
					copy.NextShip = f.state.ships[f.factory].NextShip
					f.state.ships[78] = copy
					f.state.planets[f.star][f.planet].Ships = 78
					f.state.ships[f.factory] = copy
					if e := f.store.PutShip(copy); e != nil {
						t.Error(e)
					}
					if e := f.store.PutPlanet(f.state.planets[f.star][f.planet]); e != nil {
						t.Error(e)
					}
				})
			case "sourceorphan":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.planets[f.star][f.planet].Ships = f.state.races[0].GovShip })
			default:
				switch kind {
				case "targetplanet":
					q, e := f.store.GetPlanet(f.target, 0)
					if e != nil {
						t.Fatal(e)
					}
					q.Info[0].Resource++
					if e := f.store.PutPlanet(q); e != nil {
						t.Fatal(e)
					}
				case "targetstar":
					q, e := f.store.GetStar(f.target)
					if e != nil {
						t.Fatal(e)
					}
					q.AP[0]++
					if e := f.store.PutStar(q); e != nil {
						t.Fatal(e)
					}
				case "sourceplanet", "orbithead":
					q, e := f.store.GetPlanet(f.star, f.planet)
					if e != nil {
						t.Fatal(e)
					}
					if kind == "orbithead" {
						q.Ships = 0
					} else {
						q.Info[0].Resource++
					}
					if e := f.store.PutPlanet(q); e != nil {
						t.Fatal(e)
					}
				case "racedrift":
					q, e := f.store.GetRace(1)
					if e != nil {
						t.Fatal(e)
					}
					q.Governor[0].Active = false
					if e := f.store.PutRace(q); e != nil {
						t.Fatal(e)
					}
				}
			}
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out := f.aim(t, "/AimTarget/Sight")
			if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || strings.Contains(out, "Aimed at") || f.state.Mode() != ModeHalted {
				t.Fatal("inconsistent identity/stale world was overwritten")
			}
		})
	}
}

type telescopeAllWorld struct {
	dumpWorld
	Ships    map[uint32]*model.Ship
	Races    []*model.Race
	StarData *model.StarData
	Aux      string
}

func telescopeStoredWorld(t *testing.T, f *telescopeFixture) telescopeAllWorld {
	t.Helper()
	ships, e := f.store.AllShips()
	if e != nil {
		t.Fatal(e)
	}
	races, e := f.store.AllRaces()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := f.store.GetStarData()
	if e != nil {
		t.Fatal(e)
	}
	var rows []any
	for _, key := range [][]byte{db.KeyCommit, db.KeySchedule, db.KeyUniverse, db.KeyMaintenance, []byte("enrollment/v1/placement/operator-request")} {
		x, e := f.store.GetMeta(key)
		if e != nil {
			t.Fatal(e)
		}
		rows = append(rows, x)
	}
	free, e := f.store.FreeShipNums()
	if e != nil {
		t.Fatal(e)
	}
	commod, e := f.store.FreeCommodNums()
	if e != nil {
		t.Fatal(e)
	}
	rows = append(rows, free, commod)
	b, e := json.Marshal(rows)
	if e != nil {
		t.Fatal(e)
	}
	return telescopeAllWorld{dumpStoredWorld(t, f.store), ships, races, sd, string(b)}
}

func TestTelescopeActorOtherTargetsLocationsAndAliasing(t *testing.T) {
	for _, kind := range []string{"universe", "self", "foreign", "deadGodTarget", "scope", "sourceplanet", "sourceStarOrbit", "universeOrbit", "carrier", "nestedCarrier"} {
		t.Run(kind, func(t *testing.T) {
			f := newTelescopeFixture(t, model.OtypeStele)
			target := "/AimTarget/Sight"
			if kind == "foreign" {
				enrollActor(t, f.actorEnrollmentFixture, actorEnrollmentRequest("account-2"))
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[f.factory]
				source := f.state.planets[f.star][f.planet]
				st := f.state.stars[f.star]
				switch kind {
				case "universe":
					target = "-"
				case "self":
					target = "#77"
				case "scope":
					target = "."
					f.state.dirs[0][0] = model.Directory{Level: model.LevelStar, StarNum: f.target}
				case "sourceplanet":
					target = fmt.Sprintf("/%s/%s", st.Name, st.PlanetNames[f.planet])
				case "foreign":
					// Use the real second empire's government ship. Never
					// overwrite it while constructing a visibility fixture.
					if f.state.races[1].GovShip != 78 {
						t.Error("unexpected deterministic fixture ship ticket")
						return
					}
					target = "#78"
				case "deadGodTarget":
					other := factoryCopyShip(t, sh)
					other.Number = 78
					other.Owner = 2
					other.NextShip = source.Ships
					other.Alive = false
					f.state.ships[78] = other
					if e := f.store.PutShip(other); e != nil {
						t.Error(e)
					}
					target = "#78"
				case "sourceStarOrbit":
					source.Ships = sh.NextShip
					sh.WhatOrbits = model.LevelStar
					sh.NextShip = st.Ships
					st.Ships = sh.Number
					sh.Docked = false
				case "universeOrbit":
					source.Ships = sh.NextShip
					sh.WhatOrbits = model.LevelUniv
					sh.NextShip = f.state.sdata.Ships
					f.state.sdata.Ships = sh.Number
					sh.Docked = false
					if e := f.store.PutStarData(f.state.sdata); e != nil {
						t.Error(e)
					}
				case "carrier", "nestedCarrier":
					carrier := factoryCopyShip(t, sh)
					carrier.Number = 78
					carrier.Type = model.StypeCargo
					carrier.Ships = 77
					carrier.NextShip = sh.NextShip
					carrier.Docked = false
					source.Ships = 78
					sh.NextShip = 0
					sh.WhatOrbits = model.LevelShip
					sh.WhatDest = model.LevelShip
					sh.DestShipNo = 78
					f.state.ships[78] = carrier
					if kind == "nestedCarrier" {
						parent := factoryCopyShip(t, carrier)
						parent.Number = 79
						parent.Ships = 78
						parent.NextShip = carrier.NextShip
						carrier.NextShip = 0
						carrier.WhatOrbits = model.LevelShip
						carrier.WhatDest = model.LevelShip
						carrier.DestShipNo = 79
						source.Ships = 79
						f.state.ships[79] = parent
						if e := f.store.PutShip(parent); e != nil {
							t.Error(e)
						}
					}
					if e := f.store.PutShip(carrier); e != nil {
						t.Error(e)
					}
				}
				for _, e := range []error{f.store.PutShip(sh), f.store.PutStar(st), f.store.PutPlanet(source)} {
					if e != nil {
						t.Error(e)
					}
				}
			})
			before := telescopeStoredWorld(t, f)
			out := f.aim(t, target)
			if !strings.Contains(out, "Aimed at") || f.state.Mode() != ModeServing {
				t.Fatal("valid source/target rule rejected", kind, out)
			}
			after := telescopeStoredWorld(t, f)
			expected := before
			want := factoryCopyShip(t, before.Ships[f.factory])
			want.Notified = false
			want.Fuel -= .3
			want.Mass -= .3 * .05
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				place := f.state.getplace(1, 0, target, true)
				aa := *want.Special.AimedAt
				aa.Level = place.Level
				aa.StarNum = place.StarNum
				aa.PlanetNum = place.PlanetNum
				aa.ShipNo = 0
				switch target {
				case "#77":
					aa.ShipNo = 77
				case "#78":
					aa.ShipNo = 78
				}
				want.Special.AimedAt = &aa
				if place.Level == model.LevelStar || place.Level == model.LevelPlan {
					expected.Stars[place.StarNum].SetExplored(1)
					if place.Level == model.LevelPlan {
						expected.Planets[place.StarNum][place.PlanetNum].Info[0].Explored = true
					}
				}
			})
			expected.Ships[f.factory] = want
			if !reflect.DeepEqual(after, expected) {
				t.Fatal("complete record result changed unrelated context or lost source/target alias")
			}
			assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		})
	}
}
func TestTelescopeActorFuelGovernorAndNoNewRestrictions(t *testing.T) {
	for _, kind := range []string{"gravityFree", "exactFuel", "damagedAndOff", "governor", "configuredRates", "robotic", "troopCrew"} {
		t.Run(kind, func(t *testing.T) {
			typ := model.OtypeStele
			if kind == "gravityFree" {
				typ = model.OtypeGtele
			}
			f := newTelescopeFixture(t, typ)
			g := 0
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				sh := f.state.ships[f.factory]
				switch kind {
				case "robotic":
					sh.MaxCrew = 0
					sh.Popn = 0
				case "troopCrew":
					sh.MaxCrew = 2
					sh.Troops = 2
					sh.Popn = 0
				case "gravityFree":
					sh.Fuel = 0
				case "exactFuel":
					sh.Fuel = f.state.cfg.FuelManeuver
				case "damagedAndOff":
					sh.Damage = 43
					sh.On = false
				case "governor":
					g = 1
					sh.Governor = 1
					f.state.races[0].Governor[1].Active = true
				case "configuredRates":
					f.state.cfg.FuelManeuver = .7
					f.state.cfg.MassFuel = .125
				}
				f.state.stars[f.star].AP[0] = 0
				for _, e := range []error{f.store.PutShip(sh), f.store.PutRace(f.state.races[0]), f.store.PutStar(f.state.stars[f.star])} {
					if e != nil {
						t.Error(e)
					}
				}
			})
			before := f.snapshotAim(t)
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, "order #77 aim /AimTarget/Sight", r); e != nil {
				t.Fatal(e)
			}
			x := f.snapshotAim(t)
			cost, mass := .3, .05
			if kind == "gravityFree" {
				cost = 0
			}
			if kind == "configuredRates" {
				cost = .7
				mass = .125
			}
			if !strings.Contains(r.text(), "Surveyed") || x.StoredShip.Fuel != before.StoredShip.Fuel-cost || x.StoredShip.Mass != before.StoredShip.Mass-cost*mass || x.StoredShip.Special.AimedAt.Intensity != 37 {
				t.Fatal("fuel/mass/governor/rule semantics changed")
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				if f.state.stars[f.star].AP[0] != 0 {
					t.Error("artificial AP charge")
				}
			})
		})
	}
}
func TestTelescopeActorEffectsAndPrivateWorldWaitForCommit(t *testing.T) {
	f := newTelescopeFixture(t, model.OtypeStele)
	before := f.snapshotAim(t)
	r := &recordingResponder{}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.store.SetMetaFault(func(op string) error {
		if op == "telescope.write:2" {
			if r.text() != "" || !reflect.DeepEqual(f.state.ships[f.factory], before.ActorShip) || !reflect.DeepEqual(f.state.stars[f.target], before.ActorStar) || !reflect.DeepEqual(f.state.planets[f.target][0], before.ActorPlanet) {
				t.Error("private actor proposal/output escaped before commit")
			}
			close(entered)
			<-release
		}
		return nil
	})
	done := make(chan error, 1)
	go func() {
		done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "order #77 aim /AimTarget/Sight", r)
	}()
	waitAcceptance(t, entered, "open telescope transaction")
	sh, e := f.store.GetShip(f.factory)
	if e != nil {
		t.Fatal(e)
	}
	st, e := f.store.GetStar(f.target)
	if e != nil {
		t.Fatal(e)
	}
	pl, e := f.store.GetPlanet(f.target, 0)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(sh, before.StoredShip) || !reflect.DeepEqual(st, before.StoredStar) || !reflect.DeepEqual(pl, before.StoredPlanet) || r.text() != "" {
		t.Error("reader observed transaction prefix")
	}
	unblock()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	if !strings.Contains(r.text(), "Aimed at") {
		t.Fatal("committed buffered effects missing")
	}
}
func TestTelescopeActorInstallationReadbackAndDeliveryFailures(t *testing.T) {
	for _, kind := range []string{"readback", "installpanic", "shipPointer", "starPointer", "planetPointer", "racePointer", "directory", "responseError", "responsePanic"} {
		t.Run(kind, func(t *testing.T) {
			f := newTelescopeFixture(t, model.OtypeStele)
			before := f.snapshotAim(t)
			want := telescopeExpected(t, before, model.LevelPlan, true, false)
			if kind == "readback" {
				f.store.SetMetaFault(func(op string) error {
					if op == "telescope.commit-error" || op == "command.readback" {
						return errors.New("private read failure")
					}
					return nil
				})
			} else if !strings.HasPrefix(kind, "response") {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					f.state.testHooks.beforeCommandInstall = func() {
						switch kind {
						case "installpanic":
							panic("private install")
						case "shipPointer":
							x := *f.state.ships[f.factory]
							f.state.ships[f.factory] = &x
						case "starPointer":
							x := *f.state.stars[f.target]
							f.state.stars[f.target] = &x
						case "planetPointer":
							x := *f.state.planets[f.target][0]
							f.state.planets[f.target][0] = &x
						case "racePointer":
							x := *f.state.races[0]
							f.state.races[0] = &x
						case "directory":
							f.state.dirs[0][0].Level = model.LevelUniv
						}
					}
				})
			}
			r := &recordingResponder{}
			if strings.HasPrefix(kind, "response") {
				bad := &failingFactoryResponder{panics: kind == "responsePanic"}
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "order #77 aim /AimTarget/Sight", bad); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "order #77 aim /AimTarget/Sight", r); e != nil {
					t.Fatal(e)
				}
			}
			x := f.snapshotAim(t)
			if f.state.Mode() != ModeHalted || !reflect.DeepEqual(x.StoredShip, want.StoredShip) || !reflect.DeepEqual(x.StoredStar, want.StoredStar) || !reflect.DeepEqual(x.StoredPlanet, want.StoredPlanet) || strings.Contains(r.text(), "Aimed at") || strings.Contains(f.log.text(), "private") {
				t.Fatal("outcome misclassified or durable records incomplete")
			}
			if strings.HasPrefix(kind, "response") {
				assertTelescopeComplete(t, f, want)
			} else if !reflect.DeepEqual(x.ActorShip, before.ActorShip) || !reflect.DeepEqual(x.ActorStar, before.ActorStar) || !reflect.DeepEqual(x.ActorPlanet, before.ActorPlanet) {
				t.Fatal("unverified or partial actor proposal installed")
			}
			class := "COMMITTED"
			if kind == "readback" {
				class = "UNCERTAIN"
			}
			if !strings.Contains(f.log.text(), class) {
				t.Fatal("failure lost commit outcome")
			}
			disk := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("unsafe shutdown flush")
			}
		})
	}
}
func TestTelescopeActorLegacyUsersBypassAndMixedSelectors(t *testing.T) {
	for _, kind := range []string{"mirror", "tractor", "weapon", "mixed", "direct", "cycle", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			f := newTelescopeFixture(t, model.OtypeStele)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				source := f.state.planets[f.star][f.planet]
				switch kind {
				case "mirror", "tractor", "weapon", "mixed":
					x := factoryCopyShip(t, f.state.ships[f.factory])
					x.Number = 78
					x.Type = model.StypeMirror
					switch kind {
					case "tractor":
						x.Type = model.OtypeTract
					case "weapon":
						x.Type = model.StypeBattle
						if !x.CanBombard(model.Shipdata) {
							t.Error("fixture weapon has no guns")
						}
					}
					x.Docked = false
					x.Special.AimedAt = nil
					x.NextShip = source.Ships
					source.Ships = x.Number
					f.state.ships[x.Number] = x
					for _, e := range []error{f.store.PutShip(x), f.store.PutPlanet(source)} {
						if e != nil {
							t.Error(e)
						}
					}
				case "cycle":
					f.state.ships[f.factory].NextShip = f.factory
				case "oversized":
					st := f.state.stars[f.target]
					st.Name = "AimTarget" + strings.Repeat("x", maxCommandEffectBytes+1)
					if e := f.store.PutStar(st); e != nil {
						t.Error(e)
					}
				}
			})
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			text := "order #77 aim /AimTarget/Sight"
			if kind == "mirror" || kind == "tractor" || kind == "weapon" {
				text = "order #78 aim /AimTarget"
			}
			if kind == "mixed" || kind == "cycle" {
				text = "order * aim /AimTarget"
			}
			r := &recordingResponder{}
			if kind == "direct" {
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.giveOrders(1, 0, []string{"aim", "/AimTarget/Sight"}, f.state.ships[f.factory], r) })
			} else {
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, text, r); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "mirror" || kind == "tractor" || kind == "weapon" {
				x, e := f.store.GetShip(78)
				if e != nil || !strings.Contains(r.text(), "Aimed at") || x.Special.AimedAt.Level != model.LevelStar {
					t.Fatal("adjacent aim behavior intercepted", e)
				}
				fuel := 9.7
				if kind == "tractor" {
					fuel = 10
				}
				if x.Fuel != fuel || f.snapshotAim(t).StoredStar.IsExplored(1) {
					t.Fatal("adjacent aim fuel/exploration behavior changed")
				}
			} else {
				if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || strings.Contains(r.text(), "Aimed at") {
					t.Fatal("legacy path or selector prefix bypassed staging")
				}
				if kind == "mixed" && strings.Contains(r.text(), "sp orbits") {
					t.Fatal("legacy header/prefix emitted before selector rejection")
				}
			}
		})
	}
}
func TestTelescopeActorModesEnrollmentAndAdmittedShutdown(t *testing.T) {
	for _, mode := range []Mode{ModeQuiesced, ModeHalted} {
		t.Run(mode.String(), func(t *testing.T) {
			f := newTelescopeFixture(t, model.OtypeStele)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.setMode(mode) })
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			_ = f.aim(t, "/AimTarget/Sight")
			if actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("nonserving write")
			}
		})
	}
	t.Run("enrollment", func(t *testing.T) {
		f := newTelescopeFixture(t, model.OtypeStele)
		_ = f.aim(t, "/AimTarget/Sight")
		enrollActor(t, f.actorEnrollmentFixture, actorEnrollmentRequest("account-2"))
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	})
	t.Run("shutdown", func(t *testing.T) {
		f := newTelescopeFixture(t, model.OtypeStele)
		hit, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		t.Cleanup(unblock)
		f.store.SetMetaFault(func(op string) error {
			if op == "telescope.write:1" {
				close(hit)
				<-release
			}
			return nil
		})
		done := make(chan error, 1)
		r := &recordingResponder{}
		go func() {
			done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "order #77 aim /AimTarget/Sight", r)
		}()
		waitAcceptance(t, hit, "admitted telescope write")
		stopped := make(chan error, 1)
		go func() { stopped <- f.state.Stop(ctxT(t, 10*time.Second)) }()
		unblock()
		if e := <-done; e != nil {
			t.Fatal(e)
		}
		if e := <-stopped; e != nil {
			t.Fatal(e)
		}
		f.open(t)
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		x := f.snapshotAim(t)
		if !x.StoredStar.IsExplored(1) || !x.StoredPlanet.Info[0].Explored || x.StoredShip.Special.AimedAt.Level != model.LevelPlan {
			t.Fatal("shutdown lost admitted confirmed aim")
		}
	})
}
