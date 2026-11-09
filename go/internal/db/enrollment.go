package db

// CR-07C is storage only. Callers must be the authoritative, serialized actor,
// authenticate CallerID, validate RequestHash from the canonical request, and
// supply CR-07B proposals from one stable snapshot. Never use against a store
// owned by a running engine. Browser identifiers/hashes are not authentication.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

const placementPrefix = "enrollment/v1/placement/"

// EnrollmentOutcome describes durability, independently of the error code.
type EnrollmentOutcome string

const (
	EnrollmentNotCommitted EnrollmentOutcome = "NOT_COMMITTED"
	EnrollmentCommitted    EnrollmentOutcome = "COMMITTED"
	EnrollmentUncertain    EnrollmentOutcome = "UNCERTAIN"
)

type EnrollmentCode string

const (
	EnrollmentInvalid  EnrollmentCode = "INVALID_ENROLLMENT"
	EnrollmentConflict EnrollmentCode = "ENROLLMENT_CONFLICT"
	EnrollmentStale    EnrollmentCode = "STALE_PLACEMENT"
	EnrollmentCorrupt  EnrollmentCode = "CORRUPT_ENROLLMENT"
	EnrollmentIO       EnrollmentCode = "ENROLLMENT_IO"
	EnrollmentFenced   EnrollmentCode = "ENROLLMENT_FENCED"
)

// EnrollmentError never includes secret-bearing models or hook error text.
type EnrollmentError struct {
	Code    EnrollmentCode
	Outcome EnrollmentOutcome
	cause   error
}

func (e *EnrollmentError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Outcome) }
func (e *EnrollmentError) Unwrap() error { return e.cause }
func enrollmentError(c EnrollmentCode) error {
	return &EnrollmentError{Code: c, Outcome: EnrollmentNotCommitted}
}

type enrollmentState struct {
	enrollmentMu        sync.Mutex
	enrollmentUncertain bool
}

// EnrollmentShip is an optimistic ticket, NOT a reservation. Obtain it before
// ProposePlacement; all fields are rechecked in the single write transaction.
type EnrollmentShip struct {
	Number     uint32
	FreeKey    uint64
	FreeDigest string
}

// Enrollment borrows prepared models for the duration of PutEnrollment. Callers
// must exclude concurrent mutation. ExpectedChain contains every existing ship
// from ExpectedPlanet.Ships in link order; full preimages protect unchanged heads.
// Requested ship number is fixed by Ticket, never rewritten or separately reserved.
type Enrollment struct {
	RequestID          string
	CallerID           string
	RequestHash        string
	CreatedAt          time.Time
	Race               *model.Race
	Planet             *model.Planet
	Star               *model.Star
	Ship               *model.Ship
	ExpectedPlanet     *model.Planet
	ExpectedStar       *model.Star
	ExpectedChain      []*model.Ship
	Ticket             EnrollmentShip
	CapitalX, CapitalY uint8
}

// PlacementRecord is additive metadata; no secrets or model values are stored.
// ProposalHash binds prepared entities; RequestHash binds the canonical design.
// RecordHash detects internal inconsistency, not malicious database tampering.
type PlacementRecord struct {
	SchemaVersion  int       `json:"schema_version"`
	RequestID      string    `json:"request_id"`
	CallerID       string    `json:"caller_id"`
	RequestHash    string    `json:"request_hash"`
	ProposalHash   string    `json:"proposal_hash"`
	Player         int       `json:"player"`
	Star           uint8     `json:"star"`
	Planet         uint8     `json:"planet"`
	CapitalX       uint8     `json:"capital_x"`
	CapitalY       uint8     `json:"capital_y"`
	Width          uint8     `json:"width"`
	Height         uint8     `json:"height"`
	GovernmentShip uint32    `json:"government_ship"`
	CreatedAt      time.Time `json:"created_at"`
	RecordHash     string    `json:"record_hash"`
}

var placementFields = requiredFields{required: []string{"schema_version", "request_id", "caller_id", "request_hash", "proposal_hash", "player", "star", "planet", "capital_x", "capital_y", "width", "height", "government_ship", "created_at", "record_hash"}}

