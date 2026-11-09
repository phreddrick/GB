package game

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/phreddrick/GB/go/internal/model"
	gbnet "github.com/phreddrick/GB/go/internal/net"
	"golang.org/x/crypto/bcrypt"
)

// Public dummy encoding: never an account credential. Denied well-formed
// identities still perform two default-cost bcrypt checks, avoiding the simple
// zero-work race-name oracle. Variable legacy costs/rate limits remain CR-20.
const dummyBcrypt = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

func deniedCredentials(racePassword, governorPassword string) (gbnet.AuthResult, error) {
	_ = bcrypt.CompareHashAndPassword([]byte(dummyBcrypt), []byte(racePassword))
	_ = bcrypt.CompareHashAndPassword([]byte(dummyBcrypt), []byte(governorPassword))
	return gbnet.AuthResult{}, nil
}

var ErrSessionRevoked = errors.New("session authority is no longer valid")

// AuthenticateDual verifies both credentials separately on the existing actor.
// Credential errors share one result; history corruption never becomes legacy.
func (s *State) AuthenticateDual(ctx context.Context, name, racePassword, governorPassword string, governor int) (gbnet.AuthResult, error) {
	return call(s, ctx, opAuthenticate, func() (gbnet.AuthResult, error) {
		return s.checkCredentials(name, racePassword, governorPassword, governor, true)
	})
}
func (s *State) checkCredentials(name, racePassword, governorPassword string, governor int, dual bool) (gbnet.AuthResult, error) {
	if racePassword == "" || len(racePassword) > 72 || strings.ContainsAny(racePassword, "\x00\r\n") || dual && (governorPassword == "" || len(governorPassword) > 72 || strings.ContainsAny(governorPassword, "\x00\r\n")) {
		return gbnet.AuthResult{}, nil
	}
	for _, race := range s.races {
		if race == nil || !strings.EqualFold(race.Name, name) {
			continue
		}
		if race.Dissolved || governor < 0 || governor >= len(race.Governor) || governor > int(race.Governors) || !race.Governor[governor].Active {
			return deniedCredentials(racePassword, governorPassword)
		}
		required, e := s.store.EnrollmentRequiresDual(race.PlayerNum)
		if e != nil {
			return gbnet.AuthResult{}, errors.New("authentication authority unavailable")
		}
		if required && (!dual || racePassword == governorPassword) {
			return deniedCredentials(racePassword, governorPassword)
		}
		gov := race.Governor[governor]
		if dual && governorPassword == "" || !dual && governor > 0 && gov.Password == "" {
			return deniedCredentials(racePassword, governorPassword)
		}
		raceOK := bcrypt.CompareHashAndPassword([]byte(race.Password), []byte(racePassword)) == nil
		supplied := governorPassword
		if !dual {
			supplied = racePassword
		}
		govHash := gov.Password
		// Safe historical governor0 compatibility only; new placements always require
		// an independent stored governor hash, including when records are damaged.
		if govHash == "" && governor == 0 && !required {
			govHash = race.Password
		}
		governorOK := bcrypt.CompareHashAndPassword([]byte(govHash), []byte(supplied)) == nil
		if !raceOK || !governorOK {
			return gbnet.AuthResult{}, nil
		}
		revision := authRevision(race, governor, required)
		s.initializeLoginScope(race, governor)
		return gbnet.AuthResult{OK: true, PlayerNum: race.PlayerNum, God: race.God, Revision: revision}, nil
	}
	return deniedCredentials(racePassword, governorPassword)
}
func authRevision(r *model.Race, gov int, dual bool) string {
	raw, _ := json.Marshal(struct {
		Player, Governor                    int
		RaceHash, GovernorHash              string
		God, Guest, Dissolved, Active, Dual bool
		Governors                           int8
	}{r.PlayerNum, gov, r.Password, r.Governor[gov].Password, r.God, r.Guest, r.Dissolved, r.Governor[gov].Active, dual, r.Governors})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (s *State) validSession(ident gbnet.Identity) bool {
	if s.draining.Load() || ident.ExpiresAt <= s.now().Unix() || ident.PlayerNum < 1 || ident.PlayerNum > len(s.races) {
		return false
	}
	race := s.races[ident.PlayerNum-1]
	g := ident.Governor
	if race == nil || race.Dissolved || g < 0 || g >= len(race.Governor) || g > int(race.Governors) || !race.Governor[g].Active || race.God != ident.God {
		return false
	}
	dual, e := s.store.EnrollmentRequiresDual(ident.PlayerNum)
	if e != nil {
		return false
	}
	revision := authRevision(race, g, dual)
	return len(ident.Revision) == len(revision) && subtle.ConstantTimeCompare([]byte(ident.Revision), []byte(revision)) == 1
}
func (s *State) ValidateSession(ctx context.Context, ident gbnet.Identity) (bool, error) {
	return call(s, ctx, opAuthenticate, func() (bool, error) { return s.validSession(ident), nil })
}

// WelcomeSession verifies and reads the welcome data at one actor admission.
func (s *State) WelcomeSession(ctx context.Context, ident gbnet.Identity) (string, error) {
	return call(s, ctx, opWelcome, func() (string, error) {
		if !s.validSession(ident) {
			return "", ErrSessionRevoked
		}
		return "\nWelcome back, " + s.races[ident.PlayerNum-1].Name + ".\n", nil
	})
}
func (s *State) initializeLoginScope(r *model.Race, g int) {
	if s.dirs[r.PlayerNum-1][g].Prompt != "" {
		return
	}
	gov := r.Governor[g]
	dir := model.Directory{}
	if int(gov.DefSystem) < len(s.stars) && s.stars[gov.DefSystem] != nil && (gov.DefLevel == model.LevelStar || gov.DefLevel == model.LevelPlan && s.planets[gov.DefSystem][gov.DefPlanetNum] != nil) {
		dir.Level = gov.DefLevel
		dir.StarNum = gov.DefSystem
		dir.PlanetNum = gov.DefPlanetNum
	}
	s.dirs[r.PlayerNum-1][g] = dir
	s.dirs[r.PlayerNum-1][g].Prompt = s.buildPrompt(r.PlayerNum, g)
}

// revokeSessions runs on the actor, before subsequent output delivery. It never
// calls the public actor API recursively.
func (s *State) revokeSessions() {
	if registry, ok := s.registry.(interface {
		RevalidateSessions(func(gbnet.Identity) bool)
	}); ok {
		registry.RevalidateSessions(s.validSession)
	}
}
