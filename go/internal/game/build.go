package game

// build.go ports build.c: ship construction (build, upgrade, make, mod),
// ship cost/complexity/mass calculations, and the commodity market (sell, bid).
//
// C functions ported:
//   build()              → cmdBuild()
//   upgrade()            → cmdUpgrade()
//   make_mod(mode=0)     → cmdMake()
//   make_mod(mode=1)     → cmdMod()
//   sell()               → cmdSell()
//   bid()                → cmdBid()
//   cost()               → shipCostFromDesign()
//   complexity()         → complexityOf()
//   system_cost()        → systemCost()
//   Getship()            → getshipDefaults()
//   Getfactship()        → getfactshipDefaults()
//   Shipcost()           → shipcostForType()   (replaces planet.go stub)
//   can_build_at_planet()
//   get_build_type()
//   can_build_this()
//   can_build_on_ship()
//   can_build_on_sector()
//   build_at_ship()
//   autoload_at_planet()
//   autoload_at_ship()
//   initialize_new_ship() → initializeNewShip()
//   create_ship_by_planet()
//   create_ship_by_ship()
//   shipping_cost()

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// --- Cost constants (from tweakables.h) ---

const (
	gunCost     = 1.00
	crewCost    = 0.05
	cargoCost   = 0.05
	fuelCostBld = 0.05 // building fuel cost per unit
	ammoCost    = 0.05
	speedCost   = 0.50
	cewCost     = 0.003
	armorCost   = 3.50
	hangerCost  = 0.50

	// upBid is now loaded from config.UpBid (was hardcoded to 0.10)
	merchantLength = 200000.0
)

// --- Ship cost/complexity computations ---

// shipCostFromDesign computes the resource cost to build a ship from its
// design parameters. Mirrors cost() in build.c.
func (s *State) shipCostFromDesign(ship *model.Ship) float64 {
	i := int(ship.BuildType)
	if i < 0 || i >= model.NumShipTypes {
		i = int(ship.Type)
	}
	factor := float64(model.Shipdata[model.ShipType(i)][model.AbilCost])
	factor += gunCost * float64(ship.Primary)
	factor += gunCost * float64(ship.Secondary)
	factor += crewCost * float64(ship.MaxCrew)
	factor += cargoCost * float64(ship.MaxResource)
	factor += fuelCostBld * float64(ship.MaxFuel)
	factor += ammoCost * float64(ship.MaxDestruct)
	factor += speedCost * float64(ship.MaxSpeed) * math.Sqrt(float64(ship.MaxSpeed))
	factor += hangerCost * float64(ship.MaxHanger)
	factor += armorCost * float64(ship.Armor) * math.Sqrt(float64(ship.Armor))
	factor += cewCost * float64(int(ship.CEW)*int(ship.CEWRange))

	advantage := 0.0
	if ship.HyperDrive.Has {
		advantage += 0.5
	}
	if ship.Laser != 0 {
		advantage += 0.5
	}
	if ship.Cloak != 0 {
		advantage += 0.5
	}
	if ship.Mount != 0 {
		advantage += 0.5
	}
	factor *= math.Sqrt(1.0 + advantage)
	return factor
}

// systemCost computes the fractional advantage/disadvantage for one ship
// characteristic relative to its design base. Mirrors system_cost() in build.c.
func systemCost(advantage, disadvantage *float64, value, base int) {
	factor := (float64(value)+1.0)/(float64(base)+1.0) - 1.0
	if factor >= 0.0 {
		*advantage += factor
	} else {
		*disadvantage -= factor
	}
}

// complexityOf computes the engineering complexity of a ship design.
// Mirrors complexity() in build.c.
func complexityOf(ship *model.Ship) float64 {
	i := int(ship.BuildType)
	if i < 0 || i >= model.NumShipTypes {
		i = int(ship.Type)
	}
	sd := model.Shipdata[model.ShipType(i)]

	advantage := 0.0
	disadvantage := 0.0

	systemCost(&advantage, &disadvantage, int(ship.Primary), int(sd[model.AbilGuns]))
	systemCost(&advantage, &disadvantage, int(ship.Secondary), int(sd[model.AbilGuns]))
	systemCost(&advantage, &disadvantage, int(ship.MaxCrew), int(sd[model.AbilMaxCrew]))
	systemCost(&advantage, &disadvantage, int(ship.MaxResource), int(sd[model.AbilCargo]))
	systemCost(&advantage, &disadvantage, int(ship.MaxFuel), int(sd[model.AbilFuelCap]))
	systemCost(&advantage, &disadvantage, int(ship.MaxDestruct), int(sd[model.AbilDestCap]))
	systemCost(&advantage, &disadvantage, int(ship.MaxSpeed), int(sd[model.AbilSpeed]))
	systemCost(&advantage, &disadvantage, int(ship.MaxHanger), int(sd[model.AbilHanger]))
	systemCost(&advantage, &disadvantage, int(ship.Armor), int(sd[model.AbilArmor]))

	factor := math.Sqrt((1.0 + advantage) * math.Exp(-disadvantage/10.0))
	tech := float64(sd[model.AbilTech])
	// COMPLEXITY_FACTOR = 10.0 from tweakables.h
	temp := 10.0*(factor-1.0)/math.Sqrt(tech+1.0) + 1.0
	factor = temp * temp
	return factor * tech
}

// getshipDefaults populates a new ship with default stats for its type.
// Mirrors Getship() in build.c.
func (s *State) getshipDefaults(ship *model.Ship, stype model.ShipType, race *model.Race) {
	*ship = model.Ship{}
	ship.Type = stype
	sd := model.Shipdata[stype]
	ship.Armor = uint8(sd[model.AbilArmor])
	if sd[model.AbilPrimary] > 0 {
		ship.Guns = model.GunPrimary
	}
	ship.Primary = uint8(sd[model.AbilGuns])
	ship.PrimType = uint8(sd[model.AbilPrimary])
	ship.Secondary = uint8(sd[model.AbilGuns])
	ship.SecType = uint8(sd[model.AbilSecondary])
	ship.MaxCrew = uint16(sd[model.AbilMaxCrew])
	ship.MaxResource = uint16(sd[model.AbilCargo])
	ship.MaxHanger = uint16(sd[model.AbilHanger])
	ship.MaxDestruct = uint16(sd[model.AbilDestCap])
	ship.MaxFuel = uint16(sd[model.AbilFuelCap])
	ship.MaxSpeed = uint16(sd[model.AbilSpeed])
	ship.BuildType = uint16(stype)
	if race.God {
		ship.Mount = uint8(sd[model.AbilMount])
		if sd[model.AbilJump] != 0 {
			ship.HyperDrive.Has = true
		}
		if sd[model.AbilLaser] != 0 {
			ship.Laser = 1
		}
	}
	ship.Size = uint16(s.shipSize(ship))
	ship.BaseMass = s.getMass(ship)
	ship.Mass = ship.BaseMass
	if race.God {
		ship.BuildCost = 0
	} else {
		ship.BuildCost = uint16(s.shipCostFromDesign(ship))
	}
	if stype == model.OtypeVN || stype == model.OtypeBers {
		ship.Special.Mind = &model.MindSpecial{
			Progenitor: uint8(race.PlayerNum),
		}
	}
}

// getfactshipDefaults populates a new ship template from a factory's design.
// Mirrors Getfactship() in build.c.
func (s *State) getfactshipDefaults(newship, factory *model.Ship) {
	*newship = model.Ship{}
	newship.Type = model.ShipType(factory.BuildType)
	newship.Armor = factory.Armor
	newship.Primary = factory.Primary
	newship.PrimType = factory.PrimType
	newship.Secondary = factory.Secondary
	newship.SecType = factory.SecType
	if factory.Primary > 0 {
		newship.Guns = model.GunPrimary
	}
	newship.MaxCrew = factory.MaxCrew
	newship.MaxResource = factory.MaxResource
	newship.MaxHanger = factory.MaxHanger
	newship.MaxDestruct = factory.MaxDestruct
	newship.MaxFuel = factory.MaxFuel
	newship.MaxSpeed = factory.MaxSpeed
	newship.BuildType = factory.BuildType
	newship.BuildCost = factory.BuildCost
	newship.Mount = factory.Mount
	newship.HyperDrive.Has = factory.HyperDrive.Has
	newship.Laser = factory.Laser
	newship.CEW = factory.CEW
	newship.CEWRange = factory.CEWRange
	newship.Size = uint16(s.shipSize(newship))
	newship.BaseMass = s.getMass(newship)
	newship.Mass = newship.BaseMass
}

// shipcostForType returns the default build cost for a ship type.
// Replaces the stub shipCost() in planet.go.
// Mirrors Shipcost() in build.c.
func (s *State) shipcostForType(stype model.ShipType, race *model.Race) int {
	var tmp model.Ship
	s.getshipDefaults(&tmp, stype, race)
	return int(s.shipCostFromDesign(&tmp))
}

// --- Validation helpers ---

// testShip checks that a ship exists, is alive, and is owned by the player.
// Mirrors testship() in getplace.c. Returns an error string or "".
func (s *State) testShip(playerNum, governor int, ship *model.Ship) string {
	if !ship.Alive {
		return fmt.Sprintf("%c%d has been destroyed.\n",
			model.ShipLetters[ship.Type], ship.Number)
	}
	if int(ship.Owner) != playerNum {
		return fmt.Sprintf("You don't own ship #%d.\n", ship.Number)
	}
	if !ship.Active {
		return fmt.Sprintf("%c%d is irradiated %d%% and inactive.\n",
			model.ShipLetters[ship.Type], ship.Number, ship.Rad)
	}
	return ""
}

