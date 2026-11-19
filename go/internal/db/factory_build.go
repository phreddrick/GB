package db

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

// FactoryAllocation is an optimistic read-only construction ticket, never a
// reservation. Its complete free-list sequence/content and candidate are
// independently rechecked inside CommitPlanetFactory's write transaction.
type FactoryAllocation struct {
	Number     uint32
	FreeKey    uint64
	FreeDigest string
}

func (s *Store) PlanetFactoryCandidate() (FactoryAllocation, error) {
	var ticket FactoryAllocation
	e := s.db.View(func(tx *bolt.Tx) error { var e error; ticket, e = factoryBuildShipTx(tx); return e })
	if e != nil {
		var ce *CommandError
		if errors.As(e, &ce) {
			return FactoryAllocation{}, e
		}
		return FactoryAllocation{}, commandError(CommandIO)
	}
	return ticket, nil
}

// PlanetFactoryBuild is closed to one new ordinary factory, its planet debit
// and root link, and exact allocator consumption. Star/AP/race/chain immutable.
type PlanetFactoryBuild struct {
	Input      commandrules.PlanetFactoryInput
	Proposed   commandrules.PlanetFactoryChange
	Allocation FactoryAllocation
}
type preparedFactoryBuild struct{ checks, writes []commandRecord }

func prepareFactoryBuild(in PlanetFactoryBuild) (preparedFactoryBuild, error) {
	var out preparedFactoryBuild
	want, e := commandrules.PlanPlanetFactory(in.Input)
	if e != nil || in.Input.Number != in.Allocation.Number || !reflect.DeepEqual(want, in.Proposed) {
		return out, commandError(CommandInvalid)
	}
	add := func(bucket, key []byte, before, after any, write bool) error {
		var b, a []byte
		var e error
		if before != nil {
			b, e = json.Marshal(before)
			if e != nil {
				return e
			}
		}
		if after != nil {
			a, e = json.Marshal(after)
			if e != nil {
				return e
			}
		}
		r := commandRecord{bucket, key, b, a}
		out.checks = append(out.checks, r)
		if write {
			out.writes = append(out.writes, r)
		}
		return nil
	}
	f, p, st, r := in.Proposed.Ship, in.Input.Planet, in.Input.Star, in.Input.Race
	if e := add(BucketShips, ShipKey(f.Number), nil, f, true); e != nil {
		return out, commandError(CommandInvalid)
	}
	if e := add(BucketPlanets, PlanetKey(p.StarNum, p.PlanetNum), p, in.Proposed.Planet, true); e != nil {
		return out, commandError(CommandInvalid)
	}
	if e := add(BucketStars, StarKey(st.Number), st, st, false); e != nil {
		return out, commandError(CommandInvalid)
	}
	if e := add(BucketRaces, RaceKey(r.PlayerNum), r, r, false); e != nil {
		return out, commandError(CommandInvalid)
	}
	ships := append([]*model.Ship(nil), in.Input.Ships...)
	sort.Slice(ships, func(i, j int) bool { return ships[i].Number < ships[j].Number })
	for _, sh := range ships {
		if e := add(BucketShips, ShipKey(sh.Number), sh, sh, false); e != nil {
			return out, commandError(CommandInvalid)
		}
	}
	if in.Allocation.FreeKey != 0 {
		value := make([]byte, 4)
		binary.BigEndian.PutUint32(value, in.Allocation.Number)
		out.checks = append(out.checks, commandRecord{BucketShipFree, enrollmentFreeKey(in.Allocation.FreeKey), value, nil})
	}
	return out, nil
}

// Faults factory-build.before/check:N/validated/write:1 (ship)/write:2
// (planet)/write:3 (allocator)/before-commit abort; commit-error is uncertain,
// after-commit is confirmed. Readback is diagnostic, never fsync evidence.
func (s *Store) CommitPlanetFactory(in PlanetFactoryBuild) (outcome CommandOutcome, err error) {
	outcome = CommandNotCommitted
	if e := s.writeAllowed("CommitPlanetFactory"); e != nil {
		return outcome, commandError(CommandFenced)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.CommandWriteUncertain() {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain, Readback: "unavailable"}
	}
	phase := 0
	var prepared preparedFactoryBuild
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
	prepared, e = prepareFactoryBuild(in)
	if e != nil {
		return outcome, e
	}
	if e = s.metaFault("factory-build.before"); e != nil {
		return outcome, commandError(CommandIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("CommitPlanetFactory"); e != nil {
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
			if e := s.metaFault(fmt.Sprintf("factory-build.check:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		ticket, e := factoryBuildShipTx(tx)
		if e != nil {
			return e
		}
		if ticket != in.Allocation {
			return commandError(CommandStale)
		}
		if e := s.metaFault("factory-build.validated"); e != nil {
			return commandError(CommandIO)
		}
		for i, r := range prepared.writes {
			if e := tx.Bucket(r.bucket).Put(r.key, r.after); e != nil {
				return commandError(CommandIO)
			}
			if e := s.metaFault(fmt.Sprintf("factory-build.write:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if in.Allocation.FreeKey != 0 {
			if e := tx.Bucket(BucketShipFree).Delete(enrollmentFreeKey(in.Allocation.FreeKey)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("factory-build.write:3"); e != nil {
			return commandError(CommandIO)
		}
		if e := s.metaFault("factory-build.before-commit"); e != nil {
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
	if e = s.metaFault("factory-build.commit-error"); e != nil {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
	}
	phase = 3
	outcome = CommandCommitted
	if e = s.metaFault("factory-build.after-commit"); e != nil {
		return outcome, &CommandError{Code: CommandIO, Outcome: outcome}
	}
	return outcome, nil
}
