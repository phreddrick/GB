package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
)

type dumpWorld struct {
	Stars   []*model.Star
	Planets map[uint8]map[uint8]*model.Planet
}
type dumpFixture struct {
	*actorEnrollmentFixture
	home uint8
}

func newDumpFixture(t *testing.T) *dumpFixture {
	t.Helper()
	f := newEnrollmentActor(t)
	a := enrollActor(t, f, actorEnrollmentRequest("operator")).Placement
	enrollActor(t, f, actorEnrollmentRequest("account-2"))
	d := &dumpFixture{f, a.Star}
	onEnrollmentActor(t, f, func() {
		for n, st := range f.state.stars {
			if st == nil {
				continue
			}
			if n < 3 {
				st.SetExplored(1)
			} else {
				st.Explored &^= 1
			}
			if !st.IsInhabited(2) {
				st.Explored &^= 2
			}
			if st.Number == a.Star {
				st.AP[0] = 50
			}
			if e := f.store.PutStar(st); e != nil {
				t.Error(e)
			}
			for j, pl := range f.state.planets[st.Number] {
				pl.Info[0].Explored = n < 3 && j == 0
				if pl.Info[1].NumSectsOwned == 0 {
					pl.Info[1].Explored = false
				}
				if e := f.store.PutPlanet(pl); e != nil {
					t.Error(e)
				}
			}
		}
	})
	return d
}
func (f *dumpFixture) command(t *testing.T, text string) string {
	t.Helper()
	r := &recordingResponder{}
	if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, text, r); e != nil {
		t.Fatal(e)
	}
	return r.text()
}
func dumpActorWorld(t *testing.T, f *actorEnrollmentFixture) dumpWorld {
	t.Helper()
	var w dumpWorld
	onEnrollmentActor(t, f, func() {
		w = dumpWorld{Stars: make([]*model.Star, len(f.state.stars)), Planets: map[uint8]map[uint8]*model.Planet{}}
		for n, st := range f.state.stars {
			if st != nil {
				x := *st
				w.Stars[n] = &x
			}
		}
		for st, ps := range f.state.planets {
			w.Planets[st] = map[uint8]*model.Planet{}
			for n, p := range ps {
				w.Planets[st][n] = copyCommandPlanet(p)
			}
		}
	})
	return w
}
func dumpStoredWorld(t *testing.T, s *db.Store) dumpWorld {
	t.Helper()
	st, e := s.AllStars()
	if e != nil {
		t.Fatal(e)
	}
	ps, e := s.AllPlanets()
	if e != nil {
		t.Fatal(e)
	}
	return dumpWorld{st, ps}
}
func dumpWant(before dumpWorld, home uint8, target int, shared ...uint8) dumpWorld {
	// Independent expected field changes; input is already a private snapshot.
	for _, n := range shared {
		before.Stars[n].SetExplored(target)
		for _, p := range before.Planets[n] {
			if p.Info[0].Explored {
				p.Info[target-1].Explored = true
			}
		}
	}
	before.Stars[home].AP[0]--
	return before
}
func dumpUnchangedAux(t *testing.T, s *db.Store) string {
	t.Helper()
	var records []any
	for _, key := range [][]byte{db.KeyCommit, db.KeySchedule, db.KeyMaintenance, db.KeyUniverse, []byte("enrollment/v1/placement/operator-request"), []byte("enrollment/v1/placement/account-2-request")} {
		b, e := s.GetMeta(key)
		if e != nil {
			t.Fatal(e)
		}
		records = append(records, b)
	}
	ships, e := s.AllShips()
	if e != nil {
		t.Fatal(e)
	}
	records = append(records, ships)
	races, e := s.AllRaces()
	if e != nil {
		t.Fatal(e)
	}
	records = append(records, races)
	free, e := s.FreeShipNums()
	if e != nil {
		t.Fatal(e)
	}
	records = append(records, free)
	commodities, e := s.FreeCommodNums()
	if e != nil {
		t.Fatal(e)
	}
	records = append(records, commodities)
	sd, e := s.GetStarData()
	if e != nil {
		t.Fatal(e)
	}
	records = append(records, sd)
	b, e := json.Marshal(records)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestCR17BDumpGameplayAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		target     int
		stars      []uint8
	}{
		{"one star", "2 1", 2, []uint8{0}}, {"all known", "2", 2, []uint8{0, 1, 2}}, {"AP star not selected", "2 3", 2, []uint8{2}}, {"duplicate stars", "2 1 1", 2, []uint8{0}}, {"self dump", "1 1", 1, []uint8{0}}, {"unexplored selected", "2 4", 2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDumpFixture(t)
			want := dumpWant(dumpActorWorld(t, f.actorEnrollmentFixture), f.home, tc.target, tc.stars...)
			aux := dumpUnchangedAux(t, f.store)
			var stars []*model.Star
			var planets []*model.Planet
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				stars = append(stars, f.state.stars...)
				for n := 0; n < 2; n++ {
					planets = append(planets, f.state.planets[f.home][uint8(n)])
				}
			})
			if out := f.command(t, "dump "+tc.args); !strings.Contains(out, "Exploration Data transferred") {
				t.Fatal("dump failed", out)
			}
			if !reflect.DeepEqual(dumpActorWorld(t, f.actorEnrollmentFixture), want) || !reflect.DeepEqual(dumpStoredWorld(t, f.store), want) {
				t.Fatal("complete actor/store records differ from C-derived result")
			}
			if aux != dumpUnchangedAux(t, f.store) {
				t.Fatal("dump changed ship/race/allocator/placement/turn/maintenance/schedule/SData records")
			}
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				for n, st := range stars {
					if f.state.stars[n] != st {
						t.Error("star address replaced")
					}
				}
				for n, p := range planets {
					if f.state.planets[f.home][uint8(n)] != p {
						t.Error("planet address replaced")
					}
				}
			})
			for _, stage := range []string{"segment", "update", "restart"} {
				var e error
				switch stage {
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
				after := dumpStoredWorld(t, f.store)
				for _, n := range tc.stars {
					if !after.Stars[n].IsExplored(tc.target) {
						t.Fatal(stage, "lost star exploration")
					}
					for j, p := range want.Planets[n] {
						if after.Planets[n][j].Info[tc.target-1].Explored != p.Info[tc.target-1].Explored {
							t.Fatal(stage, "lost/invented planet exploration")
						}
					}
				}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					for _, r := range f.state.races {
						if r != nil && !f.state.governed(r) {
							t.Error(stage, "government ship invalid")
						}
					}
				})
			}
		})
	}
}