// canBuildAtPlanet checks system/enslavement restrictions for planet building.
// Mirrors can_build_at_planet() in build.c.
func (s *State) canBuildAtPlanet(playerNum, governor int, starNum int, planet *model.Planet, msg *string) bool {
	if planet.SlavedTo != 0 && int(planet.SlavedTo) != playerNum {
		*msg = fmt.Sprintf("This planet is enslaved by player %d.\n", planet.SlavedTo)
		return false
	}
	star := s.stars[starNum]
	if star != nil && governor != 0 && int(star.Governor[playerNum-1]) != governor {
		*msg = "You are not authorized in this system.\n"
		return false
	}
	return true
}

// getBuildType returns the ShipType for a type letter, or -1 if invalid.
// Mirrors get_build_type() in build.c.
func getBuildType(letter byte) model.ShipType {
	for i := model.ShipType(0); i < model.NumShipTypes; i++ {
		if model.ShipLetters[i] == letter {
			return i
		}
	}
	return model.NumShipTypes // sentinel "not found"
}

// canBuildThis checks race-level restrictions on building a ship type.
// Mirrors can_build_this() in build.c. Returns false and fills msg on failure.
func canBuildThis(what model.ShipType, race *model.Race, msg *string) bool {
	if what == model.StypePod && !race.Pods {
		*msg = "Only Metamorphic races can build Spore Pods.\n"
		return false
	}
	if model.Shipdata[what][model.AbilProgrammd] == 0 {
		*msg = "This ship type has not been programmed.\n"
		return false
	}
	if model.Shipdata[what][model.AbilGod] != 0 && !race.God {
		*msg = "Only Gods can build this type of ship.\n"
		return false
	}
	if what == model.OtypeVN && !race.HasDiscovery(model.DiscVN) {
		*msg = "You have not discovered VN technology.\n"
		return false
	}
	if what == model.OtypeTransDev && !race.HasDiscovery(model.DiscAVPM) {
		*msg = "You have not discovered AVPM technology.\n"
		return false
	}
	if float64(model.Shipdata[what][model.AbilTech]) > race.Tech && !race.God {
		*msg = fmt.Sprintf("You are not advanced enough to build this ship.\n%.1f engineering technology needed. You have %.1f.\n",
			float64(model.Shipdata[what][model.AbilTech]), race.Tech)
		return false
	}
	return true
}

// canBuildOnShip checks whether a builder ship can construct a given type.
// Mirrors can_build_on_ship() in build.c.
func canBuildOnShip(what model.ShipType, race *model.Race, builder *model.Ship, msg *string) bool {
	if race.God {
		return true
	}
	buildAbil := model.Shipdata[what][model.AbilBuild]
	constructAbil := model.Shipdata[builder.Type][model.AbilConstruct]
	if (buildAbil & constructAbil) == 0 {
		*msg = fmt.Sprintf("This ship type cannot be built by a %s.\n", model.ShipNames[builder.Type])
		return false
	}
	return true
}

// canBuildOnSector checks whether a sector is a valid build site.
// Mirrors can_build_on_sector() in build.c.
func (s *State) canBuildOnSector(what model.ShipType, race *model.Race, planet *model.Planet,
	sec *model.Sector, x, y int, msg *string) bool {
	if sec.Popn == 0 {
		*msg = "You have no more civs in the sector!\n"
		return false
	}
	if sec.Condition == model.SectorWasted {
		*msg = "You can't build on wasted sectors.\n"
		return false
	}
	if int(sec.Owner) != race.PlayerNum && !race.God {
		*msg = "You don't own that sector.\n"
		return false
	}
	if (model.Shipdata[what][model.AbilBuild]&1) == 0 && !race.God {
		*msg = "This ship type cannot be built on a planet.\n"
		return false
	}
	if what == model.OtypeQuarry {
		// Only one quarry per sector
		sh := planet.Ships
		for sh != 0 {
			existing := s.ships[sh]
			if existing == nil {
				break
			}
			if existing.Alive && existing.Type == model.OtypeQuarry &&
				int(existing.LandX) == x && int(existing.LandY) == y {
				*msg = "There already is a quarry here.\n"
				return false
			}
			sh = existing.NextShip
		}
	}
	return true
}

// buildAtShip validates that the builder ship can build, and fills in
// starNum/planetNum. Mirrors build_at_ship() in build.c.
// Returns the orbit level of the builder, or -1 on error.
func (s *State) buildAtShip(playerNum, governor int, race *model.Race, builder *model.Ship,
	starNum, planetNum *int) int {
	if msg := s.testShip(playerNum, governor, builder); msg != "" {
		return -1
	}
	if model.Shipdata[builder.Type][model.AbilConstruct] == 0 {
		return -1
	}
	if builder.Popn == 0 {
		return -1
	}
	if landed(builder) {
		// OK for factories; not for others
	}
	if builder.Docked && builder.Type != model.OtypeFactory {
		return -1
	}
	if builder.Damage > 0 {
		return -1
	}
	if builder.Type == model.OtypeFactory && !builder.On {
		return -1
	}
	if builder.Type == model.OtypeFactory && !landed(builder) {
		return -1
	}
	*starNum = int(builder.StOrbits)
	*planetNum = int(builder.PNumOrbits)
	return int(builder.WhatOrbits)
}

// autoloadAtPlanet sets initial crew/fuel from the landing sector/planet.
// Mirrors autoload_at_planet() in build.c.
func autoloadAtPlanet(playerNum int, newship *model.Ship, planet *model.Planet,
	sec *model.Sector, crew *int, fuel *float64) {
	idx := playerNum - 1
	*crew = int(newship.MaxCrew)
	if int(sec.Popn) < *crew {
		*crew = int(sec.Popn)
	}
	maxFuelF := float64(newship.MaxFuel)
	availFuel := float64(planet.Info[idx].Fuel)
	*fuel = maxFuelF
	if availFuel < *fuel {
		*fuel = availFuel
	}
	sec.Popn -= uint16(*crew)
	if sec.Popn == 0 && sec.Troops == 0 {
		sec.Owner = 0
	}
	planet.Info[idx].Fuel -= uint16(*fuel)
}

// autoloadAtShip sets initial crew/fuel from the builder ship.
// Mirrors autoload_at_ship() in build.c.
func autoloadAtShip(playerNum int, newship, builder *model.Ship, crew *int, fuel *float64) {
	*crew = int(newship.MaxCrew)
	if int(builder.Popn) < *crew {
		*crew = int(builder.Popn)
	}
	maxFuelF := float64(newship.MaxFuel)
	*fuel = maxFuelF
	if float64(builder.Fuel) < *fuel {
		*fuel = builder.Fuel
	}
	builder.Popn -= uint16(*crew)
	builder.Fuel -= *fuel
}

// initializeNewShip sets up the live state of a freshly-built ship.
// Mirrors initialize_new_ship() in build.c.
func (s *State) initializeNewShip(playerNum, governor int, race *model.Race,
	newship *model.Ship, loadFuel float64, loadCrew int) {
	newship.Speed = uint8(newship.MaxSpeed)
	newship.Owner = uint8(playerNum)
	newship.Governor = uint8(governor)
	if race.God {
		newship.Fuel = float64(newship.MaxFuel)
		newship.Popn = newship.MaxCrew
		newship.Resource = newship.MaxResource
		newship.Destruct = newship.MaxDestruct
	} else {
		newship.Fuel = loadFuel
		newship.Popn = uint16(loadCrew)
		newship.Resource = 0
		newship.Destruct = 0
	}
	newship.Crystals = 0
	newship.Hanger = 0
	newship.Mass = newship.BaseMass +
		float64(newship.Popn)*race.Mass +
		newship.Fuel*s.cfg.MassFuel +
		float64(newship.Resource)*s.cfg.MassResource +
		float64(newship.Destruct)*s.cfg.MassDestruct
	newship.Alive = true
	newship.Active = true
	newship.Protect.Self = newship.Guns != 0
	newship.HyperDrive.On = false
	newship.HyperDrive.Ready = false
	newship.HyperDrive.Charge = 0
	if race.God {
		newship.Mounted = newship.Mount != 0
	} else {
		newship.Mounted = false
	}
	newship.Cloaked = false
	newship.FireLaser = 0
	newship.Mode = false
	newship.Rad = 0
	if race.God {
		newship.Damage = 0
	} else {
		newship.Damage = uint8(model.Shipdata[newship.Type][model.AbilDamage])
	}
	newship.Retaliate = newship.Primary
	newship.Ships = 0
	newship.On = false

	switch newship.Type {
	case model.OtypeVN:
		newship.Special.Mind = &model.MindSpecial{
			Busy:       true,
			Progenitor: uint8(playerNum),
			Generation: 1,
		}
	case model.StypeMine:
		if newship.Special.Trigger == nil {
			newship.Special.Trigger = &model.TriggerSpecial{}
		}
		newship.Special.Trigger.Radius = 100
		s.notify(playerNum, governor, "Mine disarmed.\nTrigger radius set at 100.\n")
	case model.OtypeTransDev:
		if newship.Special.Transport == nil {
			newship.Special.Transport = &model.TransportSpecial{}
		}
		newship.Special.Transport.Target = 0
		newship.On = false
		s.notify(playerNum, governor, "Receive OFF.  Change with order.\n")
	case model.OtypeAP:
		s.notify(playerNum, governor, "Processor OFF.\n")
	case model.OtypeStele, model.OtypeGtele:
		s.notify(playerNum, governor, fmt.Sprintf("Telescope range is %.2f.\n",
			teleRange(newship.Type, newship.Tech)))
	}

	if newship.Damage > 0 {
		s.notify(playerNum, governor,
			fmt.Sprintf("Warning: This ship is constructed with a %d%% damage level.\n",
				newship.Damage))
		if model.Shipdata[newship.Type][model.AbilRepair] == 0 && newship.MaxCrew > 0 {
			s.notify(playerNum, governor,
				"It will need resources to become fully operational.\n")
		}
	}
	if model.Shipdata[newship.Type][model.AbilRepair] != 0 && newship.MaxCrew > 0 {
		s.notify(playerNum, governor,
			"This ship does not need resources to repair.\n")
	}
	if newship.Type == model.OtypeFactory {
		s.notify(playerNum, governor,
			"This factory may not begin repairs until it has been activated.\n")
	}
	if newship.MaxCrew == 0 {
		s.notify(playerNum, governor,
			"This ship is robotic, and may not repair itself.\n")
	}
	s.notify(playerNum, governor,
		fmt.Sprintf("Loaded with %d crew and %.1f fuel.\n", loadCrew, loadFuel))
}

