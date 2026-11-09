package game

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/racegen"
	"golang.org/x/crypto/bcrypt"
)

// EnrollmentRequest has no password, account assertion, cost or request hash.
// CallerReference identifies trusted in-process authorization context, NOT a
// bearer token. The configured authority must resolve and authorize it; no
// production transport configures that authority in CR-07D.
type EnrollmentRequest struct {
	RequestID       string
	CallerReference string
	GameID          string
	Spec            racegen.Spec
	God, Guest      bool
}

// EnrollmentAuthorization must be server-owned, account/game-authorized data.
// Credential references are immutable version identifiers, never password
// digests or browser claims. Rebinding a reference to another secret violates
// the provider contract. Requested privilege flags are checked independently.
type EnrollmentAuthorization struct {
	CallerID              string
	GameID                string
	RaceCredentialRef     string
	GovernorCredentialRef string
	AllowGod, AllowGuest  bool
}
type EnrollmentCredentials struct{ RaceHash, GovernorHash string }

// EnrollmentPolicy is a trusted in-process capability configured at construction.
// Nil disables enrollment. Both callbacks run on the actor and must not call it
// recursively. Prepare supplies distinct validated bcrypt representations; it
// must propagate preparation failures without a plaintext fallback. CR-07E must
// design real operator authentication before wiring any production caller.
type EnrollmentPolicy struct {
	GameID    string
	Authorize func(EnrollmentRequest) (EnrollmentAuthorization, error)
	Prepare   func(EnrollmentAuthorization) (EnrollmentCredentials, error)
}
type EnrollmentResult struct {
	Placement db.PlacementRecord
	Replay    bool
	Outcome   db.EnrollmentOutcome
}
type EnrollmentFailure struct {
	Code       string
	Outcome    db.EnrollmentOutcome
	Validation racegen.Errors
	cause      error
}

func (e *EnrollmentFailure) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Outcome) }
func (e *EnrollmentFailure) Unwrap() error { return e.cause }
func enrollmentFailure(code string, outcome db.EnrollmentOutcome) error {
	return &EnrollmentFailure{Code: code, Outcome: outcome}
}

func enrollmentID(s string, max int) bool {
	if len(s) < 1 || len(s) > max {
		return false
	}
	for _, c := range s {
		allowed := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
		if !allowed {
			return false
		}
	}
	return true
}

var enrollmentBcrypt = regexp.MustCompile(`^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$`)