func TestCR17BDumpFailures(t *testing.T) {
	for _, stage := range []string{"command.before", "command.validated", "command.write:1", "command.write:2", "command.write:3", "command.before-commit", "command.commit-error", "command.after-commit", "fence"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				f := newDumpFixture(t)
				actorBefore, diskBefore := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				want := dumpWant(dumpActorWorld(t, f.actorEnrollmentFixture), f.home, 2, 0)
				aux := dumpUnchangedAux(t, f.store)
				r := &recordingResponder{}
				reg := &fakeRegistry{}
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
				reached := false
				f.store.SetMetaFault(func(op string) error {
					if stage == "fence" && op == "command.before" {
						reached = true
						f.store.SetWriteFence(true)
						return nil
					}
					if op == stage {
						reached = true
						if r.text() != "" || reg.count() != 0 {
							t.Error("output escaped before result")
						}
						if panics {
							panic("secret fault payload")
						}
						return errors.New("secret fault payload")
					}
					return nil
				})
				if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "dump 2 1", r); e != nil {
					t.Fatal(e)
				}
				f.store.SetWriteFence(false)
				if !reached || f.state.Mode() != ModeHalted || strings.Contains(r.text(), "Data transferred") || strings.Contains(r.text(), "secret") || strings.Contains(reg.all(), "exploration data") {
					t.Fatal("failure not reached, false success or leaked output")
				}
				committed := stage == "command.after-commit"
				visible := committed || stage == "command.commit-error"
				if visible {
					if !reflect.DeepEqual(dumpStoredWorld(t, f.store), want) {
						t.Fatal("visible commit is partial")
					}
				} else if diskBefore != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("aborted transaction changed records/allocator/metadata")
				}
				if committed {
					if !reflect.DeepEqual(dumpActorWorld(t, f.actorEnrollmentFixture), want) || !strings.Contains(r.text(), "records committed") {
						t.Fatal("confirmed committed error did not install all records")
					}
				} else if actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actorBefore {
					t.Fatal("unconfirmed operation installed actor proposal or AP")
				}
				if aux != dumpUnchangedAux(t, f.store) {
					t.Fatal("auxiliary records changed")
				}
				if stage == "fence" && (f.store.FencedWrites() != 1 || f.store.LastFencedWrite() != "CommitCommandRecords") {
					t.Fatal("fence not counted")
				}
				diskAfter := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				if e := f.state.TriggerSegment(ctxT(t, time.Second)); !errors.Is(e, ErrNotServing) {
					t.Fatal("halted segment accepted", e)
				}
				if e := f.state.Stop(ctxT(t, 10*time.Second)); !errors.Is(e, ErrHaltedNotFlushed) || diskAfter != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
					t.Fatal("failed command flushed stale state", e)
				}
				if stage == "command.commit-error" {
					f.store.SetMetaFault(nil)
					f.open(t)
					if f.state.Mode() != ModeHalted || !strings.Contains(f.log.text(), "uncertain") || diskAfter != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
						t.Fatal("new actor cleared command uncertainty guard")
					}
				}
			})
		}
	}
}