// createShipByPlanet finalizes a newly-built ship landed on a planet.
// Mirrors create_ship_by_planet() in build.c.
func (s *State) createShipByPlanet(playerNum, governor int, race *model.Race,
	newship *model.Ship, planet *model.Planet,
	starNum, planetNum, x, y int) error {
	shipno, err := s.store.AllocShipNum()
	if err != nil {
		return err
	}

	star := s.stars[starNum]
	newship.Tech = race.Tech
	newship.XPos = star.XPos + planet.XPos
	newship.YPos = star.YPos + planet.YPos
	newship.LandX = uint8(x)
	newship.LandY = uint8(y)
	if newship.Type == model.OtypeTerra || newship.Type == model.OtypePlow {
		newship.Class = "5"
	} else {
		newship.Class = "Standard"
	}
	newship.WhatOrbits = model.LevelPlan
	newship.WhatDest = model.LevelPlan
	newship.DestStar = uint8(starNum)
	newship.DestPNum = uint8(planetNum)
	newship.StOrbits = uint8(starNum)
	newship.PNumOrbits = uint8(planetNum)
	newship.Docked = true
	planet.Info[playerNum-1].Resource -= newship.BuildCost
	newship.Number = shipno
	newship.Owner = uint8(playerNum)
	newship.Governor = uint8(governor)
	newship.Ships = 0
	s.insertShipPlan(planet, newship)
	s.ships[shipno] = newship

	if newship.Type == model.OtypeToxWC {
		toxicCond := int(planet.Conditions[model.AtmosToxic])
		s.notify(playerNum, governor,
			fmt.Sprintf("Toxin concentration on planet was %d%%,", toxicCond))
		toxic := toxicCond
		if toxic > s.cfg.ToxMax {
			toxic = s.cfg.ToxMax
		}
		if newship.Special.Waste == nil {
			newship.Special.Waste = &model.WasteSpecial{}
		}
		newship.Special.Waste.Toxic = uint8(toxic)
		planet.Conditions[model.AtmosToxic] -= int16(toxic)
		s.notify(playerNum, governor,
			fmt.Sprintf(" now %d%%.\n", planet.Conditions[model.AtmosToxic]))
	}

	s.notify(playerNum, governor,
		fmt.Sprintf("%c%d built at a cost of %d resources.\n",
			model.ShipLetters[newship.Type], newship.Number, newship.BuildCost))
	s.notify(playerNum, governor,
		fmt.Sprintf("Technology %.1f.\n", newship.Tech))
	s.notify(playerNum, governor,
		fmt.Sprintf("%c%d is on sector %d,%d.\n",
			model.ShipLetters[newship.Type], newship.Number, x, y))
	return nil
}

// createShipByShip finalizes a newly-built ship created by a builder ship.
// outside=true means build outside the builder (Shuttle/Cargo/Factory style);
// outside=false means store in the builder's hanger.
// Mirrors create_ship_by_ship() in build.c.
func (s *State) createShipByShip(playerNum, governor int, race *model.Race,
	outside bool, starNum int, planet *model.Planet,
	newship, builder *model.Ship) error {
	shipno, err := s.store.AllocShipNum()
	if err != nil {
		return err
	}

	newship.Number = shipno
	newship.Owner = uint8(playerNum)
	newship.Governor = uint8(governor)
	if outside {
		newship.WhatOrbits = builder.WhatOrbits
		newship.WhatDest = model.LevelUniv
		newship.DestStar = builder.DestStar
		newship.DestPNum = builder.DestPNum
		newship.StOrbits = builder.StOrbits
		newship.PNumOrbits = builder.PNumOrbits
		newship.Docked = false
		switch builder.WhatOrbits {
		case model.LevelPlan:
			if planet != nil {
				s.insertShipPlan(planet, newship)
			}
		case model.LevelStar:
			s.insertShipStar(int(builder.StOrbits), newship)
		case model.LevelUniv:
			s.insertShipUniv(newship)
		}
	} else {
		newship.WhatOrbits = model.LevelShip
		newship.WhatDest = model.LevelShip
		newship.DestStar = builder.DestStar
		newship.DestPNum = builder.DestPNum
		newship.DestShipNo = builder.Number
		newship.StOrbits = builder.StOrbits
		newship.PNumOrbits = builder.PNumOrbits
		newship.Docked = true
		s.insertShipInShip(newship, builder)
	}

	newship.Tech = race.Tech
	newship.XPos = builder.XPos
	newship.YPos = builder.YPos
	newship.LandX = builder.LandX
	newship.LandY = builder.LandY
	if newship.Type == model.OtypeTerra || newship.Type == model.OtypePlow {
		newship.Class = "5"
	} else {
		newship.Class = "Standard"
	}
	builder.Resource -= newship.BuildCost
	s.ships[shipno] = newship

	s.notify(playerNum, governor,
		fmt.Sprintf("%c%d built at a cost of %d resources.\n",
			model.ShipLetters[newship.Type], newship.Number, newship.BuildCost))
	s.notify(playerNum, governor,
		fmt.Sprintf("Technology %.1f.\n", newship.Tech))
	return nil
}

// --- Orbit list insertion helpers ---

// insertShipStar inserts a ship into a star's orbit chain.
func (s *State) insertShipStar(starNum int, ship *model.Ship) {
	star := s.stars[starNum]
	if star == nil {
		return
	}
	ship.NextShip = star.Ships
	star.Ships = ship.Number
}

// insertShipUniv inserts a ship into the universe orbit chain.
func (s *State) insertShipUniv(ship *model.Ship) {
	ship.NextShip = s.sdata.Ships
	s.sdata.Ships = ship.Number
}

// insertShipInShip docks a ship inside another ship's hanger.
func (s *State) insertShipInShip(newship, carrier *model.Ship) {
	newship.NextShip = carrier.Ships
	carrier.Ships = newship.Number
	carrier.Hanger += newship.Size
}

// --- AP helpers ---

// enoughAP checks if there are enough APs and notifies if not.
func (s *State) enoughAP(playerNum, governor int, ap uint8, needed int) bool {
	if int(ap) < needed {
		s.notify(playerNum, governor,
			fmt.Sprintf("You don't have %d action points there.\n", needed))
		return false
	}
	return true
}

// deductAPs deducts action points from a star or from sdata.
// sdata=true deducts from the universe-level AP pool.
func (s *State) deductAPs(playerNum, governor, n, starNum int, sdataLevel bool) {
	if n == 0 {
		return
	}
	if sdataLevel {
		if s.sdata == nil {
			return
		}
		if int(s.sdata.AP[playerNum-1]) >= n {
			s.sdata.AP[playerNum-1] -= uint8(n)
		} else {
			s.sdata.AP[playerNum-1] = 0
		}
	} else {
		star := s.stars[starNum]
		if star == nil {
			return
		}
		if int(star.AP[playerNum-1]) >= n {
			star.AP[playerNum-1] -= uint8(n)
		} else {
			star.AP[playerNum-1] = 0
			s.notify(playerNum, governor,
				"WHOA!  You cheater!  Oooohh!  OOOOH!\n  I'm tellllllllliiiiiiinnnnnnnnnggggggggg!!!!!!!\n")
		}
	}
}

// --- cmdBuild: main build command ---

