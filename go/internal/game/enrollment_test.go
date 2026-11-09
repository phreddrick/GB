package game

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/racegen"
	"github.com/phreddrick/GB/go/internal/universe"
	"golang.org/x/crypto/bcrypt"
)

var enrollmentTestHashes struct {
	sync.Once
	credentials EnrollmentCredentials
	err         error
}

func enrollmentHashes(t *testing.T) EnrollmentCredentials {
	t.Helper()
	enrollmentTestHashes.Do(func() {
		r, e := bcrypt.GenerateFromPassword([]byte("fixture-r"), bcrypt.DefaultCost)
		if e != nil {
			enrollmentTestHashes.err = e
			return
		}
		g, e := bcrypt.GenerateFromPassword([]byte("fixture-g"), bcrypt.DefaultCost)
		enrollmentTestHashes.credentials = EnrollmentCredentials{RaceHash: string(r), GovernorHash: string(g)}
		enrollmentTestHashes.err = e
	})
	if enrollmentTestHashes.err != nil {
		t.Fatal(enrollmentTestHashes.err)
	}
	return enrollmentTestHashes.credentials
}

type enrollmentLog struct {
	sync.Mutex
	bytes.Buffer
}

func (l *enrollmentLog) Write(b []byte) (int, error) {
	l.Lock()
	defer l.Unlock()
	return l.Buffer.Write(b)
}
func (l *enrollmentLog) text() string { l.Lock(); defer l.Unlock(); return l.String() }

type actorEnrollmentFixture struct {
	state  *State
	store  *db.Store
	cfg    *config.Config
	policy *EnrollmentPolicy
	path   string
	log    *enrollmentLog
}