func TestCR17BDumpUncertainReadbackUnavailable(t *testing.T) {
	f := newDumpFixture(t)
	before := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture)
	read := false
	f.store.SetMetaFault(func(op string) error {
		if op == "command.commit-error" {
			return errors.New("commit unknown")
		}
		if op == "command.readback" {
			read = true
			return errors.New("read unavailable")
		}
		return nil
	})
	out := f.command(t, "dump 2 1")
	if !read || !strings.Contains(out, "UNCERTAIN") || f.state.Mode() != ModeHalted || before != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) {
		t.Fatal("failed readback became success or rollback")
	}
}

func TestCR17BDumpInstallationAndDeliveryFailures(t *testing.T) {
	for _, failure := range []string{"panic before installation", "replaced pointer", "delivery panic"} {
		t.Run(failure, func(t *testing.T) {
			f := newDumpFixture(t)
			before := dumpActorWorld(t, f.actorEnrollmentFixture)
			want := dumpWant(dumpActorWorld(t, f.actorEnrollmentFixture), f.home, 2, 0)
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				switch failure {
				case "panic before installation":
					f.state.testHooks.beforeCommandInstall = func() { panic("private install fault") }
				case "replaced pointer":
					f.state.testHooks.beforeCommandInstall = func() { old := *f.state.stars[int(f.home)]; f.state.stars[int(f.home)] = &old }
				case "delivery panic":
					f.state.SetRegistry(&panickingRegistry{marker: "exploration data"})
				}
			})
			out := f.command(t, "dump 2 1")
			if f.state.Mode() != ModeHalted || !reflect.DeepEqual(dumpStoredWorld(t, f.store), want) || strings.Contains(out, "Data transferred") {
				t.Fatal("commit/install/delivery failure not safely halted")
			}
			expected := before
			if failure == "delivery panic" {
				expected = want
			}
			if !reflect.DeepEqual(dumpActorWorld(t, f.actorEnrollmentFixture), expected) {
				t.Fatal("partial actor installation")
			}
			if !strings.Contains(f.log.text(), "COMMITTED") {
				t.Fatal("confirmed commit lost in diagnostics")
			}
		})
	}
}