func enrollmentToken(s string, max int) bool {
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
func enrollmentHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}
func hashJSON(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func (r PlacementRecord) checksum() string { r.RecordHash = ""; h, _ := hashJSON(r); return h }
func (r PlacementRecord) valid() bool {
	return r.SchemaVersion == 1 && enrollmentToken(r.RequestID, 64) && enrollmentToken(r.CallerID, 128) && enrollmentHash(r.RequestHash) && enrollmentHash(r.ProposalHash) && r.Player >= 1 && r.Player <= 63 && int(r.Star) < model.NumStars && int(r.Planet) < model.MaxPlanets && r.Width > 0 && r.Height > 0 && r.CapitalX < r.Width && r.CapitalY < r.Height && r.GovernmentShip != 0 && !r.CreatedAt.IsZero() && r.CreatedAt.Year() >= 1 && r.CreatedAt.Year() <= 9999 && r.CreatedAt.Location() == time.UTC && enrollmentHash(r.RecordHash) && r.RecordHash == r.checksum()
}
func decodePlacement(raw []byte, key string) (*PlacementRecord, error) {
	// Unlike encoding/json's map decoding, reject duplicate top-level fields.
	d := json.NewDecoder(bytes.NewReader(raw))
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return nil, enrollmentError(EnrollmentCorrupt)
	}
	seen := map[string]bool{}
	allowed := map[string]bool{}
	for _, field := range placementFields.required {
		allowed[field] = true
	}
	for d.More() {
		t, e = d.Token()
		if e != nil {
			return nil, enrollmentError(EnrollmentCorrupt)
		}
		k, ok := t.(string)
		if !ok || seen[k] || !allowed[k] {
			return nil, enrollmentError(EnrollmentCorrupt)
		}
		seen[k] = true
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return nil, enrollmentError(EnrollmentCorrupt)
		}
	}
	var r PlacementRecord
	if strictDecode(raw, placementFields, &r) != nil || !r.valid() || key != placementPrefix+r.RequestID {
		return nil, enrollmentError(EnrollmentCorrupt)
	}
	return &r, nil
}
func placementsTx(tx *bolt.Tx) (map[string]*PlacementRecord, error) {
	out := map[string]*PlacementRecord{}
	accounts := map[string]bool{}
	players := map[int]bool{}
	ships := map[uint32]bool{}
	homes := map[[2]uint8]bool{}
	c := tx.Bucket(BucketMeta).Cursor()
	prefix := []byte(placementPrefix)
	for k, v := c.Seek(prefix); k != nil && bytes.HasPrefix(k, prefix); k, v = c.Next() {
		r, e := decodePlacement(v, string(k))
		if e != nil {
			return nil, e
		}
		home := [2]uint8{r.Star, r.Planet}
		if accounts[r.CallerID] || players[r.Player] || ships[r.GovernmentShip] || homes[home] {
			return nil, enrollmentError(EnrollmentCorrupt)
		}
		homes[home] = true
		accounts[r.CallerID] = true
		players[r.Player] = true
		ships[r.GovernmentShip] = true
		out[r.RequestID] = r
	}
	return out, nil
}

// GetPlacement returns nil,nil only for absence. CallerID must come from trusted
// authentication; a mismatched caller receives a conflict and no record. Visibility
// is not proof of durability after a previous uncertain write; CR-07D reconciles.
func (s *Store) GetPlacement(callerID, requestID string) (*PlacementRecord, error) {
	if !enrollmentToken(callerID, 128) || !enrollmentToken(requestID, 64) {
		return nil, enrollmentError(EnrollmentInvalid)
	}
	if e := s.metaFault("meta.get:" + placementPrefix + requestID); e != nil {
		return nil, enrollmentError(EnrollmentIO)
	}
	var r *PlacementRecord
	e := s.db.View(func(tx *bolt.Tx) error {
		all, e := placementsTx(tx)
		if e != nil {
			return e
		}
		r = all[requestID]
		if r != nil && r.CallerID != callerID {
			return enrollmentError(EnrollmentConflict)
		}
		return nil
	})
	if e != nil {
		return nil, asEnrollmentError(e)
	}
	return r, nil
}
func asEnrollmentError(e error) error {
	var ee *EnrollmentError
	if errors.As(e, &ee) {
		return ee
	}
	if errors.Is(e, ErrWriteFenced) {
		return &EnrollmentError{Code: EnrollmentFenced, Outcome: EnrollmentNotCommitted, cause: ErrWriteFenced}
	}
	return enrollmentError(EnrollmentIO)
}
func enrollmentDecode(raw []byte, v any) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return enrollmentError(EnrollmentCorrupt)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return enrollmentError(EnrollmentCorrupt)
	}
	if d.Decode(new(any)) != io.EOF {
		return enrollmentError(EnrollmentCorrupt)
	}
	return nil
}
func enrollmentIdentity(in Enrollment) (string, error) {
	if !enrollmentToken(in.RequestID, 64) || !enrollmentToken(in.CallerID, 128) || !enrollmentHash(in.RequestHash) || in.CreatedAt.IsZero() || in.CreatedAt.Year() < 1 || in.CreatedAt.Year() > 9999 || in.Race == nil || in.Planet == nil || in.Star == nil || in.Ship == nil {
		return "", enrollmentError(EnrollmentInvalid)
	}
	h, e := hashJSON(struct {
		Race   *model.Race
		Planet *model.Planet
		Star   *model.Star
		Ship   *model.Ship
		X, Y   uint8
	}{in.Race, in.Planet, in.Star, in.Ship, in.CapitalX, in.CapitalY})
	if e != nil {
		return "", enrollmentError(EnrollmentInvalid)
	}
	return h, nil
}
func enrollmentReplay(all map[string]*PlacementRecord, in Enrollment, h string) (*PlacementRecord, error) {
	if r := all[in.RequestID]; r != nil {
		if r.CallerID != in.CallerID || r.RequestHash != in.RequestHash || r.ProposalHash != h {
			return nil, enrollmentError(EnrollmentConflict)
		}
		return r, nil
	}
	for _, r := range all {
		if r.CallerID == in.CallerID || r.Player == in.Race.PlayerNum || (r.Star == in.Planet.StarNum && r.Planet == in.Planet.PlanetNum) {
			return nil, enrollmentError(EnrollmentConflict)
		}
	}
	return nil, nil
}