// cmdBuild handles the "build" player command.
// Mirrors build() in build.c.
func (s *State) cmdBuild(p, g int, args []string, sess net.Responder) {
	race := s.raceByNum(p)
	if race == nil {
		sess.Send("Unknown player.\n")
		return
	}

	dir := s.dirs[p-1][g]
	level := int(dir.Level)

	// Information request: "build ?" or "build ? X"
	if len(args) > 0 && args[0] == "?" {
		if len(args) == 1 {
			// List all ship types
			sess.Send("     - Default ship parameters -\n")
			sess.Send(fmt.Sprintf("%1s %-15s %5s %5s %3s %4s %3s %3s %3s %4s %4s %2s %4s %4s\n",
				"?", "name", "cargo", "hang", "arm", "dest", "gun", "pri",
				"sec", "fuel", "crew", "sp", "tech", "cost"))
			for i := model.ShipType(0); i < model.NumShipTypes; i++ {
				sd := model.Shipdata[i]
				if sd[model.AbilGod] != 0 && !race.God {
					continue
				}
				if i == model.StypePod && !race.Pods {
					continue
				}
				if sd[model.AbilProgrammd] == 0 {
					continue
				}
				sess.Send(fmt.Sprintf("%c %-15.15s %5d %5d %3d %4d %3d %3d %3d %4d %4d %2d %4.0f %4d\n",
					model.ShipLetters[i], model.ShipNames[i],
					sd[model.AbilCargo], sd[model.AbilHanger],
					sd[model.AbilArmor], sd[model.AbilDestCap],
					sd[model.AbilGuns], sd[model.AbilPrimary],
					sd[model.AbilSecondary], sd[model.AbilFuelCap],
					sd[model.AbilMaxCrew], sd[model.AbilSpeed],
					float64(sd[model.AbilTech]),
					s.shipcostForType(i, race)))
			}
			return
		}
		// Describe specific type
		what := getBuildType(args[1][0])
		if what >= model.NumShipTypes {
			sess.Send("No such ship type.\n")
			return
		}
		if model.Shipdata[what][model.AbilProgrammd] == 0 {
			sess.Send("This ship type has not been programmed.\n")
			return
		}
		sd := model.Shipdata[what]
		sess.Send(fmt.Sprintf("\n%c %-15.15s %5d %5d %3d %4d %3d %3d %3d %4d %4d %2d %4.0f %4d\n",
			model.ShipLetters[what], model.ShipNames[what],
			sd[model.AbilCargo], sd[model.AbilHanger],
			sd[model.AbilArmor], sd[model.AbilDestCap],
			sd[model.AbilGuns], sd[model.AbilPrimary],
			sd[model.AbilSecondary], sd[model.AbilFuelCap],
			sd[model.AbilMaxCrew], sd[model.AbilSpeed],
			float64(sd[model.AbilTech]),
			s.shipcostForType(what, race)))
		if sd[model.AbilBuild]&1 != 0 {
			sess.Send("\nCan be constructed on planet.")
		}
		sess.Send("\n")
		return
	}

	if level != model.LevelShip && level != model.LevelPlan {
		sess.Send("You must change scope to a ship or planet to build.\n")
		return
	}

	starNum := int(dir.StarNum)
	planetNum := int(dir.PlanetNum)

	switch level {
	case model.LevelPlan:
		s.buildFromPlanet(p, g, race, starNum, planetNum, args, sess)
	case model.LevelShip:
		s.buildFromShip(p, g, race, args, sess)
	}
}

func (s *State) buildFromPlanet(playerNum, governor int, race *model.Race,
	starNum, planetNum int, args []string, sess net.Responder) {
	if len(args) < 1 {
		sess.Send("Build what?\n")
		return
	}
	what := getBuildType(args[0][0])
	if what >= model.NumShipTypes {
		sess.Send("No such ship type.\n")
		return
	}
	var errMsg string
	if !canBuildThis(what, race, &errMsg) && !race.God {
		sess.Send(errMsg)
		return
	}
	if (model.Shipdata[what][model.AbilBuild]&1) == 0 && !race.God {
		sess.Send("This ship cannot be built by a planet.\n")
		return
	}
	if len(args) < 2 {
		sess.Send("Build where?\n")
		return
	}

	planet := s.planets[uint8(starNum)][uint8(planetNum)]
	if planet == nil {
		sess.Send("No such planet.\n")
		return
	}

	if !s.canBuildAtPlanet(playerNum, governor, starNum, planet, &errMsg) && !race.God {
		sess.Send(errMsg)
		return
	}

	var x, y int
	fmt.Sscanf(args[1], "%d,%d", &x, &y)
	if x < 0 || x >= int(planet.MaxX) || y < 0 || y >= int(planet.MaxY) {
		sess.Send("Illegal sector.\n")
		return
	}
	sec := *planet.SectorAt(x, y)

	if !s.canBuildOnSector(what, race, planet, &sec, x, y, &errMsg) && !race.God {
		sess.Send(errMsg)
		return
	}

	count := 1
	if len(args) >= 3 {
		n, _ := strconv.Atoi(args[2])
		if n > 0 {
			count = n
		}
	}

	var newship model.Ship
	s.getshipDefaults(&newship, what, race)

	for i := 0; i < count; i++ {
		if int(newship.BuildCost) > int(planet.Info[playerNum-1].Resource) {
			sess.Send(fmt.Sprintf("You need %dr to construct this ship.\n", newship.BuildCost))
			break
		}
		built := newship // copy
		if err := s.createShipByPlanet(playerNum, governor, race, &built, planet, starNum, planetNum, x, y); err != nil {
			sess.Send(fmt.Sprintf("Build error: %v\n", err))
			break
		}
		var loadCrew int
		var loadFuel float64
		if race.Governor[governor].Toggle.AutoLoad && what != model.OtypeTransDev && !race.God {
			autoloadAtPlanet(playerNum, &built, planet, &sec, &loadCrew, &loadFuel)
		}
		s.initializeNewShip(playerNum, governor, race, &built, loadFuel, loadCrew)
		if err := s.store.PutShip(&built); err != nil {
			sess.Send(fmt.Sprintf("Error saving ship: %v\n", err))
		}
		// Update ship in memory
		s.ships[built.Number] = &built
	}
	// Save sector changes
	*planet.SectorAt(x, y) = sec
}

func (s *State) buildFromShip(playerNum, governor int, race *model.Race,
	args []string, sess net.Responder) {
	dir := s.dirs[playerNum-1][governor]
	builder := s.ships[dir.ShipNo]
	if builder == nil {
		sess.Send("No such ship.\n")
		return
	}

	var snum, pnum int
	buildLevel := s.buildAtShip(playerNum, governor, race, builder, &snum, &pnum)
	if buildLevel < 0 {
		switch {
		case !builder.Alive:
			sess.Send(fmt.Sprintf("%c%d has been destroyed.\n",
				model.ShipLetters[builder.Type], builder.Number))
		case model.Shipdata[builder.Type][model.AbilConstruct] == 0:
			sess.Send("This ship cannot construct other ships.\n")
		case builder.Popn == 0:
			sess.Send("This ship has no crew.\n")
		case builder.Damage > 0:
			sess.Send("This ship is damaged and cannot build.\n")
		case builder.Type == model.OtypeFactory && !builder.On:
			sess.Send("This factory is not online.\n")
		case builder.Type == model.OtypeFactory && !landed(builder):
			sess.Send("Factories must be landed on a planet.\n")
		default:
			sess.Send("You can't build here.\n")
		}
		return
	}

	outside := false
	var what model.ShipType
	var newship model.Ship
	var planet *model.Planet
	var sec model.Sector
	var x, y int
	count := 1

	switch builder.Type {
	case model.OtypeFactory:
		if len(args) >= 1 {
			n, _ := strconv.Atoi(args[0])
			if n > 0 {
				count = n
			} else {
				count = 1
			}
		}
		if buildLevel == model.LevelPlan {
			planet = s.planets[uint8(snum)][uint8(pnum)]
		}
		s.getfactshipDefaults(&newship, builder)
		outside = true
		what = model.ShipType(builder.BuildType)
	case model.StypeShuttle, model.StypeCargo:
		if landed(builder) {
			sess.Send("This ship cannot build when landed.\n")
			return
		}
		outside = true
		fallthrough
	default:
		if len(args) < 1 {
			sess.Send("Build what?\n")
			return
		}
		what = getBuildType(args[0][0])
		if what >= model.NumShipTypes {
			sess.Send("No such ship type.\n")
			return
		}
		var errMsg string
		if !canBuildOnShip(what, race, builder, &errMsg) {
			sess.Send(errMsg)
			return
		}
		if len(args) >= 2 {
			n, _ := strconv.Atoi(args[1])
			if n > 0 {
				count = n
			}
		}
		s.getshipDefaults(&newship, what, race)
	}

	tech := s.cfg.ComplexityFactor
	if builder.Type == model.OtypeFactory {
		tech = complexityOf(builder)
	} else {
		tech = float64(model.Shipdata[what][model.AbilTech])
	}
	if tech > race.Tech && !race.God {
		sess.Send(fmt.Sprintf("You are not advanced enough to build this ship.\n%.1f engineering technology needed. You have %.1f.\n",
			tech, race.Tech))
		return
	}

	if outside && buildLevel == model.LevelPlan && builder.Type == model.OtypeFactory {
		planet = s.planets[uint8(snum)][uint8(pnum)]
		if planet == nil {
			sess.Send("No planet found.\n")
			return
		}
		x = int(builder.LandX)
		y = int(builder.LandY)
		what = model.ShipType(builder.BuildType)
		sec = *planet.SectorAt(x, y)
		var errMsg string
		if !s.canBuildOnSector(what, race, planet, &sec, x, y, &errMsg) {
			sess.Send(errMsg)
			return
		}
	}

	for i := 0; i < count; i++ {
		switch builder.Type {
		case model.OtypeFactory:
			if int(newship.BuildCost) > int(planet.Info[playerNum-1].Resource) {
				sess.Send(fmt.Sprintf("You need %dr to construct this ship.\n", newship.BuildCost))
				goto done
			}
			built := newship
			if err := s.createShipByPlanet(playerNum, governor, race, &built, planet, snum, pnum, x, y); err != nil {
				sess.Send(fmt.Sprintf("Build error: %v\n", err))
				goto done
			}
			var loadCrew int
			var loadFuel float64
			if race.Governor[governor].Toggle.AutoLoad && what != model.OtypeTransDev && !race.God {
				autoloadAtPlanet(playerNum, &built, planet, &sec, &loadCrew, &loadFuel)
			}
			s.initializeNewShip(playerNum, governor, race, &built, loadFuel, loadCrew)
			if err := s.store.PutShip(&built); err != nil {
				sess.Send(fmt.Sprintf("Error saving: %v\n", err))
			}
			s.ships[built.Number] = &built

		case model.StypeShuttle, model.StypeCargo:
			if int(builder.Resource) < int(newship.BuildCost) {
				sess.Send(fmt.Sprintf("You need %dr to construct the ship.\n", newship.BuildCost))
				goto done
			}
			var pPtr *model.Planet
			if buildLevel == model.LevelPlan {
				pPtr = s.planets[uint8(snum)][uint8(pnum)]
			}
			built := newship
			if err := s.createShipByShip(playerNum, governor, race, true, snum, pPtr, &built, builder); err != nil {
				sess.Send(fmt.Sprintf("Build error: %v\n", err))
				goto done
			}
			var loadCrew int
			var loadFuel float64
			if race.Governor[governor].Toggle.AutoLoad && what != model.OtypeTransDev && !race.God {
				autoloadAtShip(playerNum, &built, builder, &loadCrew, &loadFuel)
			}
			s.initializeNewShip(playerNum, governor, race, &built, loadFuel, loadCrew)
			if err := s.store.PutShip(&built); err != nil {
				sess.Send(fmt.Sprintf("Error saving: %v\n", err))
			}
			s.ships[built.Number] = &built

		default:
			needed := int(newship.BuildCost)
			if int(builder.Hanger)+s.shipSize(&newship) > int(builder.MaxHanger) {
				sess.Send("Not enough hanger space.\n")
				goto done
			}
			if int(builder.Resource) < needed {
				sess.Send(fmt.Sprintf("You need %dr to construct the ship.\n", newship.BuildCost))
				goto done
			}
			built := newship
			if err := s.createShipByShip(playerNum, governor, race, false, snum, nil, &built, builder); err != nil {
				sess.Send(fmt.Sprintf("Build error: %v\n", err))
				goto done
			}
			var loadCrew int
			var loadFuel float64
			if race.Governor[governor].Toggle.AutoLoad && what != model.OtypeTransDev && !race.God {
				autoloadAtShip(playerNum, &built, builder, &loadCrew, &loadFuel)
			}
			s.initializeNewShip(playerNum, governor, race, &built, loadFuel, loadCrew)
			if err := s.store.PutShip(&built); err != nil {
				sess.Send(fmt.Sprintf("Error saving: %v\n", err))
			}
			s.ships[built.Number] = &built
		}
	}

done:
	if outside && buildLevel == model.LevelPlan && planet != nil {
		*planet.SectorAt(x, y) = sec
	}
}