func newEnrollmentActor(t *testing.T) *actorEnrollmentFixture {
	t.Helper()
	f := &actorEnrollmentFixture{path: filepath.Join(t.TempDir(), "enrollment.db"), log: &enrollmentLog{}}
	f.cfg = testConfig(t, f.path)
	store, e := db.Open(f.path)
	if e != nil {
		t.Fatal(e)
	}
	f.store = store
	// Reproducible generated universe with normalized terrestrial test candidates.
	// Generation remains unmodified; these are isolated test fixture edits.
	u, e := universe.Generate(universe.Params{Stars: 4, MinPlanets: 2, MaxPlanets: 2}, 404)
	if e != nil {
		t.Fatal(e)
	}
	for _, st := range u.Stars {
		st.Inhabited = 0
		st.Explored = 0
	}
	for _, p := range u.Planets {
		p.Type = model.PlanetEarth
		p.Conditions[model.AtmosRTemp] = 0
		p.MaxX = 3
		p.MaxY = 2
		p.Sectors = make([]model.Sector, 6)
		for i := range p.Sectors {
			p.Sectors[i] = model.Sector{Condition: model.SectorLand, Type: model.SectorLand, Fert: 47, Eff: 2, Resource: 37, Crystals: 1}
		}
	}
	now := time.Now().UTC()
	if e = store.PutUniverse(u.StarData, u.Stars, u.Planets, &db.UniverseRecord{Seed: 404, Stars: 4, MinPlanets: 2, MaxPlanets: 2, Planets: 8, InitializedAt: now, Generator: universe.GeneratorVersion, Tool: "isolated CR-07D fixture"}); e != nil {
		t.Fatal(e)
	}
	credentials := enrollmentHashes(t)
	f.policy = &EnrollmentPolicy{GameID: "fixture-game", Authorize: func(req EnrollmentRequest) (EnrollmentAuthorization, error) {
		if req.CallerReference != "operator" && req.CallerReference != "account-2" && req.CallerReference != "account-3" && req.CallerReference != "guest-operator" {
			return EnrollmentAuthorization{}, errors.New("untrusted context")
		}
		return EnrollmentAuthorization{CallerID: req.CallerReference, GameID: "fixture-game", RaceCredentialRef: req.CallerReference + "-race-v1", GovernorCredentialRef: req.CallerReference + "-governor-v1", AllowGod: req.CallerReference == "operator", AllowGuest: req.CallerReference == "guest-operator"}, nil
	}, Prepare: func(EnrollmentAuthorization) (EnrollmentCredentials, error) { return credentials, nil }}
	f.open(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = f.state.Stop(ctx)
		_ = f.store.Close()
	})
	return f
}
func (f *actorEnrollmentFixture) open(t *testing.T) {
	t.Helper()
	s, e := New(f.cfg, f.store, Options{Logger: slog.New(slog.NewTextHandler(f.log, nil)), TrustedEnrollment: f.policy})
	if e != nil {
		t.Fatal(e)
	}
	f.state = s
	onEnrollmentActor(t, f, func() {
		s.testHooks = &testHooks{enrollmentOrder: func(n int) ([]int, error) {
			order := make([]int, n)
			for i := range order {
				order[i] = i
			}
			return order, nil
		}}
	})
}
func (f *actorEnrollmentFixture) restart(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := f.state.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	if e := f.store.Close(); e != nil {
		t.Fatal(e)
	}
	s, e := db.Open(f.path)
	if e != nil {
		t.Fatal(e)
	}
	f.store = s
	f.open(t)
}
func onEnrollmentActor(t *testing.T, f *actorEnrollmentFixture, fn func()) {
	t.Helper()
	if e := f.state.request(ctxT(t, 10*time.Second), opTest, func() error { fn(); return nil }); e != nil {
		t.Fatal(e)
	}
}
func actorEnrollmentRequest(caller string) EnrollmentRequest {
	spec := racegen.Default()
	spec.Name = caller + " species"
	spec.Attributes.Fertilize = .25
	spec.SectorCompat[racegen.Land] = 100
	return EnrollmentRequest{RequestID: caller + "-request", CallerReference: caller, GameID: "fixture-game", Spec: spec, God: caller == "operator"}
}
func enrollActor(t *testing.T, f *actorEnrollmentFixture, req EnrollmentRequest) EnrollmentResult {
	t.Helper()
	r, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), req)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func actorEnrollmentSnapshot(t *testing.T, f *actorEnrollmentFixture) string {
	t.Helper()
	value, e := call(f.state, ctxT(t, 10*time.Second), opTest, func() (string, error) {
		b, e := json.Marshal([]any{f.state.stars, f.state.planets, f.state.races, f.state.ships, f.state.sdata, f.state.dirs, f.state.power, f.state.blocks, f.state.powerBlocks})
		return string(b), e
	})
	if e != nil {
		t.Fatal(e)
	}
	return value
}
func enrollmentDBSnapshot(t *testing.T, f *actorEnrollmentFixture, ids ...string) string {
	t.Helper()
	digest, e := f.store.WorldDigest()
	if e != nil {
		t.Fatal(e)
	}
	meta := [][]byte{}
	for _, key := range [][]byte{db.KeyCommit, db.KeySchedule, db.KeyUniverse, db.KeyMaintenance} {
		raw, e := f.store.GetMeta(key)
		if e != nil {
			t.Fatal(e)
		}
		meta = append(meta, raw)
	}
	for _, id := range append([]string{"operator-request", "account-2-request", "account-3-request", "guest-operator-request"}, ids...) {
		raw, e := f.store.GetMeta([]byte("enrollment/v1/placement/" + id))
		if e != nil {
			t.Fatal(e)
		}
		meta = append(meta, raw)
	}
	free, e := f.store.FreeShipNums()
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal([]any{digest, meta, free})
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func assertActorEnrollmentFailure(t *testing.T, e error, code string, outcome db.EnrollmentOutcome) {
	t.Helper()
	var failure *EnrollmentFailure
	if !errors.As(e, &failure) || failure.Code != code || failure.Outcome != outcome {
		t.Fatalf("error %v, want %s/%s", e, code, outcome)
	}
}
func assertActorStoreAgreement(t *testing.T, f *actorEnrollmentFixture) {
	t.Helper()
	onEnrollmentActor(t, f, func() {
		r, e := f.store.AllRaces()
		if e != nil || !reflect.DeepEqual(r, f.state.races) {
			t.Error("race store/actor disagreement", e)
		}
		st, e := f.store.AllStars()
		if e != nil || !reflect.DeepEqual(st, f.state.stars) {
			t.Error("star store/actor disagreement", e)
		}
		p, e := f.store.AllPlanets()
		if e != nil || !reflect.DeepEqual(p, f.state.planets) {
			t.Error("planet store/actor disagreement", e)
		}
		sh, e := f.store.AllShips()
		if e != nil || !reflect.DeepEqual(sh, f.state.ships) {
			t.Error("ship store/actor disagreement", e)
		}
	})
}

func TestActorEnrollmentCompletePlacement(t *testing.T) {
	f := newEnrollmentActor(t)
	req := actorEnrollmentRequest("operator")
	beforeCommit, _ := f.store.GetMeta(db.KeyCommit)
	beforeSchedule, _ := f.store.GetMeta(db.KeySchedule)
	result := enrollActor(t, f, req)
	rec := result.Placement
	if rec.Player != 1 || rec.GovernmentShip != 1 || result.Replay || result.Outcome != db.EnrollmentCommitted {
		t.Fatal("incorrect allocation")
	}
	assertActorStoreAgreement(t, f)
	onEnrollmentActor(t, f, func() {
		s := f.state
		r := s.races[0]
		p := s.planets[rec.Star][rec.Planet]
		st := s.stars[rec.Star]
		sh := s.ships[rec.GovernmentShip]
		sec := p.SectorAt(int(rec.CapitalX), int(rec.CapitalY))
		g := r.Governor[0]
		dir := s.dirs[0][0]
		if r.Name != req.Spec.Name || r.IQ != 150 || r.Fighters != 4 || r.Fertilize != 25 || r.Mass != 1 || r.Metabolism != 1 || r.BirthRate != .6 || r.Adventurism != .4 || r.NumberSexes != 2 || r.GovShip != sh.Number || !r.God || r.Governors != 0 || !g.Active {
			t.Error("race initialization")
		}
		if sec.Owner != 1 || sec.Race != 1 || sec.Popn != 2 || sec.Fert != 100 || sec.Eff != 10 || sec.Troops != 0 || sec.Resource != 37 || sec.Crystals != 1 || p.Popn != 2 || p.Troops != 0 || p.MaxPopn != 3300 || p.Info[0].NumSectsOwned != 1 || !p.Info[0].Explored || p.Ships != sh.Number || p.Explored {
			t.Error("colony initialization")
		}
		if !st.IsInhabited(1) || !st.IsExplored(1) || st.AP[0] != 5 || r.Conditions[0] != p.Conditions[0] || r.Translate[0] != 100 {
			t.Error("star initialization")
		}
		if sh.Owner != 1 || sh.Type != model.OtypeGov || sh.Popn != 10 || sh.MaxCrew != 10 || sh.BaseMass != 100 || sh.Mass != 110 || sh.NextShip != 0 || !s.governed(r) {
			t.Error("government ship initialization")
		}
		if g.HomeSystem != rec.Star || g.DefPlanetNum != rec.Planet || dir.Level != model.LevelPlan || dir.StarNum != rec.Star || dir.PlanetNum != rec.Planet || !strings.Contains(dir.Prompt, "[5]") {
			t.Error("governor/directory initialization")
		}
		hashes := enrollmentHashes(t)
		if r.Password != hashes.RaceHash || g.Password != hashes.GovernorHash || r.Password == g.Password {
			t.Error("credentials not independently installed")
		}
	})
	if f.state.Status().Races != 1 || f.state.Status().Ships != 1 || f.state.metrics.WorldRaces.Value() != 1 || f.state.metrics.WorldShips.Value() != 1 || f.state.metrics.Enrollments.Value("success") != 1 {
		t.Fatal("world counts/metrics")
	}
	afterCommit, _ := f.store.GetMeta(db.KeyCommit)
	afterSchedule, _ := f.store.GetMeta(db.KeySchedule)
	if !bytes.Equal(beforeCommit, afterCommit) || !bytes.Equal(beforeSchedule, afterSchedule) {
		t.Fatal("enrollment changed turn metadata")
	}
	f.restart(t)
	assertActorStoreAgreement(t, f)
	r, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), req)
	if e != nil || !r.Replay || r.Placement != rec {
		t.Fatal("restart replay", e)
	}
}
func TestActorEnrollmentDistinctAndReplay(t *testing.T) {
	f := newEnrollmentActor(t)
	first := enrollActor(t, f, actorEnrollmentRequest("operator"))
	second := enrollActor(t, f, actorEnrollmentRequest("account-2"))
	if first.Placement.Player == second.Placement.Player || first.Placement.GovernmentShip == second.Placement.GovernmentShip || first.Placement.Star == second.Placement.Star {
		t.Fatal("duplicate allocation")
	}
	before := enrollmentDBSnapshot(t, f)
	memory := actorEnrollmentSnapshot(t, f)
	r := enrollActor(t, f, actorEnrollmentRequest("operator"))
	if !r.Replay || r.Placement != first.Placement || before != enrollmentDBSnapshot(t, f) || memory != actorEnrollmentSnapshot(t, f) {
		t.Fatal("duplicate retry changed world")
	}
	assertActorStoreAgreement(t, f)
}
func TestActorEnrollmentRetryAfterScheduledTurnAndDestruction(t *testing.T) {
	f := newEnrollmentActor(t)
	req := actorEnrollmentRequest("operator")
	placed := enrollActor(t, f, req)
	// Arm the actual actor timer; no TriggerUpdate or direct doTurn call.
	beforeTurns := f.state.Status().NumUpdates
	onEnrollmentActor(t, f, func() {
		f.state.sched.nextUpdate = time.Now().Add(20 * time.Millisecond)
		f.state.sched.nextSegment = time.Now().Add(time.Hour)
		if e := f.store.PutSchedule(f.state.scheduleRecord(f.state.sched, time.Now())); e != nil {
			t.Error(e)
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for f.state.Status().NumUpdates == beforeTurns || f.state.Mode() != ModeServing {
		if time.Now().After(deadline) {
			t.Fatal("scheduled update did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	r := enrollActor(t, f, req)
	if !r.Replay || r.Placement != placed.Placement {
		t.Fatal("later-turn retry")
	}
	assertActorStoreAgreement(t, f)
	onEnrollmentActor(t, f, func() {
		ship := f.state.ships[placed.Placement.GovernmentShip]
		ship.Alive = false
		if e := f.store.PutShip(ship); e != nil {
			t.Error(e)
		}
		f.state.races[0].Name = "Renamed later"
		if e := f.store.PutRace(f.state.races[0]); e != nil {
			t.Error(e)
		}
	})
	r = enrollActor(t, f, req)
	if !r.Replay {
		t.Fatal("destroyed government/renamed race prevented historical replay")
	}
	assertActorStoreAgreement(t, f)
}
func TestActorEnrollmentRetryDoesNotPrepareOrSearch(t *testing.T) {
	f := newEnrollmentActor(t)
	req := actorEnrollmentRequest("operator")
	placed := enrollActor(t, f, req)
	onEnrollmentActor(t, f, func() {
		f.state.enrollmentPolicy.Prepare = func(EnrollmentAuthorization) (EnrollmentCredentials, error) {
			t.Error("prepared credentials for committed retry")
			return EnrollmentCredentials{}, errors.New("bad")
		}
		f.state.testHooks.enrollmentOrder = func(int) ([]int, error) { t.Error("searched for committed retry"); return nil, errors.New("bad") }
	})
	r := enrollActor(t, f, req)
	if !r.Replay || r.Placement != placed.Placement {
		t.Fatal("replay failed")
	}
}
func TestActorEnrollmentIdentityConflicts(t *testing.T) {
	for _, which := range []string{"changed-spec", "changed-privilege", "other-caller", "same-account-new-id", "same-account-new-name", "duplicate-name"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			req := actorEnrollmentRequest("operator")
			enrollActor(t, f, req)
			switch which {
			case "changed-spec":
				req.Spec.Info = "changed"
			case "changed-privilege":
				req.God = false
			case "other-caller":
				req.CallerReference = "account-2"
				req.God = false
			case "same-account-new-id":
				req.RequestID = "other"
				req.God = false
			case "same-account-new-name":
				req.RequestID = "other"
				req.Spec.Name = "Other species"
				req.God = false
			case "duplicate-name":
				req = actorEnrollmentRequest("account-2")
				req.Spec.Name = strings.ToUpper(actorEnrollmentRequest("operator").Spec.Name)
			}
			before := enrollmentDBSnapshot(t, f, req.RequestID)
			memory := actorEnrollmentSnapshot(t, f)
			_, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), req)
			if e == nil {
				t.Fatal("accepted conflicting identity")
			}
			if before != enrollmentDBSnapshot(t, f, req.RequestID) || memory != actorEnrollmentSnapshot(t, f) || f.state.Mode() != ModeServing {
				t.Fatal("conflict changed world or mode")
			}
		})
	}
}
func TestActorEnrollmentInvalidDesignAndNames(t *testing.T) {
	for _, which := range []string{"attributes", "unsupported-race", "gas", "budget", "NaN", "name-control", "name-whitespace", "name-long", "request-id", "game", "unknown-caller"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			req := actorEnrollmentRequest("operator")
			switch which {
			case "attributes":
				req.Spec.Attributes.IQ = 0
			case "unsupported-race":
				req.Spec.RaceType = 99
			case "gas":
				req.Spec.SectorCompat[racegen.Gas] = 100
			case "budget":
				req.Spec.Attributes.Fighters = 20
				req.Spec.Attributes.IQ = 220
				req.Spec.Attributes.Metabolism = 4
			case "NaN":
				req.Spec.Attributes.Mass = math.NaN()
			case "name-control":
				req.Spec.Name = "bad\nname"
			case "name-whitespace":
				req.Spec.Name = " bad"
			case "name-long":
				req.Spec.Name = strings.Repeat("x", 65)
			case "request-id":
				req.RequestID = "../../bad"
			case "game":
				req.GameID = "other"
			case "unknown-caller":
				req.CallerReference = "untrusted"
			}
			before := enrollmentDBSnapshot(t, f, req.RequestID)
			memory := actorEnrollmentSnapshot(t, f)
			_, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), req)
			if e == nil {
				t.Fatal("accepted invalid input")
			}
			if before != enrollmentDBSnapshot(t, f, req.RequestID) || memory != actorEnrollmentSnapshot(t, f) {
				t.Fatal("invalid input changed world")
			}
		})
	}
}
func TestActorEnrollmentNoHomeOrCapital(t *testing.T) {
	for _, which := range []string{"home", "capital"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			onEnrollmentActor(t, f, func() {
				for _, group := range f.state.planets {
					for _, p := range group {
						if which == "home" {
							p.Type = model.PlanetGasGiant
						} else {
							for i := range p.Sectors {
								p.Sectors[i].Condition = model.SectorForest
							}
						}
						if e := f.store.PutPlanet(p); e != nil {
							t.Error(e)
						}
					}
				}
			})
			before := enrollmentDBSnapshot(t, f)
			memory := actorEnrollmentSnapshot(t, f)
			_, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("operator"))
			var failure *PlacementError
			if !errors.As(e, &failure) {
				t.Fatal(e)
			}
			want := PlacementNoHomePlanet
			if which == "capital" {
				want = PlacementNoCapitalSector
			}
			if failure.Code != want || before != enrollmentDBSnapshot(t, f) || memory != actorEnrollmentSnapshot(t, f) {
				t.Fatal("placement failure changed world")
			}
		})
	}
}
func TestActorEnrollmentCredentialFailures(t *testing.T) {
	for _, which := range []string{"bcrypt-error", "plaintext", "reuse", "malformed", "weak", "panic"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			onEnrollmentActor(t, f, func() {
				f.state.enrollmentPolicy.Prepare = func(EnrollmentAuthorization) (EnrollmentCredentials, error) {
					c := enrollmentHashes(t)
					switch which {
					case "bcrypt-error":
						_, e := bcrypt.GenerateFromPassword(bytes.Repeat([]byte("x"), 73), bcrypt.DefaultCost)
						return c, e
					case "plaintext":
						c.RaceHash = "plaintext must never persist"
					case "reuse":
						c.GovernorHash = c.RaceHash
					case "malformed":
						c.RaceHash = strings.Repeat("x", 60)
					case "weak":
						h, e := bcrypt.GenerateFromPassword([]byte("fixture-r"), bcrypt.MinCost)
						if e != nil {
							return c, e
						}
						c.RaceHash = string(h)
					case "panic":
						panic("sensitive-provider-panic")
					}
					return c, nil
				}
			})
			before := enrollmentDBSnapshot(t, f)
			memory := actorEnrollmentSnapshot(t, f)
			_, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("operator"))
			if e == nil || strings.Contains(e.Error(), "sensitive") || strings.Contains(f.log.text(), "sensitive-provider-panic") {
				t.Fatal("credential failure leaked or succeeded")
			}
			if before != enrollmentDBSnapshot(t, f) || memory != actorEnrollmentSnapshot(t, f) {
				t.Fatal("credential failure wrote")
			}
		})
	}
}
func TestActorEnrollmentGodGuestPolicy(t *testing.T) {
	for _, which := range []string{"first-without-god", "unauthorized-god", "unauthorized-guest", "both", "authorized-guest", "authorized-god"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			req := actorEnrollmentRequest("operator")
			switch which {
			case "first-without-god":
				req.God = false
			case "unauthorized-god":
				req = actorEnrollmentRequest("account-2")
				req.God = true
			case "unauthorized-guest":
				req.Guest = true
				req.God = false
			case "both":
				req.Guest = true
			case "authorized-guest":
				enrollActor(t, f, req)
				req = actorEnrollmentRequest("guest-operator")
				req.Guest = true
			}
			before := enrollmentDBSnapshot(t, f)
			r, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), req)
			if which == "authorized-god" || which == "authorized-guest" {
				if e != nil || r.Outcome != db.EnrollmentCommitted {
					t.Fatal(e)
				}
				assertActorStoreAgreement(t, f)
			} else {
				if e == nil || before != enrollmentDBSnapshot(t, f) {
					t.Fatal("privilege denial wrote")
				}
			}
		})
	}
}
func TestActorEnrollmentTransactionFailures(t *testing.T) {
	for _, stage := range []string{"before", "construct", "race", "planet", "star", "ship", "free", "metadata", "before-commit"} {
		for _, panicFault := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panicFault), func(t *testing.T) {
				f := newEnrollmentActor(t)
				before := enrollmentDBSnapshot(t, f)
				memory := actorEnrollmentSnapshot(t, f)
				hit := false
				f.store.SetMetaFault(func(op string) error {
					if op == "enrollment."+stage {
						hit = true
						if panicFault {
							panic("private-db-panic")
						}
						return errors.New("private-db-error")
					}
					return nil
				})
				_, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("operator"))
				assertActorEnrollmentFailure(t, e, string(db.EnrollmentIO), db.EnrollmentNotCommitted)
				f.store.SetMetaFault(nil)
				if !hit || before != enrollmentDBSnapshot(t, f) || memory != actorEnrollmentSnapshot(t, f) || f.state.Mode() != ModeServing {
					t.Fatal("rollback changed actor/db or halted")
				}
				if strings.Contains(f.log.text(), "private-db") {
					t.Fatal("logged hook data")
				}
				enrollActor(t, f, actorEnrollmentRequest("operator"))
			})
		}
	}
}
func TestActorEnrollmentPostCommitAndUncertainty(t *testing.T) {
	for _, stage := range []string{"after-commit", "commit-error"} {
		for _, panicFault := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/panic=%t", stage, panicFault), func(t *testing.T) {
				f := newEnrollmentActor(t)
				memory := actorEnrollmentSnapshot(t, f)
				hit := false
				f.store.SetMetaFault(func(op string) error {
					if op == "enrollment."+stage {
						hit = true
						if panicFault {
							panic("private-postcommit")
						}
						return errors.New("private-postcommit")
					}
					return nil
				})
				r, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("operator"))
				f.store.SetMetaFault(nil)
				if !hit {
					t.Fatal("fault not reached")
				}
				if stage == "after-commit" {
					assertActorEnrollmentFailure(t, e, "ENROLLMENT_COMMITTED_ERROR", db.EnrollmentCommitted)
					if r.Outcome != db.EnrollmentCommitted || f.state.Mode() != ModeServing || f.state.metrics.Enrollments.Value("committed_error") != 1 {
						t.Fatal("committed outcome not installed")
					}
					assertActorStoreAgreement(t, f)
					retry := enrollActor(t, f, actorEnrollmentRequest("operator"))
					if !retry.Replay {
						t.Fatal("committed retry duplicated")
					}
				} else {
					assertActorEnrollmentFailure(t, e, "ENROLLMENT_RECONCILIATION_REQUIRED", db.EnrollmentUncertain)
					if f.state.Mode() != ModeHalted || memory != actorEnrollmentSnapshot(t, f) || f.state.metrics.Enrollments.Value("uncertain") != 1 {
						t.Fatal("uncertain write installed or not halted")
					}
					before := enrollmentDBSnapshot(t, f)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if e = f.state.Stop(ctx); !errors.Is(e, ErrHaltedNotFlushed) {
						t.Fatal("uncertain shutdown", e)
					}
					if before != enrollmentDBSnapshot(t, f) {
						t.Fatal("uncertain shutdown flushed stale memory")
					}
				}
				if strings.Contains(f.log.text(), "private-postcommit") {
					t.Fatal("secret hook logged")
				}
			})
		}
	}
}
func TestActorEnrollmentReadbackAndInstallFailures(t *testing.T) {
	for _, which := range []string{"readback", "install-panic", "readback-panic"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			memory := actorEnrollmentSnapshot(t, f)
			committed := false
			hit := false
			f.store.SetMetaFault(func(op string) error {
				if op == "enrollment.after-commit" {
					committed = true
				}
				if committed && strings.HasPrefix(op, "meta.get:enrollment/") && which != "install-panic" {
					hit = true
					if which == "readback-panic" {
						panic("private-readback")
					}
					return errors.New("private-readback")
				}
				return nil
			})
			if which == "install-panic" {
				onEnrollmentActor(t, f, func() { f.state.testHooks.beforeEnrollmentInstall = func() { hit = true; panic("private-install") } })
			}
			_, e := f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest("operator"))
			assertActorEnrollmentFailure(t, e, "ENROLLMENT_RECONCILIATION_REQUIRED", db.EnrollmentCommitted)
			f.store.SetMetaFault(nil)
			if !hit || !committed || f.state.Mode() != ModeHalted || memory != actorEnrollmentSnapshot(t, f) {
				t.Fatal("failed install changed actor or did not halt")
			}
			before := enrollmentDBSnapshot(t, f)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if e = f.state.Stop(ctx); !errors.Is(e, ErrHaltedNotFlushed) {
				t.Fatal(e)
			}
			if before != enrollmentDBSnapshot(t, f) {
				t.Fatal("committed but uninstalled state overwritten")
			}
			if strings.Contains(f.log.text(), "private-readback") || strings.Contains(f.log.text(), "private-install") {
				t.Fatal("panic details leaked")
			}
		})
	}
}
func TestActorEnrollmentModesAndDraining(t *testing.T) {
	for _, mode := range []Mode{ModeStarting, ModeUninitialized, ModeTurnInProgress, ModeQuiesced, ModeHalted, ModeStopped} {
		t.Run(mode.String(), func(t *testing.T) {
			f := newEnrollmentActor(t)
			onEnrollmentActor(t, f, func() { f.state.setMode(mode) })
			before := enrollmentDBSnapshot(t, f)
			_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
			if !errors.Is(e, ErrNotServing) || before != enrollmentDBSnapshot(t, f) {
				t.Fatal("non-serving enrollment", e)
			}
			onEnrollmentActor(t, f, func() { f.state.setMode(ModeServing) })
		})
	}
	t.Run("draining", func(t *testing.T) {
		f := newEnrollmentActor(t)
		f.state.draining.Store(true)
		_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
		if !errors.Is(e, ErrNotServing) {
			t.Fatal(e)
		}
		f.state.draining.Store(false)
	})
}
func TestActorEnrollmentQueuedCancellationAndStartedTimeout(t *testing.T) {
	t.Run("queued", func(t *testing.T) {
		f := newEnrollmentActor(t)
		before := enrollmentDBSnapshot(t, f)
		release := blockActor(t, f.state)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, e := f.state.EnrollTrusted(ctx, actorEnrollmentRequest("operator"))
		if !errors.Is(e, ErrNotExecuted) {
			t.Fatal(e)
		}
		release()
		barrier(t, f.state)
		if before != enrollmentDBSnapshot(t, f) {
			t.Fatal("queued cancellation later wrote")
		}
	})
	t.Run("started", func(t *testing.T) {
		f := newEnrollmentActor(t)
		entered := make(chan struct{})
		release := make(chan struct{})
		f.store.SetMetaFault(func(op string) error {
			if op == "enrollment.before" {
				close(entered)
				<-release
			}
			return nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, e := f.state.EnrollTrusted(ctx, actorEnrollmentRequest("operator")); done <- e }()
		select {
		case <-entered:
		case early := <-done:
			close(release)
			t.Fatalf("request finished before fault hook: %v", early)
		case <-time.After(10 * time.Second):
			close(release)
			t.Fatal("fault hook not reached")
		}
		e := <-done
		if !errors.Is(e, ErrStillRunning) {
			close(release)
			t.Fatal(e)
		}
		close(release)
		barrier(t, f.state)
		f.store.SetMetaFault(nil)
		r := enrollActor(t, f, actorEnrollmentRequest("operator"))
		if !r.Replay || f.state.metrics.Enrollments.Value("success") != 1 || f.state.metrics.Enrollments.Value("replay") != 1 {
			t.Fatal("started timeout duplicate or metrics")
		}
		assertActorStoreAgreement(t, f)
	})
}
func TestActorEnrollmentTurnSerialization(t *testing.T) {
	f := newEnrollmentActor(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	onEnrollmentActor(t, f, func() { f.state.testHooks.beforeTurn = func(turnKind) { close(entered); <-release } })
	done := make(chan error, 1)
	go func() { done <- f.state.TriggerUpdate(ctxT(t, 10*time.Second)) }()
	<-entered
	_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
	if !errors.Is(e, ErrNotServing) {
		close(release)
		t.Fatal(e)
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	enrollActor(t, f, actorEnrollmentRequest("operator"))
}
func TestActorEnrollmentSecurityAndCommandVisibility(t *testing.T) {
	f := newEnrollmentActor(t)
	req := actorEnrollmentRequest("operator")
	placed := enrollActor(t, f, req)
	// The existing transport accepts only one password. Distinct new credentials
	// intentionally cannot authenticate through it; CR-07E must extend that wire
	// contract. Neither credential alone is silently accepted as both factors.
	for _, password := range []string{"fixture-r", "fixture-g"} {
		r, e := f.state.Authenticate(ctxT(t, 5*time.Second), req.Spec.Name, password, 0)
		if e != nil || r.OK {
			t.Fatal("single-password contract was weakened", e)
		}
	}
	// Exercise real dispatcher visibility from the trusted in-process boundary;
	// this is explicitly not an authenticated transport/onboarding acceptance test.
	for _, command := range []string{"cs", "survey", "map", "capital"} {
		responder := &recordingResponder{}
		if e := f.state.HandleCommand(ctxT(t, 5*time.Second), placed.Placement.Player, 0, command, responder); e != nil {
			t.Fatal(e)
		}
		text := strings.ToLower(responder.text())
		if command != "cs" && strings.TrimSpace(text) == "" {
			t.Fatal("command produced no world view", command)
		}
		if strings.Contains(text, "unknown player") || strings.Contains(text, "bad scope") || strings.Contains(text, "not explored") {
			t.Fatal("new world not visible to command", command)
		}
	}
	onEnrollmentActor(t, f, func() {
		dir := f.state.dirs[0][0]
		if dir.Level != model.LevelPlan || dir.StarNum != placed.Placement.Star || dir.PlanetNum != placed.Placement.Planet {
			t.Error("cs used stale scope")
		}
	})
	if e := f.state.TriggerUpdate(ctxT(t, 10*time.Second)); e != nil {
		t.Fatal(e)
	}
	assertActorStoreAgreement(t, f)
	f.restart(t)
	assertActorStoreAgreement(t, f)
	onEnrollmentActor(t, f, func() {
		r := f.state.races[0]
		p := f.state.planets[placed.Placement.Star][placed.Placement.Planet]
		if !f.state.governed(r) || p.Info[0].NumSectsOwned == 0 || !r.Governor[0].Active {
			t.Error("following turn/restart lost playable placement")
		}
	})
}
func TestActorEnrollmentGovernorAuthenticationSafety(t *testing.T) {
	s, _, _ := newTestEngine(t, true)
	for _, tc := range []struct {
		name     string
		governor int
		mutate   func(*model.Race)
	}{{"inactive-zero", 0, func(r *model.Race) { r.Governor[0].Active = false }}, {"inactive-one", 1, func(r *model.Race) { r.Governors = 1 }}, {"active-empty-one", 1, func(r *model.Race) { r.Governor[1].Active = true }}, {"out-of-array", model.MaxGovernors + 1, func(r *model.Race) { r.Governors = 127 }}, {"negative", -1, func(r *model.Race) {}}} {
		t.Run(tc.name, func(t *testing.T) {
			if e := s.request(ctxT(t, 5*time.Second), opTest, func() error {
				r := fixtureRace(t, 1, "Testers", "pw-testers")
				tc.mutate(r)
				s.races[0] = r
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			res, e := s.Authenticate(ctxT(t, 5*time.Second), "Testers", "pw-testers", tc.governor)
			if e != nil || res.OK || s.Mode() != ModeServing {
				t.Fatal("unsafe governor login", e)
			}
		})
	}
}

func TestActorEnrollmentCapacityAndHistoricalSlots(t *testing.T) {
	for _, which := range []string{"full", "gap", "history", "last-slot"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			if which == "history" {
				placed := enrollActor(t, f, actorEnrollmentRequest("operator"))
				onEnrollmentActor(t, f, func() {
					r := f.state.races[0]
					r.Dissolved = true
					if e := f.store.PutRace(r); e != nil {
						t.Error(e)
					}
				})
				next := enrollActor(t, f, actorEnrollmentRequest("account-2"))
				if next.Placement.Player != placed.Placement.Player+1 {
					t.Fatal("dissolved number reused")
				}
				return
			}
			onEnrollmentActor(t, f, func() {
				n := 5
				if which == "last-slot" {
					n = 62
				}
				if which == "full" {
					n = 63
				}
				r := &model.Race{PlayerNum: n, Name: "retained", Dissolved: true, God: n == 1}
				f.state.races[n-1] = r
				if e := f.store.PutRace(r); e != nil {
					t.Error(e)
				}
			})
			req := actorEnrollmentRequest("account-2")
			if which == "full" {
				before := enrollmentDBSnapshot(t, f)
				_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), req)
				assertActorEnrollmentFailure(t, e, "SLOTS_FULL", db.EnrollmentNotCommitted)
				if before != enrollmentDBSnapshot(t, f) {
					t.Fatal("capacity failure wrote")
				}
			} else {
				placed := enrollActor(t, f, req)
				want := 6
				if which == "last-slot" {
					want = 63
				}
				if placed.Placement.Player != want {
					t.Fatal("append policy reused dissolved slot")
				}
				if retry := enrollActor(t, f, req); !retry.Replay {
					t.Fatal("capacity/gap retry failed")
				}
			}
		})
	}
}
func TestActorEnrollmentPreexistingStoreDrift(t *testing.T) {
	for _, which := range []string{"race", "planet", "star", "sdata", "replay"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			req := actorEnrollmentRequest("operator")
			if which == "replay" {
				enrollActor(t, f, req)
			}
			switch which {
			case "race":
				if e := f.store.PutRace(&model.Race{PlayerNum: 1, Name: "uninstalled"}); e != nil {
					t.Fatal(e)
				}
			case "planet", "replay":
				p, e := f.store.GetPlanet(0, 0)
				if e != nil {
					t.Fatal(e)
				}
				p.TotalResources++
				if e = f.store.PutPlanet(p); e != nil {
					t.Fatal(e)
				}
			case "star":
				st, e := f.store.GetStar(0)
				if e != nil {
					t.Fatal(e)
				}
				st.Stability++
				if e = f.store.PutStar(st); e != nil {
					t.Fatal(e)
				}
			case "sdata":
				data, e := f.store.GetStarData()
				if e != nil {
					t.Fatal(e)
				}
				data.VNHitList[0]++
				if e = f.store.PutStarData(data); e != nil {
					t.Fatal(e)
				}
			}
			before := enrollmentDBSnapshot(t, f)
			memory := actorEnrollmentSnapshot(t, f)
			_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), req)
			if e == nil || f.state.Mode() != ModeHalted || memory != actorEnrollmentSnapshot(t, f) {
				t.Fatal("unverified actor/store drift accepted")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if e = f.state.Stop(ctx); !errors.Is(e, ErrHaltedNotFlushed) {
				t.Fatal(e)
			}
			if before != enrollmentDBSnapshot(t, f) {
				t.Fatal("drift overwritten during shutdown")
			}
		})
	}
}
func TestActorEnrollmentStaleTransactionPreconditions(t *testing.T) {
	for _, which := range []string{"planet", "capital", "head", "tail", "race", "chain", "aborted-io-with-drift"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			if which == "chain" {
				onEnrollmentActor(t, f, func() {
					ship := &model.Ship{Number: 7, Alive: true, WhatOrbits: model.LevelPlan, Owner: 64, Race: 64}
					if e := f.store.PutShip(ship); e != nil {
						t.Error(e)
					}
					f.state.ships[7] = ship
					p := f.state.planets[0][0]
					p.Ships = 7
					if e := f.store.PutPlanet(p); e != nil {
						t.Error(e)
					}
				})
			}
			memory := actorEnrollmentSnapshot(t, f)
			hit := false
			var afterForeignWrite string
			f.store.SetMetaFault(func(op string) error {
				if op != "enrollment.before" {
					return nil
				}
				hit = true
				switch which {
				case "tail":
					if e := f.store.DeleteShip(99); e != nil {
						return e
					}
				case "race":
					if e := f.store.PutRace(&model.Race{PlayerNum: 1, Name: "foreign race"}); e != nil {
						return e
					}
				case "chain":
					sh, e := f.store.GetShip(7)
					if e != nil {
						return e
					}
					sh.Fuel = 3
					if e = f.store.PutShip(sh); e != nil {
						return e
					}
				default:
					p, e := f.store.GetPlanet(0, 0)
					if e != nil {
						return e
					}
					switch which {
					case "capital":
						p.Sectors[0].Owner = 64
					case "head":
						p.Ships = 99
					default:
						p.TotalResources++
					}
					if e = f.store.PutPlanet(p); e != nil {
						return e
					}
				}
				afterForeignWrite = enrollmentDBSnapshot(t, f)
				if which == "aborted-io-with-drift" {
					return errors.New("abort but foreign state visible")
				}
				return nil
			})
			_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
			f.store.SetMetaFault(nil)
			if !hit {
				t.Fatal("stale fault not reached")
			}
			assertActorEnrollmentFailure(t, e, "ENROLLMENT_RECONCILIATION_REQUIRED", db.EnrollmentNotCommitted)
			if f.state.Mode() != ModeHalted || memory != actorEnrollmentSnapshot(t, f) || afterForeignWrite != enrollmentDBSnapshot(t, f) {
				t.Fatal("stale proposal installed or partially written")
			}
		})
	}
}
func TestActorEnrollmentCorruptOrUnreadableMetadata(t *testing.T) {
	for _, which := range []string{"corrupt", "read-error", "uncertain-read-error"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			memory := actorEnrollmentSnapshot(t, f)
			hit := false
			if which == "corrupt" {
				if e := f.store.PutMeta([]byte("enrollment/v1/placement/bad"), []byte(`{"schema_version":99}`)); e != nil {
					t.Fatal(e)
				}
			} else {
				committed := false
				f.store.SetMetaFault(func(op string) error {
					if which == "uncertain-read-error" && op == "enrollment.commit-error" {
						committed = true
						return errors.New("ambiguous")
					}
					if strings.HasPrefix(op, "meta.get:enrollment/") && (which == "read-error" || committed) {
						hit = true
						return errors.New("unreadable")
					}
					return nil
				})
			}
			_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
			f.store.SetMetaFault(nil)
			if which != "corrupt" && !hit {
				t.Fatal("read fault not reached")
			}
			outcome := db.EnrollmentNotCommitted
			if which == "uncertain-read-error" {
				outcome = db.EnrollmentUncertain
			}
			assertActorEnrollmentFailure(t, e, "ENROLLMENT_RECONCILIATION_REQUIRED", outcome)
			if f.state.Mode() != ModeHalted || memory != actorEnrollmentSnapshot(t, f) {
				t.Fatal("metadata failure installed state")
			}
		})
	}
}
func TestActorEnrollmentPreservedChainAndIndependentCopies(t *testing.T) {
	f := newEnrollmentActor(t)
	var borrowedPlanet *model.Planet
	var borrowedStar *model.Star
	var oldShip *model.Ship
	onEnrollmentActor(t, f, func() {
		borrowedPlanet = f.state.planets[0][0]
		borrowedStar = f.state.stars[0]
		oldShip = &model.Ship{Number: 7, Alive: true, WhatOrbits: model.LevelPlan, Owner: 64, Race: 64}
		borrowedPlanet.Ships = 7
		f.state.ships[7] = oldShip
		if e := f.store.PutShip(oldShip); e != nil {
			t.Error(e)
		}
		if e := f.store.PutPlanet(borrowedPlanet); e != nil {
			t.Error(e)
		}
	})
	beforePlanet, _ := json.Marshal(borrowedPlanet)
	beforeStar, _ := json.Marshal(borrowedStar)
	placed := enrollActor(t, f, actorEnrollmentRequest("operator"))
	onEnrollmentActor(t, f, func() {
		p := f.state.planets[0][0]
		st := f.state.stars[0]
		sh := f.state.ships[placed.Placement.GovernmentShip]
		if sh.NextShip != 7 || p.Ships != sh.Number || f.state.ships[7].NextShip != 0 || p == borrowedPlanet || st == borrowedStar || f.state.ships[7] == oldShip {
			t.Error("borrowed references/chain not isolated")
		}
		pJSON, _ := json.Marshal(borrowedPlanet)
		stJSON, _ := json.Marshal(borrowedStar)
		if !bytes.Equal(beforePlanet, pJSON) || !bytes.Equal(beforeStar, stJSON) {
			t.Error("preimage mutated")
		}
		borrowedPlanet.Sectors[0].Resource = 0
		borrowedStar.Stability++
		oldShip.Fuel = 99
		if p.Sectors[0].Resource != 37 || f.state.ships[7].Fuel == 99 {
			t.Error("installed state aliases preimages")
		}
	})
	assertActorStoreAgreement(t, f)
}
func TestActorEnrollmentUncertainStoreCannotBeReused(t *testing.T) {
	f := newEnrollmentActor(t)
	f.store.SetMetaFault(func(op string) error {
		if op == "enrollment.commit-error" {
			return errors.New("uncertain")
		}
		return nil
	})
	_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
	assertActorEnrollmentFailure(t, e, "ENROLLMENT_RECONCILIATION_REQUIRED", db.EnrollmentUncertain)
	f.store.SetMetaFault(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = f.state.Stop(ctx)
	// Reusing the same handle must retain CR-07C's taint. This explicitly does
	// not implement or recommend restarting/reopening as reconciliation.
	before := enrollmentDBSnapshot(t, f)
	f.open(t)
	if f.state.Mode() != ModeHalted || before != enrollmentDBSnapshot(t, f) {
		t.Fatal("tainted handle startup served or wrote")
	}
	_, e = f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
	if !errors.Is(e, ErrNotServing) {
		t.Fatal("tainted startup admitted enrollment", e)
	}
	if e = f.state.TriggerUpdate(ctxT(t, 5*time.Second)); !errors.Is(e, ErrHalted) {
		t.Fatal("tainted startup admitted turn", e)
	}
	if f.state.Mode() != ModeHalted || before != enrollmentDBSnapshot(t, f) {
		t.Fatal("tainted store replay became success")
	}
}
func TestActorEnrollmentOrderingAndPolicyDisabled(t *testing.T) {
	for _, size := range []int{0, 1, 2, 31, 255 * 255} {
		order, e := secureEnrollmentPermutation(size)
		if e != nil || placementPermutation(order, size, "test") != nil {
			t.Fatal("crypto permutation invalid", size, e)
		}
	}
	if _, e := secureEnrollmentPermutation(-1); e == nil {
		t.Fatal("negative permutation")
	}
	for _, which := range []string{"duplicate", "invalid-sector", "entropy-error", "nil-policy", "malformed-hash"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			onEnrollmentActor(t, f, func() {
				switch which {
				case "nil-policy":
					f.state.enrollmentPolicy = nil
				case "malformed-hash":
					f.state.enrollmentPolicy.Prepare = func(EnrollmentAuthorization) (EnrollmentCredentials, error) {
						c := enrollmentHashes(t)
						c.RaceHash = c.RaceHash[:28] + "z" + c.RaceHash[29:]
						return c, nil
					}
				default:
					step := 0
					f.state.testHooks.enrollmentOrder = func(n int) ([]int, error) {
						step++
						if which == "entropy-error" {
							return nil, errors.New("entropy")
						}
						order := make([]int, n)
						for i := range order {
							order[i] = i
						}
						if which == "duplicate" || step == 2 {
							order[1] = 0
						}
						return order, nil
					}
				}
			})
			before := enrollmentDBSnapshot(t, f)
			memory := actorEnrollmentSnapshot(t, f)
			_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
			if e == nil || before != enrollmentDBSnapshot(t, f) || memory != actorEnrollmentSnapshot(t, f) {
				t.Fatal("invalid order/policy wrote")
			}
		})
	}
}
func TestActorEnrollmentQueuedModeChange(t *testing.T) {
	f := newEnrollmentActor(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	doneBarrier := make(chan error, 1)
	go func() {
		doneBarrier <- f.state.request(ctxT(t, 5*time.Second), opTest, func() error { close(entered); <-release; f.state.setMode(ModeQuiesced); return nil })
	}()
	<-entered
	done := make(chan error, 1)
	go func() {
		_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
		done <- e
	}()
	deadline := time.After(5 * time.Second)
	for len(f.state.adminCh) == 0 {
		select {
		case <-deadline:
			close(release)
			t.Fatal("request not queued")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	if e := <-doneBarrier; e != nil {
		t.Fatal(e)
	}
	if e := <-done; !errors.Is(e, ErrNotServing) {
		t.Fatal("queued mode recheck", e)
	}
	onEnrollmentActor(t, f, func() { f.state.setMode(ModeServing) })
	if f.state.Status().Races != 0 {
		t.Fatal("queued mode change enrolled")
	}
}

func TestActorEnrollmentConcurrentRequests(t *testing.T) {
	f := newEnrollmentActor(t)
	firstReq := actorEnrollmentRequest("operator")
	first := enrollActor(t, f, firstReq)
	var wg sync.WaitGroup
	results := make([]EnrollmentResult, 4)
	errs := make([]error, 4)
	wg.Add(4)
	for i := 0; i < 4; i++ {
		go func(n int) {
			defer wg.Done()
			results[n], errs[n] = f.state.EnrollTrusted(ctxT(t, 10*time.Second), firstReq)
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil || !results[i].Replay || results[i].Placement != first.Placement {
			t.Fatal("concurrent duplicate", i, e)
		}
	}
	wg.Add(2)
	for i, caller := range []string{"account-2", "account-3"} {
		go func(n int, caller string) {
			defer wg.Done()
			results[n], errs[n] = f.state.EnrollTrusted(ctxT(t, 10*time.Second), actorEnrollmentRequest(caller))
		}(i, caller)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].Placement.Player == results[1].Placement.Player || results[0].Placement.GovernmentShip == results[1].Placement.GovernmentShip || results[0].Placement.Star == results[1].Placement.Star {
		t.Fatal("concurrent new allocations", errs[0], errs[1])
	}
	if f.state.Status().Races != 3 || f.state.metrics.Enrollments.Value("success") != 3 || f.state.metrics.Enrollments.Value("replay") != 4 {
		t.Fatal("concurrent counts")
	}
	assertActorStoreAgreement(t, f)
}
func TestActorEnrollmentMaintenanceRetryGate(t *testing.T) {
	f := newEnrollmentActor(t)
	req := actorEnrollmentRequest("operator")
	placed := enrollActor(t, f, req)
	if e := f.state.Quiesce(ctxT(t, 5*time.Second)); e != nil {
		t.Fatal(e)
	}
	before := enrollmentDBSnapshot(t, f)
	hit := false
	f.store.SetMetaFault(func(op string) error {
		if strings.HasPrefix(op, "meta.get:enrollment/") {
			hit = true
			return errors.New("lookup while quiesced")
		}
		return nil
	})
	_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), req)
	f.store.SetMetaFault(nil)
	if !errors.Is(e, ErrNotServing) || hit || before != enrollmentDBSnapshot(t, f) {
		t.Fatal("quiesced retry read/wrote enrollment")
	}
	f.restart(t)
	if f.state.Mode() != ModeQuiesced {
		t.Fatal("CR-43 maintenance lost")
	}
	if _, e = f.state.EnrollTrusted(ctxT(t, 5*time.Second), req); !errors.Is(e, ErrNotServing) {
		t.Fatal(e)
	}
	if e = f.state.Resume(ctxT(t, 5*time.Second)); e != nil {
		t.Fatal(e)
	}
	retry := enrollActor(t, f, req)
	if !retry.Replay || retry.Placement != placed.Placement {
		t.Fatal("resume retry")
	}
	assertActorStoreAgreement(t, f)
}
func TestActorEnrollmentChangedCredentialReference(t *testing.T) {
	f := newEnrollmentActor(t)
	req := actorEnrollmentRequest("operator")
	enrollActor(t, f, req)
	onEnrollmentActor(t, f, func() {
		previous := f.state.enrollmentPolicy.Authorize
		f.state.enrollmentPolicy.Authorize = func(req EnrollmentRequest) (EnrollmentAuthorization, error) {
			a, e := previous(req)
			a.GovernorCredentialRef = "replacement-version"
			return a, e
		}
	})
	before := enrollmentDBSnapshot(t, f)
	_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), req)
	assertActorEnrollmentFailure(t, e, "REQUEST_CONFLICT", db.EnrollmentNotCommitted)
	if before != enrollmentDBSnapshot(t, f) {
		t.Fatal("changed credential reference wrote")
	}
}

