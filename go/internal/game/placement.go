package game

import (
	"fmt"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/racegen"
)

// PlacementUniverse borrows a consistent, read-only world snapshot. The caller
// must exclude concurrent world mutation for the duration of ProposePlacement.
// No store, State method, or RNG is consulted. Stars are indexed by star number;
// Planets use the same star/planet keys as State. Races may have trailing nils.
type PlacementUniverse struct {
	Stars   []*model.Star
	Planets map[uint8]map[uint8]*model.Planet
	Races   []*model.Race
	Ships   map[uint32]*model.Ship
}

// PlacementOrder contains full permutations, never prefixes or multisets.
// Stars permutes [0,len(world.Stars)). Sectors permutes the selected homeworld's
// row-major indices [0,MaxX*MaxY). It is checked after selecting that homeworld;
// if there is no homeworld it is unused. No order is sorted or mutated here.
type PlacementOrder struct {
	Stars   []int
	Sectors []int
}

// PlacementIdentity contains opaque credentials already validated by a trusted
// future workflow. Strings are copied verbatim, never hashed, verified, logged,
// or echoed in errors. Nonempty values are required; that is NOT proof they are
// secure credentials. God/Guest authorization and God-first policy belong to
// the caller, not to a species design or a public placement API.
type PlacementIdentity struct {
	RaceCredential     string
	GovernorCredential string
	God                bool
	Guest              bool
}

// Placement owns independent proposals. Only Planet.Sectors needs a deep copy;
// all other fields in these newly constructed/copied models are value fields.
// Existing ships are not modified: Ship.NextShip retains the old planet head.
// Nothing is reserved, persisted, installed in State, or made playable here.
type Placement struct {
	Race     *model.Race
	Planet   *model.Planet
	Star     *model.Star
	Ship     *model.Ship
	CapitalX uint8
	CapitalY uint8
}

type PlacementErrorCode string

const (
	PlacementInvalidSpec       PlacementErrorCode = "INVALID_SPEC"
	PlacementInvalidPlayer     PlacementErrorCode = "INVALID_PLAYER"
	PlacementPlayerInUse       PlacementErrorCode = "PLAYER_IN_USE"
	PlacementInvalidShip       PlacementErrorCode = "INVALID_SHIP"
	PlacementShipInUse         PlacementErrorCode = "SHIP_IN_USE"
	PlacementInvalidIdentity   PlacementErrorCode = "INVALID_IDENTITY"
	PlacementInvalidOrder      PlacementErrorCode = "INVALID_SEARCH_ORDER"
	PlacementInvalidUniverse   PlacementErrorCode = "INVALID_UNIVERSE"
	PlacementInvalidOrbitChain PlacementErrorCode = "INVALID_ORBIT_CHAIN"
	PlacementNoHomePlanet      PlacementErrorCode = "NO_HOME_PLANET"
	PlacementNoCapitalSector   PlacementErrorCode = "NO_CAPITAL_SECTOR"
)

type PlacementError struct {
	Code   PlacementErrorCode
	Field  string
	Reason string
}

func (e *PlacementError) Error() string {
	return fmt.Sprintf("%s (%s): %s", e.Code, e.Field, e.Reason)
}

func placementError(code PlacementErrorCode, field, reason string) error {
	return &PlacementError{Code: code, Field: field, Reason: reason}
}

