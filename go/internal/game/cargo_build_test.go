package game

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

func newCargoBuildFixture(t *testing.T, free bool, level uint8) *factoryFixture {
	t.Helper()
	f := newFactoryBuildFixture(t, free)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		st := f.state.stars[f.star]
		pl := f.state.planets[f.star][f.planet]
		b := f.state.ships[77]
		r := f.state.races[0]
		r.Governor[0].Toggle.AutoLoad = true
		old := *b
		f.state.getshipDefaults(b, model.StypeCargo, r)
		b.Number = 77
		b.Owner = 1
		b.Alive = true
		b.Active = true
		b.Resource = 1000
		b.Popn = 100
		b.Fuel = 500
		b.Mass = 2000
		b.WhatOrbits = level
		b.StOrbits = f.star
		b.PNumOrbits = f.planet
		b.XPos = old.XPos
		b.YPos = old.YPos
		b.NextShip = old.NextShip
		switch level {
		case model.LevelStar:
			pl.Ships = old.NextShip
			b.NextShip = st.Ships
			st.Ships = 77
		case model.LevelUniv:
			pl.Ships = old.NextShip
			b.NextShip = f.state.sdata.Ships
			f.state.sdata.Ships = 77
		}
		f.state.dirs[0][0] = model.Directory{Level: model.LevelShip, ShipNo: 77, StarNum: f.star, PlanetNum: f.planet}
		for _, e := range []error{f.store.PutRace(r), f.store.PutShip(b), f.store.PutPlanet(pl), f.store.PutStar(st), f.store.PutStarData(f.state.sdata)} {
			if e != nil {
				t.Error(e)
			}
		}
	})
	return f
}

func cargoBuildInput(t *testing.T, f *factoryFixture) db.CargoCarrierBuild {
	t.Helper()
	var in db.CargoCarrierBuild
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		_, x, pending, e := f.state.cargoBuildStage(1, 0)
		if e != nil || pending {
			t.Errorf("prepare: pending %v, error %v", pending, e)
		}
		in = x
	})
	return in
}

func cargoBuildExpected(t *testing.T, before map[string]string, in db.CargoCarrierBuild) map[string]string {
	t.Helper()
	out := maps.Clone(before)
	put := func(bucket, key []byte, v any) { out[fmt.Sprintf("%x/%x", bucket, key)] = factoryBuildJSON(t, v) }
	c := in.Child
	put(db.BucketShips, db.ShipKey(c.Number), c)
	b := *in.Input.Ships[0]
	b.Resource = 646
	b.Popn = 70
	b.Fuel = 0
	put(db.BucketShips, db.ShipKey(77), &b)
	switch c.WhatOrbits {
	case model.LevelPlan:
		pl := *in.Input.Planet
		pl.Ships = c.Number
		put(db.BucketPlanets, db.PlanetKey(pl.StarNum, pl.PlanetNum), &pl)
	case model.LevelStar:
		st := *in.Input.Star
		st.Ships = c.Number
		put(db.BucketStars, db.StarKey(st.Number), &st)
	case model.LevelUniv:
		d := *in.Input.Universe
		d.Ships = c.Number
		put(db.BucketSData, []byte("global"), &d)
	}
	if in.Allocation.FreeKey != 0 {
		delete(out, fmt.Sprintf("%x/%016x", db.BucketShipFree, in.Allocation.FreeKey))
	}
	return out
}