func TestCR17BDumpInvalidAndModeRefusal(t *testing.T) {
	for _, text := range []string{"dump", "dump missing", "dump 2 missing-star", "dump 2 0", "dump 2 999999999999999999999", "dump 2 1junk", "dump 2 1 500"} {
		t.Run(text, func(t *testing.T) {
			f := newDumpFixture(t)
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, text)
			if out == "" || strings.Contains(out, "Data transferred") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) || f.state.Mode() != ModeServing {
				t.Fatal("invalid command changed state or succeeded")
			}
		})
	}
	for _, kind := range []string{"source guest", "recipient guest", "no AP", "governor", "quiesced", "halted"} {
		t.Run(kind, func(t *testing.T) {
			f := newDumpFixture(t)
			g := 0
			onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
				switch kind {
				case "source guest":
					f.state.races[0].Guest = true
					if e := f.store.PutRace(f.state.races[0]); e != nil {
						t.Error(e)
					}
				case "recipient guest":
					f.state.races[1].Guest = true
					if e := f.store.PutRace(f.state.races[1]); e != nil {
						t.Error(e)
					}
				case "no AP":
					f.state.stars[int(f.home)].AP[0] = 0
					if e := f.store.PutStar(f.state.stars[int(f.home)]); e != nil {
						t.Error(e)
					}
				case "governor":
					g = 1
					f.state.dirs[0][1] = f.state.dirs[0][0]
				case "halted":
					f.state.halt("isolated mode fixture", "")
				}
			})
			if kind == "quiesced" {
				if e := f.state.Quiesce(ctxT(t, 10*time.Second)); e != nil {
					t.Fatal(e)
				}
			}
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			r := &recordingResponder{}
			if e := f.state.HandleCommand(ctxT(t, 10*time.Second), 1, g, "dump 2 1", r); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(r.text(), "Data transferred") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("permission/mode refusal mutated records")
			}
		})
	}
}

func TestCR17BDumpStaleAndMissingActorState(t *testing.T) {
	for _, kind := range []string{"legacy load", "store star AP", "store unchanged planet", "missing actor planet", "actor planet identity", "actor identity"} {
		t.Run(kind, func(t *testing.T) {
			f := newDumpFixture(t)
			switch kind {
			case "legacy load":
				var ship uint32
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
					r := f.state.races[0]
					ship = r.GovShip
					p := f.state.planets[f.home][0]
					p.Info[0].Resource = 100
					if e := f.store.PutPlanet(p); e != nil {
						t.Error(e)
					}
				})
				if out := f.command(t, fmt.Sprintf("load #%d r 7", ship)); !strings.Contains(out, "transferred") {
					t.Fatal("legacy load boundary not exercised")
				}
			case "store star AP":
				st, e := f.store.GetStar(f.home)
				if e != nil {
					t.Fatal(e)
				}
				st.AP[0]--
				if e := f.store.PutStar(st); e != nil {
					t.Fatal(e)
				}
			case "store unchanged planet":
				p, e := f.store.GetPlanet(f.home, 1)
				if e != nil {
					t.Fatal(e)
				}
				p.Info[0].Resource++
				if e := f.store.PutPlanet(p); e != nil {
					t.Fatal(e)
				}
			case "actor planet identity":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { // A changed actor identity must be refused before storage.
					f.state.planets[f.home][1].PlanetNum = 2
				})
			case "missing actor planet":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { delete(f.state.planets[f.home], 1) })
			case "actor identity":
				onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.stars[int(f.home)].Number = 255 })
			}
			actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
			out := f.command(t, "dump 2 1")
			if f.state.Mode() != ModeHalted || strings.Contains(out, "Data transferred") || actor != actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) || disk != enrollmentDBSnapshot(t, f.actorEnrollmentFixture) {
				t.Fatal("stale world was overwritten or partially installed")
			}
		})
	}
}

