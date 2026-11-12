package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"reflect"
	"sort"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/model"
)

// CargoTransfer is a closed actor-only resource/crystal operation. Exactly one
// partner ship or planet participates, together with complete immutable context.
// Scope and rates are authoritative server inputs, not player claims. AP is
// read-only: the owner approved original-C zero-AP behavior. No creation,
// allocation, metadata, orbit rewrite or other commodity/effect is supported.
type CargoTransfer struct {
	Player, Governor int
	Selected         CommandShip
	Partner          *CommandShip
	Planet           *CommandPlanet
	Context          CommandContext
	Scope            model.Directory
	Request          commandrules.CargoRequest
}
type preparedCargo struct{ checks, writes []commandRecord }

// cargoLocation validates actual docking rather than performing legacy undock
// repairs. Recursive location checks also require full, alive, unique chains.
func cargoLocation(f, other *model.Ship, p *model.Planet, scope model.Directory, ctx telescopeContext) bool {
	if f == nil || !f.Docked || !ctx.location(f.Number, map[uint32]bool{}) {
		return false
	}
	if p != nil {
		return f.WhatOrbits == model.LevelPlan && f.WhatDest == model.LevelPlan && f.StOrbits == p.StarNum && f.PNumOrbits == p.PlanetNum && f.DestStar == p.StarNum && f.DestPNum == p.PlanetNum && scope.StarNum == p.StarNum && scope.PlanetNum == p.PlanetNum && f.LandX < p.MaxX && f.LandY < p.MaxY
	}
	if other == nil || other.Number == f.Number || !other.Alive || f.WhatDest != model.LevelShip || f.DestShipNo != other.Number || !ctx.location(other.Number, map[uint32]bool{}) {
		return false
	}
	if other.WhatOrbits == model.LevelShip && commandrules.CargoOverloaded(other) {
		return false
	}
	if f.WhatOrbits == model.LevelShip {
		// The validated parent chain determines the actual root location.
		// Planet numbers are irrelevant at star/universe roots.
		root := other
		for root.WhatOrbits == model.LevelShip {
			root = ctx.ships[root.DestShipNo]
		}
		switch root.WhatOrbits {
		case model.LevelPlan:
			return f.StOrbits == root.StOrbits && f.PNumOrbits == root.PNumOrbits
		case model.LevelStar:
			return f.StOrbits == root.StOrbits
		case model.LevelUniv:
			return true
		default:
			return false
		}
	}
	if !other.Docked || other.WhatDest != model.LevelShip || other.DestShipNo != f.Number || f.WhatOrbits != other.WhatOrbits {
		return false
	}
	if (f.WhatOrbits == model.LevelStar && f.StOrbits != other.StOrbits) || (f.WhatOrbits == model.LevelPlan && (f.StOrbits != other.StOrbits || f.PNumOrbits != other.PNumOrbits)) {
		return false
	}
	return true
}
func prepareCargo(in CargoTransfer) (preparedCargo, error) {
	invalid := func() (preparedCargo, error) { return preparedCargo{}, commandError(CommandInvalid) }
	f, n, r := in.Selected.Expected, in.Selected.Proposed, in.Context.Race
	if f == nil || n == nil || r == nil || in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || r.PlayerNum != in.Player || r.Dissolved || !r.Governor[in.Governor].Active || f.Number == 0 || !f.Alive || !f.Active || int(f.Type) >= len(model.Shipdata) || int(f.Owner) != in.Player || (in.Governor != 0 && int(f.Governor) != in.Governor) || (in.Partner == nil) == (in.Planet == nil) {
		return invalid()
	}
	var other *model.Ship
	var planet *model.Planet
	if in.Partner != nil {
		other = in.Partner.Expected
		if other == nil || in.Partner.Proposed == nil || int(other.Type) >= len(model.Shipdata) {
			return invalid()
		}
	}
	if in.Planet != nil {
		planet = in.Planet.Expected
		if planet == nil || in.Planet.Proposed == nil {
			return invalid()
		}
	}
	ctx := telescopeContext{stars: map[uint8]*model.Star{}, planets: map[[2]uint8]*model.Planet{}, ships: map[uint32]*model.Ship{}, data: in.Context.StarData}
	if ctx.data != nil && int(ctx.data.NumStars) > model.NumStars {
		return invalid()
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
		if p == nil || ctx.planets[[2]uint8{p.StarNum, p.PlanetNum}] != nil || p.MaxX == 0 || p.MaxY == 0 || len(p.Sectors) != int(p.MaxX)*int(p.MaxY) {
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
	if !reflect.DeepEqual(ctx.ships[f.Number], f) || (other != nil && !reflect.DeepEqual(ctx.ships[other.Number], other)) || (planet != nil && !reflect.DeepEqual(ctx.planets[[2]uint8{planet.StarNum, planet.PlanetNum}], planet)) || !cargoLocation(f, other, planet, in.Scope, ctx) {
		return invalid()
	}
	change, e := commandrules.PlanCargo(f, other, planet, in.Player, in.Request)
	if e != nil {
		return invalid()
	}
	wantF := *f
	wantF.Mass = change.SelectedMass
	if in.Request.Commodity == 'r' {
		wantF.Resource = change.Selected
	} else {
		wantF.Crystals = change.Selected
	}
	if !reflect.DeepEqual(&wantF, n) {
		return invalid()
	}
	if other != nil {
		want := *other
		want.Mass = change.OtherMass
		if in.Request.Commodity == 'r' {
			want.Resource = change.Other
		} else {
			want.Crystals = change.Other
		}
		if !reflect.DeepEqual(&want, in.Partner.Proposed) {
			return invalid()
		}
	} else {
		want := *planet
		if in.Request.Commodity == 'r' {
			want.Info[in.Player-1].Resource = change.Other
		} else {
			want.Info[in.Player-1].Crystals = change.Other
		}
		if !reflect.DeepEqual(&want, in.Planet.Proposed) {
			return invalid()
		}
	}
	var out preparedCargo
	add := func(bucket, key []byte, before, after any, write bool) error {
		b, e := json.Marshal(before)
		if e != nil {
			return e
		}
		a, e := json.Marshal(after)
		if e != nil {
			return e
		}
		rec := commandRecord{bucket, key, b, a}
		out.checks = append(out.checks, rec)
		if write {
			out.writes = append(out.writes, rec)
		}
		return nil
	}
	// Write order is explicit even for zero-quantity transfers. All read-only
	// records participate in precondition checks, never in the write set.
	if e := add(BucketShips, ShipKey(f.Number), f, n, true); e != nil {
		return invalid()
	}
	if other != nil {
		if e := add(BucketShips, ShipKey(other.Number), other, in.Partner.Proposed, true); e != nil {
			return invalid()
		}
	} else {
		if e := add(BucketPlanets, PlanetKey(planet.StarNum, planet.PlanetNum), planet, in.Planet.Proposed, true); e != nil {
			return invalid()
		}
	}
	if e := add(BucketRaces, RaceKey(in.Player), r, r, false); e != nil {
		return invalid()
	}
	if ctx.data != nil {
		if e := add(BucketSData, starDataKey, ctx.data, ctx.data, false); e != nil {
			return invalid()
		}
	}
	stars := append([]*model.Star(nil), in.Context.Stars...)
	sort.Slice(stars, func(i, j int) bool { return stars[i].Number < stars[j].Number })
	planets := append([]*model.Planet(nil), in.Context.Planets...)
	sort.Slice(planets, func(i, j int) bool {
		a, b := planets[i], planets[j]
		return int(a.StarNum)*model.MaxPlanets+int(a.PlanetNum) < int(b.StarNum)*model.MaxPlanets+int(b.PlanetNum)
	})
	ships := append([]*model.Ship(nil), in.Context.Ships...)
	sort.Slice(ships, func(i, j int) bool { return ships[i].Number < ships[j].Number })
	for _, s := range stars {
		if e := add(BucketStars, StarKey(s.Number), s, s, false); e != nil {
			return invalid()
		}
	}
	for _, p := range planets {
		if planet != nil && p.StarNum == planet.StarNum && p.PlanetNum == planet.PlanetNum {
			continue
		}
		if e := add(BucketPlanets, PlanetKey(p.StarNum, p.PlanetNum), p, p, false); e != nil {
			return invalid()
		}
	}
	for _, s := range ships {
		if s.Number == f.Number || (other != nil && s.Number == other.Number) {
			continue
		}
		if e := add(BucketShips, ShipKey(s.Number), s, s, false); e != nil {
			return invalid()
		}
	}
	return out, nil
}

// CommitCargoTransfer checks every preimage and writes selected then recipient
// in one transaction. AP remains read-only. Durable maintenance refuses writes.
func (s *Store) CommitCargoTransfer(in CargoTransfer) (outcome CommandOutcome, err error) {
	outcome = CommandNotCommitted
	if e := s.writeAllowed("CommitCargoTransfer"); e != nil {
		return outcome, commandError(CommandFenced)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.CommandWriteUncertain() {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain, Readback: "unavailable"}
	}
	phase := 0
	var prepared preparedCargo
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
	prepared, e = prepareCargo(in)
	if e != nil {
		return outcome, e
	}
	if e = s.metaFault("cargo.before"); e != nil {
		return outcome, commandError(CommandIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("CommitCargoTransfer"); e != nil {
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
			if e := s.metaFault(fmt.Sprintf("cargo.check:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("cargo.validated"); e != nil {
			return commandError(CommandIO)
		}
		for i, r := range prepared.writes {
			if e := tx.Bucket(r.bucket).Put(r.key, r.after); e != nil {
				return commandError(CommandIO)
			}
			if e := s.metaFault(fmt.Sprintf("cargo.write:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("cargo.before-commit"); e != nil {
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
	if e = s.metaFault("cargo.commit-error"); e != nil {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
	}
	phase = 3
	outcome = CommandCommitted
	if e = s.metaFault("cargo.after-commit"); e != nil {
		return outcome, &CommandError{Code: CommandIO, Outcome: outcome}
	}
	return outcome, nil
}