func TestCargoBuildDispatch(t *testing.T) {
	for _, level := range []uint8{model.LevelPlan, model.LevelStar, model.LevelUniv} {
		for _, free := range []bool{true, false} {
			t.Run(fmt.Sprintf("%d/free=%v", level, free), func(t *testing.T) {
				f := newCargoBuildFixture(t, free, level)
				in := cargoBuildInput(t, f)
				want := cargoBuildExpected(t, factoryBuildRecords(t, f), in)
				id := uint32(78)
				if free {
					id = 70
				}
				reg := &factoryBuildRegistry{}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					builder := f.state.ships[77]
					reg.at = func(string) {
						var head uint32
						switch level {
						case model.LevelPlan:
							head = f.state.planets[f.star][f.planet].Ships
						case model.LevelStar:
							head = f.state.stars[f.star].Ships
						case model.LevelUniv:
							head = f.state.sdata.Ships
						}
						if head != id {
							t.Error("output before orbit installation")
						}
						b, e := f.store.GetShip(77)
						c, e2 := f.store.GetShip(id)
						if e != nil || e2 != nil || c == nil || b == nil || builder != f.state.ships[77] || !reflect.DeepEqual(b, builder) || !reflect.DeepEqual(c, f.state.ships[id]) || c.BuildCost != 354 || c.Popn != 30 || c.Fuel != 500 || c.NextShip != 77 || c.Type != model.StypeCarrier {
							t.Error("output before complete installation or wrong child")
						}
					}
					f.state.SetRegistry(reg)
				})
				msg := f.command(t, "build X")
				if !strings.Contains(reg.all(), "built at a cost of 354") || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatal("partial or incorrect build", msg)
				}
			})
		}
	}
}

func TestCargoBuildScheduledPersistence(t *testing.T) {
	f := newCargoBuildFixture(t, false, model.LevelPlan)
	f.command(t, "build X")
	check := func() {
		t.Helper()
		assertActorStoreAgreement(t, f.actorEnrollmentFixture)
		b, e := f.store.GetShip(77)
		c, ce := f.store.GetShip(78)
		pl, pe := f.store.GetPlanet(f.star, f.planet)
		if e != nil || ce != nil || pe != nil || b == nil || c == nil || b.Resource != 646 || b.Popn != 70 || b.Fuel != 0 || c.Popn != 30 || c.Fuel != 500 {
			t.Fatalf("lost construction debits or complement: builder=%+v child=%+v", b, c)
		}
		seen := map[uint32]bool{}
		for id := pl.Ships; id != 0; {
			sh, e := f.store.GetShip(id)
			if e != nil || sh == nil || seen[id] {
				t.Fatal("broken orbit", id)
			}
			seen[id] = true
			id = sh.NextShip
		}
		if !seen[77] || !seen[78] {
			t.Fatal("lost orbit membership")
		}
	}
	check()
	for _, update := range []bool{false, true} {
		before := f.state.Status()
		onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
			when := f.state.sched.nextSegment.Add(time.Nanosecond)
			if update {
				when = f.state.sched.nextUpdate.Add(time.Nanosecond)
			}
			f.state.now = func() time.Time { return when }
		})
		deadline := time.Now().Add(10 * time.Second)
		for {
			st := f.state.Status()
			if (!update && st.NumSegments > before.NumSegments) || (update && st.NumUpdates > before.NumUpdates) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal(st)
			}
			time.Sleep(time.Millisecond)
		}
		check()
	}
	f.restart(t)
	check()
}

func TestCargoBuildFailures(t *testing.T) {
	for _, stage := range []string{"before", "validated", "write:1", "write:2", "write:3", "write:4", "before-commit", "commit-error", "after-commit"} {
		t.Run(stage, func(t *testing.T) {
			f := newCargoBuildFixture(t, true, model.LevelPlan)
			before := factoryBuildRecords(t, f)
			in := cargoBuildInput(t, f)
			want := before
			if stage == "commit-error" || stage == "after-commit" {
				want = cargoBuildExpected(t, before, in)
			}
			reg := &fakeRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
			actorBefore := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
			hit := false
			f.store.SetMetaFault(func(op string) error {
				if op == "cargo-build."+stage {
					hit = true
					return errors.New("injected")
				}
				return nil
			})
			f.command(t, "build X")
			if !hit || f.state.Mode() != ModeHalted || strings.Contains(reg.all(), "built at a cost") || !reflect.DeepEqual(want, factoryBuildRecords(t, f)) || f.store.CommandWriteUncertain() != (stage == "commit-error") {
				t.Fatal("unexpected failure classification, writes or output")
			}
			f.store.SetMetaFault(nil)
			if stage == "after-commit" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			} else if actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actorBefore {
				t.Fatal("unconfirmed build installed actor changes")
			}
			if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Fatal("shutdown overwrote result")
			}
		})
	}
}

