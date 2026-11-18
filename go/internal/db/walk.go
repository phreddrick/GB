package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
	"reflect"
	"sort"
)

// NoncombatWalk is a closed two-record command. Complete planet, race and
// planet-root ship-chain preimages are read-only defense/authority witnesses.
// Scope is immutable actor-local context, not a persistent directory record.
type NoncombatWalk struct {
	Input    commandrules.WalkInput
	Proposed commandrules.WalkChange
	Scope    model.Directory
}
type preparedWalk struct{ checks, writes []commandRecord }

func prepareWalk(in NoncombatWalk) (preparedWalk, error) {
	var out preparedWalk
	want, e := commandrules.PlanWalk(in.Input)
	if e != nil || in.Scope.Level > model.LevelShip || !reflect.DeepEqual(want, in.Proposed) {
		return out, commandError(CommandInvalid)
	}
	add := func(bucket, key []byte, before, after any, write bool) error {
		b, e := json.Marshal(before)
		if e != nil {
			return e
		}
		a, e := json.Marshal(after)
		if e != nil {
			return e
		}
		r := commandRecord{bucket, key, b, a}
		out.checks = append(out.checks, r)
		if write {
			out.writes = append(out.writes, r)
		}
		return nil
	}
	f, st, p := in.Input.Ship, in.Input.Star, in.Input.Planet
	records := []struct {
		bucket, key   []byte
		before, after any
		write         bool
	}{
		{BucketShips, ShipKey(f.Number), f, in.Proposed.Ship, true},
		{BucketStars, StarKey(st.Number), st, in.Proposed.Star, true},
		{BucketPlanets, PlanetKey(p.StarNum, p.PlanetNum), p, p, false},
	}
	races := append([]*model.Race(nil), in.Input.Races...)
	sort.Slice(races, func(i, j int) bool { return races[i].PlayerNum < races[j].PlayerNum })
	for _, r := range races {
		records = append(records, struct {
			bucket, key   []byte
			before, after any
			write         bool
		}{BucketRaces, RaceKey(r.PlayerNum), r, r, false})
	}
	ships := append([]*model.Ship(nil), in.Input.Ships...)
	sort.Slice(ships, func(i, j int) bool { return ships[i].Number < ships[j].Number })
	for _, sh := range ships {
		if sh.Number != f.Number {
			records = append(records, struct {
				bucket, key   []byte
				before, after any
				write         bool
			}{BucketShips, ShipKey(sh.Number), sh, sh, false})
		}
	}
	for _, r := range records {
		if e := add(r.bucket, r.key, r.before, r.after, r.write); e != nil {
			return preparedWalk{}, commandError(CommandInvalid)
		}
	}
	return out, nil
}

// CommitNoncombatWalk checks full preimages inside one bbolt transaction.
// It shares the command mutex, fence, maintenance refusal, uncertainty guard and
// diagnostic-only readback with the existing closed CR-17 operations. Fault
// stages: walk.before/check:N/validated/write:1 (ship)/write:2 (star)/
// before-commit abort; commit-error is uncertain; after-commit is committed.
func (s *Store) CommitNoncombatWalk(in NoncombatWalk) (outcome CommandOutcome, err error) {
	outcome = CommandNotCommitted
	if e := s.writeAllowed("CommitNoncombatWalk"); e != nil {
		return outcome, commandError(CommandFenced)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.CommandWriteUncertain() {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain, Readback: "unavailable"}
	}
	phase := 0
	var prepared preparedWalk
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
	prepared, e = prepareWalk(in)
	if e != nil {
		return outcome, e
	}
	if e = s.metaFault("walk.before"); e != nil {
		return outcome, commandError(CommandIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("CommitNoncombatWalk"); e != nil {
			return commandError(CommandFenced)
		}
		meta := tx.Bucket(BucketMeta)
		if meta == nil {
			return commandError(CommandCorrupt)
		}
		if raw := meta.Get(KeyMaintenance); raw != nil {
			if _, e := DecodeMaintenanceRecord(raw); e != nil {
				return commandError(CommandCorrupt)
			}
			return commandError(CommandInvalid)
		}
		for i, r := range prepared.checks {
			b := tx.Bucket(r.bucket)
			if b == nil {
				return commandError(CommandCorrupt)
			}
			if !bytes.Equal(b.Get(r.key), r.before) {
				return commandError(CommandStale)
			}
			if e := s.metaFault(fmt.Sprintf("walk.check:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("walk.validated"); e != nil {
			return commandError(CommandIO)
		}
		for i, r := range prepared.writes {
			if e := tx.Bucket(r.bucket).Put(r.key, r.after); e != nil {
				return commandError(CommandIO)
			}
			if e := s.metaFault(fmt.Sprintf("walk.write:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("walk.before-commit"); e != nil {
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
	if e = s.metaFault("walk.commit-error"); e != nil {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
	}
	phase = 3
	outcome = CommandCommitted
	if e = s.metaFault("walk.after-commit"); e != nil {
		return outcome, &CommandError{Code: CommandIO, Outcome: outcome}
	}
	return outcome, nil
}