// PutEnrollment writes race, planet (embedded sector), star, ship, free-list pop
// and placement metadata in ONE bbolt Update. It never changes CR-18, schedule,
// maintenance, RNG or turn counters. Successful replay performs no write.
//
// SetMetaFault stages for isolated tests: enrollment.before, .construct, .race,
// .planet, .star, .ship, .free, .metadata, .before-commit (rollback); .commit-error
// simulates a commit reporting an error despite visibility (UNCERTAIN);
// .after-commit is notification failure/panic after confirmed success (COMMITTED).
// Actual commit errors/panics are UNCERTAIN; no visible record proves durability.
// An uncertain outcome refuses ALL later enrollment writes on this Store; the actor must halt and
// reconcile, including a failed readback. Opening another Store is NOT a retry policy.
func (s *Store) PutEnrollment(in Enrollment) (record *PlacementRecord, err error) {
	if e := s.writeAllowed("PutEnrollment"); e != nil {
		return nil, asEnrollmentError(e)
	}
	s.enrollmentMu.Lock()
	defer s.enrollmentMu.Unlock()
	h, e := enrollmentIdentity(in)
	if e != nil {
		return nil, e
	}
	if s.enrollmentUncertain {
		return nil, &EnrollmentError{Code: EnrollmentIO, Outcome: EnrollmentUncertain}
	}
	phase := 0 // 0 pretransaction, 1 callback, 2 attempting commit, 3 confirmed commit
	defer func() {
		if recover() != nil {
			switch phase {
			case 3:
				err = &EnrollmentError{Code: EnrollmentIO, Outcome: EnrollmentCommitted}
			case 2:
				err = &EnrollmentError{Code: EnrollmentIO, Outcome: EnrollmentUncertain}
			default:
				err = enrollmentError(EnrollmentIO)
				record = nil
			}
		}
		var ee *EnrollmentError
		if errors.As(err, &ee) && ee.Outcome == EnrollmentUncertain {
			s.enrollmentUncertain = true
			record = s.enrollmentReadback(in.CallerID, in.RequestID)
		}
	}()
	var all map[string]*PlacementRecord
	e = s.db.View(func(tx *bolt.Tx) error { var e error; all, e = placementsTx(tx); return e })
	if e != nil {
		return nil, asEnrollmentError(e)
	}
	record, e = enrollmentReplay(all, in, h)
	if e != nil || record != nil {
		return record, e
	}
	if e = s.metaFault("enrollment.before"); e != nil {
		return nil, enrollmentError(EnrollmentIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("PutEnrollment"); e != nil {
			return e
		}
		all, e := placementsTx(tx)
		if e != nil {
			return e
		}
		record, e = enrollmentReplay(all, in, h)
		if e != nil || record != nil {
			return e
		}
		if e = enrollmentPreconditions(tx, in); e != nil {
			return e
		}
		if e = s.metaFault("enrollment.construct"); e != nil {
			return enrollmentError(EnrollmentIO)
		}
		record = &PlacementRecord{SchemaVersion: 1, RequestID: in.RequestID, CallerID: in.CallerID, RequestHash: in.RequestHash, ProposalHash: h, Player: in.Race.PlayerNum, Star: in.Planet.StarNum, Planet: in.Planet.PlanetNum, CapitalX: in.CapitalX, CapitalY: in.CapitalY, Width: in.Planet.MaxX, Height: in.Planet.MaxY, GovernmentShip: in.Ship.Number, CreatedAt: in.CreatedAt.UTC()}
		record.RecordHash = record.checksum()
		if !record.valid() {
			return enrollmentError(EnrollmentInvalid)
		}
		writes := []struct {
			bucket, key []byte
			value       any
			stage       string
		}{{BucketRaces, RaceKey(in.Race.PlayerNum), in.Race, "race"}, {BucketPlanets, PlanetKey(in.Planet.StarNum, in.Planet.PlanetNum), in.Planet, "planet"}, {BucketStars, StarKey(in.Star.Number), in.Star, "star"}, {BucketShips, ShipKey(in.Ship.Number), in.Ship, "ship"}}
		for _, w := range writes {
			b, e := json.Marshal(w.value)
			if e != nil {
				return enrollmentError(EnrollmentInvalid)
			}
			if e = tx.Bucket(w.bucket).Put(w.key, b); e != nil {
				return e
			}
			if e = s.metaFault("enrollment." + w.stage); e != nil {
				return enrollmentError(EnrollmentIO)
			}
		}
		if in.Ticket.FreeKey != 0 {
			if e = tx.Bucket(BucketShipFree).Delete(enrollmentFreeKey(in.Ticket.FreeKey)); e != nil {
				return e
			}
		}
		if e = s.metaFault("enrollment.free"); e != nil {
			return enrollmentError(EnrollmentIO)
		}
		b, e := json.Marshal(record)
		if e != nil {
			return enrollmentError(EnrollmentInvalid)
		}
		if e = tx.Bucket(BucketMeta).Put([]byte(placementPrefix+in.RequestID), b); e != nil {
			return e
		}
		if e = s.metaFault("enrollment.metadata"); e != nil {
			return enrollmentError(EnrollmentIO)
		}
		if e = s.metaFault("enrollment.before-commit"); e != nil {
			return enrollmentError(EnrollmentIO)
		}
		phase = 2
		return nil
	})
	if e != nil {
		if phase == 2 {
			return nil, &EnrollmentError{Code: EnrollmentIO, Outcome: EnrollmentUncertain}
		}
		return nil, asEnrollmentError(e)
	}
	if phase == 1 {
		return record, nil
	} // Already committed by another caller.
	// Hook below models an OS/commit error after changed state became visible.
	if e = s.metaFault("enrollment.commit-error"); e != nil {
		return nil, &EnrollmentError{Code: EnrollmentIO, Outcome: EnrollmentUncertain}
	}
	phase = 3
	if e = s.metaFault("enrollment.after-commit"); e != nil {
		return record, &EnrollmentError{Code: EnrollmentIO, Outcome: EnrollmentCommitted}
	}
	return record, nil
}

