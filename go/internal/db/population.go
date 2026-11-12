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

// PopulationTransfer is a closed actor-only crew/troop operation. Exactly one
// partner ship or planet participates, together with complete immutable context.
// Scope is authoritative server context, not a player claim. AP is
// read-only: the owner approved original-C zero-AP behavior. No creation,
// allocation, metadata, orbit rewrite or other commodity/effect is supported.
type PopulationTransfer struct {
	Player, Governor int
	Selected         CommandShip
	Partner          *CommandShip
	Planet           *CommandPlanet
	Context          CommandContext
	Scope            model.Directory
	Request          commandrules.PopulationRequest
}
type preparedPopulation struct{ checks, writes []commandRecord }

func preparePopulation(in PopulationTransfer) (preparedPopulation, error) {
	invalid := func() (preparedPopulation, error) { return preparedPopulation{}, commandError(CommandInvalid) }
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
	change, e := commandrules.PlanPopulation(f, other, planet, r, in.Request)
	if e != nil {
		return invalid()
	}
	wantF := *f
	wantF.Mass = change.SelectedMass
	if in.Request.Commodity == 'c' {
		wantF.Popn = change.Selected
	} else {
		wantF.Troops = change.Selected
	}
	if !reflect.DeepEqual(&wantF, n) {
		return invalid()
	}
	if other != nil {
		want := *other
		want.Mass = change.OtherMass
		if in.Request.Commodity == 'c' {
			want.Popn = change.Other
		} else {
			want.Troops = change.Other
		}
		if !reflect.DeepEqual(&want, in.Partner.Proposed) {
			return invalid()
		}
	} else {
		want := *planet
		want.Sectors = append([]model.Sector(nil), planet.Sectors...)
		*want.SectorAt(int(f.LandX), int(f.LandY)) = change.Sector
		want.Info[in.Player-1] = change.Info
		want.Popn = change.PlanetPopn
		want.Troops = change.PlanetTroops
		if !reflect.DeepEqual(&want, in.Planet.Proposed) {
			return invalid()
		}
	}
	var out preparedPopulation
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

// CommitPopulationTransfer checks every preimage and writes selected then recipient
// in one transaction. AP remains read-only. Durable maintenance refuses writes.
func (s *Store) CommitPopulationTransfer(in PopulationTransfer) (outcome CommandOutcome, err error) {
	outcome = CommandNotCommitted
	if e := s.writeAllowed("CommitPopulationTransfer"); e != nil {
		return outcome, commandError(CommandFenced)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.CommandWriteUncertain() {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain, Readback: "unavailable"}
	}
	phase := 0
	var prepared preparedPopulation
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
	prepared, e = preparePopulation(in)
	if e != nil {
		return outcome, e
	}
	if e = s.metaFault("population.before"); e != nil {
		return outcome, commandError(CommandIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("CommitPopulationTransfer"); e != nil {
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
			if e := s.metaFault(fmt.Sprintf("population.check:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("population.validated"); e != nil {
			return commandError(CommandIO)
		}
		for i, r := range prepared.writes {
			if e := tx.Bucket(r.bucket).Put(r.key, r.after); e != nil {
				return commandError(CommandIO)
			}
			if e := s.metaFault(fmt.Sprintf("population.write:%d", i+1)); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("population.before-commit"); e != nil {
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
	if e = s.metaFault("population.commit-error"); e != nil {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
	}
	phase = 3
	outcome = CommandCommitted
	if e = s.metaFault("population.after-commit"); e != nil {
		return outcome, &CommandError{Code: CommandIO, Outcome: outcome}
	}
	return outcome, nil
}