func TestCargoBuildConflicts(t *testing.T) {
	for _, which := range []string{"ticket", "builder", "proposal", "pending", "governor"} {
		t.Run(which, func(t *testing.T) {
			f := newCargoBuildFixture(t, false, model.LevelPlan)
			in := cargoBuildInput(t, f)
			switch which {
			case "ticket":
				if e := f.store.DeleteShip(65); e != nil {
					t.Fatal(e)
				}
			case "builder":
				b := *in.Input.Ships[0]
				b.Resource--
				if e := f.store.PutShip(&b); e != nil {
					t.Fatal(e)
				}
			case "proposal":
				in.Child.BuildCost--
			case "pending", "governor":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					if which == "pending" {
						f.state.ships[77].Resource--
					} else {
						f.state.races[0].Governor[0].Active = false
						if e := f.store.PutRace(f.state.races[0]); e != nil {
							t.Error(e)
						}
					}
				})
				before := factoryBuildRecords(t, f)
				msg := f.command(t, "build X")
				if !reflect.DeepEqual(before, factoryBuildRecords(t, f)) || f.state.Mode() != ModeServing || msg == "" {
					t.Fatal("admission wrote or halted", msg)
				}
				return
			}
			before := factoryBuildRecords(t, f)
			o, e := f.store.CommitCargoCarrier(in)
			if e == nil || o != db.CommandNotCommitted || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) {
				t.Fatal("conflict committed", o, e)
			}
		})
	}
}
func TestCargoBuildRealProcessCrash(t *testing.T) {
	for _, free := range []bool{false, true} {
		for _, stage := range []string{"before", "write:1", "write:2", "write:3", "write:4", "before-commit", "after-commit", "preinstall", "installed"} {
			t.Run(fmt.Sprintf("free=%v/%s", free, stage), func(t *testing.T) {
				f := newCargoBuildFixture(t, free, model.LevelPlan)
				in := cargoBuildInput(t, f)
				if e := f.state.Stop(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
				sched, e := f.store.GetSchedule()
				if e != nil {
					t.Fatal(e)
				}
				before := factoryBuildRecords(t, f)
				want := before
				if stage == "after-commit" || stage == "preinstall" || stage == "installed" {
					want = cargoBuildExpected(t, before, in)
				}
				killConstructionAt(t, f, "cargo-build."+stage, sched.SavedAt)
				if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
					t.Fatal("partial or unexpected recovered state", stage)
				}
			})
		}
	}
}

func TestCargoBuildLegacyDesignAndLoading(t *testing.T) {
	for _, god := range []bool{false, true} {
		for _, autoload := range []bool{false, true} {
			t.Run(fmt.Sprintf("god=%v/autoload=%v", god, autoload), func(t *testing.T) {
				f := newCargoBuildFixture(t, false, model.LevelPlan)
				var want model.Ship
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					r := f.state.races[0]
					r.God = god
					r.Governor[0].Toggle.AutoLoad = autoload
					if e := f.store.PutRace(r); e != nil {
						t.Error(e)
					}
					f.state.getshipDefaults(&want, model.StypeCarrier, r)
					b := *f.state.ships[77]
					want.Number = 78
					want.Tech = r.Tech
					want.Class = "Standard"
					want.WhatOrbits = b.WhatOrbits
					want.StOrbits = b.StOrbits
					want.PNumOrbits = b.PNumOrbits
					want.NextShip = 77
					want.XPos = b.XPos
					want.YPos = b.YPos
					var crew int
					var fuel float64
					if autoload && !god {
						autoloadAtShip(1, &want, &b, &crew, &fuel)
					}
					f.state.initializeNewShip(1, 0, r, &want, fuel, crew)
				})
				msg := f.command(t, "build X 1")
				got, e := f.store.GetShip(78)
				if e != nil || !reflect.DeepEqual(&want, got) {
					t.Fatalf("child differs from legacy design and initialization: %s %v\nwant %+v\n got %+v", msg, e, want, got)
				}
				b, e := f.store.GetShip(77)
				if e != nil || b.Resource != 1000-want.BuildCost {
					t.Fatal("wrong resource cost")
				}
				if (!autoload || god) && (b.Popn != 100 || b.Fuel != 500) {
					t.Fatal("unexpected passenger debit")
				}
			})
		}
	}
}

