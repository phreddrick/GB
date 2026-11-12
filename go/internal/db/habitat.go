package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
	bolt "go.etcd.io/bbolt"
)

// CommandContext is the shared immutable read-witness set of the closed
// command operations (the same shape the telescope operation introduced).
type CommandContext = TelescopeContext

// HabitatActivation is the closed actor-only operation for `order #N on` of a
// factory docked aboard a habitat. The only writes are the factory's
// On/Notified/Size and the carrier's Resource/Hanger (order.c:528-551). The
// race, universe root, every ship of the carrier's docked chain, the carrier's
// own location chain and its root star/planet are read-only witnesses that
// must be unchanged inside the transaction. Size carries the server SIZE_*
// rates. No AP, creation, deletion, allocator, orbit rewrite, effect or
// metadata change is supported.
type HabitatActivation struct {
	Player, Governor int
	Factory, Carrier CommandShip
	Context          CommandContext
	Size             commandrules.SizeFactors
}
type preparedHabitat struct{ checks, writes []commandRecord }

func prepareHabitatActivation(in HabitatActivation) (preparedHabitat, error) {
	invalid := func() (preparedHabitat, error) { return preparedHabitat{}, commandError(CommandInvalid) }
	f, n, c, d, r := in.Factory.Expected, in.Factory.Proposed, in.Carrier.Expected, in.Carrier.Proposed, in.Context.Race
	if f == nil || n == nil || c == nil || d == nil || r == nil || in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || r.PlayerNum != in.Player || !r.Governor[in.Governor].Active || r.Dissolved {
		return invalid()
	}
	if f.Number == 0 || f.Type != model.OtypeFactory || !f.Alive || !f.Active || f.On || int(f.Owner) != in.Player || (in.Governor != 0 && int(f.Governor) != in.Governor) || f.WhatOrbits != model.LevelShip || !f.Docked || f.WhatDest != model.LevelShip || f.DestShipNo != c.Number {
		return invalid()
	}
	if model.Shipdata[model.OtypeFactory][model.AbilMaxCrew]-int64(f.Troops) > 0 && f.Popn == 0 {
		return invalid()
	}
	// C charges the actual containing habitat, independent of its owner.
	// Factory authority is checked above; full carrier identity, membership
	// and persisted preimages remain required below.
	if c.Number == 0 || c.Number == f.Number || c.Type != model.StypeHabitat || !c.Alive || f.StOrbits != c.StOrbits || f.PNumOrbits != c.PNumOrbits {
		return invalid()
	}
	ctx := telescopeContext{stars: map[uint8]*model.Star{}, planets: map[[2]uint8]*model.Planet{}, ships: map[uint32]*model.Ship{}, data: in.Context.StarData}
	var out preparedHabitat
	add := func(bucket, key []byte, before, after any) error {
		b, e := json.Marshal(before)
		if e != nil {
			return e
		}
		a, e := json.Marshal(after)
		if e != nil {
			return e
		}
		out.checks = append(out.checks, commandRecord{bucket, key, b, a})
		return nil
	}
	if e := add(BucketRaces, RaceKey(in.Player), r, r); e != nil {
		return invalid()
	}
	if ctx.data != nil {
		if int(ctx.data.NumStars) > model.NumStars {
			return invalid()
		}
		if e := add(BucketSData, starDataKey, ctx.data, ctx.data); e != nil {
			return invalid()
		}
	}
	if len(in.Context.Stars) > model.NumStars || len(in.Context.Planets) > model.NumStars*model.MaxPlanets {
		return invalid()
	}
	for _, s := range in.Context.Stars {
		if s == nil || ctx.stars[s.Number] != nil || int(s.NumPlanets) > model.MaxPlanets {
			return invalid()
		}
		ctx.stars[s.Number] = s
	}
	for _, p := range in.Context.Planets {
		if p == nil || p.MaxX == 0 || p.MaxY == 0 || len(p.Sectors) != int(p.MaxX)*int(p.MaxY) || ctx.planets[[2]uint8{p.StarNum, p.PlanetNum}] != nil {
			return invalid()
		}
		ctx.planets[[2]uint8{p.StarNum, p.PlanetNum}] = p
	}
	for _, s := range in.Context.Ships {
		if s == nil || s.Number == 0 || ctx.ships[s.Number] != nil {
			return invalid()
		}
		ctx.ships[s.Number] = s
	}
	// The factory must sit exactly once in the carrier's docked chain and the
	// carrier exactly once in its own root chain (recursively through parents).
	if !reflect.DeepEqual(ctx.ships[f.Number], f) || !reflect.DeepEqual(ctx.ships[c.Number], c) || !ctx.location(f.Number, map[uint32]bool{}) {
		return invalid()
	}
	change, e := commandrules.PlanHabitatActivation(f, c, in.Size)
	if e != nil {
		return invalid()
	}
	wantF := *f
	wantF.On = true
	wantF.Notified = false
	wantF.Size = change.Size
	wantC := *c
	wantC.Resource = change.Resource
	wantC.Hanger = change.Hanger
	if !reflect.DeepEqual(&wantF, n) || !reflect.DeepEqual(&wantC, d) {
		return invalid()
	}
	stars := append([]*model.Star(nil), in.Context.Stars...)
	planets := append([]*model.Planet(nil), in.Context.Planets...)
	ships := append([]*model.Ship(nil), in.Context.Ships...)
	sort.Slice(stars, func(i, j int) bool { return stars[i].Number < stars[j].Number })
	sort.Slice(planets, func(i, j int) bool {
		a, b := planets[i], planets[j]
		return int(a.StarNum)*model.MaxPlanets+int(a.PlanetNum) < int(b.StarNum)*model.MaxPlanets+int(b.PlanetNum)
	})
	sort.Slice(ships, func(i, j int) bool { return ships[i].Number < ships[j].Number })
	for _, s := range stars {
		if e := add(BucketStars, StarKey(s.Number), s, s); e != nil {
			return invalid()
		}
	}
	for _, p := range planets {
		if e := add(BucketPlanets, PlanetKey(p.StarNum, p.PlanetNum), p, p); e != nil {
			return invalid()
		}
	}
	var factoryRec, carrierRec *commandRecord
	for _, s := range ships {
		next := s
		switch s.Number {
		case f.Number:
			next = n
		case c.Number:
			next = d
		}
		if e := add(BucketShips, ShipKey(s.Number), s, next); e != nil {
			return invalid()
		}
		rec := &out.checks[len(out.checks)-1]
		switch s.Number {
		case f.Number:
			factoryRec = rec
		case c.Number:
			carrierRec = rec
		}
	}
	if factoryRec == nil || carrierRec == nil {
		return invalid()
	}
	out.writes = []commandRecord{*factoryRec, *carrierRec}
	return out, nil
}

