package game

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

// The fixture supplies the cruiser/target; all protection/navigation orders and
// factory construction use actual actor dispatch. Retirement below is explicitly
// fixture-only so both append and free-ID cases can have identical advisory data.
func factoryAdvisoryFixture(t *testing.T, owner int, free bool, navigation bool) (*factoryFixture, uint32) {
	t.Helper()
	if owner < 1 || owner > 2 {
		t.Fatal("fixture owner")
	}
	f := newFactoryBuildFixture(t, false)
	ownerID := uint8(1)
	if owner == 2 {
		ownerID = 2
		enrollActor(t, f.actorEnrollmentFixture, actorEnrollmentRequest("account-2"))
	}
	n := uint32(80)
	if free {
		n = 70
	}
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		pl := f.state.planets[f.star][f.planet]
		sh := &model.Ship{}
		f.state.getshipDefaults(sh, model.StypeCruiser, f.state.races[owner-1])
		sh.Number = 79
		sh.Owner = ownerID
		sh.Race = ownerID
		sh.Alive = true
		sh.Active = true
		sh.Popn = 1
		sh.WhatOrbits = model.LevelPlan
		sh.StOrbits = f.star
		sh.PNumOrbits = f.planet
		sh.XPos = f.state.stars[f.star].XPos + pl.XPos
		sh.YPos = f.state.stars[f.star].YPos + pl.YPos
		sh.NextShip = pl.Ships
		pl.Ships = 79
		f.state.ships[79] = sh
		f.state.dirs[owner-1][0] = model.Directory{Level: model.LevelPlan, StarNum: f.star, PlanetNum: f.planet}
		if navigation {
			q := factoryCopyShip(t, sh)
			q.Number = n
			q.NextShip = pl.Ships
			pl.Ships = n
			f.state.ships[n] = q
			if e := f.store.PutShip(q); e != nil {
				t.Error(e)
			}
		} else if free {
			if e := f.store.DeleteShip(n); e != nil {
				t.Error(e)
			}
		}
		for _, e := range []error{f.store.PutShip(sh), f.store.PutPlanet(pl)} {
			if e != nil {
				t.Error(e)
			}
		}
	})
	return f, n
}
func factoryAdvisoryOrder(t *testing.T, f *factoryFixture, owner int, cmd string) string {
	t.Helper()
	r := &recordingResponder{}
	if e := f.state.HandleCommand(ctxT(t, 10*time.Second), owner, 0, cmd, r); e != nil {
		t.Fatal(e)
	}
	return r.text()
}
func factoryRetireNavigationTarget(t *testing.T, f *factoryFixture, n uint32, free bool) {
	t.Helper()
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		pl := f.state.planets[f.star][f.planet]
		target := f.state.ships[n]
		f.state.removeShipPlan(pl, target)
		delete(f.state.ships, n)
		if e := f.store.DeleteShip(n); e != nil {
			t.Error(e)
		}
		if !free {
			got, e := f.store.AllocShipNum()
			if e != nil || got != n {
				t.Error("fixture free-pop", got, e)
			}
		}
		if e := f.store.PutPlanet(pl); e != nil {
			t.Error(e)
		}
		for id := pl.Ships; id != 0; id = f.state.ships[id].NextShip {
			if e := f.store.PutShip(f.state.ships[id]); e != nil {
				t.Error(e)
			}
		}
	})
}
func TestPlanetFactoryBuildAdvisoryOrdersDoNotReserveIDs(t *testing.T) {
	for _, owner := range []int{1, 2} {
		for _, free := range []bool{false, true} {
			for _, segments := range []int{0, 3} {
				for _, order := range []string{"protect-active", "protect-off", "destination", "destination-off"} {
					t.Run(fmt.Sprintf("owner=%d/free=%t/segments=%d/%s", owner, free, segments, order), func(t *testing.T) {
						nav := strings.HasPrefix(order, "destination")
						f, n := factoryAdvisoryFixture(t, owner, free, nav)
						if nav {
							factoryAdvisoryOrder(t, f, owner, fmt.Sprintf("order #79 destination #%d", n))
							if order == "destination-off" {
								factoryAdvisoryOrder(t, f, owner, "order #79 destination /")
							}
							factoryRetireNavigationTarget(t, f, n, free)
						} else {
							factoryAdvisoryOrder(t, f, owner, fmt.Sprintf("order #79 protect #%d", n))
							if order == "protect-off" {
								factoryAdvisoryOrder(t, f, owner, "order #79 protect 0")
							}
						}
						for i := 0; i < segments; i++ {
							if e := f.state.TriggerSegment(ctxT(t, 10*time.Second)); e != nil {
								t.Fatal(e)
							}
						}
						old, e := f.store.GetShip(79)
						if e != nil {
							t.Fatal(e)
						}
						if nav {
							if old.DestShipNo != n {
								t.Fatal("navigation command did not retain target", old.DestShipNo, n)
							}
						} else {
							if uint32(old.Protect.Ship) != n || old.Protect.On != (order == "protect-active") {
								t.Fatal("protection command not exercised")
							}
						}
						before := factoryBuildRecords(t, f)
						text := f.command(t, "build F 0,0")
						sh, e := f.store.GetShip(n)
						if e != nil || sh == nil || sh.Type != model.OtypeFactory || sh.Owner != 1 || f.state.Mode() != ModeServing || strings.Contains(text, "deferred") {
							t.Fatalf("advisory orders must not reserve construction ID %d: %q ship=%t err=%v", n, text, sh != nil, e)
						}
						after, e := f.store.GetShip(79)
						if e != nil || !reflect.DeepEqual(old, after) {
							t.Fatal("construction rewrote advisory ship")
						}
						if reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
							t.Fatal("command rejected instead of building")
						}
						assertActorStoreAgreement(t, f.actorEnrollmentFixture)
					})
				}
			}
		}
	}
}