func preparedEnrollmentCredentials(c EnrollmentCredentials) bool {
	if c.RaceHash == c.GovernorHash {
		return false
	}
	for _, h := range []string{c.RaceHash, c.GovernorHash} {
		cost, e := bcrypt.Cost([]byte(h))
		if e != nil || cost < bcrypt.DefaultCost || !enrollmentBcrypt.MatchString(h) || strings.IndexByte("./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", h[28])%16 != 0 || strings.IndexByte("./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", h[59])%4 != 0 {
			return false
		}
	}
	return true
}
func enrollmentFingerprint(req EnrollmentRequest, auth EnrollmentAuthorization) (string, error) {
	// Deliberately exclude prepared bcrypt bytes: random salts are not retry IDs.
	canonical := struct {
		Version                                           int
		RequestID, CallerID, GameID, RaceRef, GovernorRef string
		Spec                                              racegen.Spec
		God, Guest                                        bool
	}{1, req.RequestID, auth.CallerID, auth.GameID, auth.RaceCredentialRef, auth.GovernorCredentialRef, req.Spec, req.God, req.Guest}
	raw, e := json.Marshal(canonical)
	if e != nil {
		return "", enrollmentFailure("INVALID_DESIGN", db.EnrollmentNotCommitted)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// EnrollTrusted is intentionally absent from net.GameIface and the admin HTTP
// interface. It is enabled only by explicit in-process policy, never by preview
// configuration. Requests admitted during a turn/non-serving mode are refused;
// requests queued before a transition are checked again on the actor. Started
// timeouts follow submit's ErrStillRunning contract, not rollback semantics.
func (s *State) EnrollTrusted(ctx context.Context, req EnrollmentRequest) (EnrollmentResult, error) {
	if s.enrollmentPolicy == nil {
		s.metrics.Enrollments.Inc("disabled")
		return EnrollmentResult{}, ErrEnrollmentDisabled
	}
	if s.Mode() != ModeServing || s.draining.Load() {
		s.metrics.Enrollments.Inc("not_serving")
		return EnrollmentResult{}, ErrNotServing
	}
	start := s.now()
	result, e := call(s, ctx, opEnroll, func() (EnrollmentResult, error) { return s.enrollTrusted(req) })
	s.adminDone(opEnroll, start, e)
	return result, e
}

func (s *State) enrollTrusted(req EnrollmentRequest) (result EnrollmentResult, err error) {
	outcome := db.EnrollmentNotCommitted
	label := "rejected"
	defer func() {
		if recover() != nil {
			label = "not_committed"
			switch outcome {
			case db.EnrollmentCommitted:
				label = "committed_error"
			case db.EnrollmentUncertain:
				label = "uncertain"
			}
			s.halt("enrollment panic: "+string(outcome)+"; operator reconciliation required; no shutdown flush", "")
			result = EnrollmentResult{Outcome: outcome}
			err = enrollmentFailure("ENROLLMENT_RECONCILIATION_REQUIRED", outcome)
		}
		var failure *EnrollmentFailure
		if errors.As(err, &failure) {
			switch failure.Outcome {
			case db.EnrollmentUncertain:
				label = "uncertain"
			case db.EnrollmentCommitted:
				label = "committed_error"
			}
		}
		s.metrics.Enrollments.Inc(label)
	}()
	if s.Mode() != ModeServing || s.draining.Load() {
		label = "not_serving"
		return result, ErrNotServing
	}
	policy := s.enrollmentPolicy
	if !enrollmentID(req.RequestID, 64) || !enrollmentID(req.CallerReference, 128) || req.GameID != policy.GameID || !enrollmentID(policy.GameID, 128) {
		return result, enrollmentFailure("INVALID_IDENTITY", outcome)
	}
	auth, e := policy.Authorize(req)
	if e != nil || !enrollmentID(auth.CallerID, 128) || auth.GameID != policy.GameID || !enrollmentID(auth.RaceCredentialRef, 128) || !enrollmentID(auth.GovernorCredentialRef, 128) || auth.RaceCredentialRef == auth.GovernorCredentialRef || req.God && !auth.AllowGod || req.Guest && !auth.AllowGuest || req.God && req.Guest {
		return result, enrollmentFailure("UNAUTHORIZED_ENROLLMENT", outcome)
	}
	if !enrollmentName(req.Spec.Name) {
		return result, enrollmentFailure("INVALID_NAME", outcome)
	}
	if validation := racegen.Validate(req.Spec); len(validation) > 0 {
		return result, &EnrollmentFailure{Code: "INVALID_DESIGN", Outcome: outcome, Validation: validation}
	}
	fingerprint, e := enrollmentFingerprint(req, auth)
	if e != nil {
		return result, e
	}
	// This non-reserving read also checks CR-07C's uncertainty guard. Exhaustion
	// is acted on only AFTER replay lookup, so consumed slots do not defeat retry.
	admission, e := s.store.EnrollmentAdmission(auth.CallerID)
	if e != nil {
		return result, s.enrollmentReadFailure(e)
	}
	recorded, e := s.store.GetPlacement(auth.CallerID, req.RequestID)
	if e != nil {
		var failure *db.EnrollmentError
		if errors.As(e, &failure) && failure.Code == db.EnrollmentConflict {
			return result, enrollmentFailure("REQUEST_CONFLICT", outcome)
		}
		return result, s.enrollmentReadFailure(e)
	}
	if recorded != nil && recorded.RequestHash != fingerprint {
		return result, enrollmentFailure("REQUEST_CONFLICT", outcome)
	}
	consistent, pendingAP := s.enrollmentWorldState()
	if pendingAP {
		return result, enrollmentFailure("ENROLLMENT_PENDING_TURN", outcome)
	}
	if !consistent {
		return result, s.enrollmentReconcile(outcome, "actor world differs from stored world")
	}
	if recorded != nil {
		if !s.enrollmentReplayConsistent(recorded) {
			return result, s.enrollmentReconcile(db.EnrollmentCommitted, "durable retry disagrees with current actor/store state")
		}
		label = "replay"
		return EnrollmentResult{Placement: *recorded, Replay: true, Outcome: db.EnrollmentCommitted}, nil
	}
	player := admission.NextPlayer
	if admission.CallerPlaced {
		return result, enrollmentFailure("ACCOUNT_ALREADY_PLACED", outcome)
	}
	if player > 63 {
		return result, enrollmentFailure("SLOTS_FULL", outcome)
	}
	if player == 1 && !req.God {
		return result, enrollmentFailure("GOD_BOOTSTRAP_REQUIRED", outcome)
	}
	for _, r := range s.races {
		if r != nil && strings.EqualFold(r.Name, req.Spec.Name) {
			return result, enrollmentFailure("NAME_TAKEN", outcome)
		}
	}
	credentials, e := policy.Prepare(auth)
	if e != nil || !preparedEnrollmentCredentials(credentials) {
		return result, enrollmentFailure("CREDENTIAL_PREPARATION_FAILED", outcome)
	}
	ticket, e := s.store.EnrollmentShipCandidate()
	if e != nil {
		return result, s.enrollmentPreparationFailure(e)
	}
	// Stored availability is insufficient if the actor still knows this ID or
	// a session directory would be retargeted by reuse. Do not repair/clear either.
	if _, exists := s.ships[ticket.Number]; exists {
		return result, enrollmentFailure("SHIP_ALLOCATION_CONFLICT", outcome)
	}
	for _, governors := range s.dirs {
		for _, dir := range governors {
			if dir.ShipNo == ticket.Number {
				return result, enrollmentFailure("SHIP_ALLOCATION_CONFLICT", outcome)
			}
		}
	}
	ships, e := s.store.AllShips()
	if e != nil {
		return result, s.enrollmentReadFailure(e)
	}
	if s.sdata == nil || s.sdata.NumStars == 0 || int(s.sdata.NumStars) > len(s.stars) {
		return result, s.enrollmentReconcile(outcome, "invalid star count")
	}
	count := int(s.sdata.NumStars)
	for _, st := range s.stars[count:] {
		if st != nil {
			return result, s.enrollmentReconcile(outcome, "star data omits existing stars")
		}
	}
	world := PlacementUniverse{Stars: s.stars[:count], Planets: s.planets, Races: s.races, Ships: ships}
	order, e := s.enrollmentOrders(req.Spec, world)
	if e != nil {
		return result, e
	}
	proposal, e := ProposePlacement(req.Spec, player, ticket.Number, world, order, PlacementIdentity{RaceCredential: credentials.RaceHash, GovernorCredential: credentials.GovernorHash, God: req.God, Guest: req.Guest})
	if e != nil {
		return result, e
	}
	oldPlanet := s.planets[proposal.Planet.StarNum][proposal.Planet.PlanetNum]
	chain := []*model.Ship{}
	for n := oldPlanet.Ships; n != 0; n = ships[n].NextShip {
		chain = append(chain, ships[n])
	} // ProposePlacement validated the complete chain.
	input := db.Enrollment{RequestID: req.RequestID, CallerID: auth.CallerID, RequestHash: fingerprint, CreatedAt: s.now().UTC(), Race: proposal.Race, Planet: proposal.Planet, Star: proposal.Star, Ship: proposal.Ship, ExpectedPlanet: oldPlanet, ExpectedStar: s.stars[proposal.Star.Number], ExpectedChain: chain, Ticket: ticket, CapitalX: proposal.CapitalX, CapitalY: proposal.CapitalY}
	beforeDigest, digestError := s.store.WorldDigest()
	if digestError != nil {
		return result, s.enrollmentReadFailure(digestError)
	}
	outcome = db.EnrollmentUncertain // Protect a panic around an attempted write.
	recorded, e = s.store.PutEnrollment(input)
	if e != nil {
		var failure *db.EnrollmentError
		if !errors.As(e, &failure) {
			label = "uncertain"
			return result, s.enrollmentReconcile(outcome, "unclassified persistence failure")
		}
		outcome = failure.Outcome
		switch outcome {
		case db.EnrollmentUncertain:
			label = "uncertain"
			return result, s.enrollmentReconcile(outcome, "commit outcome uncertain; visibility does not prove durability")
		case db.EnrollmentNotCommitted:
			label = "not_committed"
			if failure.Code == db.EnrollmentStale || failure.Code == db.EnrollmentCorrupt || failure.Code == db.EnrollmentFenced || failure.Code == db.EnrollmentConflict {
				return result, s.enrollmentReconcile(outcome, "storage preconditions or integrity disagree with actor")
			}
			afterDigest, readErr := s.store.WorldDigest()
			if readErr != nil || afterDigest != beforeDigest {
				return result, s.enrollmentReconcile(outcome, "store changed unexpectedly after rejected enrollment")
			}
			return result, enrollmentFailure(string(failure.Code), outcome)
		case db.EnrollmentCommitted:
			label = "committed_error"
		default:
			label = "uncertain"
			outcome = db.EnrollmentUncertain
			return result, s.enrollmentReconcile(outcome, "unknown persistence outcome")
		}
	} else {
		outcome = db.EnrollmentCommitted
		label = "success"
	}
	if recorded == nil {
		return result, s.enrollmentReconcile(outcome, "missing committed placement identity")
	}
	// Read independent stored copies; verify all proposal/chain data before any
	// installation. A failed readback after a confirmed commit still means COMMITTED.
	if err = s.installEnrollment(input, recorded, ships); err != nil {
		label = "committed_error"
		return EnrollmentResult{Outcome: outcome}, s.enrollmentReconcile(outcome, "committed enrollment could not be verified or installed")
	}
	result = EnrollmentResult{Placement: *recorded, Outcome: outcome}
	if e != nil {
		return result, enrollmentFailure("ENROLLMENT_COMMITTED_ERROR", outcome)
	}
	return result, nil
}

func (s *State) enrollmentReconcile(outcome db.EnrollmentOutcome, reason string) error {
	s.halt("enrollment: "+reason+" ("+string(outcome)+"); operator reconciliation required; shutdown must not flush memory", "")
	return enrollmentFailure("ENROLLMENT_RECONCILIATION_REQUIRED", outcome)
}
func (s *State) enrollmentReadFailure(e error) error {
	outcome := db.EnrollmentNotCommitted
	var failure *db.EnrollmentError
	if errors.As(e, &failure) && failure.Outcome == db.EnrollmentUncertain {
		outcome = db.EnrollmentUncertain
	}
	return s.enrollmentReconcile(outcome, "metadata/world could not be trusted")
}
func (s *State) enrollmentPreparationFailure(e error) error {
	var failure *db.EnrollmentError
	if errors.As(e, &failure) && failure.Code == db.EnrollmentConflict {
		return enrollmentFailure("SHIP_ALLOCATION_CONFLICT", db.EnrollmentNotCommitted)
	}
	return s.enrollmentReadFailure(e)
}

func secureEnrollmentPermutation(size int) ([]int, error) {
	if size < 0 || size > 255*255 {
		return nil, enrollmentFailure("INVALID_SEARCH_ORDER", db.EnrollmentNotCommitted)
	}
	order := make([]int, size)
	for i := range order {
		order[i] = i
	}
	for i := size - 1; i > 0; i-- {
		j, e := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(i+1)))
		if e != nil {
			return nil, enrollmentFailure("ENROLLMENT_RANDOMNESS_FAILED", db.EnrollmentNotCommitted)
		}
		k := int(j.Int64())
		order[i], order[k] = order[k], order[i]
	}
	return order, nil
}
func (s *State) enrollmentOrders(spec racegen.Spec, world PlacementUniverse) (PlacementOrder, error) {
	perm := secureEnrollmentPermutation
	if s.testHooks != nil && s.testHooks.enrollmentOrder != nil {
		perm = s.testHooks.enrollmentOrder
	}
	stars, e := perm(len(world.Stars))
	if e != nil {
		return PlacementOrder{}, enrollmentFailure("ENROLLMENT_RANDOMNESS_FAILED", db.EnrollmentNotCommitted)
	}
	if e = placementPermutation(stars, len(world.Stars), "stars"); e != nil {
		return PlacementOrder{}, e
	}
	for i, st := range world.Stars {
		if st == nil || int(st.Number) != i || int(st.NumPlanets) > model.MaxPlanets {
			return PlacementOrder{}, enrollmentFailure("INVALID_UNIVERSE", db.EnrollmentNotCommitted)
		}
	}
	_, planet, e := placementHome(spec, world, stars)
	if e != nil {
		return PlacementOrder{}, e
	}
	sectors, e := perm(len(planet.Sectors))
	if e != nil {
		return PlacementOrder{}, enrollmentFailure("ENROLLMENT_RANDOMNESS_FAILED", db.EnrollmentNotCommitted)
	}
	return PlacementOrder{Stars: stars, Sectors: sectors}, nil
}

