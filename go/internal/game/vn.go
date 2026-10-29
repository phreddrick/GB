package game

// vn.go implements Von Neumann machine (VN) and Berserker ship behaviour.
//
// C source ported: VN.c
//
//   doVN(ship)         — per-ship action called by doShip (update only)
//   planetDoVN(ship)   — per-planet action called by doPlanet (update only)
//   orderVN(ship)      — set destination for a newly launched VN
//   orderBerserker(ship) — set destination/mission for a newly launched berserker

import (
	"fmt"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

const vnResTake = 0.5 // fraction of sector resource a VN mines each turn

// doVN handles the ship-level VN/Berserker action.
// Called by doShip for OtypeVN and OtypeBers ships during an update.
// Corresponds to do_VN() in VN.c.
func (s *State) doVN(ship *model.Ship) {
	// Ensure mind special is initialised.
	if ship.Special.Mind == nil {
		ship.Special.Mind = &model.MindSpecial{}
	}
	mind := ship.Special.Mind

	if landed(ship) {
		// Mark the planet as inhabited.
		s.stinfo[ship.StOrbits][ship.PNumOrbits].Inhab = true

		if !mind.Busy {
			// No assignment: launch if tank is full.
			if ship.Fuel >= float64(ship.MaxFuel) {
				star := s.stars[ship.StOrbits]
				planet := s.planets[ship.StOrbits][ship.PNumOrbits]
				if star != nil && planet != nil {
					ship.XPos = star.XPos + planet.XPos + float64(intRand(-10, 10))
					ship.YPos = star.YPos + planet.YPos + float64(intRand(-10, 10))
				}
				ship.Docked = false
				ship.WhatDest = model.LevelUniv
			}
		} else {
			// Busy: steal resources from whoever has the most on this planet.
			planet := s.planets[ship.StOrbits][ship.PNumOrbits]
			if planet == nil {
				return
			}

			// Permute race order to randomise theft target.
			n := len(s.races)
			nums := make([]int, n)
			for i := range nums {
				nums[i] = i + 1
			}
			for i := 0; i < n; i++ {
				j := intRand(0, n-1)
				nums[i], nums[j] = nums[j], nums[i]
			}

			// Find the race with the most resource on the planet.
			victim := 0
			for _, rn := range nums {
				if rn < 1 || rn > len(s.races) {
					continue
				}
				if planet.Info[rn-1].Resource > 0 {
					victim = rn
				}
			}

			if victim > 0 {
				cost := int(model.Shipdata[model.OtypeVN][model.AbilCost])
				prod := int(planet.Info[victim-1].Resource)
				if prod > cost {
					prod = cost
				}
				planet.Info[victim-1].Resource -= uint16(prod)

				if ship.Type == model.OtypeVN {
					s.rcvResource(ship, prod)
				} else {
					s.rcvDestruct(ship, prod)
				}

				msg := fmt.Sprintf("%d resources stolen from [%d] by %c%d at /%s/%s.\n",
					prod, victim,
					model.ShipLetters[ship.Type], ship.Number,
					s.starName(int(ship.StOrbits)),
					s.planetName(int(ship.StOrbits), int(ship.PNumOrbits)))
				s.pushTelegramRace(victim, msg)
				if victim != int(ship.Owner) {
					s.pushTelegram(int(ship.Owner), int(ship.Governor), msg)
				}
			}
		}
	} else {
		// Not landed: if freshly built (not busy), assign a mission.
		if !mind.Busy {
			if ship.Type == model.OtypeBers {
				s.orderBerserker(ship)
			} else {
				s.orderVN(ship)
			}
		}
	}
}

// orderVN assigns a destination star/planet to a VN that has just launched.
// Corresponds to order_VN() in VN.c.
func (s *State) orderVN(ship *model.Ship) {
	if ship.Special.Mind == nil {
		ship.Special.Mind = &model.MindSpecial{}
	}
	numStars := int(s.sdata.NumStars)
	if numStars == 0 {
		return
	}

	// Find the closest star (min) and second-closest (min2).
	min, min2 := 0, 0
	for st := 0; st < numStars; st++ {
		if st == int(ship.StOrbits) || s.stars[st] == nil {
			continue
		}
		if distSq(s.stars[st].XPos, s.stars[st].YPos, ship.XPos, ship.YPos) <
			distSq(s.stars[min].XPos, s.stars[min].YPos, ship.XPos, ship.YPos) {
			min2 = min
			min = st
		}
	}

	// Prefer a star that player 1 (VN owner) doesn't already inhabit.
	dest := min
	if s.stars[min] != nil && s.stars[min].IsInhabited(1) {
		if s.stars[min2] != nil && !s.stars[min2].IsInhabited(1) {
			dest = min2
		} else {
			dest = intRand(0, numStars-1)
		}
	}

	star := s.stars[dest]
	if star == nil || star.NumPlanets == 0 {
		ship.Special.Mind.Busy = false
		return
	}

	ship.DestStar = uint8(dest)
	ship.DestPNum = uint8(intRand(0, int(star.NumPlanets)-1))
	ship.WhatDest = model.LevelPlan
	ship.Special.Mind.Busy = true
	ship.Speed = uint8(model.Shipdata[model.OtypeVN][model.AbilSpeed])
}

// orderBerserker assigns a bombardment mission to a newly spawned Berserker.
// Corresponds to order_berserker() in VN.c.
func (s *State) orderBerserker(ship *model.Ship) {
	if ship.Special.Mind == nil {
		ship.Special.Mind = &model.MindSpecial{}
	}
	if s.vnMostMad == 0 {
		return
	}
	target := int(s.vnMostMad)

	ship.Bombard = true
	ship.Special.Mind.Target = uint8(target)
	ship.WhatDest = model.LevelPlan

	// Aim at a remembered VN-kill location for the target player.
	var destStar uint8
	if s.sdata != nil {
		if intRand(0, 1) == 0 {
			destStar = s.sdata.VNIndex1[target-1]
		} else {
			destStar = s.sdata.VNIndex2[target-1]
		}
	}
	ship.DestStar = destStar

	targetStar := s.stars[destStar]
	if targetStar != nil && targetStar.NumPlanets > 0 {
		ship.DestPNum = uint8(intRand(0, int(targetStar.NumPlanets)-1))
	}

	if ship.HyperDrive.Has && ship.Mounted {
		ship.HyperDrive.On = true
		ship.HyperDrive.Ready = true
		ship.Special.Mind.Busy = true
	}
}

// planetDoVN handles VN/Berserker planet-level behaviour: resource mining
// and self-replication.  Called by doPlanet for each VN/Berserker landed
// on a planet with a busy mind.
// Corresponds to planet_doVN() in VN.c.
func (s *State) planetDoVN(ship *model.Ship, planet *model.Planet) {
	if ship.Special.Mind == nil {
		ship.Special.Mind = &model.MindSpecial{}
	}
	mind := ship.Special.Mind

	if !landed(ship) {
		// Orbiting: if busy and aimed at this planet, find a landing spot.
		if mind.Busy &&
			ship.WhatDest == model.LevelPlan &&
			ship.DestStar == ship.StOrbits &&
			ship.DestPNum == ship.PNumOrbits {

			if planet.Type == model.PlanetGasGiant {
				mind.Busy = false
				return
			}

			// Find a sector with resources to land on.
			coords := permuteSects(planet)
			for _, c := range coords {
				sec := planet.SectorAt(c.x, c.y)
				if sec == nil || sec.Resource == 0 {
					continue
				}
				ship.Docked = true
				ship.WhatDest = model.LevelPlan
				ship.DestStar = ship.StOrbits
				ship.DestPNum = ship.PNumOrbits
				if s.stars[ship.StOrbits] != nil {
					ship.XPos = s.stars[ship.StOrbits].XPos + planet.XPos
					ship.YPos = s.stars[ship.StOrbits].YPos + planet.YPos
				}
				ship.LandX = uint8(c.x)
				ship.LandY = uint8(c.y)
				mind.Busy = true
				return
			}
			// No resource sector found.
			mind.Busy = false
		}
		return
	}

	// Landed and busy (VN only; berserkers that are bombing are handled elsewhere).
	if ship.Type != model.OtypeVN || !mind.Busy {
		return
	}

	// Mine the current sector.
	sec := planet.SectorAt(int(ship.LandX), int(ship.LandY))
	if sec == nil {
		return
	}

	if sec.Resource == 0 {
		// Move to a random adjacent sector.
		dx := intRand(-1, 1)
		nx := (int(ship.LandX) + dx + int(planet.MaxX)) % int(planet.MaxX)
		dy := 0
		if ship.LandY == 0 {
			dy = 1
		} else if int(ship.LandY) == int(planet.MaxY)-1 {
			dy = -1
		} else {
			dy = intRand(-1, 1)
		}
		ship.LandX = uint8(nx)
		ship.LandY = uint8(int(ship.LandY) + dy)
	} else {
		// Mine the sector.
		oldRes := int(sec.Resource)
		newRes := int(math.Round(float64(sec.Resource) * vnResTake))
		sec.Resource = uint16(newRes)
		prod := oldRes - newRes
		s.rcvResource(ship, prod)
		s.rcvFuel(ship, float64(prod))
	}

	// Try to replicate.
	vnCost := int(model.Shipdata[model.OtypeVN][model.AbilCost])
	bersCost := int(model.Shipdata[model.OtypeBers][model.AbilCost])

	// Decide build type: prefer berserker if VN brain is very angry.
	buildType := model.ShipType(model.OtypeVN)
	buildCost := vnCost
	if int(s.vnTotalMad) > 100 && intRand(0, 1) == 0 {
		buildType = model.OtypeBers
		buildCost = bersCost
	}

	if int(ship.Resource) < buildCost {
		return
	}

	numNew := int(ship.Resource) / buildCost
	for j := 0; j < numNew; j++ {
		s.useResource(ship, buildCost)
		s.spawnVN(ship, planet, buildType)
	}
}

// spawnVN creates one new VN or Berserker ship on the given planet.
// Mirrors the ship construction block inside planet_doVN() in VN.c.
func (s *State) spawnVN(parent *model.Ship, planet *model.Planet, buildType model.ShipType) {
	shipNum, err := s.store.AllocShipNum()
	if err != nil {
		return
	}

	sd := model.Shipdata[buildType]

	child := &model.Ship{
		Number:      shipNum,
		Type:        buildType,
		Owner:       1, // VNs always owned by player 1
		Governor:    0,
		Alive:       true,
		Active:      true,
		Docked:      true,
		WhatOrbits:  model.LevelPlan,
		WhatDest:    parent.WhatDest,
		DestStar:    parent.DestStar,
		DestPNum:    parent.DestPNum,
		StOrbits:    parent.StOrbits,
		PNumOrbits:  parent.PNumOrbits,
		XPos:        parent.XPos,
		YPos:        parent.YPos,
		LandX:       parent.LandX,
		LandY:       parent.LandY,
		Armor:       parent.Armor + 1,
		MaxCrew:     uint16(sd[model.AbilMaxCrew]),
		MaxResource: uint16(sd[model.AbilCargo]),
		MaxFuel:     uint16(sd[model.AbilFuelCap]),
		MaxDestruct: uint16(sd[model.AbilDestCap]),
		MaxSpeed:    uint16(sd[model.AbilSpeed]),
	}

	if sd[model.AbilPrimary] != 0 {
		child.Guns = model.GunPrimary
	}
	child.Primary = uint8(sd[model.AbilGuns])
	child.PrimType = uint8(sd[model.AbilPrimary])
	child.Secondary = uint8(sd[model.AbilGuns])
	child.SecType = uint8(sd[model.AbilSecondary])

	child.Special.Mind = &model.MindSpecial{
		Busy:       false,
		Progenitor: parent.Special.Mind.Progenitor,
		Generation: parent.Special.Mind.Generation + 1,
	}

	if buildType == model.OtypeBers {
		child.Special.Mind.Target = s.vnMostMad
		child.Name = fmt.Sprintf("%x", s.vnMostMad)
		child.Speed = uint8(model.Shipdata[model.OtypeBers][model.AbilSpeed])
		child.Tech = parent.Tech + 100.0
		child.Bombard = true
		child.Armor += 10
		child.Fuel = 5.0 * parent.Fuel
		child.Destruct = 500
		child.Retaliate = child.Primary
		child.HyperDrive = model.HyperDriveState{
			Has:   true,
			On:    true,
			Ready: true,
		}
		child.Mounted = true
		parent.Fuel *= 0.5
		child.Special.Mind.Tampered = false

		msg := fmt.Sprintf("%c%d constructed %c%d.\n",
			model.ShipLetters[parent.Type], parent.Number,
			model.ShipLetters[child.Type], child.Number)
		s.pushTelegram(int(parent.Owner), int(parent.Governor), msg)
	} else {
		child.Tech = parent.Tech + 20.0
		// Random binary name.
		n := intRand(3, min2(10, 12))
		name := make([]byte, n)
		for i := range name {
			name[i] = byte('0' + intRand(0, 1))
		}
		child.Name = string(name)
		child.Speed = uint8(model.Shipdata[model.OtypeVN][model.AbilSpeed])
		child.Fuel = 0.5 * parent.Fuel
		parent.Fuel *= 0.5
	}

	child.BaseMass = s.getMass(child)
	child.Mass = child.BaseMass
	child.BuildCost = uint16(s.shipcostForType(buildType, nil))

	s.ships[shipNum] = child
	s.insertShipPlan(planet, child)

	// Mark the parent as (optionally) done for this update.
	parent.Special.Mind.Busy = intRand(0, 1) == 0
}

// starName and planetName helpers are in planet.go.
