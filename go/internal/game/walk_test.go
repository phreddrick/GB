package game

import (
	"fmt"
	"github.com/phreddrick/GB/go/internal/model"
	"reflect"
	"testing"
	"time"
)

func newWalkFixture(t *testing.T) *cargoFixture {
	t.Helper()
	f := newCargoFixture(t, false)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		sh := f.state.ships[f.factory]
		sh.Type = model.OtypeAFV
		sh.Popn = 1
		sh.Fuel = 10
		sh.Mass = 100
		sh.Speed = 0
		pl := f.state.planets[f.star][f.planet]
		x := (int(sh.LandX) + 1) % int(pl.MaxX)
		y := int(sh.LandY)
		sect := pl.SectorAt(x, y)
		if sect.Owner != 0 || sect.Popn != 0 || sect.Troops != 0 {
			t.Error("target fixture is not empty neutral")
		}
		if sect.Condition != model.SectorLand || f.state.races[0].Likes[model.SectorLand] == 0 {
			t.Error("fixture terrain not walkable")
		}
		f.state.stars[f.star].AP[0] = 50
		if e := f.store.PutStar(f.state.stars[f.star]); e != nil {
			t.Error(e)
		}
		if e := f.store.PutShip(sh); e != nil {
			t.Error(e)
		}
	})
	return f
}

func TestWalkDurableShipAndAP(t *testing.T) {
	f := newWalkFixture(t)
	before := f.snapshot(t)
	f.command(t, fmt.Sprintf("walk #%d 6", f.factory))
	after := f.snapshot(t)
	st, e := f.store.GetStar(f.star)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("actor position=%d,%d fuel=%g mass=%g AP=%d; stored position=%d,%d fuel=%g mass=%g AP=%d", after.ActorShip.LandX, after.ActorShip.LandY, after.ActorShip.Fuel, after.ActorShip.Mass, after.Star.AP[0], after.StoredShip.LandX, after.StoredShip.LandY, after.StoredShip.Fuel, after.StoredShip.Mass, st.AP[0])
	if !reflect.DeepEqual(after.ActorShip, after.StoredShip) || after.Star.AP[0] != st.AP[0] || after.ActorShip.LandX == before.ActorShip.LandX || after.ActorShip.Fuel != before.ActorShip.Fuel-1 || after.Star.AP[0] != before.Star.AP[0]-1 {
		t.Fatal("noncombat walk did not persist complete ship and AP")
	}
}

func TestWalkScheduledTurnsAndRestart(t *testing.T) {
	f := newWalkFixture(t)
	f.command(t, "walk #77 6")
	f.restart(t)
	for _, update := range []bool{false, true} {
		before := f.state.Status()
		seenIncome := false
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			when := f.state.sched.nextSegment.Add(time.Nanosecond)
			if update {
				when = f.state.sched.nextUpdate.Add(time.Nanosecond)
			}
			f.state.now = func() time.Time { return when }
			if update {
				f.state.testHooks.midTurn = func(up bool) {
					if up {
						seenIncome = true
						if f.state.stars[f.star].AP[0] != 49 {
							t.Error("walk AP debit lost before legitimate update income")
						}
					}
				}
			}
		})
		deadline := time.Now().Add(10 * time.Second)
		for {
			st := f.state.Status()
			if ((!update && st.NumSegments > before.NumSegments) || (update && st.NumUpdates > before.NumUpdates)) && f.state.Mode() == ModeServing {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("scheduled turn failed", st)
			}
			time.Sleep(time.Millisecond)
		}
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		sh, e := f.store.GetShip(f.factory)
		if e != nil || sh.LandX != 1 || sh.Fuel != 9 {
			t.Fatal("scheduled turn reverted walk", e)
		}
		st, e := f.store.GetStar(f.star)

		if e != nil || (!update && st.AP[0] != 49) || (update && st.AP[0] < 49) {
			t.Fatal("AP not preserved", e)
		}
		if update {
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				if !seenIncome {
					t.Error("update hook not exercised")
				}
				f.state.testHooks.midTurn = nil
			})
		}

	}
	f.restart(t)
	sh, e := f.store.GetShip(f.factory)
	if e != nil || sh.LandX != 1 || sh.Fuel != 9 {
		t.Fatal("shutdown lost walk", e)
	}
}