func TestActorEnrollmentNormalRaceFollowingCommandsTurnAndRestart(t *testing.T) {
	f := newEnrollmentActor(t)
	enrollActor(t, f, actorEnrollmentRequest("operator"))
	req := actorEnrollmentRequest("account-2")
	placed := enrollActor(t, f, req)
	p := placed.Placement.Player
	for _, command := range []string{"cs", "survey", "map", fmt.Sprintf("capital #%d", placed.Placement.GovernmentShip)} {
		responder := &recordingResponder{}
		if e := f.state.HandleCommand(ctxT(t, 5*time.Second), p, 0, command, responder); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(strings.ToLower(responder.text()), "you can't") {
			t.Fatal("normal race command refused", command)
		}
	}
	// capital spends AP in actor memory; legacy command persistence flushes it
	// at the following turn. Full store agreement is asserted after that commit.
	if e := f.state.TriggerUpdate(ctxT(t, 10*time.Second)); e != nil {
		t.Fatal(e)
	}
	assertActorStoreAgreement(t, f)
	f.restart(t)
	assertActorStoreAgreement(t, f)
	onEnrollmentActor(t, f, func() {
		r := f.state.races[p-1]
		planet := f.state.planets[placed.Placement.Star][placed.Placement.Planet]
		if r.God || !f.state.governed(r) || !r.Governor[0].Active || planet.Info[p-1].NumSectsOwned == 0 || int(planet.SectorAt(int(placed.Placement.CapitalX), int(placed.Placement.CapitalY)).Owner) != p {
			t.Error("normal race lost initialized colony/government")
		}
	})
	retry := enrollActor(t, f, req)
	if !retry.Replay || retry.Placement != placed.Placement {
		t.Fatal("normal race replay after turn/restart")
	}
}
func TestActorEnrollmentUnsafeShipCandidate(t *testing.T) {
	for _, alive := range []bool{false, true} {
		t.Run(fmt.Sprint(alive), func(t *testing.T) {
			f := newEnrollmentActor(t)
			onEnrollmentActor(t, f, func() {
				if e := f.store.DeleteShip(7); e != nil {
					t.Error(e)
				}
				ship := &model.Ship{Number: 7, Alive: alive, Owner: 64, Race: 64}
				if e := f.store.PutShip(ship); e != nil {
					t.Error(e)
				}
				f.state.ships[7] = ship
			})
			before := enrollmentDBSnapshot(t, f)
			memory := actorEnrollmentSnapshot(t, f)
			_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
			assertActorEnrollmentFailure(t, e, "SHIP_ALLOCATION_CONFLICT", db.EnrollmentNotCommitted)
			if before != enrollmentDBSnapshot(t, f) || memory != actorEnrollmentSnapshot(t, f) {
				t.Fatal("unsafe candidate overwritten or consumed")
			}
		})
	}
}

