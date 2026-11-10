package game

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/test/acceptance"
)

func TestCR07FActorInitialEmpires(t *testing.T) {
	f := newEnrollmentActor(t)
	for _, caller := range []string{"operator", "account-2"} {
		req := actorEnrollmentRequest(caller)
		if caller == "account-2" {
			req.Spec.Attributes.Fighters = 7
			req.Spec.Attributes.IQ = 120
			req.Spec.Attributes.Mass = 1.25
			req.Spec.Attributes.Sexes = 3
			req.Spec.Attributes.Fertilize = .3
		}
		stars, e := f.store.AllStars()
		if e != nil {
			t.Fatal(e)
		}
		planets, e := f.store.AllPlanets()
		if e != nil {
			t.Fatal(e)
		}
		sd, e := f.store.GetStarData()
		if e != nil {
			t.Fatal(e)
		}
		placed := enrollActor(t, f, req).Placement
		hashes := enrollmentHashes(t)
		design := acceptance.Design{Spec: req.Spec, Player: placed.Player, Ship: placed.GovernmentShip, X: placed.CapitalX, Y: placed.CapitalY, God: req.God, RaceHash: hashes.RaceHash, GovernorHash: hashes.GovernorHash}
		var differences []string
		onEnrollmentActor(t, f, func() {
			s := f.state
			differences = acceptance.InitialDifferences(design, stars[placed.Star], planets[placed.Star][placed.Planet], s.races[placed.Player-1], s.stars[placed.Star], s.planets[placed.Star][placed.Planet], s.ships[placed.GovernmentShip])
			if !s.governed(s.races[placed.Player-1]) {
				differences = append(differences, "actor.governed")
			}
			if s.sdata.AP != sd.AP {
				differences = append(differences, "universe.AP")
			}
			dir := s.dirs[placed.Player-1][0]
			if dir.Level != model.LevelPlan || dir.StarNum != placed.Star || dir.PlanetNum != placed.Planet {
				differences = append(differences, "directory.home")
			}
		})
		if len(differences) > 0 {
			t.Fatal("C initial actor field mismatch:", differences)
		}
		r, e := f.store.GetRace(placed.Player)
		if e != nil {
			t.Fatal(e)
		}
		p, e := f.store.GetPlanet(placed.Star, placed.Planet)
		if e != nil {
			t.Fatal(e)
		}
		st, e := f.store.GetStar(placed.Star)
		if e != nil {
			t.Fatal(e)
		}
		sh, e := f.store.GetShip(placed.GovernmentShip)
		if e != nil {
			t.Fatal(e)
		}
		if differences = acceptance.InitialDifferences(design, stars[placed.Star], planets[placed.Star][placed.Planet], r, st, p, sh); len(differences) > 0 {
			t.Fatal("C initial store field mismatch:", differences)
		}
		assertActorStoreAgreement(t, f)
	}
}

func TestCR07FFinalSlotCompetition(t *testing.T) {
	f := newEnrollmentActor(t)
	onEnrollmentActor(t, f, func() {
		r := &model.Race{PlayerNum: 62, Name: "historical", Dissolved: true}
		f.state.races[61] = r
		if e := f.store.PutRace(r); e != nil {
			t.Error(e)
		}
	})
	start := make(chan struct{})
	type answer struct {
		result EnrollmentResult
		err    error
	}
	answers := make(chan answer, 2)
	for _, caller := range []string{"account-2", "account-3"} {
		go func(caller string) {
			<-start
			r, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest(caller))
			answers <- answer{r, e}
		}(caller)
	}
	close(start)
	success, full := 0, 0
	for range 2 {
		a := <-answers
		if a.err == nil {
			success++
			if a.result.Placement.Player != 63 {
				t.Fatal("last slot was not63")
			}
		} else {
			assertActorEnrollmentFailure(t, a.err, "SLOTS_FULL", db.EnrollmentNotCommitted)
			full++
		}
	}
	if success != 1 || full != 1 {
		t.Fatal("final-slot competition result counts")
	}
	ships, e := f.store.AllShips()
	if e != nil || len(ships) != 1 {
		t.Fatal("losing request consumed a ship", e)
	}
	found := 0
	for _, caller := range []string{"account-2", "account-3"} {
		rec, e := f.store.GetPlacement(caller, caller+"-request")
		if e != nil {
			t.Fatal(e)
		}
		if rec != nil {
			found++
		}
	}
	if found != 1 {
		t.Fatal("losing request persisted metadata")
	}
	assertActorStoreAgreement(t, f)
}

