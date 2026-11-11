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

// TelescopeContext consists solely of private immutable read witnesses. No
// race, directory, universe, orbit or allocator mutation is supported.
type TelescopeContext struct {
	Race     *model.Race
	StarData *model.StarData
	Stars    []*model.Star
	Planets  []*model.Planet
	Ships    []*model.Ship
}

// TelescopeAim is a closed actor-only operation. Target resolution and config
// rates are trusted server inputs. Full source and target orbital context and
// authority are checked again; the transaction validates every stored preimage.
type TelescopeAim struct {
	Player, Governor       int
	Ship                   CommandShip
	Target                 model.Place
	Star                   *CommandStar
	Planet                 *CommandPlanet
	Context                TelescopeContext
	ManeuverFuel, MassFuel float64
}
type telescopeContext struct {
	stars   map[uint8]*model.Star
	planets map[[2]uint8]*model.Planet
	ships   map[uint32]*model.Ship
	data    *model.StarData
}

func (c telescopeContext) location(id uint32, parents map[uint32]bool) bool {
	sh := c.ships[id]
	if sh == nil || !sh.Alive || parents[id] {
		return false
	}
	parents[id] = true
	var head uint32
	switch sh.WhatOrbits {
	case model.LevelUniv:
		if c.data == nil {
			return false
		}
		head = c.data.Ships
	case model.LevelStar:
		st := c.stars[sh.StOrbits]
		if st == nil {
			return false
		}
		head = st.Ships
	case model.LevelPlan:
		st := c.stars[sh.StOrbits]
		pl := c.planets[[2]uint8{sh.StOrbits, sh.PNumOrbits}]
		if st == nil || pl == nil || pl.PlanetNum >= st.NumPlanets {
			return false
		}
		head = pl.Ships
	case model.LevelShip:
		parent := c.ships[sh.DestShipNo]
		if parent == nil || !c.location(parent.Number, parents) {
			return false
		}
		head = parent.Ships
	default:
		return false
	}
	seen := map[uint32]bool{}
	found := false
	for head != 0 {
		x := c.ships[head]
		if x == nil || !x.Alive || seen[head] || x.WhatOrbits != sh.WhatOrbits {
			return false
		}
		seen[head] = true
		switch sh.WhatOrbits {
		case model.LevelStar:
			if x.StOrbits != sh.StOrbits {
				return false
			}
		case model.LevelPlan:
			if x.StOrbits != sh.StOrbits || x.PNumOrbits != sh.PNumOrbits {
				return false
			}
		case model.LevelShip:
			if x.DestShipNo != sh.DestShipNo {
				return false
			}
		}
		if head == id {
			found = true
		}
		head = x.NextShip
	}
	return found
}

type preparedTelescope struct{ checks, writes []commandRecord }