func (s *State) enrollmentReplayConsistent(rec *db.PlacementRecord) bool {
	if rec.Player < 1 || rec.Player > len(s.races) || int(rec.Star) >= len(s.stars) {
		return false
	}
	r, e := s.store.GetRace(rec.Player)
	if e != nil || r == nil || r.PlayerNum != rec.Player || !reflect.DeepEqual(r, s.races[rec.Player-1]) {
		return false
	}
	p, e := s.store.GetPlanet(rec.Star, rec.Planet)
	if e != nil || p == nil || !reflect.DeepEqual(p, s.planets[rec.Star][rec.Planet]) {
		return false
	}
	st, e := s.store.GetStar(rec.Star)
	if e != nil || st == nil || !reflect.DeepEqual(st, s.stars[rec.Star]) {
		return false
	}
	for _, n := range []uint32{rec.GovernmentShip, r.GovShip} {
		if n == 0 {
			continue
		}
		sh, e := s.store.GetShip(n)
		if e != nil || !reflect.DeepEqual(sh, s.ships[n]) {
			return false
		}
	}
	return true
}

func (s *State) installEnrollment(in db.Enrollment, recorded *db.PlacementRecord, beforeShips map[uint32]*model.Ship) error {
	rec, e := s.store.GetPlacement(in.CallerID, in.RequestID)
	if e != nil || !reflect.DeepEqual(rec, recorded) {
		return errors.New("placement verification failed")
	}
	r, e := s.store.GetRace(in.Race.PlayerNum)
	if e != nil || !reflect.DeepEqual(r, in.Race) {
		return errors.New("race verification failed")
	}
	p, e := s.store.GetPlanet(in.Planet.StarNum, in.Planet.PlanetNum)
	if e != nil || !reflect.DeepEqual(p, in.Planet) {
		return errors.New("planet verification failed")
	}
	st, e := s.store.GetStar(in.Star.Number)
	if e != nil || !reflect.DeepEqual(st, in.Star) {
		return errors.New("star verification failed")
	}
	ships, e := s.store.AllShips()
	if e != nil {
		return errors.New("ship verification failed")
	}
	expected := make(map[uint32]*model.Ship, len(beforeShips)+1)
	for n, sh := range beforeShips {
		expected[n] = sh
	}
	expected[in.Ship.Number] = in.Ship
	if !reflect.DeepEqual(ships, expected) || r.GovShip != in.Ship.Number || p.Ships != in.Ship.Number || int(st.Number) >= len(s.stars) || r.PlayerNum > len(s.races) {
		return errors.New("placement association failed")
	}
	races := append([]*model.Race(nil), s.races...)
	races[r.PlayerNum-1] = r
	stars := append([]*model.Star(nil), s.stars...)
	stars[st.Number] = st
	planets := make(map[uint8]map[uint8]*model.Planet, len(s.planets))
	for n, group := range s.planets {
		planets[n] = group
	}
	group := make(map[uint8]*model.Planet, len(planets[p.StarNum]))
	for n, old := range planets[p.StarNum] {
		group[n] = old
	}
	group[p.PlanetNum] = p
	planets[p.StarNum] = group
	storedRaces, readErr := s.store.AllRaces()
	if readErr != nil || !reflect.DeepEqual(storedRaces, races) {
		return errors.New("race collection changed")
	}
	storedStars, readErr := s.store.AllStars()
	if readErr != nil || !reflect.DeepEqual(storedStars, stars) {
		return errors.New("star collection changed")
	}
	storedPlanets, readErr := s.store.AllPlanets()
	if readErr != nil || !reflect.DeepEqual(storedPlanets, planets) {
		return errors.New("planet collection changed")
	}
	data, readErr := s.store.GetStarData()
	if readErr != nil || !reflect.DeepEqual(data, s.sdata) {
		return errors.New("universe roots changed")
	}
	gov := r.Governor[0]
	dir := model.Directory{Level: gov.DefLevel, StarNum: gov.DefSystem, PlanetNum: gov.DefPlanetNum, LastY: [2]float64{0, st.YPos}, LastX: [2]float64{0, st.XPos}}
	if s.testHooks != nil && s.testHooks.beforeEnrollmentInstall != nil {
		s.testHooks.beforeEnrollmentInstall()
	}
	s.races, s.stars, s.planets, s.ships = races, stars, planets, ships
	s.dirs[r.PlayerNum-1][0] = dir
	s.dirs[r.PlayerNum-1][0].Prompt = s.buildPrompt(r.PlayerNum, 0)
	s.statusMu.Lock()
	universe := s.status.universe
	s.statusMu.Unlock()
	s.publishWorld(universe)
	return nil
}