// ProposePlacement reproduces source/GB_racegen.c:107-365 on private copies.
// Spec is revalidated before conversions. Identifiers are representationally
// valid (players 1..64, nonzero uint32 ships); allocation, cap 63 vs 64, name
// uniqueness, privileges and credential policy remain caller responsibilities.
// Every failure returns a nil proposal and leaves all inputs untouched.
func ProposePlacement(spec racegen.Spec, player int, shipNumber uint32, world PlacementUniverse, order PlacementOrder, identity PlacementIdentity) (*Placement, error) {
	if len(racegen.Validate(spec)) != 0 {
		return nil, placementError(PlacementInvalidSpec, "spec", "a validated species design is required")
	}
	if player < 1 || player > model.MaxPlayers {
		return nil, placementError(PlacementInvalidPlayer, "player", "player number is outside the model range")
	}
	if len(world.Races) > model.MaxPlayers {
		return nil, placementError(PlacementInvalidUniverse, "races", "race slice exceeds the model range")
	}
	if player <= len(world.Races) && world.Races[player-1] != nil {
		return nil, placementError(PlacementPlayerInUse, "player", "race slot is occupied")
	}
	if shipNumber == 0 {
		return nil, placementError(PlacementInvalidShip, "ship", "ship number must be nonzero")
	}
	if _, exists := world.Ships[shipNumber]; exists {
		return nil, placementError(PlacementShipInUse, "ship", "ship slot is occupied")
	}
	if identity.RaceCredential == "" || identity.GovernorCredential == "" || (identity.God && identity.Guest) {
		return nil, placementError(PlacementInvalidIdentity, "identity", "validated credentials and consistent privileges are required")
	}
	if len(world.Stars) > model.NumStars {
		return nil, placementError(PlacementInvalidUniverse, "stars", "star slice exceeds the model range")
	}
	if err := placementPermutation(order.Stars, len(world.Stars), "stars"); err != nil {
		return nil, err
	}
	// Validate the entire order and indexed star layout before accepting a hit.
	for i, star := range world.Stars {
		if star == nil || int(star.Number) != i || int(star.NumPlanets) > model.MaxPlanets {
			return nil, placementError(PlacementInvalidUniverse, "stars", "missing star or inconsistent star identity/count")
		}
	}
	star, planet, err := placementHome(spec, world, order.Stars)
	if err != nil {
		return nil, err
	}
	if err := placementPermutation(order.Sectors, len(planet.Sectors), "sectors"); err != nil {
		return nil, err
	}
	if err := placementOrbitChain(world, planet); err != nil {
		return nil, err
	}
	if !placementFinite(star.XPos+planet.XPos) || !placementFinite(star.YPos+planet.YPos) {
		return nil, placementError(PlacementInvalidUniverse, "position", "homeworld position must be finite")
	}
	race := placementRace(spec, player, shipNumber, star, planet, identity)
	capital := -1
	for _, i := range order.Sectors {
		sec := planet.Sectors[i]
		if sec.Condition == race.LikesBest && sec.Owner == 0 && sec.Popn == 0 && sec.Troops == 0 {
			capital = i
			break
		}
	}
	if capital < 0 {
		return nil, placementError(PlacementNoCapitalSector, "sectors", "no unoccupied sector has the required terrain")
	}
	newPlanet := *planet
	newPlanet.Sectors = append([]model.Sector(nil), planet.Sectors...)
	sec := &newPlanet.Sectors[capital]
	sec.Owner, sec.Race = uint8(player), uint8(player) //nolint:gosec // player range checked above
	sec.Popn = uint16(race.NumberSexes)
	sec.Fert, sec.Eff, sec.Troops = 100, 10, 0
	newPlanet.Popn, newPlanet.Troops = uint64(race.NumberSexes), 0
	newPlanet.Info[player-1].NumSectsOwned = 1
	newPlanet.Info[player-1].Explored = true
	newPlanet.Explored = false
	newPlanet.MaxPopn = uint64(maxSupport(race, sec, 100, 0)) * uint64(planet.MaxX) * uint64(planet.MaxY) / 2 //nolint:gosec // liked sector, eff 10/fert 100 => support 1100
	newStar := *star
	newStar.SetExplored(player)
	newStar.SetInhabited(player)
	newStar.AP[player-1] = 5
	x, y := capital%int(planet.MaxX), capital/int(planet.MaxX)
	ship := placementGovernmentShip(race, &newStar, &newPlanet, uint8(x), uint8(y)) //nolint:gosec // validated sector index within uint8 map dimensions
	newPlanet.Ships = shipNumber
	return &Placement{Race: race, Planet: &newPlanet, Star: &newStar, Ship: ship, CapitalX: uint8(x), CapitalY: uint8(y)}, nil //nolint:gosec // coordinates bounded by map dimensions
}