// --- cmdUpgrade ---

// cmdUpgrade handles the "upgrade" player command.
// Mirrors upgrade() in build.c.
func (s *State) cmdUpgrade(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]
	if int(dir.Level) != model.LevelShip {
		sess.Send("You have to change scope to the ship you wish to upgrade.\n")
		return
	}
	dirship := s.ships[dir.ShipNo]
	if dirship == nil {
		sess.Send("Illegal dir value.\n")
		return
	}
	if msg := s.testShip(p, g, dirship); msg != "" {
		sess.Send(msg)
		return
	}
	if dirship.Damage > 0 {
		sess.Send("You cannot upgrade damaged ships.\n")
		return
	}
	if dirship.Type == model.OtypeFactory {
		sess.Send("You can't upgrade factories.\n")
		return
	}

	race := s.raceByNum(p)
	if race == nil {
		return
	}

	if model.Shipdata[dirship.Type][model.AbilMod] == 0 {
		sess.Send("This ship cannot be upgraded.\n")
		return
	}

	if len(args) < 1 {
		sess.Send("Usage: upgrade <characteristic> [value]\n")
		return
	}

	// Work on a copy
	ship := *dirship
	value := 0
	if len(args) >= 2 {
		value, _ = strconv.Atoi(args[1])
	}
	if value < 0 {
		sess.Send("That's a ridiculous setting.\n")
		return
	}

	sd := model.Shipdata[model.ShipType(ship.BuildType)]
	characteristic := strings.ToLower(args[0])
	var sub string
	if len(args) >= 3 {
		sub = strings.ToLower(args[2])
	}

	switch {
	case matchCmd(characteristic, "armor"):
		if int(ship.Armor) < value && value <= 100 {
			ship.Armor = uint8(value)
		}
	case matchCmd(characteristic, "crew") && sd[model.AbilMaxCrew] != 0:
		if value > int(ship.MaxCrew) && value <= 10000 {
			ship.MaxCrew = uint16(value)
		}
	case matchCmd(characteristic, "cargo") && sd[model.AbilCargo] != 0:
		if value > int(ship.MaxResource) && value <= 10000 {
			ship.MaxResource = uint16(value)
		}
	case matchCmd(characteristic, "hanger") && sd[model.AbilHanger] != 0:
		if value > int(ship.MaxHanger) && value <= 10000 {
			ship.MaxHanger = uint16(value)
		}
	case matchCmd(characteristic, "fuel") && sd[model.AbilFuelCap] != 0:
		if value > int(ship.MaxFuel) && value <= 10000 {
			ship.MaxFuel = uint16(value)
		}
	case matchCmd(characteristic, "destruct") && sd[model.AbilDestCap] != 0:
		if value > int(ship.MaxDestruct) && value <= 10000 {
			ship.MaxDestruct = uint16(value)
		}
	case matchCmd(characteristic, "speed") && sd[model.AbilSpeed] != 0:
		v := value
		if v < 1 {
			v = 1
		}
		if v > 9 {
			v = 9
		}
		if v > int(ship.MaxSpeed) {
			ship.MaxSpeed = uint16(v)
		}
	case matchCmd(characteristic, "mount") && sd[model.AbilMount] != 0 && !dirship.Mounted:
		if !race.HasDiscovery(model.DiscCrystal) {
			sess.Send("Your race does not know how to utilize crystal power yet.\n")
			return
		}
		ship.Mount = 1
	case matchCmd(characteristic, "hyperdrive") && sd[model.AbilJump] != 0 && !dirship.HyperDrive.Has:
		if !race.HasDiscovery(model.DiscHyperDrive) {
			sess.Send("Your race has not discovered hyperdrive.\n")
			return
		}
		ship.HyperDrive.Has = true
	case matchCmd(characteristic, "primary") && sd[model.AbilPrimary] != 0:
		if sub == "" || len(args) < 3 {
			sess.Send("Usage: upgrade primary strength|caliber <value>\n")
			return
		}
		if matchCmd(sub, "strength") {
			if ship.PrimType == 0 {
				sess.Send("No caliber defined.\n")
				return
			}
			v, _ := strconv.Atoi(args[2])
			if v > int(ship.Primary) {
				ship.Primary = uint8(v)
			}
		} else if matchCmd(sub, "caliber") {
			calStr := strings.ToLower(args[2])
			var cal uint8
			switch {
			case matchCmd(calStr, "light"):
				cal = caliberLight
			case matchCmd(calStr, "medium"):
				cal = caliberMedium
			case matchCmd(calStr, "heavy"):
				cal = caliberHeavy
			default:
				sess.Send("No such caliber.\n")
				return
			}
			if cal > ship.PrimType {
				ship.PrimType = cal
			}
			maxCal := uint8(sd[model.AbilPrimary])
			if ship.PrimType > maxCal {
				ship.PrimType = maxCal
			}
		} else {
			sess.Send("No such gun characteristic.\n")
			return
		}
	case matchCmd(characteristic, "secondary") && sd[model.AbilSecondary] != 0:
		if sub == "" || len(args) < 3 {
			sess.Send("Usage: upgrade secondary strength|caliber <value>\n")
			return
		}
		if matchCmd(sub, "strength") {
			if ship.SecType == 0 {
				sess.Send("No caliber defined.\n")
				return
			}
			v, _ := strconv.Atoi(args[2])
			if v > int(ship.Secondary) {
				ship.Secondary = uint8(v)
			}
		} else if matchCmd(sub, "caliber") {
			calStr := strings.ToLower(args[2])
			var cal uint8
			switch {
			case matchCmd(calStr, "light"):
				cal = caliberLight
			case matchCmd(calStr, "medium"):
				cal = caliberMedium
			case matchCmd(calStr, "heavy"):
				cal = caliberHeavy
			default:
				sess.Send("No such caliber.\n")
				return
			}
			if cal > ship.SecType {
				ship.SecType = cal
			}
			maxCal := uint8(sd[model.AbilSecondary])
			if ship.SecType > maxCal {
				ship.SecType = maxCal
			}
		} else {
			sess.Send("No such gun characteristic.\n")
			return
		}
	case matchCmd(characteristic, "cew") && sd[model.AbilCEW] != 0:
		if !race.HasDiscovery(model.DiscCEW) {
			sess.Send("Your race cannot build confined energy weapons.\n")
			return
		}
		if len(args) < 3 {
			sess.Send("Usage: upgrade cew strength|range <value>\n")
			return
		}
		val, _ := strconv.Atoi(args[2])
		if matchCmd(sub, "strength") {
			ship.CEW = uint8(val)
		} else if matchCmd(sub, "range") {
			ship.CEWRange = uint16(val)
		} else {
			sess.Send("No such option for CEWs.\n")
			return
		}
	case matchCmd(characteristic, "laser") && sd[model.AbilLaser] != 0:
		if !race.HasDiscovery(model.DiscLaser) {
			sess.Send("Your race cannot build lasers.\n")
			return
		}
		ship.Laser = 1
	default:
		sess.Send("That characteristic either doesn't exist or can't be modified.\n")
		return
	}

	complex := complexityOf(&ship)
	if complex > race.Tech {
		sess.Send(fmt.Sprintf("This upgrade requires an engineering technology of %.1f.\n", complex))
		return
	}

	newcost := int(s.shipCostFromDesign(&ship))
	oldcost := int(dirship.BuildCost)
	if newcost < oldcost {
		sess.Send("You cannot downgrade ships!\n")
		return
	}
	netcost := 2 * (newcost - oldcost)
	if !race.God && netcost == 0 {
		netcost = 1
	}

	sess.Send(fmt.Sprintf("Old value %dr   New value %dr\n", oldcost, newcost))
	if !race.God && int(dirship.Resource) < netcost {
		sess.Send(fmt.Sprintf("You need %d resources on board to make this modification.\n", netcost))
		return
	}
	if netcost > 0 || race.God {
		sess.Send(fmt.Sprintf("Characteristic modified at a cost of %d resources.\n", netcost))
		*dirship = ship
		dirship.Resource -= uint16(netcost)
		dirship.Size = uint16(s.shipSize(dirship))
		dirship.BaseMass = s.getMass(dirship)
		dirship.BuildCost = uint16(s.shipCostFromDesign(dirship))
		dirship.Complexity = complexityOf(dirship)
		if err := s.store.PutShip(dirship); err != nil {
			sess.Send(fmt.Sprintf("Error saving ship: %v\n", err))
		}
	} else {
		sess.Send("You cannot make this modification.\n")
	}
}