func TestActorEnrollmentPendingCommandAPWaitsForTurn(t *testing.T) {
	f := newEnrollmentActor(t)
	enrollActor(t, f, actorEnrollmentRequest("operator"))
	req := actorEnrollmentRequest("account-2")
	placed := enrollActor(t, f, req)
	responder := &recordingResponder{}
	if e := f.state.HandleCommand(ctxT(t, 5*time.Second), placed.Placement.Player, 0, fmt.Sprintf("capital #%d", placed.Placement.GovernmentShip), responder); e != nil {
		t.Fatal(e)
	}
	before := enrollmentDBSnapshot(t, f)
	memory := actorEnrollmentSnapshot(t, f)
	for _, request := range []EnrollmentRequest{req, actorEnrollmentRequest("account-3")} {
		_, e := f.state.EnrollTrusted(ctxT(t, 5*time.Second), request)
		assertActorEnrollmentFailure(t, e, "ENROLLMENT_PENDING_TURN", db.EnrollmentNotCommitted)
		if f.state.Mode() != ModeServing || before != enrollmentDBSnapshot(t, f) || memory != actorEnrollmentSnapshot(t, f) {
			t.Fatal("pending AP admission changed or halted world")
		}
	}
	if e := f.state.TriggerUpdate(ctxT(t, 10*time.Second)); e != nil {
		t.Fatal(e)
	}
	assertActorStoreAgreement(t, f)
	retry := enrollActor(t, f, req)
	if !retry.Replay || retry.Placement != placed.Placement {
		t.Fatal("retry after normal AP flush")
	}
	enrollActor(t, f, actorEnrollmentRequest("account-3"))
}