func enrollmentPreconditions(tx *bolt.Tx, in Enrollment) error {
	p, r, st, sh := in.Planet, in.Race, in.Star, in.Ship
	if in.ExpectedPlanet == nil || in.ExpectedStar == nil || r.PlayerNum < 1 || r.PlayerNum > 63 || sh.Number == 0 || sh.Number != in.Ticket.Number || r.GovShip != sh.Number || p.Ships != sh.Number || p.StarNum != st.Number || int(st.Number) >= model.NumStars || int(p.PlanetNum) >= model.MaxPlanets || p.PlanetNum >= st.NumPlanets || p.MaxX == 0 || p.MaxY == 0 || len(p.Sectors) != int(p.MaxX)*int(p.MaxY) || in.CapitalX >= p.MaxX || in.CapitalY >= p.MaxY || sh.Owner != uint8(r.PlayerNum) || sh.Race != sh.Owner || sh.StOrbits != p.StarNum || sh.PNumOrbits != p.PlanetNum || sh.LandX != in.CapitalX || sh.LandY != in.CapitalY || sh.NextShip != in.ExpectedPlanet.Ships || !sh.Alive || !sh.Active || r.Name == "" {
		return enrollmentError(EnrollmentInvalid)
	}
	maxPlayer := 0
	if e := tx.Bucket(BucketRaces).ForEach(func(k, v []byte) error {
		var old model.Race
		if len(k) != 2 || enrollmentDecode(v, &old) != nil || !bytes.Equal(k, RaceKey(old.PlayerNum)) || old.PlayerNum < 1 || old.PlayerNum > model.MaxPlayers {
			return enrollmentError(EnrollmentCorrupt)
		}
		if old.PlayerNum > maxPlayer {
			maxPlayer = old.PlayerNum
		}
		if strings.EqualFold(old.Name, r.Name) {
			return enrollmentError(EnrollmentConflict)
		}
		return nil
	}); e != nil {
		return e
	}

	// Metadata retains historical allocations even if a legacy writer removed a
	// race record. Such numbers must never be resurrected by append allocation.
	all, e := placementsTx(tx)
	if e != nil {
		return e
	}
	for _, placed := range all {
		if placed.Player > maxPlayer {
			maxPlayer = placed.Player
		}
	}
	if r.PlayerNum != maxPlayer+1 || tx.Bucket(BucketRaces).Get(RaceKey(r.PlayerNum)) != nil {
		return enrollmentError(EnrollmentConflict)
	}
	if e := enrollmentPlayerUnused(tx, r.PlayerNum); e != nil {
		return e
	}
	var oldP model.Planet
	var oldS model.Star
	if tx.Bucket(BucketPlanets).Get(PlanetKey(p.StarNum, p.PlanetNum)) == nil || tx.Bucket(BucketStars).Get(StarKey(st.Number)) == nil {
		return enrollmentError(EnrollmentStale)
	}
	if e := enrollmentDecode(tx.Bucket(BucketPlanets).Get(PlanetKey(p.StarNum, p.PlanetNum)), &oldP); e != nil {
		return e
	}
	if e := enrollmentDecode(tx.Bucket(BucketStars).Get(StarKey(st.Number)), &oldS); e != nil {
		return e
	}
	if !reflect.DeepEqual(&oldP, in.ExpectedPlanet) || !reflect.DeepEqual(&oldS, in.ExpectedStar) {
		return enrollmentError(EnrollmentStale)
	}
	if oldP.StarNum != st.Number || oldP.PlanetNum != p.PlanetNum || oldS.Number != st.Number || oldS.Inhabited != 0 || len(oldP.Sectors) != len(p.Sectors) {
		return enrollmentError(EnrollmentConflict)
	}
	for _, info := range oldP.Info {
		if info.NumSectsOwned != 0 {
			return enrollmentError(EnrollmentConflict)
		}
	}
	for _, sec := range oldP.Sectors {
		if sec.Owner != 0 {
			return enrollmentError(EnrollmentConflict)
		}
	}
	index := int(in.CapitalX) + int(in.CapitalY)*int(p.MaxX)
	sec := oldP.Sectors[index]
	if sec.Owner != 0 || sec.Popn != 0 || sec.Troops != 0 {
		return enrollmentError(EnrollmentStale)
	}
	// Allow only CR-07B's modifications; gameplay-derived MaxPopn is trusted.
	wantP := oldP
	wantP.Sectors = append([]model.Sector(nil), oldP.Sectors...)
	dest := &wantP.Sectors[index]
	dest.Owner = uint8(r.PlayerNum)
	dest.Race = uint8(r.PlayerNum)
	dest.Popn = uint16(r.NumberSexes)
	dest.Fert = 100
	dest.Eff = 10
	dest.Troops = 0
	wantP.Popn = uint64(r.NumberSexes)
	wantP.Troops = 0
	wantP.MaxPopn = p.MaxPopn
	wantP.Info[r.PlayerNum-1].NumSectsOwned = 1
	wantP.Info[r.PlayerNum-1].Explored = true
	wantP.Explored = false
	wantP.Ships = sh.Number
	wantS := oldS
	wantS.SetExplored(r.PlayerNum)
	wantS.SetInhabited(r.PlayerNum)
	wantS.AP[r.PlayerNum-1] = 5
	if !reflect.DeepEqual(&wantP, p) || !reflect.DeepEqual(&wantS, st) {
		return enrollmentError(EnrollmentInvalid)
	}
	next := oldP.Ships
	seen := map[uint32]bool{}
	for _, expected := range in.ExpectedChain {
		if expected == nil || next == 0 || expected.Number != next || seen[next] {
			return enrollmentError(EnrollmentInvalid)
		}
		seen[next] = true
		var current model.Ship
		if e := enrollmentDecode(tx.Bucket(BucketShips).Get(ShipKey(next)), &current); e != nil {
			return e
		}
		if !reflect.DeepEqual(expected, &current) {
			return enrollmentError(EnrollmentStale)
		}
		if !current.Alive || current.WhatOrbits != model.LevelPlan || current.StOrbits != p.StarNum || current.PNumOrbits != p.PlanetNum {
			return enrollmentError(EnrollmentConflict)
		}
		next = current.NextShip
	}
	if next != 0 {
		return enrollmentError(EnrollmentInvalid)
	}

	if e := tx.Bucket(BucketShips).ForEach(func(k, v []byte) error {
		var current model.Ship
		if enrollmentDecode(v, &current) != nil {
			return enrollmentError(EnrollmentCorrupt)
		}
		if current.Alive && current.WhatOrbits == model.LevelPlan && current.StOrbits == p.StarNum && current.PNumOrbits == p.PlanetNum && !seen[current.Number] {
			return enrollmentError(EnrollmentStale)
		}
		return nil
	}); e != nil {
		return e
	}
	ticket, e := enrollmentShipTx(tx)
	if e != nil {
		return e
	}
	if ticket != in.Ticket {
		return enrollmentError(EnrollmentStale)
	}
	return nil
}