func prepareTelescopeAim(in TelescopeAim) (preparedTelescope, error) {
	invalid := func() (preparedTelescope, error) { return preparedTelescope{}, commandError(CommandInvalid) }
	f, n, r := in.Ship.Expected, in.Ship.Proposed, in.Context.Race
	if f == nil || n == nil || r == nil || in.Player < 1 || in.Player > model.MaxPlayers || in.Governor < 0 || in.Governor > model.MaxGovernors || r.PlayerNum != in.Player || !r.Governor[in.Governor].Active || r.Dissolved || f.Number == 0 || !f.Alive || !f.Active || int(f.Owner) != in.Player || (in.Governor != 0 && int(f.Governor) != in.Governor) {
		return invalid()
	}
	if f.Type != model.OtypeStele && f.Type != model.OtypeGtele {
		return invalid()
	}
	if int64(f.MaxCrew)-int64(f.Troops) > 0 && f.Popn == 0 {
		return invalid()
	}
	c := telescopeContext{stars: map[uint8]*model.Star{}, planets: map[[2]uint8]*model.Planet{}, ships: map[uint32]*model.Ship{}, data: in.Context.StarData}
	var out preparedTelescope
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
	if c.data != nil {
		if int(c.data.NumStars) > model.NumStars {
			return invalid()
		}
		if e := add(BucketSData, starDataKey, c.data, c.data); e != nil {
			return invalid()
		}
	}
	if len(in.Context.Stars) > model.NumStars || len(in.Context.Planets) > model.NumStars*model.MaxPlanets {
		return invalid()
	}
	for _, s := range in.Context.Stars {
		if s == nil || c.stars[s.Number] != nil || int(s.NumPlanets) > model.MaxPlanets {
			return invalid()
		}
		c.stars[s.Number] = s
	}
	for _, p := range in.Context.Planets {
		if p == nil || p.MaxX == 0 || p.MaxY == 0 || len(p.Sectors) != int(p.MaxX)*int(p.MaxY) || c.planets[[2]uint8{p.StarNum, p.PlanetNum}] != nil {
			return invalid()
		}
		c.planets[[2]uint8{p.StarNum, p.PlanetNum}] = p
	}
	for _, s := range in.Context.Ships {
		if s == nil || s.Number == 0 || c.ships[s.Number] != nil {
			return invalid()
		}
		c.ships[s.Number] = s
	}
	if !reflect.DeepEqual(c.ships[f.Number], f) || !c.location(f.Number, map[uint32]bool{}) {
		return invalid()
	}
	var st *model.Star
	var pl *model.Planet
	if in.Star != nil {
		st = in.Star.Expected
		if st == nil || in.Star.Share || !reflect.DeepEqual(c.stars[st.Number], st) {
			return invalid()
		}
	}
	if in.Planet != nil {
		pl = in.Planet.Expected
		if pl == nil || !reflect.DeepEqual(c.planets[[2]uint8{pl.StarNum, pl.PlanetNum}], pl) {
			return invalid()
		}
	}
	if in.Target.Level == model.LevelShip {
		target := c.ships[in.Target.ShipNo]
		if target == nil || (!target.Alive && !r.God) || target.StOrbits != in.Target.StarNum || target.PNumOrbits != in.Target.PlanetNum {
			return invalid()
		}
		if target.Alive && !c.location(target.Number, map[uint32]bool{}) {
			return invalid()
		}
	}
	change, e := commandrules.PlanTelescope(f, in.Target, st, pl, in.ManeuverFuel, in.MassFuel)
	if e != nil {
		return invalid()
	}
	want := *f
	aim := change.Aim
	want.Special.AimedAt = &aim
	want.Fuel = change.Fuel
	want.Mass = change.Mass
	want.Notified = false
	if !reflect.DeepEqual(&want, n) {
		return invalid()
	}
	if st != nil {
		want := *st
		if change.Explore {
			want.SetExplored(in.Player)
		}
		if !reflect.DeepEqual(&want, in.Star.Proposed) {
			return invalid()
		}
	}
	if pl != nil {
		want := *pl
		if change.Explore {
			want.Info[in.Player-1].Explored = true
		}
		if !reflect.DeepEqual(&want, in.Planet.Proposed) {
			return invalid()
		}
	}
	in.Context.Stars = append([]*model.Star(nil), in.Context.Stars...)
	in.Context.Planets = append([]*model.Planet(nil), in.Context.Planets...)
	in.Context.Ships = append([]*model.Ship(nil), in.Context.Ships...)
	// Canonical witness ordering makes readback and fault evidence reproducible.
	sort.Slice(in.Context.Stars, func(i, j int) bool { return in.Context.Stars[i].Number < in.Context.Stars[j].Number })
	sort.Slice(in.Context.Planets, func(i, j int) bool {
		a, b := in.Context.Planets[i], in.Context.Planets[j]
		return int(a.StarNum)*model.MaxPlanets+int(a.PlanetNum) < int(b.StarNum)*model.MaxPlanets+int(b.PlanetNum)
	})
	sort.Slice(in.Context.Ships, func(i, j int) bool { return in.Context.Ships[i].Number < in.Context.Ships[j].Number })
	for _, s := range in.Context.Stars {
		next := s
		if st != nil && s.Number == st.Number {
			next = in.Star.Proposed
		}
		if e := add(BucketStars, StarKey(s.Number), s, next); e != nil {
			return invalid()
		}
	}
	for _, p := range in.Context.Planets {
		next := p
		if pl != nil && p.StarNum == pl.StarNum && p.PlanetNum == pl.PlanetNum {
			next = in.Planet.Proposed
		}
		if e := add(BucketPlanets, PlanetKey(p.StarNum, p.PlanetNum), p, next); e != nil {
			return invalid()
		}
	}
	for _, s := range in.Context.Ships {
		next := s
		if s.Number == f.Number {
			next = n
		}
		if e := add(BucketShips, ShipKey(s.Number), s, next); e != nil {
			return invalid()
		}
	}
	find := func(bucket, key []byte) (commandRecord, bool) {
		for _, r := range out.checks {
			if string(r.bucket) == string(bucket) && string(r.key) == string(key) {
				return r, true
			}
		}
		return commandRecord{}, false
	}
	shipRec, ok := find(BucketShips, ShipKey(f.Number))
	if !ok {
		return invalid()
	}
	out.writes = append(out.writes, shipRec)
	if st != nil {
		r, ok := find(BucketStars, StarKey(st.Number))
		if !ok {
			return invalid()
		}
		out.writes = append(out.writes, r)
	}
	if pl != nil {
		r, ok := find(BucketPlanets, PlanetKey(pl.StarNum, pl.PlanetNum))
		if !ok {
			return invalid()
		}
		out.writes = append(out.writes, r)
	}
	return out, nil
}

// CommitTelescopeAim commits only the prepared telescope records in one write
// transaction. Fault phases and conservative outcomes match factory/dump.
func (s *Store) CommitTelescopeAim(in TelescopeAim) (outcome CommandOutcome, err error) {
	outcome = CommandNotCommitted
	if e := s.writeAllowed("CommitTelescopeAim"); e != nil {
		return outcome, commandError(CommandFenced)
	}
	s.commandMu.Lock()
	defer s.commandMu.Unlock()
	if s.CommandWriteUncertain() {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain, Readback: "unavailable"}
	}
	phase := 0
	var prepared preparedTelescope
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
	prepared, e = prepareTelescopeAim(in)
	if e != nil {
		return outcome, e
	}
	if e = s.metaFault("telescope.before"); e != nil {
		return outcome, commandError(CommandIO)
	}
	e = s.db.Update(func(tx *bolt.Tx) error {
		phase = 1
		if e := s.writeAllowed("CommitTelescopeAim"); e != nil {
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
		if e := s.metaFault("telescope.validated"); e != nil {
			return commandError(CommandIO)
		}
		for i, r := range prepared.writes {
			if e := tx.Bucket(r.bucket).Put(r.key, r.after); e != nil {
				return commandError(CommandIO)
			}
			stage := fmt.Sprintf("telescope.write:%d", i+1)
			if e := s.metaFault(stage); e != nil {
				return commandError(CommandIO)
			}
		}
		if e := s.metaFault("telescope.before-commit"); e != nil {
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
	if e = s.metaFault("telescope.commit-error"); e != nil {
		return CommandUncertain, &CommandError{Code: CommandIO, Outcome: CommandUncertain}
	}
	phase = 3
	outcome = CommandCommitted
	if e = s.metaFault("telescope.after-commit"); e != nil {
		return outcome, &CommandError{Code: CommandIO, Outcome: outcome}
	}
	return outcome, nil
}