func TestCargoBuildLateWriter(t *testing.T) {
	f := newCargoBuildFixture(t, false, model.LevelPlan)
	var after map[string]string
	f.store.SetMetaFault(func(op string) error {
		if op != "cargo-build.before" {
			return nil
		}
		if e := f.store.DeleteShip(65); e != nil {
			return e
		}
		after = commandFixtureRecords(t, stringMustReadFactoryBuild(t, f.path))
		return nil
	})
	f.command(t, "build X")
	f.store.SetMetaFault(nil)
	if f.state.Mode() != ModeHalted || after == nil || !reflect.DeepEqual(after, factoryBuildRecords(t, f)) {
		t.Fatal("late change to free list was not fenced")
	}
}

func TestCargoBuildAuthorityAndLocation(t *testing.T) {
	for _, scenario := range []string{"foreign owner", "wrong governor", "assigned governor", "broken actor orbit"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCargoBuildFixture(t, false, model.LevelPlan)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				s := f.state
				s.dirs[0][1] = s.dirs[0][0]
				b := s.ships[77]
				b.Governor = 1
				r := s.races[0]
				r.Governor[1].Active = true
				switch scenario {
				case "foreign owner":
					b.Owner = 2
				case "wrong governor":
					b.Governor = 0
				}
				if e := f.store.PutShip(b); e != nil {
					t.Error(e)
				}
				if e := f.store.PutRace(r); e != nil {
					t.Error(e)
				}
				if scenario == "broken actor orbit" {
					b.NextShip = b.Number
				}
			})
			before := factoryBuildRecords(t, f)
			reply := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 1, "build X", reply); e != nil {
				t.Fatal(e)
			}
			child, e := f.store.GetShip(78)
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "assigned governor" {
				if child == nil || child.Governor != 1 || child.Owner != 1 || f.state.Mode() != ModeServing {
					t.Fatal("authorized build failed", reply.text())
				}
				return
			}
			if child != nil || !reflect.DeepEqual(before, factoryBuildRecords(t, f)) || (scenario == "broken actor orbit") != (f.state.Mode() == ModeHalted) || reply.text() == "" {
				t.Fatal("wrong admission outcome", reply.text())
			}
		})
	}
}

func TestCargoBuildLateWitnessWriter(t *testing.T) {
	for _, witness := range []string{"race", "orbit-ship"} {
		t.Run(witness, func(t *testing.T) {
			f := newCargoBuildFixture(t, true, model.LevelPlan)
			in := cargoBuildInput(t, f).Input
			want := factoryBuildRecords(t, f)
			var change func() error
			switch witness {
			case "race":
				r := *in.Race
				r.Tech++
				want[fmt.Sprintf("%x/%x", db.BucketRaces, db.RaceKey(in.Player))] = factoryBuildJSON(t, &r)
				change = func() error { return f.store.PutRace(&r) }
			case "orbit-ship":
				if len(in.Ships) < 2 || in.Ships[0].Number != in.Scope.ShipNo || in.Ships[0].NextShip != in.Ships[1].Number {
					t.Fatal("fixture needs a linked ship after the builder")
				}
				sh := *in.Ships[1]
				sh.Name += "-renamed"
				want[fmt.Sprintf("%x/%x", db.BucketShips, db.ShipKey(sh.Number))] = factoryBuildJSON(t, &sh)
				change = func() error { return f.store.PutShip(&sh) }
			}

			reg := &fakeRegistry{}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
			wrote := false
			f.store.SetMetaFault(func(op string) error {
				if op != "cargo-build.before" {
					return nil
				}
				err := change()
				wrote = err == nil
				return err
			})
			reply := f.command(t, "build X")
			f.store.SetMetaFault(nil)
			if !wrote {
				t.Fatal("external write did not complete")
			}
			if !reflect.DeepEqual(want, factoryBuildRecords(t, f)) {
				t.Errorf("construction changed durable records after late %s write", witness)
			}
			if f.state.Mode() != ModeHalted || !strings.Contains(reply, string(db.CommandNotCommitted)) || f.store.CommandWriteUncertain() {
				t.Errorf("expected confirmed rejection and HALT, got %s: %q", f.state.Mode(), reply)
			}
			if strings.Contains(reg.all(), "gov:") || strings.Contains(reply, "built at a cost") {
				t.Errorf("construction output escaped: %q %q", reg.all(), reply)
			}
		})
	}
}