func placementPermutation(order []int, size int, field string) error {
	if len(order) != size {
		return placementError(PlacementInvalidOrder, field, "order must include every index exactly once")
	}
	seen := make([]bool, size)
	for _, i := range order {
		if i < 0 || i >= size || seen[i] {
			return placementError(PlacementInvalidOrder, field, "order contains a duplicate or out-of-range index")
		}
		seen[i] = true
	}
	return nil
}

func placementHome(spec racegen.Spec, world PlacementUniverse, stars []int) (*model.Star, *model.Planet, error) {
	target := racegen.Homeworlds()[spec.Homeworld].PlanetType
	for _, i := range stars {
		star := world.Stars[i]
		if star.NumPlanets < 2 || star.Inhabited != 0 {
			continue
		}
		for j := 0; j < int(star.NumPlanets); j++ {
			planet := world.Planets[star.Number][uint8(j)] //nolint:gosec // j bounded by MaxPlanets
			if planet == nil || planet.StarNum != star.Number || int(planet.PlanetNum) != j {
				return nil, nil, placementError(PlacementInvalidUniverse, "planets", "missing planet or inconsistent planet identity")
			}
			if planet.Type != target || planet.Conditions[model.AtmosRTemp] < -200 || planet.Conditions[model.AtmosRTemp] > 100 {
				continue
			}
			if planet.MaxX == 0 || planet.MaxY == 0 || int(planet.MaxX)*int(planet.MaxY) != len(planet.Sectors) {
				return nil, nil, placementError(PlacementInvalidUniverse, "sectors", "sector map dimensions are inconsistent")
			}
			occupied := false
			for _, info := range planet.Info {
				occupied = occupied || info.NumSectsOwned != 0
			}
			for _, sector := range planet.Sectors {
				occupied = occupied || sector.Owner != 0
			}
			if !occupied {
				return star, planet, nil
			}
		}
	}
	return nil, nil, placementError(PlacementNoHomePlanet, "homeworld", "no vacant homeworld satisfies the selection rules")
}

// Validate before prepending: never break an existing chain or adopt a dangling,
// cyclic, wrong-location chain. Unlinked ships at this planet are also refused.
func placementOrbitChain(world PlacementUniverse, planet *model.Planet) error {
	seen := make(map[uint32]bool)
	for n := planet.Ships; n != 0; {
		ship := world.Ships[n]
		if seen[n] || ship == nil || !ship.Alive || ship.Number != n || ship.WhatOrbits != model.LevelPlan || ship.StOrbits != planet.StarNum || ship.PNumOrbits != planet.PlanetNum {
			return placementError(PlacementInvalidOrbitChain, "ships", "planet ship chain is malformed")
		}
		seen[n] = true
		n = ship.NextShip
	}
	for _, ship := range world.Ships {
		if ship != nil && ship.Alive && ship.WhatOrbits == model.LevelPlan && ship.StOrbits == planet.StarNum && ship.PNumOrbits == planet.PlanetNum && !seen[ship.Number] {
			return placementError(PlacementInvalidOrbitChain, "ships", "planet has an unlinked ship")
		}
	}
	return nil
}