// --- cmdMake (make_mod mode=0): set factory ship type ---

// cmdMake handles "make <ship_type>": designates what a factory should produce.
// Mirrors make_mod(mode=0) in build.c.
func (s *State) cmdMake(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]
	if int(dir.Level) != model.LevelShip {
		sess.Send("You have to change scope to an installation.\n")
		return
	}
	dirship := s.ships[dir.ShipNo]
	if dirship == nil {
		sess.Send("Illegal dir value.\n")
		return
	}
	if msg := s.testShip(p, g, dirship); msg != "" {
		sess.Send(msg)
		return
	}
	if dirship.Type != model.OtypeFactory {
		sess.Send("That is not a factory.\n")
		return
	}
	if dirship.On && len(args) > 0 {
		sess.Send("This factory is already online.\n")
		return
	}
	race := s.raceByNum(p)
	if race == nil {
		return
	}

	if len(args) == 0 {
		// List current settings
		if dirship.BuildType == 0 {
			sess.Send("No ship type specified.\n")
			return
		}
		bt := model.ShipType(dirship.BuildType)
		sd := model.Shipdata[bt]
		sess.Send("  --- Current Production Specifications ---\n")
		status := "Offline"
		if dirship.On {
			status = "Online"
		}
		sess.Send(fmt.Sprintf("%s\t\t\tArmor:    %4d\t\tGuns:", status, dirship.Armor))
		if sd[model.AbilPrimary] != 0 && dirship.PrimType != 0 {
			calStr := "N"
			switch dirship.PrimType {
			case caliberLight:
				calStr = "L"
			case caliberMedium:
				calStr = "M"
			case caliberHeavy:
				calStr = "H"
			}
			sess.Send(fmt.Sprintf("%3d%s", dirship.Primary, calStr))
		}
		if sd[model.AbilSecondary] != 0 && dirship.SecType != 0 {
			calStr := "N"
			switch dirship.SecType {
			case caliberLight:
				calStr = "L"
			case caliberMedium:
				calStr = "M"
			case caliberHeavy:
				calStr = "H"
			}
			sess.Send(fmt.Sprintf("/%d%s", dirship.Secondary, calStr))
		}
		sess.Send("\n")
		sess.Send(fmt.Sprintf("Ship:  %-16.16s\tCrew:     %4d\n",
			model.ShipNames[bt], dirship.MaxCrew))
		sess.Send(fmt.Sprintf("Class: %s\t\tFuel:     %4d\n",
			dirship.Class, dirship.MaxFuel))
		sess.Send(fmt.Sprintf("Cost:  %d r\t\tCargo:    %4d\n",
			dirship.BuildCost, dirship.MaxResource))
		sess.Send(fmt.Sprintf("Mass:  %.1f\t\tHanger:   %4d\n",
			dirship.BaseMass, dirship.MaxHanger))
		sess.Send(fmt.Sprintf("Size:  %-6d\t\tDestruct: %4d\n",
			dirship.Size, dirship.MaxDestruct))
		sess.Send(fmt.Sprintf("Tech:  %.1f (%.1f)\tSpeed:    %4d\n",
			dirship.Complexity, race.Tech, dirship.MaxSpeed))
		if race.Tech < dirship.Complexity {
			sess.Send("Your engineering capability is not advanced enough to produce this design.\n")
		}
		return
	}

	i := getBuildType(args[0][0])
	if i >= model.NumShipTypes {
		sess.Send("Illegal ship letter.\n")
		return
	}
	if i == model.StypePod && !race.Pods {
		sess.Send("Illegal ship letter.\n")
		return
	}
	if model.Shipdata[i][model.AbilGod] != 0 && !race.God {
		sess.Send("Nice try!\n")
		return
	}
	if (model.Shipdata[i][model.AbilBuild] & model.Shipdata[model.OtypeFactory][model.AbilConstruct]) == 0 {
		sess.Send("This kind of ship does not require a factory to construct.\n")
		return
	}

	dirship.BuildType = uint16(i)
	sd := model.Shipdata[i]
	dirship.Armor = uint8(sd[model.AbilArmor])
	dirship.Guns = 0
	dirship.Primary = uint8(sd[model.AbilGuns])
	dirship.PrimType = uint8(sd[model.AbilPrimary])
	dirship.Secondary = uint8(sd[model.AbilGuns])
	dirship.SecType = uint8(sd[model.AbilSecondary])
	dirship.MaxCrew = uint16(sd[model.AbilMaxCrew])
	dirship.MaxResource = uint16(sd[model.AbilCargo])
	dirship.MaxHanger = uint16(sd[model.AbilHanger])
	dirship.MaxFuel = uint16(sd[model.AbilFuelCap])
	dirship.MaxDestruct = uint16(sd[model.AbilDestCap])
	dirship.MaxSpeed = uint16(sd[model.AbilSpeed])

	if sd[model.AbilMount] != 0 && race.HasDiscovery(model.DiscCrystal) {
		dirship.Mount = 1
	} else {
		dirship.Mount = 0
	}
	if sd[model.AbilJump] != 0 && race.HasDiscovery(model.DiscHyperDrive) {
		dirship.HyperDrive.Has = true
	} else {
		dirship.HyperDrive.Has = false
	}
	dirship.Cloak = 0
	if sd[model.AbilLaser] != 0 && race.HasDiscovery(model.DiscLaser) {
		dirship.Laser = 1
	} else {
		dirship.Laser = 0
	}
	dirship.CEW = 0
	dirship.Mode = false

	dirship.Size = uint16(s.shipSize(dirship))
	dirship.Complexity = complexityOf(dirship)
	dirship.Class = fmt.Sprintf("mod %d", dir.ShipNo)

	sess.Send(fmt.Sprintf("Factory designated to produce %ss.\n", model.ShipNames[i]))
	sess.Send(fmt.Sprintf("Design complexity %.1f (%.1f).\n", dirship.Complexity, race.Tech))
	if dirship.Complexity > race.Tech {
		sess.Send("You can't produce this design yet!\n")
	}

	dirship.BuildCost = uint16(s.shipCostFromDesign(dirship))
	cost0 := s.shipCostFromDesign(dirship)
	if cost0 > 65535 {
		sess.Send("Woah!! YOU CHEATER!!!  The max cost allowed is 65535!!! I'm Telllllllling!!!\n")
		return
	}
	if race.God {
		dirship.BuildCost = 0
	} else {
		dirship.BuildCost = uint16(cost0)
	}
	sess.Send(fmt.Sprintf("The current cost of the ship is %d resources.\n", dirship.BuildCost))
	dirship.BaseMass = s.getMass(dirship)
	sess.Send(fmt.Sprintf("The current base mass of the ship is %.1f - size is %d.\n",
		dirship.BaseMass, dirship.Size))
	sess.Send(fmt.Sprintf("Ship complexity is %.1f (you have %.1f engineering technology).\n",
		dirship.Complexity, race.Tech))

	if err := s.store.PutShip(dirship); err != nil {
		sess.Send(fmt.Sprintf("Error saving factory: %v\n", err))
	}
}

// --- cmdMod (make_mod mode=1): modify factory design parameters ---

