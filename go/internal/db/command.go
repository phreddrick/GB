package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

// CommandRecords is intentionally limited to a complete exploration dump.
// The authoritative actor supplies private, immutable preimages/proposals and
// excludes concurrent mutation for the entire call. This is not a second writer
// or a generic record/allocator/effects API. No creation or deletion is allowed.
// All planets of shared stars, even unchanged ones, must participate. APStar
// participates even when not shared. No CR-18 metadata is read or modified.
type CommandRecords struct {
	Player, Recipient int
	APStar            uint8
	Stars             []CommandStar
	Planets           []CommandPlanet
}
type CommandStar struct {
	Expected, Proposed *model.Star
	Share              bool
}
type CommandPlanet struct{ Expected, Proposed *model.Planet }

type CommandOutcome string

const (
	CommandNotCommitted CommandOutcome = "NOT_COMMITTED"
	CommandCommitted    CommandOutcome = "COMMITTED"
	CommandUncertain    CommandOutcome = "UNCERTAIN"
)

type CommandCode string

const (
	CommandInvalid CommandCode = "INVALID_COMMAND_RECORDS"
	CommandStale   CommandCode = "STALE_COMMAND_RECORDS"
	CommandCorrupt CommandCode = "CORRUPT_COMMAND_RECORDS"
	CommandIO      CommandCode = "COMMAND_IO"
	CommandFenced  CommandCode = "COMMAND_FENCED"
)

// CommandError exposes bounded diagnostics only, never models or hook text.
// Readback is diagnostic visibility (not proof of fsync/durability).
type CommandError struct {
	Code     CommandCode
	Outcome  CommandOutcome
	Readback string
}

func (e *CommandError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Outcome) }
func (e *CommandError) Unwrap() error {
	if e.Code == CommandFenced {
		return ErrWriteFenced
	}
	return nil
}
func commandError(code CommandCode) error {
	return &CommandError{Code: code, Outcome: CommandNotCommitted}
}

type commandState struct {
	commandMu        sync.Mutex
	commandUncertain atomic.Bool
}

// CommandWriteUncertain prevents a new actor on a tainted handle from implicitly
// clearing uncertainty. Reopening is not a recovery policy; reconciliation is
// operator-controlled. No automatic repair operation is supplied.
func (s *Store) CommandWriteUncertain() bool { return s.commandUncertain.Load() }

type commandRecord struct{ bucket, key, before, after []byte }

// prepareCommand permits only source-known exploration flags and one AP debit.
// Sector contents, stockpiles, ownership, geometry, identities and orbit roots
// cannot be changed through this API. Unsupported record families have no input.
func prepareCommand(in CommandRecords) ([]commandRecord, error) {
	if in.Player < 1 || in.Player > model.MaxPlayers || in.Recipient < 1 || in.Recipient > model.MaxPlayers || len(in.Stars) < 1 || len(in.Stars) > model.NumStars || len(in.Planets) > model.NumStars*model.MaxPlanets {
		return nil, commandError(CommandInvalid)
	}
	stars := map[uint8]CommandStar{}
	records := make([]commandRecord, 0, len(in.Stars)+len(in.Planets))
	add := func(bucket, key []byte, before, after any) error {
		b, e := json.Marshal(before)
		if e != nil {
			return commandError(CommandInvalid)
		}
		a, e := json.Marshal(after)
		if e != nil {
			return commandError(CommandInvalid)
		}
		records = append(records, commandRecord{bucket, key, b, a})
		return nil
	}
	for _, edit := range in.Stars {
		b, a := edit.Expected, edit.Proposed
		if b == nil || a == nil || int(b.NumPlanets) > model.MaxPlanets || (!edit.Share && b.Number != in.APStar) {
			return nil, commandError(CommandInvalid)
		}
		if _, duplicate := stars[b.Number]; duplicate {
			return nil, commandError(CommandInvalid)
		}
		want := *b
		if edit.Share {
			if !b.IsExplored(in.Player) {
				return nil, commandError(CommandInvalid)
			}
			want.SetExplored(in.Recipient)
		}
		if b.Number == in.APStar {
			if want.AP[in.Player-1] < 1 {
				return nil, commandError(CommandInvalid)
			}
			want.AP[in.Player-1]--
		}
		if !reflect.DeepEqual(&want, a) {
			return nil, commandError(CommandInvalid)
		}
		stars[b.Number] = edit
		if e := add(BucketStars, StarKey(b.Number), b, a); e != nil {
			return nil, e
		}
	}
	if _, ok := stars[in.APStar]; !ok {
		return nil, commandError(CommandInvalid)
	}
	planets := map[[2]uint8]bool{}
	for _, edit := range in.Planets {
		b, a := edit.Expected, edit.Proposed
		if b == nil || a == nil || b.MaxX == 0 || b.MaxY == 0 || len(b.Sectors) != int(b.MaxX)*int(b.MaxY) {
			return nil, commandError(CommandInvalid)
		}
		st, ok := stars[b.StarNum]
		key := [2]uint8{b.StarNum, b.PlanetNum}
		if !ok || !st.Share || b.PlanetNum >= st.Expected.NumPlanets || planets[key] {
			return nil, commandError(CommandInvalid)
		}
		planets[key] = true
		want := *b
		if b.Info[in.Player-1].Explored {
			want.Info[in.Recipient-1].Explored = true
		}
		if !reflect.DeepEqual(&want, a) {
			return nil, commandError(CommandInvalid)
		}
		if e := add(BucketPlanets, PlanetKey(b.StarNum, b.PlanetNum), b, a); e != nil {
			return nil, e
		}
	}
	for _, st := range in.Stars {
		if st.Share {
			for n := uint8(0); n < st.Expected.NumPlanets; n++ {
				if !planets[[2]uint8{st.Expected.Number, n}] {
					return nil, commandError(CommandInvalid)
				}
			}
		}
	}
	return records, nil
}