func placementRace(spec racegen.Spec, player int, shipNumber uint32, star *model.Star, planet *model.Planet, identity PlacementIdentity) *model.Race {
	a := spec.Attributes
	race := &model.Race{
		PlayerNum: player, Name: spec.Name, Info: spec.Info, Password: identity.RaceCredential,
		God: identity.God, Guest: identity.Guest, Metamorph: spec.RaceType == racegen.Metamorph,
		Absorb: a.Absorb == 1, Pods: a.Pods == 1, CollectiveIQ: a.CollectiveIQ == 1,
		Fighters: uint8(a.Fighters), NumberSexes: uint8(a.Sexes), Fertilize: uint8(a.Fertilize * 100), //nolint:gosec // validated CR-07A ranges and integral fields
		Adventurism: a.Adventurism, BirthRate: a.Birthrate, Mass: a.Mass, Metabolism: a.Metabolism,
		GovShip: shipNumber,
	}
	if race.CollectiveIQ {
		race.IQLimit = uint8(a.IQ) //nolint:gosec // validated integer 50..220
	} else {
		race.IQ = uint8(a.IQ) //nolint:gosec // validated integer 50..220
	}
	for i := model.AtmosRTemp; i <= model.AtmosOther; i++ {
		race.Conditions[i] = planet.Conditions[i]
	}
	for i := range race.Translate {
		race.Translate[i] = 1
		if i == player-1 || player == 1 || identity.God {
			race.Translate[i] = 100
		}
	}
	for i, compat := range spec.SectorCompat {
		race.Likes[i] = compat / 100
		if compat == 100 && racegen.Homeworlds()[spec.Homeworld].CommonSectors[i] {
			race.LikesBest = uint8(i) //nolint:gosec // index in eight sector types
		}
	}
	race.Governor[0] = model.Governor{
		Password: identity.GovernorCredential, Active: true,
		HomeLevel: model.LevelPlan, DefLevel: model.LevelPlan,
		HomeSystem: star.Number, DefSystem: star.Number,
		HomePlanetNum: planet.PlanetNum, DefPlanetNum: planet.PlanetNum,
		Toggle: model.Toggle{Highlight: uint8(player), Inverse: true}, //nolint:gosec // validated 1..64
	}
	return race
}

// Enrollment's special government ship differs from generic build helpers:
// fixed size/base mass/cost, no cargo even for God, on=true, damage=retaliate=0.
// initializeNewShip also emits notices; no State helper is invoked here.
func placementGovernmentShip(race *model.Race, star *model.Star, planet *model.Planet, x, y uint8) *model.Ship {
	sd := model.Shipdata[model.OtypeGov]
	return &model.Ship{
		Number: race.GovShip, Type: model.OtypeGov, BuildType: uint16(model.OtypeGov),
		Owner: uint8(race.PlayerNum), Race: uint8(race.PlayerNum), //nolint:gosec // validated player number
		Class: "Standard", Tech: 100, Size: 100, BaseMass: 100,
		Mass:  100 + float64(sd[model.AbilMaxCrew])*race.Mass,
		Armor: uint8(sd[model.AbilArmor]), Guns: model.GunPrimary, //nolint:gosec // static government table
		Primary: uint8(sd[model.AbilGuns]), Secondary: uint8(sd[model.AbilGuns]), //nolint:gosec // static government table
		PrimType: uint8(sd[model.AbilPrimary]), SecType: uint8(sd[model.AbilSecondary]), //nolint:gosec // static government table
		MaxCrew: uint16(sd[model.AbilMaxCrew]), Popn: uint16(sd[model.AbilMaxCrew]), //nolint:gosec // static government table
		MaxResource: uint16(sd[model.AbilCargo]), MaxFuel: uint16(sd[model.AbilFuelCap]), //nolint:gosec // static government table
		MaxDestruct: uint16(sd[model.AbilDestCap]), MaxSpeed: uint16(sd[model.AbilSpeed]), //nolint:gosec // static government table
		BuildCost: uint16(sd[model.AbilCost]), //nolint:gosec // static government table
		XPos:      star.XPos + planet.XPos, YPos: star.YPos + planet.YPos, LandX: x, LandY: y,
		WhatOrbits: model.LevelPlan, WhatDest: model.LevelPlan,
		StOrbits: star.Number, DestStar: star.Number, PNumOrbits: planet.PlanetNum, DestPNum: planet.PlanetNum,
		Alive: true, Active: true, On: true, Docked: true, Protect: model.ProtectState{Self: true},
		NextShip: planet.Ships,
	}
}

func placementFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