func TestActorEnrollmentRefusesCachedOrScopedFreeID(t *testing.T) {
	for _, which := range []string{"cache-live", "cache-dead", "cache-nil", "directory"} {
		t.Run(which, func(t *testing.T) {
			f := newEnrollmentActor(t)
			onEnrollmentActor(t, f, func() {
				if e := f.store.DeleteShip(7); e != nil {
					t.Error(e)
				}
				switch which {
				case "cache-live":
					f.state.ships[7] = &model.Ship{Number: 7, Alive: true}
				case "cache-dead":
					f.state.ships[7] = &model.Ship{Number: 7}
				case "cache-nil":
					f.state.ships[7] = nil
				case "directory":
					f.state.dirs[0][0] = model.Directory{Level: model.LevelShip, ShipNo: 7}
				}
			})
			ticket, e := f.store.EnrollmentShipCandidate()
			if e != nil || ticket.Number != 7 {
				t.Fatal("fixture did not expose storage-eligible ID", e)
			}
			before := enrollmentDBSnapshot(t, f)
			memory := actorEnrollmentSnapshot(t, f)
			_, e = f.state.EnrollTrusted(ctxT(t, 5*time.Second), actorEnrollmentRequest("operator"))
			assertActorEnrollmentFailure(t, e, "SHIP_ALLOCATION_CONFLICT", db.EnrollmentNotCommitted)
			if before != enrollmentDBSnapshot(t, f) || memory != actorEnrollmentSnapshot(t, f) || f.state.Mode() != ModeServing {
				t.Fatal("cached/scoped ID reused, consumed or repaired")
			}
		})
	}
}