// commandChain validates existing referenced orbital chains read-only in the
// same transaction. Dump preserves all heads/links and allocates no identity.
func commandChain(tx *bolt.Tx, head uint32, level, star, planet uint8) error {
	seen := map[uint32]bool{}
	for head != 0 {
		if seen[head] {
			return commandError(CommandCorrupt)
		}
		seen[head] = true
		var ship model.Ship
		raw := tx.Bucket(BucketShips).Get(ShipKey(head))
		if raw == nil || json.Unmarshal(raw, &ship) != nil || ship.Number != head || !ship.Alive || ship.WhatOrbits != level || ship.StOrbits != star || (level == model.LevelPlan && ship.PNumOrbits != planet) {
			return commandError(CommandCorrupt)
		}
		head = ship.NextShip
	}
	return nil
}

// CommitCommandRecords uses exactly ONE bbolt write transaction on this handle.
// Fault stages (existing SetMetaFault, isolated tests only): command.before,
// .validated, .write:N (one-based), .before-commit roll back; .commit-error models
// an error despite visibility (UNCERTAIN); .after-commit is a reporting error
// after a confirmed nil Update (COMMITTED). .readback can fail diagnostics.
// Callback errors/panics are rolled back by bbolt. A commit attempt that reports
// failure is never promoted to durable success by a visible readback.
func (s *Store) CommitCommandRecords(in CommandRecords) (outcome CommandOutcome, err error) {
	outcome = CommandNotCommitted
	if e := s.writeAllowed("CommitCommandRecords"); e != nil {
		return outcome, commandError(CommandFenced)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.CommandWriteUncertain() {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain, Readback: "unavailable"}
	}
	phase := 0 // before transaction / callback / attempting commit / confirmed commit
	var records []commandRecord
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
				ce.Readback = s.commandReadback(records)
			}
		}
	}()
	var e error
	records, e = prepareCommand(in)
	if e != nil {
		return outcome, e
	}
	if e = s.metaFault("command.before"); e != nil {
		return outcome, commandError(CommandIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("CommitCommandRecords"); e != nil {
			return commandError(CommandFenced)
		}
		for _, r := range records {
			b := tx.Bucket(r.bucket)
			if b == nil {
				return commandError(CommandCorrupt)
			}
			if !bytes.Equal(b.Get(r.key), r.before) {
				return commandError(CommandStale)
			}
		}
		for _, st := range in.Stars {
			if e := commandChain(tx, st.Expected.Ships, model.LevelStar, st.Expected.Number, 0); e != nil {
				return e
			}
		}
		for _, p := range in.Planets {
			if e := commandChain(tx, p.Expected.Ships, model.LevelPlan, p.Expected.StarNum, p.Expected.PlanetNum); e != nil {
				return e
			}
		}
		if e := s.metaFault("command.validated"); e != nil {
			return commandError(CommandIO)
		}
		for i, r := range records {
			if e := tx.Bucket(r.bucket).Put(r.key, r.after); e != nil {
				return commandError(CommandIO)
			}
			if e := s.metaFault(fmt.Sprintf("command.write:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("command.before-commit"); e != nil {
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
	if e = s.metaFault("command.commit-error"); e != nil {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
	}
	phase = 3
	outcome = CommandCommitted
	if e = s.metaFault("command.after-commit"); e != nil {
		return outcome, &CommandError{Code: CommandIO, Outcome: outcome}
	}
	return outcome, nil
}

func (s *Store) commandReadback(records []commandRecord) (visibility string) {
	visibility = "unavailable"
	defer func() {
		if recover() != nil {
			visibility = "unavailable"
		}
	}()
	if s.metaFault("command.readback") != nil {
		return visibility
	}
	before, after := true, true
	e := s.db.View(func(tx *bolt.Tx) error {
		for _, r := range records {
			b := tx.Bucket(r.bucket)
			if b == nil {
				return errors.New("missing bucket")
			}
			v := b.Get(r.key)
			before = before && bytes.Equal(v, r.before)
			after = after && bytes.Equal(v, r.after)
		}
		return nil
	})
	if e != nil {
		return visibility
	}
	switch {
	case after:
		return "proposal_visible"
	case before:
		return "preimage_visible"
	default:
		return "unexpected"
	}
}