func TestCR17BDumpOutputWaitsForCommit(t *testing.T) {
	f := newDumpFixture(t)
	before := dumpActorWorld(t, f.actorEnrollmentFixture)
	r := &recordingResponder{}
	reg := &fakeRegistry{}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.SetRegistry(reg) })
	f.store.SetMetaFault(func(op string) error {
		if op == "command.write:1" {
			if r.text() != "" || reg.count() != 0 {
				t.Error("early output")
			}
			for n, st := range before.Stars {
				if !reflect.DeepEqual(st, f.state.stars[n]) {
					t.Error("actor star installed before commit")
				}
			}
			for n, ps := range before.Planets {
				for j, p := range ps {
					if !reflect.DeepEqual(p, f.state.planets[n][j]) {
						t.Error("actor planet installed before commit")
					}
				}
			}
			close(entered)
			<-release
		}
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "dump 2 1", r) }()
	waitAcceptance(t, entered, "open command transaction")
	if r.text() != "" || reg.count() != 0 || !reflect.DeepEqual(dumpStoredWorld(t, f.store), before) {
		t.Error("uncommitted transaction leaked state or output")
	}
	unblock()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	if !strings.Contains(r.text(), "Data transferred") || reg.count() != 1 {
		t.Fatal("committed effects missing")
	}
}

func TestCR17BDumpSerializationAndShutdown(t *testing.T) {
	for _, action := range []string{"second command", "quiesce", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			f := newDumpFixture(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			first := true
			f.store.SetMetaFault(func(op string) error {
				if op == "command.write:1" && first {
					first = false
					close(entered)
					<-release
				}
				return nil
			})
			r := &recordingResponder{}
			done := make(chan error, 1)
			go func() { done <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "dump 2 1", r) }()
			waitAcceptance(t, entered, "started transaction")
			following := make(chan error, 1)
			r2 := &recordingResponder{}
			go func() {
				switch action {
				case "second command":
					following <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "dump 2 1", r2)
				case "quiesce":
					following <- f.state.Quiesce(ctxT(t, 10*time.Second))
				case "shutdown":
					following <- f.state.Stop(ctxT(t, 10*time.Second))
				}
			}()
			if action == "shutdown" {
				deadline := time.Now().Add(5 * time.Second)
				for !f.state.draining.Load() {
					if time.Now().After(deadline) {
						t.Fatal("shutdown not queued")
					}
					time.Sleep(time.Millisecond)
				}
			}
			select {
			case e := <-following:
				t.Fatal("following mutation overlapped transaction", e)
			default:
			}
			unblock()
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			if e := <-following; e != nil {
				t.Fatal(e)
			}
			if !strings.Contains(r.text(), "Data transferred") {
				t.Fatal("admitted command withdrawn during drain")
			}
			expectedAP := uint8(49)
			if action == "second command" {
				expectedAP = 48
				if !strings.Contains(r2.text(), "Data transferred") {
					t.Fatal("serialized second command failed")
				}
			}
			st, e := f.store.GetStar(f.home)
			if e != nil || st.AP[0] != expectedAP {
				t.Fatal("AP debit duplicated/lost", e)
			}
			if action != "shutdown" {
				assertActorStoreAgreement(t, f.actorEnrollmentFixture)
			}
			if action == "quiesce" {
				before := enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				if out := f.command(t, "dump 2 1"); strings.Contains(out, "Data transferred") || enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != before {
					t.Fatal("new quiesced command admitted")
				}
				f.restart(t)
				if f.state.Mode() != ModeQuiesced {
					t.Fatal("maintenance lost after restart")
				}
			}
		})
	}
}