func enrollmentName(name string) bool {
	if name == "" || len(name) > 64 || !utf8.ValidString(name) || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Stars/global AP can legitimately be actor-owned pending deductions (CR-17).
// Reject admission temporarily until the normal turn flush; never merge/flush
// them here. Any other disagreement remains an integrity/reconciliation failure.
func (s *State) enrollmentWorldState() (consistent, pendingAP bool) {
	races, e := s.store.AllRaces()
	if e != nil || !reflect.DeepEqual(races, s.races) {
		return false, false
	}
	planets, e := s.store.AllPlanets()
	if e != nil || !reflect.DeepEqual(planets, s.planets) {
		return false, false
	}
	stars, e := s.store.AllStars()
	if e != nil || len(stars) != len(s.stars) {
		return false, false
	}
	for i, stored := range stars {
		actor := s.stars[i]
		if stored == nil || actor == nil {
			if stored != actor {
				return false, false
			}
			continue
		}
		a, b := *stored, *actor
		pendingAP = pendingAP || a.AP != b.AP
		a.AP = b.AP
		if !reflect.DeepEqual(a, b) {
			return false, false
		}
	}
	data, e := s.store.GetStarData()
	if e != nil || s.sdata == nil {
		return false, false
	}
	if data == nil {
		data = &model.StarData{}
	}
	a, b := *data, *s.sdata
	pendingAP = pendingAP || a.AP != b.AP
	a.AP = b.AP
	if !reflect.DeepEqual(a, b) {
		return false, false
	}
	return !pendingAP, pendingAP
}