// cmdMod handles "mod <characteristic> [value]": changes a factory's design.
// Mirrors make_mod(mode=1) in build.c.
func (s *State) cmdMod(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]
	if int(dir.Level) != model.LevelShip {
		sess.Send("You have to change scope to an installation.\n")
		return
	}
	dirship := s.ships[dir.ShipNo]
	if dirship == nil {
		sess.Send("Illegal dir value.\n")
		return
	}
	if msg := s.testShip(p, g, dirship); msg != "" {
		sess.Send(msg)
		return
	}
	if dirship.Type != model.OtypeFactory {
		sess.Send("That is not a factory.\n")
		return
	}
	race := s.raceByNum(p)
	if race == nil {
		return
	}
	if dirship.BuildType == 0 {
		sess.Send("No ship design specified. Use 'make <ship type>' first.\n")
		return
	}
	if len(args) < 1 {
		sess.Send("You have to specify the characteristic you wish to modify.\n")
		return
	}

	sd := model.Shipdata[model.ShipType(dirship.BuildType)]
	characteristic := strings.ToLower(args[0])

	value := 0
	if len(args) >= 2 {
		value, _ = strconv.Atoi(args[1])
	}
	if value < 0 {
		sess.Send("That's a ridiculous setting.\n")
		return
	}

	if sd[model.AbilMod] != 0 {
		var sub string
		if len(args) >= 3 {
			sub = strings.ToLower(args[2])
		}

		switch {
		case matchCmd(characteristic, "armor"):
			if value > 100 {
				value = 100
			}
			dirship.Armor = uint8(value)
		case matchCmd(characteristic, "crew") && sd[model.AbilMaxCrew] != 0:
			if value > 10000 {
				value = 10000
			}
			dirship.MaxCrew = uint16(value)
		case matchCmd(characteristic, "cargo") && sd[model.AbilCargo] != 0:
			if value > 10000 {
				value = 10000
			}
			dirship.MaxResource = uint16(value)
		case matchCmd(characteristic, "hanger") && sd[model.AbilHanger] != 0:
			if value > 10000 {
				value = 10000
			}
			dirship.MaxHanger = uint16(value)
		case matchCmd(characteristic, "fuel") && sd[model.AbilFuelCap] != 0:
			if value > 10000 {
				value = 10000
			}
			dirship.MaxFuel = uint16(value)
		case matchCmd(characteristic, "destruct") && sd[model.AbilDestCap] != 0:
			if value > 10000 {
				value = 10000
			}
			dirship.MaxDestruct = uint16(value)
		case matchCmd(characteristic, "speed") && sd[model.AbilSpeed] != 0:
			if value < 1 {
				value = 1
			}
			if value > 9 {
				value = 9
			}
			dirship.MaxSpeed = uint16(value)
		case matchCmd(characteristic, "mount") && sd[model.AbilMount] != 0:
			if !race.HasDiscovery(model.DiscCrystal) {
				sess.Send("Your race does not understand crystal power.\n")
				return
			}
			if dirship.Mount != 0 {
				dirship.Mount = 0
			} else {
				dirship.Mount = 1
			}
		case matchCmd(characteristic, "hyperdrive") && sd[model.AbilJump] != 0:
			if !race.HasDiscovery(model.DiscHyperDrive) {
				sess.Send("Your race has not discovered hyperdrive.\n")
				return
			}
			dirship.HyperDrive.Has = !dirship.HyperDrive.Has
		case matchCmd(characteristic, "primary") && sd[model.AbilPrimary] != 0:
			if len(args) < 3 {
				sess.Send("Usage: mod primary strength|caliber <value>\n")
				return
			}
			if matchCmd(sub, "strength") {
				dirship.Primary = uint8(atoi(args[2]))
			} else if matchCmd(sub, "caliber") {
				calStr := strings.ToLower(args[2])
				var cal uint8
				switch {
				case matchCmd(calStr, "light"):
					cal = caliberLight
				case matchCmd(calStr, "medium"):
					cal = caliberMedium
				case matchCmd(calStr, "heavy"):
					cal = caliberHeavy
				default:
					sess.Send("No such caliber.\n")
					return
				}
				dirship.PrimType = cal
				maxCal := uint8(sd[model.AbilPrimary])
				if dirship.PrimType > maxCal {
					dirship.PrimType = maxCal
				}
			} else {
				sess.Send("No such gun characteristic.\n")
				return
			}
		case matchCmd(characteristic, "secondary") && sd[model.AbilSecondary] != 0:
			if len(args) < 3 {
				sess.Send("Usage: mod secondary strength|caliber <value>\n")
				return
			}
			if matchCmd(sub, "strength") {
				dirship.Secondary = uint8(atoi(args[2]))
			} else if matchCmd(sub, "caliber") {
				calStr := strings.ToLower(args[2])
				var cal uint8
				switch {
				case matchCmd(calStr, "light"):
					cal = caliberLight
				case matchCmd(calStr, "medium"):
					cal = caliberMedium
				case matchCmd(calStr, "heavy"):
					cal = caliberHeavy
				default:
					sess.Send("No such caliber.\n")
					return
				}
				dirship.SecType = cal
				maxCal := uint8(sd[model.AbilSecondary])
				if dirship.SecType > maxCal {
					dirship.SecType = maxCal
				}
			} else {
				sess.Send("No such gun characteristic.\n")
				return
			}
		case matchCmd(characteristic, "cew") && sd[model.AbilCEW] != 0:
			if !race.HasDiscovery(model.DiscCEW) {
				sess.Send("Your race does not understand confined energy weapons.\n")
				return
			}
			if len(args) < 3 {
				sess.Send("Usage: mod cew strength|range <value>\n")
				return
			}
			val := atoi(args[2])
			if matchCmd(sub, "strength") {
				dirship.CEW = uint8(val)
			} else if matchCmd(sub, "range") {
				dirship.CEWRange = uint16(val)
			} else {
				sess.Send("No such option for CEWs.\n")
				return
			}
		case matchCmd(characteristic, "laser") && sd[model.AbilLaser] != 0:
			if !race.HasDiscovery(model.DiscLaser) {
				sess.Send("Your race does not understand lasers yet.\n")
				return
			}
			dirship.Laser = 1 - dirship.Laser
		default:
			sess.Send("That characteristic either doesn't exist or can't be modified.\n")
			return
		}
	} else if race.HasDiscovery(model.DiscHyperDrive) {
		if matchCmd(characteristic, "hyperdrive") {
			dirship.HyperDrive.Has = !dirship.HyperDrive.Has
		} else {
			sess.Send("You may only modify hyperdrive installation on this kind of ship.\n")
			return
		}
	} else {
		sess.Send("Sorry, but you can't modify this ship right now.\n")
		return
	}

	cost0 := s.shipCostFromDesign(dirship)
	if cost0 > 65535.0 {
		sess.Send("Woah!! YOU CHEATER!!!  The max cost allowed is 65535!!! I'm Telllllllling!!!\n")
		return
	}
	if race.God {
		dirship.BuildCost = 0
	} else {
		dirship.BuildCost = uint16(cost0)
	}
	sess.Send(fmt.Sprintf("The current cost of the ship is %d resources.\n", dirship.BuildCost))
	dirship.Size = uint16(s.shipSize(dirship))
	dirship.BaseMass = s.getMass(dirship)
	sess.Send(fmt.Sprintf("The current base mass of the ship is %.1f - size is %d.\n",
		dirship.BaseMass, dirship.Size))
	dirship.Complexity = complexityOf(dirship)
	sess.Send(fmt.Sprintf("Ship complexity is %.1f (you have %.1f engineering technology).\n",
		dirship.Complexity, race.Tech))

	if err := s.store.PutShip(dirship); err != nil {
		sess.Send(fmt.Sprintf("Error saving factory: %v\n", err))
	}
}

// --- Market commands (cmdSell / cmdBid) ---

// commodityNames maps commodity type to display name.
var commodityNames = [4]string{"resources", "destruct", "fuel", "crystals"}