func (s *Store) enrollmentReadback(caller, request string) (r *PlacementRecord) {
	defer func() {
		if recover() != nil {
			r = nil
		}
	}()
	r, _ = s.GetPlacement(caller, request)
	return r
}

// EnrollmentAdmission is a read-only hint for the authoritative actor. History
// counts toward numbering; lookup must precede acting on CallerPlaced or capacity.
type EnrollmentAdmission struct {
	NextPlayer   int
	CallerPlaced bool
}

// EnrollmentAdmission validates metadata and the handle's uncertainty guard.
// It reserves nothing; PutEnrollment remains the final transaction authority.
func (s *Store) EnrollmentAdmission(callerID string) (EnrollmentAdmission, error) {
	s.enrollmentMu.Lock()
	defer s.enrollmentMu.Unlock()
	if s.enrollmentUncertain {
		return EnrollmentAdmission{}, &EnrollmentError{Code: EnrollmentIO, Outcome: EnrollmentUncertain}
	}
	if !enrollmentToken(callerID, 128) {
		return EnrollmentAdmission{}, enrollmentError(EnrollmentInvalid)
	}
	result := EnrollmentAdmission{NextPlayer: 1}
	e := s.db.View(func(tx *bolt.Tx) error {
		all, e := placementsTx(tx)
		if e != nil {
			return e
		}
		for _, r := range all {
			if r.Player >= result.NextPlayer {
				result.NextPlayer = r.Player + 1
			}
			if r.CallerID == callerID {
				result.CallerPlaced = true
			}
		}
		return tx.Bucket(BucketRaces).ForEach(func(k, v []byte) error {
			var r model.Race
			if enrollmentDecode(v, &r) != nil || r.PlayerNum < 1 || r.PlayerNum > model.MaxPlayers || !bytes.Equal(k, RaceKey(r.PlayerNum)) {
				return enrollmentError(EnrollmentCorrupt)
			}
			if r.PlayerNum >= result.NextPlayer {
				result.NextPlayer = r.PlayerNum + 1
			}
			return nil
		})
	})
	return result, asEnrollmentOptionalError(e)
}

// EnrollmentWriteUncertain preserves the CR-07C guard when a caller attempts
// to construct another actor on the same handle. It is read-only and is not a
// reconciliation operation; a fresh handle is not an approved recovery policy.
func (s *Store) EnrollmentWriteUncertain() bool {
	s.enrollmentMu.Lock()
	defer s.enrollmentMu.Unlock()
	return s.enrollmentUncertain
}

// EnrollmentRequiresDual classifies authentication using trusted persistent
// placement history. Malformed metadata is an error, never legacy compatibility.
func (s *Store) EnrollmentRequiresDual(player int) (bool, error) {
	if player < 1 || player > model.MaxPlayers {
		return false, enrollmentError(EnrollmentInvalid)
	}
	found := false
	e := s.db.View(func(tx *bolt.Tx) error {
		all, e := placementsTx(tx)
		if e != nil {
			return e
		}
		for _, r := range all {
			found = found || r.Player == player
		}
		return nil
	})
	return found, asEnrollmentOptionalError(e)
}
