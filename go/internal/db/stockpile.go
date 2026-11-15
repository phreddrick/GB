package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

// StockpileTransfer is a closed actor-only operation: two stockpiles within one
// planet and exactly one star AP. Races are immutable identity witnesses; Scope
// is actor-local context, not a persistent directory record. No orbital,
// allocator, sector, race, universe-AP or CR-18 metadata change is supported.
type StockpileTransfer struct {
	Player, Governor, Recipient int
	Commodity                   byte
	Amount                      uint16
	Scope                       model.Directory
	Planet                      CommandPlanet
	Star                        CommandStar
	Caller, Target              *model.Race
}

type preparedStockpile struct{ checks, writes []commandRecord }

func prepareStockpile(in StockpileTransfer) (preparedStockpile, error) {
	invalid := func() (preparedStockpile, error) { return preparedStockpile{}, commandError(CommandInvalid) }
	p, q, st, next := in.Planet.Expected, in.Planet.Proposed, in.Star.Expected, in.Star.Proposed
	if in.Player < 1 || in.Player > model.MaxPlayers || in.Recipient < 1 || in.Recipient > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || p == nil || q == nil || st == nil || next == nil || in.Caller == nil || in.Target == nil {
		return invalid()
	}
	if in.Caller.PlayerNum != in.Player || in.Target.PlayerNum != in.Recipient || in.Caller.Dissolved || !in.Caller.Governor[in.Governor].Active || (in.Player == in.Recipient && !reflect.DeepEqual(in.Caller, in.Target)) {
		return invalid()
	}
	if in.Star.Share || in.Scope.Level != model.LevelPlan || in.Scope.StarNum != st.Number || in.Scope.PlanetNum != p.PlanetNum || p.StarNum != st.Number || int(st.Number) >= model.NumStars || int(st.NumPlanets) > model.MaxPlanets || p.PlanetNum >= st.NumPlanets || p.MaxX == 0 || p.MaxY == 0 || len(p.Sectors) != int(p.MaxX)*int(p.MaxY) || st.AP[in.Player-1] < 1 {
		return invalid()
	}
	want := *p
	from, to := stockpileBalances(&want, in.Player, in.Recipient, in.Commodity)
	if from == nil || in.Amount > *from {
		return invalid()
	}
	// Preserve load.c/CR-17A uint16 assignment arithmetic, including self,
	// zero and destination wrap. Changing those gameplay rules is out of scope.
	*from -= in.Amount
	*to += in.Amount
	wantStar := *st
	wantStar.AP[in.Player-1]--
	if !reflect.DeepEqual(&want, q) || !reflect.DeepEqual(&wantStar, next) {
		return invalid()
	}
	var out preparedStockpile
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
	if e := add(BucketPlanets, PlanetKey(p.StarNum, p.PlanetNum), p, q, true); e != nil {
		return invalid()
	}
	if e := add(BucketStars, StarKey(st.Number), st, next, true); e != nil {
		return invalid()
	}
	if e := add(BucketRaces, RaceKey(in.Player), in.Caller, in.Caller, false); e != nil {
		return invalid()
	}
	if in.Recipient != in.Player {
		if e := add(BucketRaces, RaceKey(in.Recipient), in.Target, in.Target, false); e != nil {
			return invalid()
		}
	}
	return out, nil
}

func stockpileBalances(p *model.Planet, player, target int, commodity byte) (*uint16, *uint16) {
	from, to := &p.Info[player-1], &p.Info[target-1]
	switch commodity {
	case 'r':
		return &from.Resource, &to.Resource
	case 'x', '&':
		return &from.Crystals, &to.Crystals
	case 'f':
		return &from.Fuel, &to.Fuel
	case 'd':
		return &from.Destruct, &to.Destruct
	default:
		return nil, nil
	}
}

// CommitStockpileTransfer checks full preimages inside one bbolt transaction.
// It shares the command mutex, fence, maintenance refusal, uncertainty guard and
// diagnostic-only readback with the existing closed CR-17 operations. Fault
// stages: stockpile.before/check:N/validated/write:1 (planet)/write:2 (star)/
// before-commit abort; commit-error is uncertain; after-commit is committed.
func (s *Store) CommitStockpileTransfer(in StockpileTransfer) (outcome CommandOutcome, err error) {
	outcome = CommandNotCommitted
	if e := s.writeAllowed("CommitStockpileTransfer"); e != nil {
		return outcome, commandError(CommandFenced)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.CommandWriteUncertain() {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain, Readback: "unavailable"}
	}
	phase := 0
	var prepared preparedStockpile
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
	prepared, e = prepareStockpile(in)
	if e != nil {
		return outcome, e
	}
	if e = s.metaFault("stockpile.before"); e != nil {
		return outcome, commandError(CommandIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("CommitStockpileTransfer"); e != nil {
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
			if e := s.metaFault(fmt.Sprintf("stockpile.check:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("stockpile.validated"); e != nil {
			return commandError(CommandIO)
		}
		for i, r := range prepared.writes {
			if e := tx.Bucket(r.bucket).Put(r.key, r.after); e != nil {
				return commandError(CommandIO)
			}
			if e := s.metaFault(fmt.Sprintf("stockpile.write:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("stockpile.before-commit"); e != nil {
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
	if e = s.metaFault("stockpile.commit-error"); e != nil {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
	}
	phase = 3
	outcome = CommandCommitted
	if e = s.metaFault("stockpile.after-commit"); e != nil {
		return outcome, &CommandError{Code: CommandIO, Outcome: outcome}
	}
	return outcome, nil
}