// cmdSell handles "sell <commodity> <amount>": puts a lot on the market.
// Mirrors sell() in build.c. Only available when Market feature is enabled.
func (s *State) cmdSell(p, g int, args []string, sess net.Responder) {
	if !s.cfg.Market {
		sess.Send("The commodity market is not enabled.\n")
		return
	}

	dir := s.dirs[p-1][g]
	if int(dir.Level) != model.LevelPlan {
		sess.Send("You have to be in a planet scope to sell.\n")
		return
	}
	if len(args) < 2 {
		sess.Send("Syntax: sell <commodity> <amount>\n")
		return
	}

	race := s.raceByNum(p)
	if race == nil {
		return
	}
	if race.Guest {
		sess.Send("Guest races can't sell anything.\n")
		return
	}

	snum := int(dir.StarNum)
	pnum := int(dir.PlanetNum)

	star := s.stars[snum]
	if star != nil && g != 0 && int(star.Governor[p-1]) != g {
		sess.Send("You are not authorized in this system.\n")
		return
	}

	amount, _ := strconv.Atoi(args[1])
	if amount <= 0 {
		sess.Send("Try using positive values.\n")
		return
	}

	ap := star.AP[p-1]
	apNeeded := amount
	if int(ap) < apNeeded {
		apNeeded = int(ap)
	}
	if !s.enoughAP(p, g, ap, apNeeded) {
		return
	}

	planet := s.planets[uint8(snum)][uint8(pnum)]
	if planet == nil {
		sess.Send("No such planet.\n")
		return
	}
	if planet.SlavedTo != 0 && int(planet.SlavedTo) != p {
		sess.Send(fmt.Sprintf("This planet is enslaved to player %d.\n", planet.SlavedTo))
		return
	}

	// Check for undamaged space port or government center
	ok := false
	sh := planet.Ships
	for sh != 0 {
		existing := s.ships[sh]
		if existing == nil {
			break
		}
		if existing.Alive && int(existing.Owner) == p && existing.Damage == 0 &&
			model.Shipdata[existing.Type][model.AbilPort] != 0 {
			ok = true
			break
		}
		sh = existing.NextShip
	}
	if !ok {
		sess.Send("You don't have an undamaged space port or government center here.\n")
		return
	}

	commod := args[0][0]
	var item uint8
	switch commod {
	case 'r':
		if planet.Info[p-1].Resource == 0 {
			sess.Send("You don't have any resources here to sell!\n")
			return
		}
		if amount > int(planet.Info[p-1].Resource) {
			amount = int(planet.Info[p-1].Resource)
		}
		planet.Info[p-1].Resource -= uint16(amount)
		item = model.CommodResource
	case 'd':
		if planet.Info[p-1].Destruct == 0 {
			sess.Send("You don't have any destruct here to sell!\n")
			return
		}
		if amount > int(planet.Info[p-1].Destruct) {
			amount = int(planet.Info[p-1].Destruct)
		}
		planet.Info[p-1].Destruct -= uint16(amount)
		item = model.CommodDestruct
	case 'f':
		if planet.Info[p-1].Fuel == 0 {
			sess.Send("You don't have any fuel here to sell!\n")
			return
		}
		if amount > int(planet.Info[p-1].Fuel) {
			amount = int(planet.Info[p-1].Fuel)
		}
		planet.Info[p-1].Fuel -= uint16(amount)
		item = model.CommodFuel
	case 'x':
		if planet.Info[p-1].Crystals == 0 {
			sess.Send("You don't have any crystals here to sell!\n")
			return
		}
		if amount > int(planet.Info[p-1].Crystals) {
			amount = int(planet.Info[p-1].Crystals)
		}
		planet.Info[p-1].Crystals -= uint16(amount)
		item = model.CommodCrystal
	default:
		sess.Send("Permitted commodities are r, d, f, and x.\n")
		return
	}

	commodno, err := s.store.AllocCommodNum()
	if err != nil {
		sess.Send(fmt.Sprintf("Error allocating lot: %v\n", err))
		return
	}

	c := &model.Commodity{
		Number:     commodno,
		Owner:      uint8(p),
		Governor:   uint8(g),
		Type:       item,
		Amount:     uint16(amount),
		Deliver:    false,
		Bid:        0,
		Bidder:     0,
		StarFrom:   uint8(snum),
		PlanetFrom: uint8(pnum),
	}

	sess.Send(fmt.Sprintf("Lot #%d - %d units of %s.\n", commodno, amount, commodityNames[item]))
	announce := fmt.Sprintf("Lot #%d - %d units of %s for sale by %s [%d].\n",
		commodno, amount, commodityNames[item], race.Name, p)
	for i := 1; i <= model.MaxPlayers; i++ {
		if s.raceByNum(i) != nil {
			s.notifyRace(i, announce)
		}
	}

	if err := s.store.PutCommod(c); err != nil {
		sess.Send(fmt.Sprintf("Error storing lot: %v\n", err))
		return
	}
	s.deductAPs(p, g, apNeeded, snum, false)
}

// cmdBid handles "bid [<lot> <amount>]": bids on a market lot.
// Mirrors bid() in build.c.
func (s *State) cmdBid(p, g int, args []string, sess net.Responder) {
	if !s.cfg.Market {
		sess.Send("The commodity market is not enabled.\n")
		return
	}

	if len(args) == 0 {
		// List all lots
		sess.Send("+++ Galactic Bloodshed Commodities Market +++\n\n")
		sess.Send("  Lot Stock      Type  Owner  Bidder  Amount Cost/Unit    Ship  Dest\n")
		s.listCommodities(p, g, -1, sess)
		return
	}
	if len(args) == 1 {
		// Filter by type
		commod := args[0][0]
		var item int
		switch commod {
		case 'r':
			item = model.CommodResource
		case 'd':
			item = model.CommodDestruct
		case 'f':
			item = model.CommodFuel
		case 'x':
			item = model.CommodCrystal
		default:
			sess.Send("No such type of commodity.\n")
			return
		}
		sess.Send("+++ Galactic Bloodshed Commodities Market +++\n\n")
		sess.Send("  Lot Stock      Type  Owner  Bidder  Amount Cost/Unit    Ship  Dest\n")
		s.listCommodities(p, g, item, sess)
		return
	}

	// Placing a bid
	dir := s.dirs[p-1][g]
	if int(dir.Level) != model.LevelPlan {
		sess.Send("You have to be in a planet scope to buy.\n")
		return
	}
	snum := int(dir.StarNum)
	pnum := int(dir.PlanetNum)
	star := s.stars[snum]
	if star != nil && g != 0 && int(star.Governor[p-1]) != g {
		sess.Send("You are not authorized in this system.\n")
		return
	}

	planet := s.planets[uint8(snum)][uint8(pnum)]
	if planet == nil {
		sess.Send("No such planet.\n")
		return
	}
	if planet.SlavedTo != 0 && int(planet.SlavedTo) != p {
		sess.Send(fmt.Sprintf("This planet is enslaved to player %d.\n", planet.SlavedTo))
		return
	}

	// Check space port
	ok := false
	sh := planet.Ships
	for sh != 0 {
		existing := s.ships[sh]
		if existing == nil {
			break
		}
		if existing.Alive && int(existing.Owner) == p && existing.Damage == 0 &&
			model.Shipdata[existing.Type][model.AbilPort] != 0 {
			ok = true
			break
		}
		sh = existing.NextShip
	}
	if !ok {
		sess.Send("You don't have an undamaged space port or government center here.\n")
		return
	}

	lot, _ := strconv.Atoi(args[0])
	bid0, _ := strconv.Atoi(args[1])

	c, err := s.store.GetCommod(uint32(lot))
	if err != nil || c == nil || c.Owner == 0 {
		sess.Send("No such lot for sale.\n")
		return
	}

	race := s.raceByNum(p)
	if race == nil {
		return
	}
	if race.Guest {
		sess.Send("Guest races cannot bid.\n")
		return
	}

	minbid := int(float64(c.Bid) * (1.0 + s.cfg.UpBid))
	if bid0 < minbid {
		sess.Send(fmt.Sprintf("You have to bid more than %d.\n", minbid))
		return
	}
	if uint64(bid0) > race.Governor[g].Money {
		sess.Send("Sorry, no buying on credit allowed.\n")
		return
	}

	// Notify previous bidder
	if c.Bidder != 0 {
		msg := fmt.Sprintf("The bid on lot #%d (%d %s) has been upped to %d by %s [%d].\n",
			lot, c.Amount, commodityNames[c.Type], bid0, race.Name, p)
		s.notify(int(c.Bidder), int(c.BidderGov), msg)
	}

	c.Bid = uint64(bid0)
	c.Bidder = uint8(p)
	c.BidderGov = uint8(g)
	c.StarTo = uint8(snum)
	c.PlanetTo = uint8(pnum)

	shipping := s.shippingCost(snum, int(c.StarFrom), bid0)
	sess.Send(fmt.Sprintf("There will be an additional %d charged to you for shipping costs.\n", shipping))

	if err := s.store.PutCommod(c); err != nil {
		sess.Send(fmt.Sprintf("Error saving bid: %v\n", err))
		return
	}
	sess.Send("Bid accepted.\n")
}

// listCommodities displays market lots, filtering by item type (-1 = all).
func (s *State) listCommodities(playerNum, governor, filterItem int, sess net.Responder) {
	for i := uint32(1); ; i++ {
		c, err := s.store.GetCommod(i)
		if err != nil {
			break
		}
		if c == nil {
			continue
		}
		if c.Owner == 0 || c.Amount == 0 {
			continue
		}
		if filterItem >= 0 && int(c.Type) != filterItem {
			continue
		}
		rate := 0.0
		if c.Amount > 0 {
			rate = float64(c.Bid) / float64(c.Amount)
		}
		dest := ""
		if c.Bidder == uint8(playerNum) {
			from := s.stars[c.StarTo]
			if from != nil {
				pl := s.planets[c.StarTo][c.PlanetTo]
				pname := fmt.Sprintf("P%d", c.PlanetTo)
				_ = pl // planet has no Name field; use default pname
				dest = fmt.Sprintf("%4.4s/%-4.4s", from.Name, pname)
			}
		}
		lot := int(c.Number)
		delChar := ' '
		if c.Deliver {
			delChar = '*'
		}
		fromStar := s.stars[c.StarFrom]
		shipping := 0
		if fromStar != nil {
			dir := s.dirs[playerNum-1][governor]
			shipping = s.shippingCost(int(c.StarFrom), int(dir.StarNum), int(c.Bid))
		}
		sess.Send(fmt.Sprintf(" %4d%c%5d%10s%7d%8d%8d%10.2f%8d %10s\n",
			lot, delChar, c.Amount,
			commodityNames[c.Type], c.Owner, c.Bidder, int(c.Bid), rate,
			shipping, dest))
	}
}

// shippingCost computes the shipping cost between two star systems.
// Mirrors shipping_cost() in build.c.
func (s *State) shippingCost(to, from, value int) int {
	starTo := s.stars[to]
	starFrom := s.stars[from]
	if starTo == nil || starFrom == nil {
		return 0
	}
	dx := starTo.XPos - starFrom.XPos
	dy := starTo.YPos - starFrom.YPos
	dist := math.Sqrt(dx*dx + dy*dy)
	chunk := int(dist/10000.0) * 10000
	factor := 1.0 - math.Exp(-float64(chunk)/merchantLength)
	return int(factor * float64(value))
}

// atoi is a simple strconv.Atoi without error checking for use in int conversions.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