func TestCR07FEnrollmentQuiesceAndShutdown(t *testing.T) {
	for _, operation := range []string{"quiesce", "shutdown"} {
		t.Run(operation, func(t *testing.T) {
			f := newEnrollmentActor(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			f.store.SetMetaFault(func(stage string) error {
				if stage == "enrollment.before-commit" {
					close(entered)
					<-release
				}
				return nil
			})
			enrolled := make(chan error, 1)
			go func() {
				_, e := f.state.EnrollTrusted(ctxT(t, 15*time.Second), actorEnrollmentRequest("operator"))
				enrolled <- e
			}()
			waitAcceptance(t, entered, "enrollment transaction")
			finished := make(chan error, 1)
			go func() {
				if operation == "quiesce" {
					finished <- f.state.Quiesce(ctxT(t, 15*time.Second))
				} else {
					finished <- f.state.Stop(ctxT(t, 15*time.Second))
				}
			}()
			// Synchronize at the actual queue/draining boundary; no fixed delay assumes
			// an operation was admitted or completed.
			deadline := time.After(5 * time.Second)
			admitted := func() bool {
				return operation == "quiesce" && len(f.state.adminCh) > 0 || operation == "shutdown" && f.state.draining.Load()
			}
			for !admitted() {
				select {
				case <-deadline:
					unblock()
					t.Fatal("maintenance/shutdown not admitted")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			select {
			case e := <-finished:
				unblock()
				t.Fatal("operation overtook open enrollment transaction", e)
			default:
			}
			unblock()
			if e := <-enrolled; e != nil {
				t.Fatal(e)
			}
			if e := <-finished; e != nil {
				t.Fatal(e)
			}
			f.store.SetMetaFault(nil)
			rec, e := f.store.GetPlacement("operator", "operator-request")
			if e != nil || rec == nil {
				t.Fatal("placement lost at operation boundary", e)
			}
			r, e := f.store.GetRace(rec.Player)
			if e != nil || r.GovShip != rec.GovernmentShip {
				t.Fatal("race/ship association lost", e)
			}
			if operation == "quiesce" {
				if f.state.Mode() != ModeQuiesced {
					t.Fatal("quiesce not applied")
				}
				f.restart(t)
				if f.state.Mode() != ModeQuiesced {
					t.Fatal("maintenance not durable")
				}
				assertActorStoreAgreement(t, f)
			} else {
				commit, e := f.store.GetCommitRecord()
				if e != nil || commit.Kind != "shutdown" {
					t.Fatal("shutdown did not flush complete enrollment", e)
				}
				f.restart(t)
				assertActorStoreAgreement(t, f)
			}
		})
	}
}

func waitAcceptance(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("barrier not reached:", what)
	}
}

func TestCR07FTurnAdmission(t *testing.T) {
	t.Run("scheduled-timer", func(t *testing.T) {
		f := newEnrollmentActor(t)
		req := actorEnrollmentRequest("operator")
		enrollActor(t, f, req)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		t.Cleanup(unblock)
		onEnrollmentActor(t, f, func() {
			f.state.testHooks.beforeTurn = func(turnKind) { close(entered); <-release }
			f.state.sched.nextUpdate = f.state.now().Add(-time.Second)
			f.state.sched.nextSegment = f.state.now().Add(time.Hour)
		})
		waitAcceptance(t, entered, "scheduled timer turn")
		before := enrollmentDBSnapshot(t, f)
		if _, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("account-2")); !errors.Is(e, ErrNotServing) {
			t.Fatal("enrollment admitted during scheduled turn", e)
		}
		login := make(chan bool, 1)
		go func() {
			r, e := f.state.AuthenticateDual(ctxT(t, 15*time.Second), req.Spec.Name, "fixture-r", "fixture-g", 0)
			login <- e == nil && r.OK
		}()
		deadline := time.After(5 * time.Second)
		for len(f.state.adminCh) == 0 {
			select {
			case <-deadline:
				unblock()
				t.Fatal("login not queued")
			default:
				time.Sleep(time.Millisecond)
			}
		}
		select {
		case <-login:
			unblock()
			t.Fatal("login ran concurrently with turn")
		default:
		}
		if before != enrollmentDBSnapshot(t, f) {
			t.Fatal("turn body or rejected enrollment wrote before commit")
		}
		unblock()
		if !<-login {
			t.Fatal("queued login failed")
		}
		assertActorStoreAgreement(t, f)
		if f.state.Status().NumUpdates != 1 {
			t.Fatal("scheduled update not committed")
		}
	})
	t.Run("enrollment-queued-before-turn-start", func(t *testing.T) {
		f := newEnrollmentActor(t)
		enrollActor(t, f, actorEnrollmentRequest("operator"))
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		t.Cleanup(unblock)
		turnDone := make(chan error, 1)
		go func() {
			turnDone <- f.state.request(ctxT(t, 15*time.Second), opTest, func() error { close(entered); <-release; return f.state.runTurn(turnUpdate, "acceptance-boundary") })
		}()
		waitAcceptance(t, entered, "pre-turn actor barrier")
		enrollment := make(chan error, 1)
		go func() {
			_, e := f.state.EnrollTrusted(ctxT(t, 15*time.Second), actorEnrollmentRequest("account-2"))
			enrollment <- e
		}()
		deadline := time.After(5 * time.Second)
		for len(f.state.adminCh) == 0 {
			select {
			case <-deadline:
				unblock()
				t.Fatal("enrollment not queued")
			default:
				time.Sleep(time.Millisecond)
			}
		}
		unblock()
		if e := <-turnDone; e != nil {
			t.Fatal(e)
		}
		if e := <-enrollment; e != nil {
			t.Fatal(e)
		}
		assertActorStoreAgreement(t, f)
		if f.state.Status().Races != 2 || f.state.Status().NumUpdates != 1 {
			t.Fatal("serialized turn/enrollment result")
		}
	})
}