func TestCR17BDumpQueuedCancellationAndTurn(t *testing.T) {
	f := newDumpFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { f.state.testHooks.beforeTurn = func(turnKind) { close(entered); <-release } })
	turn := make(chan error, 1)
	go func() { turn <- f.state.TriggerSegment(ctxT(t, 10*time.Second)) }()
	waitAcceptance(t, entered, "turn body")
	actorBefore := dumpStoredWorld(t, f.store)
	r := &recordingResponder{}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { canceled <- f.state.HandleCommand(ctx, 1, 0, "dump 2 1", r) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(f.state.cmdCh) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("request did not queue behind turn")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if e := <-canceled; !errors.Is(e, ErrNotExecuted) {
		t.Fatal("canceled queued command accepted", e)
	}
	if !reflect.DeepEqual(dumpStoredWorld(t, f.store), actorBefore) {
		t.Fatal("canceled command wrote during turn")
	}
	queued := make(chan error, 1)
	go func() { queued <- f.state.HandleCommand(ctxT(t, 10*time.Second), 1, 0, "dump 2 1", r) }()
	select {
	case e := <-queued:
		t.Fatal("command executed concurrently with turn", e)
	default:
	}
	unblock()
	if e := <-turn; e != nil {
		t.Fatal(e)
	}
	if e := <-queued; e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(r.text(), "Data transferred") {
		t.Fatal("queued command failed after turn")
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestCR17BDumpEnrollmentAndAPBoundary(t *testing.T) {
	f := newDumpFixture(t)
	if out := f.command(t, "dump 2 1"); !strings.Contains(out, "Data transferred") {
		t.Fatal(out)
	}
	if _, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("account-3")); e != nil {
		t.Fatal("atomic dump left admission disagreement", e)
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
	// CR-17A stockpile transfer still has its documented separate AP boundary.
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() {
		p := f.state.planets[f.home][0]
		p.Info[0].Resource = 100
		if e := f.store.PutPlanet(p); e != nil {
			t.Error(e)
		}
	})
	if out := f.command(t, "transfer 2 r 1"); !strings.Contains(out, "transferred") {
		t.Fatal(out)
	}
	_, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("guest-operator"))
	assertActorEnrollmentFailure(t, e, "ENROLLMENT_PENDING_TURN", db.EnrollmentNotCommitted)
	if f.state.Mode() != ModeServing {
		t.Fatal("pending AP halted actor")
	}
}

func TestCR17BDumpStarNamesAndReadOnlyPlanets(t *testing.T) {
	f := newDumpFixture(t)
	var name string
	onEnrollmentActor(t, f.actorEnrollmentFixture, func() { name = strings.ToUpper(f.state.stars[int(f.home)].Name) })
	if out := f.command(t, "dump 2 "+name); !strings.Contains(out, "Data transferred") {
		t.Fatal("case-insensitive star name rejected", out)
	}
	p, e := f.store.GetPlanet(f.home, 1)
	if e != nil || p.Info[0].Explored || p.Info[1].Explored {
		t.Fatal("source-unknown planet knowledge was invented", e)
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}

func TestCR17BDumpAllStarsRollback(t *testing.T) {
	for _, stage := range []string{"command.write:2", "command.write:5", "command.write:9"} {
		for _, panics := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panics), func(t *testing.T) {
				f := newDumpFixture(t)
				actor, disk := actorEnrollmentSnapshot(t, f.actorEnrollmentFixture), enrollmentDBSnapshot(t, f.actorEnrollmentFixture)
				reached := false
				f.store.SetMetaFault(func(op string) error {
					if op == stage {
						reached = true
						if panics {
							panic("multi-star abort")
						}
						return errors.New("multi-star abort")
					}
					return nil
				})
				out := f.command(t, "dump 2")
				if !reached || f.state.Mode() != ModeHalted || strings.Contains(out, "Data transferred") || actorEnrollmentSnapshot(t, f.actorEnrollmentFixture) != actor || enrollmentDBSnapshot(t, f.actorEnrollmentFixture) != disk {
					t.Fatal("failed invocation persisted a star/planet/AP prefix")
				}
			})
		}
	}
}

func TestCR17BDumpStartedCancellation(t *testing.T) {
	f := newDumpFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.store.SetMetaFault(func(op string) error {
		if op == "command.write:1" {
			close(entered)
			<-release
		}
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &recordingResponder{}
	done := make(chan error, 1)
	go func() { done <- f.state.HandleCommand(ctx, 1, 0, "dump 2 1", r) }()
	waitAcceptance(t, entered, "started command write")
	cancel()
	select {
	case e := <-done:
		t.Fatal("started command cancellation guessed an outcome", e)
	default:
	}
	unblock()
	if e := <-done; e != nil {
		t.Fatal("completed outcome lost after cancellation", e)
	}
	if !strings.Contains(r.text(), "Data transferred") {
		t.Fatal("committed command did not report actual result")
	}
	assertActorStoreAgreement(t, f.actorEnrollmentFixture)
}
