package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

// FactoryActivation is an actor-only, landed-factory operation, not a generic
// ship writer. Capture private immutable preimages/proposals and exclude their
// concurrent mutation. Orbit contains the complete planet ship chain, including
// the factory. Star and other ships are full read-only identity witnesses.
// The only writes are factory On/Notified and planet resource cost. No AP,
// creation, deletion, allocator, orbit mutation, effects or metadata is supported.
type FactoryActivation struct {
	Player, Governor int
	Factory          CommandShip
	Planet           CommandPlanet
	Star             *model.Star
	Orbit            []*model.Ship
}
type CommandShip struct{ Expected, Proposed *model.Ship }
type preparedFactory struct{ checks, writes []commandRecord }

func prepareFactoryActivation(in FactoryActivation) (preparedFactory, error) {
	invalid := func() (preparedFactory, error) { return preparedFactory{}, commandError(CommandInvalid) }
	f, n, p, q, star := in.Factory.Expected, in.Factory.Proposed, in.Planet.Expected, in.Planet.Proposed, in.Star
	if in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || f == nil || n == nil || p == nil || q == nil || star == nil {
		return invalid()
	}
	if f.Number == 0 || f.Type != model.OtypeFactory || !f.Alive || !f.Active || f.On || int(f.Owner) != in.Player || (in.Governor != 0 && int(f.Governor) != in.Governor) || f.WhatOrbits != model.LevelPlan || !f.Docked || f.WhatDest != model.LevelPlan {
		return invalid()
	}
	if model.Shipdata[model.OtypeFactory][model.AbilMaxCrew]-int64(f.Troops) > 0 && f.Popn == 0 {
		return invalid()
	}
	if f.StOrbits != p.StarNum || f.PNumOrbits != p.PlanetNum || f.DestStar != p.StarNum || f.DestPNum != p.PlanetNum || star.Number != p.StarNum || int(star.NumPlanets) > model.MaxPlanets || p.PlanetNum >= star.NumPlanets || p.MaxX == 0 || p.MaxY == 0 || len(p.Sectors) != int(p.MaxX)*int(p.MaxY) || f.LandX >= p.MaxX || f.LandY >= p.MaxY {
		return invalid()
	}
	// order.c uses unsigned int oncost, not a uint16 temporary. Large costs
	// cannot wrap into an affordable activation against uint16 stockpiles.
	cost := uint32(f.BuildCost) * 2
	if cost > uint32(p.Info[in.Player-1].Resource) {
		return invalid()
	}
	wantShip := *f
	wantShip.On = true
	wantShip.Notified = false
	wantPlanet := *p
	wantPlanet.Info[in.Player-1].Resource -= uint16(cost) //nolint:gosec // cost <= uint16 source balance
	if !reflect.DeepEqual(&wantShip, n) || !reflect.DeepEqual(&wantPlanet, q) {
		return invalid()
	}
	var out preparedFactory
	record := func(bucket, key []byte, before, after any) (commandRecord, error) {
		b, e := json.Marshal(before)
		if e != nil {
			return commandRecord{}, commandError(CommandInvalid)
		}
		a, e := json.Marshal(after)
		if e != nil {
			return commandRecord{}, commandError(CommandInvalid)
		}
		return commandRecord{bucket, key, b, a}, nil
	}
	st, e := record(BucketStars, StarKey(star.Number), star, star)
	if e != nil {
		return invalid()
	}
	out.checks = append(out.checks, st)
	pl, e := record(BucketPlanets, PlanetKey(p.StarNum, p.PlanetNum), p, q)
	if e != nil {
		return invalid()
	}
	out.checks = append(out.checks, pl)
	next := p.Ships
	seen := map[uint32]bool{}
	found := false
	for _, ship := range in.Orbit {
		if ship == nil || next == 0 || ship.Number != next || seen[next] || !ship.Alive || ship.WhatOrbits != model.LevelPlan || ship.StOrbits != p.StarNum || ship.PNumOrbits != p.PlanetNum {
			return invalid()
		}
		seen[next] = true
		proposed := ship
		if ship.Number == f.Number {
			if !reflect.DeepEqual(ship, f) {
				return invalid()
			}
			found = true
			proposed = n
		}
		sh, e := record(BucketShips, ShipKey(ship.Number), ship, proposed)
		if e != nil {
			return invalid()
		}
		out.checks = append(out.checks, sh)
		if ship.Number == f.Number {
			out.writes = append(out.writes, sh)
		}
		next = ship.NextShip
	}
	if next != 0 || !found {
		return invalid()
	}
	out.writes = append(out.writes, pl)
	return out, nil
}

// CommitFactoryActivation leaves CR-17B's dump-only API unchanged. It shares
// that API's mutex, uncertainty guard, error/outcome types and readback rules.
// Faults via SetMetaFault: factory.before, .validated, .write:1 (factory),
// .write:2 (planet), .before-commit abort; .commit-error is UNCERTAIN despite
// visibility; .after-commit is COMMITTED/reporting-error. command.readback can
// fail diagnostics. No production caller configures these test-only hooks.
func (s *Store) CommitFactoryActivation(in FactoryActivation) (outcome CommandOutcome, err error) {
	outcome = CommandNotCommitted
	if e := s.writeAllowed("CommitFactoryActivation"); e != nil {
		return outcome, commandError(CommandFenced)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.CommandWriteUncertain() {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain, Readback: "unavailable"}
	}
	phase := 0
	var prepared preparedFactory
	defer func() {
		if recover() != nil {
			switch phase {
			case 3:
				outcome = CommandCommitted
			case 2:
				outcome = CommandUncertain
			default:
				outcome = CommandNotCommitted
			}
			err = &CommandError{Code: CommandIO, Outcome: outcome}
		}
		if outcome == CommandUncertain {
			s.commandUncertain.Store(true)
			if ce, ok := err.(*CommandError); ok {
				ce.Readback = s.commandReadback(prepared.checks)
			}
		}
	}()
	var e error
	prepared, e = prepareFactoryActivation(in)
	if e != nil {
		return outcome, e
	}
	if e = s.metaFault("factory.before"); e != nil {
		return outcome, commandError(CommandIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("CommitFactoryActivation"); e != nil {
			return commandError(CommandFenced)
		}
		for _, r := range prepared.checks {
			b := tx.Bucket(r.bucket)
			if b == nil {
				return commandError(CommandCorrupt)
			}
			if !bytes.Equal(b.Get(r.key), r.before) {
				return commandError(CommandStale)
			}
		}
		if e := s.metaFault("factory.validated"); e != nil {
			return commandError(CommandIO)
		}
		for i, r := range prepared.writes {
			if e := tx.Bucket(r.bucket).Put(r.key, r.after); e != nil {
				return commandError(CommandIO)
			}
			stage := "factory.write:1"
			if i == 1 {
				stage = "factory.write:2"
			}
			if e := s.metaFault(stage); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("factory.before-commit"); e != nil {
			return commandError(CommandIO)
		}
		phase = 2
		return nil
	})
	if e != nil {
		if phase == 2 {
			return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
		}
		var ce *CommandError
		if errors.As(e, &ce) {
			return outcome, ce
		}
		return outcome, commandError(CommandIO)
	}
	if e = s.metaFault("factory.commit-error"); e != nil {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
	}
	phase = 3
	outcome = CommandCommitted
	if e = s.metaFault("factory.after-commit"); e != nil {
		return outcome, &CommandError{Code: CommandIO, Outcome: outcome}
	}
	return outcome, nil
}