// CommitHabitatActivation performs exactly two checked Puts (factory, then
// carrier) in one existing bbolt Update after every witness preimage was
// compared inside the same transaction. It shares the command mutex,
// uncertainty guard, outcome/error types and readback of the other closed
// operations. Fault stages (isolated tests only, via SetMetaFault):
// habitat.before, .validated, .write:1 (factory), .write:2 (carrier),
// .before-commit abort; .commit-error is UNCERTAIN despite visibility;
// .after-commit is a reporting error after a confirmed commit.
func (s *Store) CommitHabitatActivation(in HabitatActivation) (outcome CommandOutcome, err error) {
	outcome = CommandNotCommitted
	if e := s.writeAllowed("CommitHabitatActivation"); e != nil {
		return outcome, commandError(CommandFenced)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.CommandWriteUncertain() {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain, Readback: "unavailable"}
	}
	phase := 0
	var prepared preparedHabitat
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
	prepared, e = prepareHabitatActivation(in)
	if e != nil {
		return outcome, e
	}
	if e = s.metaFault("habitat.before"); e != nil {
		return outcome, commandError(CommandIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("CommitHabitatActivation"); e != nil {
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
		if e := s.metaFault("habitat.validated"); e != nil {
			return commandError(CommandIO)
		}
		for i, r := range prepared.writes {
			if e := tx.Bucket(r.bucket).Put(r.key, r.after); e != nil {
				return commandError(CommandIO)
			}
			if e := s.metaFault(fmt.Sprintf("habitat.write:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("habitat.before-commit"); e != nil {
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
	if e = s.metaFault("habitat.commit-error"); e != nil {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
	}
	phase = 3
	outcome = CommandCommitted
	if e = s.metaFault("habitat.after-commit"); e != nil {
		return outcome, &CommandError{Code: CommandIO, Outcome: outcome}
	}
	return outcome, nil
}
