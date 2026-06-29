package game

// planet.go implements per-planet turn processing.
//
// Sources ported:
//   doplanet.c  – doplanet(), terraform(), plow(), do_dome(), do_quarry(),
//                 do_berserker(), do_recover(), moveship_onplanet()
//   dosector.c  – produce(), spread(), Migrate2(), explore(), plate()
//   moveplanet.c – moveplanet()
//   max.c       – maxsupport(), compatibility()
//   perm.c      – PermuteSects() / Getxysect()

import (
	"fmt"
	"math"
	"time"

	"github.com/gbgame/gb/internal/model"
)

// ---- Helper types --------------------------------------------------------

// sectorCoord is a (x, y) pair used for randomised sector iteration.
type sectorCoord struct{ x, y int }

// sectInfoCell mirrors struct sectinfo in doturn.h.
// One per sector, allocated locally inside doPlanet.
type sectInfoCell struct {
	explored uint8 // player number that has explored this cell (0 = no one)
	vn       bool
	done     bool
}

// StarPlanetInfo mirrors struct stinfo in doturn.h.
// Persists across all planet calls within a single doTurn.
type StarPlanetInfo struct {
	TempAdd     int16
	ThingAdd    uint8
	Inhab       bool
	Intimidated bool
}

// prodAccum holds per-player production accumulators for one planet turn.
// Replaces the global prod_res[], prod_fuel[], etc. arrays in the C source.
type prodAccum struct {
	res      int64
	fuel     int64
	destruct int64
	crystals int64
	avgMob   int64
}

// ---- Orbital mechanics ---------------------------------------------------

// movePlanet advances a planet's orbital position by one time unit using
// Kepler's third law, and drags all ships in orbit along with it.
// Corresponds to moveplanet() in moveplanet.c.
func (s *State) movePlanet(star *model.Star, planet *model.Planet, starNum, planetNum int) {
	// Mark planet as (potentially) inhabited.
	if planet.Popn > 0 || planet.Ships != 0 {
		s.stinfo[starNum][planetNum].Inhab = true
	}

	// Update the star's inhabited/explored aggregate flags.
	// The inhabited bitmap is reset here and repopulated by sector processing.
	star.Inhabited = 0
	starExplored := star.Explored != 0

	if !starExplored {
		return // nobody has explored this star yet; skip orbital math
	}

	dist := math.Hypot(planet.YPos, planet.XPos)
	if dist == 0 {
		return
	}

	phase := math.Atan2(planet.YPos, planet.XPos)
	period := dist * math.Sqrt(dist/(model.SystemGravConst*star.Gravity))
	if period == 0 {
		return
	}

	// One update tick of counter-clockwise Keplerian orbit.
	xAdd := dist*math.Cos(-1.0/period+phase) - planet.XPos
	yAdd := dist*math.Sin(-1.0/period+phase) - planet.YPos

	// Drag all orbiting ships with the planet.
	shipNo := planet.Ships
	for shipNo != 0 {
		ship := s.ships[shipNo]
		if ship == nil {
			break
		}
		ship.XPos += xAdd
		ship.YPos += yAdd
		shipNo = ship.NextShip
	}

	planet.XPos += xAdd
	planet.YPos += yAdd
}

// ---- Main planet turn ----------------------------------------------------

// doPlanet processes one full turn on a planet (or movement segment).
// When update is true, runs production + population dynamics.
// Corresponds to doplanet() in doplanet.c.
func (s *State) doPlanet(star *model.Star, planet *model.Planet, starNum, planetNum int, update bool) {
	if !update {
		return // movement segments do not process planet interiors
	}

	numRaces := s.numRaces()

	// ---- Per-race compatibility with this planet -------------------------
	compat := make([]float64, model.MaxPlayers)
	for i := 0; i < numRaces; i++ {
		if s.races[i] != nil {
			compat[i] = compatibility(planet, s.races[i])
		}
	}

	// Snapshot sector ownership before reset, for war-status reporting at end.
	prevSectsOwned := make([]uint16, model.MaxPlayers)
	for i := 0; i < numRaces; i++ {
		prevSectsOwned[i] = planet.Info[i].NumSectsOwned
	}

	// Reset per-player planet info counts.
	planet.MaxPopn = 0
	planet.Popn = 0
	planet.Troops = 0
	planet.TotalResources = 0
	for i := 0; i < numRaces; i++ {
		planet.Info[i].NumSectsOwned = 0
		planet.Info[i].Troops = 0
		planet.Info[i].Popn = 0
		planet.Info[i].EstProduction = 0
	}

	// Per-player production accumulators for this planet.
	accum := make([]prodAccum, model.MaxPlayers)

	// Per-sector scratch info.
	sectInfo := make([][]sectInfoCell, int(planet.MaxX))
	for x := range sectInfo {
		sectInfo[x] = make([]sectInfoCell, int(planet.MaxY))
	}

	// ---- Process ships in orbit / landed on this planet ------------------
	s.processPlanetShips(star, planet, starNum, planetNum, accum)

	// ---- Adjust temperature from space mirrors ---------------------------
	planet.Conditions[model.AtmosTemp] = planet.Conditions[model.AtmosRTemp] +
		s.stinfo[starNum][planetNum].TempAdd + int16(intRand(-5, 5))

	// ---- Sector loop (randomised order) ----------------------------------
	var totCapt int64
	claims := 0

	coords := permuteSects(planet)
	for _, c := range coords {
		p := planet.SectorAt(c.x, c.y)

		if p.Owner != 0 && (p.Popn > 0 || p.Troops > 0) {
			if star.NovaStage == 0 {
				s.produce(star, planet, p, c.x, c.y, compat, accum)
				if p.Owner != 0 {
					planet.Info[p.Owner-1].EstProduction += estProduction(p, s.races[p.Owner-1])
				}
				s.sectorSpread(planet, p, c.x, c.y, compat, &totCapt, &claims)
			} else {
				// Supernova damage
				p.Resource++
				p.Fert = uint8(float64(p.Fert) * 0.8)
				if star.NovaStage == 14 {
					p.Popn = 0
					p.Owner = 0
					p.Troops = 0
				} else {
					p.Popn = uint16(roundRand(float64(p.Popn) * 0.50))
				}
			}
			sectInfo[c.x][c.y].done = true
		}

		if (p.Popn == 0 && p.Troops == 0) || p.Owner == 0 {
			p.Owner = 0
			p.Popn = 0
			p.Troops = 0
		}
	}

	// ---- Count sectors owned per player ----------------------------------
	for _, c := range coords {
		p := planet.SectorAt(c.x, c.y)
		if p.Owner != 0 {
			planet.Info[p.Owner-1].NumSectsOwned++
		}
	}

	// ---- Exploration timer -----------------------------------------------
	if planet.ExplTimer >= 1 {
		planet.ExplTimer--
	}
	if star.NovaStage == 0 && planet.ExplTimer == 0 {
		planet.ExplTimer = 5
		allExp := false
		for i := 0; i < numRaces && !allExp && claims == 0; i++ {
			if planet.Info[i].NumSectsOwned == 0 {
				continue
			}
			allSectExplored := true
			timer := 20
			for timer > 0 && claims == 0 && !allExp {
				timer--
				for _, c := range coords {
					p := planet.SectorAt(c.x, c.y)
					if sectInfo[c.x][c.y].explored == 0 {
						allSectExplored = false
					}
					if sectInfo[c.x][c.y].explored == uint8(i+1) &&
						(intRand(0, 3) == 0) &&
						p.Owner == 0 &&
						p.Condition != model.SectorWasted &&
						s.races[i] != nil &&
						uint8(p.Condition) == s.races[i].LikesBest {
						// Found a new island!
						claims = i + 1
						if s.races[i] != nil {
							p.Popn = uint16(s.races[i].NumberSexes)
						} else {
							p.Popn = 1
						}
						p.Owner = uint8(i + 1)
						totCapt = 1
					} else {
						s.exploreSector(planet, p, c.x, c.y, i+1, sectInfo)
					}
				}
				allExp = allSectExplored
			}
		}
		if allExp {
			planet.ExplTimer = 5
		}
	}

	// ---- Environmental toxicity damage -----------------------------------
	var nukeX, nukeY int
	nuked := false
	if planet.Conditions[model.AtmosToxic] > int16(s.cfg.EnvirDamageTox) {
		nukeX = intRand(0, int(planet.MaxX)-1)
		nukeY = intRand(0, int(planet.MaxY)-1)
		p := planet.SectorAt(nukeX, nukeY)
		p.Condition = model.SectorWasted
		p.Popn = 0
		p.Owner = 0
		p.Troops = 0
		nuked = true
	}

	// ---- Telegrams for war-status changes --------------------------------
	for i := 0; i < numRaces; i++ {
		// Report sector gains and losses since the previous update.
		prev := int(prevSectsOwned[i])
		curr := int(planet.Info[i].NumSectsOwned)
		gained := 0
		lost := 0
		if curr > prev {
			gained = curr - prev
		} else if curr < prev {
			lost = prev - curr
		}
		if gained > 0 || lost > 0 {
			govNum := 0
			if i < len(s.stars[starNum].Governor) {
				govNum = int(star.Governor[i])
			}
			warMsg := fmt.Sprintf("****** Report: Planet /%s/%s ******\n WAR STATUS: %d sectors gained, %d sectors lost.\n",
				star.Name, star.PlanetNames[planetNum], gained, lost)
			s.pushTelegram(i+1, govNum, warMsg)
		}

		if planet.Info[i].AutoRep > 0 {
			planet.Info[i].AutoRep--
			msg := fmt.Sprintf("\nFrom /%s/%s\n", star.Name, star.PlanetNames[planetNum])
			if s.stinfo[starNum][planetNum].TempAdd != 0 {
				msg += fmt.Sprintf("Temp: %d to %d\n",
					planet.Conditions[model.AtmosRTemp],
					planet.Conditions[model.AtmosTemp])
			}
			msg += fmt.Sprintf("Total      Prod: %dr %df %dd\n",
				accum[i].res, accum[i].fuel, accum[i].destruct)
			if accum[i].crystals > 0 {
				msg += fmt.Sprintf("    %d crystals found\n", accum[i].crystals)
			}
			if totCapt > 0 {
				msg += fmt.Sprintf("%d sectors captured\n", totCapt)
			}
			if star.NovaStage > 0 {
				msg += fmt.Sprintf("This planet's primary is in a Stage %d nova.\n", star.NovaStage)
			}
			if nuked {
				msg += fmt.Sprintf("Environmental damage on sector %d,%d\n", nukeX, nukeY)
			}
			if planet.SlavedTo != 0 {
				msg += fmt.Sprintf("ENSLAVED to player %d\n", planet.SlavedTo)
			}
			s.pushTelegram(i+1, int(star.Governor[i]), msg)
		}
	}

	// ---- Nova stage 1: evacuation warning --------------------------------
	if star.NovaStage == 1 {
		msg := fmt.Sprintf("BULLETIN from /%s/%s\n\nStar %s is undergoing nova.\n",
			star.Name, star.PlanetNames[planetNum], star.Name)
		switch planet.Type {
		case model.PlanetEarth, model.PlanetWater, model.PlanetForest:
			msg += "Seas and rivers are boiling!\n"
		}
		msg += "This planet must be evacuated immediately!\n"
		for i := 0; i < numRaces; i++ {
			if planet.Info[i].NumSectsOwned > 0 {
				s.pushTelegram(i+1, int(star.Governor[i]), msg)
			}
		}
	}

	// ---- Resource recovery for conquered planets -------------------------
	s.doRecover(planet, star, starNum, planetNum)

	// ---- Final population / resource recount -----------------------------
	planet.Popn = 0
	planet.Troops = 0
	planet.MaxPopn = 0
	planet.TotalResources = 0
	for i := range planet.Info {
		planet.Info[i].NumSectsOwned = 0
		planet.Info[i].Popn = 0
		planet.Info[i].Troops = 0
	}

	for _, c := range coords {
		p := planet.SectorAt(c.x, c.y)
		if p.Owner != 0 {
			idx := p.Owner - 1
			planet.Info[idx].NumSectsOwned++
			planet.Info[idx].Troops += uint32(p.Troops)
			planet.Info[idx].Popn += uint32(p.Popn)
			planet.Popn += uint64(p.Popn)
			planet.Troops += uint64(p.Troops)
			if s.races[idx] != nil {
				planet.MaxPopn += uint64(maxSupport(s.races[idx], p, compat[idx],
					int(planet.Conditions[model.AtmosToxic])))
			}
			s.power[idx].Troops += uint64(p.Troops)
			s.power[idx].Popn += uint64(p.Popn)
			s.power[idx].SumEff += uint64(p.Eff)
			s.power[idx].SumMob += uint64(p.Mobilization)
		} else {
			p.Popn = 0
			p.Troops = 0
		}
		planet.TotalResources += uint64(p.Resource)
	}

	// ---- Enslaved planet: redistribute production to master --------------
	if planet.SlavedTo != 0 {
		masterIdx := planet.SlavedTo - 1
		if uint64(planet.Info[masterIdx].Popn) > planet.Popn/1000 {
			for i := 0; i < numRaces; i++ {
				if planet.Info[i].NumSectsOwned > 0 {
					planet.Info[masterIdx].Resource += uint16(int64(planet.Info[i].Resource) - accum[i].res)
					planet.Info[masterIdx].Fuel += uint16(int64(planet.Info[i].Fuel) - accum[i].fuel)
					planet.Info[masterIdx].Destruct += uint16(int64(planet.Info[i].Destruct) - accum[i].destruct)
					planet.Info[i].Resource = 0
					planet.Info[i].Fuel = 0
					planet.Info[i].Destruct = 0
					accum[i].res = 0
					accum[i].fuel = 0
					accum[i].destruct = 0
				}
			}
		} else {
			// Slave revolt!
			revolts := int(planet.Popn/1000) + 1
			for revolts > 0 {
				revolts--
				rx := intRand(0, int(planet.MaxX)-1)
				ry := intRand(0, int(planet.MaxY)-1)
				p := planet.SectorAt(rx, ry)
				if p.Popn+p.Troops > 0 {
					p.Owner = 0
					p.Popn = 0
					p.Troops = 0
					p.Condition = model.SectorWasted
				}
			}
			msg := fmt.Sprintf("\nThere has been a SLAVE REVOLT on /%s/%s!\n"+
				"All population belonging to player #%d on the planet have been killed!\n"+
				"Production now goes to their rightful owners.\n",
				star.Name, star.PlanetNames[planetNum], planet.SlavedTo)
			for i := 0; i < numRaces; i++ {
				if planet.Info[i].NumSectsOwned > 0 {
					s.pushTelegram(i+1, int(star.Governor[i]), msg)
				}
			}
			planet.SlavedTo = 0
		}
	}

	// ---- Commit production to planet stockpiles --------------------------
	for i := 0; i < numRaces; i++ {
		if planet.Info[i].NumSectsOwned == 0 {
			continue
		}
		planet.Info[i].Fuel += uint16(accum[i].fuel)
		planet.Info[i].Resource += uint16(accum[i].res)
		planet.Info[i].Destruct += uint16(accum[i].destruct)
		planet.Info[i].Crystals += uint16(accum[i].crystals)

		planet.Info[i].ProdRes = uint16(accum[i].res)
		planet.Info[i].ProdFuel = uint16(accum[i].fuel)
		planet.Info[i].ProdDest = uint16(accum[i].destruct)
		planet.Info[i].ProdCrystals = uint16(accum[i].crystals)

		if s.races[i] == nil {
			continue
		}
		race := s.races[i]
		govIdx := int(star.Governor[i])

		// Tax collection
		if race.GovShip > 0 {
			taxIncome := int64(float64(s.cfg.IncomeFactor) *
				float64(planet.Info[i].Tax) * float64(planet.Info[i].Popn))
			planet.Info[i].ProdMoney = uint64(taxIncome)
			race.Governor[govIdx].Money += uint64(taxIncome)
			// Gradually adjust tax toward new target
			diff := int(planet.Info[i].NewTax) - int(planet.Info[i].Tax)
			if diff > 5 {
				diff = 5
			} else if diff < -5 {
				diff = -5
			}
			planet.Info[i].Tax = uint8(int(planet.Info[i].Tax) + diff)
			race.Governor[govIdx].Income += uint64(taxIncome)
		} else {
			planet.Info[i].ProdMoney = 0
		}

		// Tech investment
		if race.GovShip > 0 {
			invest := uint64(planet.Info[i].TechInvest)
			if race.Governor[govIdx].Money >= invest {
				planet.Info[i].ProdTech = techProd(int(invest), int(planet.Info[i].Popn))
				race.Governor[govIdx].Money -= invest
				race.Tech += planet.Info[i].ProdTech
				race.Governor[govIdx].CostTech += invest
			} else {
				planet.Info[i].ProdTech = 0
			}
		} else {
			planet.Info[i].ProdTech = 0
		}

		// Auto-build toxic waste containers if threshold exceeded
		if planet.Info[i].ToxThresh > 0 &&
			planet.Conditions[model.AtmosToxic] >= int16(planet.Info[i].ToxThresh) {
			s.buildToxWC(planet, star, i+1, int(star.Governor[i]), starNum, planetNum)
		}
	}

	// ---- Update toxicity -------------------------------------------------
	if planet.MaxPopn > 0 && planet.Conditions[model.AtmosToxic] < 100 {
		planet.Conditions[model.AtmosToxic] += int16(planet.Popn / planet.MaxPopn)
	}
	if planet.Conditions[model.AtmosToxic] > 100 {
		planet.Conditions[model.AtmosToxic] = 100
	} else if planet.Conditions[model.AtmosToxic] < 0 {
		planet.Conditions[model.AtmosToxic] = 0
	}

	// ---- Power report accumulators ---------------------------------------
	for i := 0; i < numRaces; i++ {
		s.power[i].Resource += uint64(planet.Info[i].Resource)
		s.power[i].Destruct += uint64(planet.Info[i].Destruct)
		s.power[i].Fuel += uint64(planet.Info[i].Fuel)
		s.power[i].SectorsOwned += uint64(planet.Info[i].NumSectsOwned)
		if planet.Info[i].NumSectsOwned > 0 {
			s.power[i].PlanetsOwned++
			// Combat readiness → average mobilization
			if planet.Info[i].NumSectsOwned > 0 {
				planet.Info[i].MobPoints = uint64(accum[i].avgMob)
				avgMob := accum[i].avgMob / int64(planet.Info[i].NumSectsOwned)
				planet.Info[i].ComRead = uint8(avgMob)
			}
		} else {
			planet.Info[i].ComRead = 0
		}
		planet.Info[i].Guns = planetGuns(planet.Info[i].MobPoints)
	}
}

// ---- Sector processing helpers ------------------------------------------

// processPlanetShips iterates ships orbiting / landed on a planet and runs
// their planet-level functions (terraformers, plows, domes, quarries, VNs,
// berserkers, gas-giant fuel collection).
// Called at the start of doPlanet before the sector loop.
func (s *State) processPlanetShips(star *model.Star, planet *model.Planet,
	starNum, planetNum int, accum []prodAccum) {

	shipNo := planet.Ships
	for shipNo != 0 {
		ship := s.ships[shipNo]
		if ship == nil {
			break
		}
		if ship.Alive && ship.Rad == 0 {
			switch ship.Type {
			case model.OtypeVN:
				s.planetDoVN(ship, planet)
			case model.OtypeBers:
				if ship.Destruct == 0 || !ship.Bombard {
					s.planetDoVN(ship, planet)
				} else {
					s.doBerserker(ship, planet)
				}
			case model.OtypeTerra:
				if ship.On && landed(ship) && ship.Popn > 0 {
					if ship.Fuel >= float64(s.cfg.FuelCostTerra) {
						s.terraform(ship, planet)
					} else if !ship.Notified {
						ship.Notified = true
						s.msgOOF(ship)
					}
				}
			case model.OtypePlow:
				if ship.On && landed(ship) {
					if ship.Fuel >= float64(s.cfg.FuelCostPlow) {
						s.plow(ship, planet)
					} else if !ship.Notified {
						ship.Notified = true
						s.msgOOF(ship)
					}
				} else if ship.On {
					s.pushTelegram(int(ship.Owner), int(ship.Governor),
						fmt.Sprintf("K%d is not landed.", ship.Number))
				} else {
					s.pushTelegram(int(ship.Owner), int(ship.Governor),
						fmt.Sprintf("K%d is not switched on.", ship.Number))
				}
			case model.OtypeDome:
				if ship.On && landed(ship) {
					if ship.Resource >= uint16(s.cfg.ResCostDome) {
						s.doDome(ship, planet)
					} else {
						s.pushTelegram(int(ship.Owner), int(ship.Governor),
							fmt.Sprintf("Y%d does not have enough resources.", ship.Number))
					}
				} else if ship.On {
					s.pushTelegram(int(ship.Owner), int(ship.Governor),
						fmt.Sprintf("Y%d is not landed.", ship.Number))
				} else {
					s.pushTelegram(int(ship.Owner), int(ship.Governor),
						fmt.Sprintf("Y%d is not switched on.", ship.Number))
				}
			case model.OtypeWPlant:
				if landed(ship) {
					if ship.Resource >= uint16(s.cfg.ResCostWPlant) &&
						ship.Fuel >= float64(s.cfg.FuelCostWPlant) {
						prod := s.doWeaponPlant(ship)
						if int(ship.Owner) <= len(accum) {
							accum[ship.Owner-1].destruct += int64(prod)
						}
					} else {
						var msg string
						if ship.Resource < uint16(s.cfg.ResCostWPlant) {
							msg = fmt.Sprintf("W%d does not have enough resources.", ship.Number)
						} else {
							msg = fmt.Sprintf("W%d does not have enough fuel.", ship.Number)
						}
						s.pushTelegram(int(ship.Owner), int(ship.Governor), msg)
					}
				} else {
					s.pushTelegram(int(ship.Owner), int(ship.Governor),
						fmt.Sprintf("W%d is not landed.", ship.Number))
				}
			case model.OtypeQuarry:
				if ship.On && landed(ship) && ship.Popn > 0 {
					if ship.Fuel >= float64(s.cfg.FuelCostQuarry) {
						s.doQuarry(ship, planet, accum)
					} else if !ship.Notified {
						ship.On = false
						s.msgOOF(ship)
					}
				} else {
					var msg string
					if !ship.On {
						msg = fmt.Sprintf("q%d is not switched on.", ship.Number)
					} else if !landed(ship) {
						msg = fmt.Sprintf("q%d is not landed.", ship.Number)
					} else {
						msg = fmt.Sprintf("q%d does not have workers aboard.", ship.Number)
					}
					s.pushTelegram(int(ship.Owner), int(ship.Governor), msg)
				}
			}

			// Gas giant fuel collection for orbiting (not landed) ships.
			if !landed(ship) && planet.Type == model.PlanetGasGiant {
				var fAdd float64
				switch ship.Type {
				case model.StypeTanker:
					fAdd = s.cfg.FuelGasAddTanker
				case model.StypeHabitat:
					fAdd = s.cfg.FuelGasAddHabitat
				default:
					fAdd = s.cfg.FuelGasAdd
				}
				available := shipMaxFuel(ship) - ship.Fuel
				if fAdd > available {
					fAdd = available
				}
				ship.Fuel += fAdd
			}
		}
		shipNo = ship.NextShip
	}
}

// produce runs resource/population production for one sector.
// Mirrors produce() in dosector.c.
func (s *State) produce(star *model.Star, planet *model.Planet,
	sec *model.Sector, x, y int, compat []float64, accum []prodAccum) {

	if sec.Owner == 0 {
		return
	}
	idx := sec.Owner - 1
	if s.races[idx] == nil {
		return
	}
	race := s.races[idx]
	a := &accum[idx]

	// Resource extraction: chance proportional to efficiency
	if sec.Resource > 0 && success(int(sec.Eff)) {
		prod := roundRand(race.Metabolism) * intRand(1, int(sec.Eff))
		if prod > int(sec.Resource) {
			prod = int(sec.Resource)
		}
		sec.Resource -= uint16(prod)
		fuel := prod
		if sec.Condition == model.SectorGas {
			fuel *= 2
		}
		var dest, res int
		if success(int(sec.Mobilization)) {
			dest = prod
		} else {
			res = prod
		}
		a.fuel += int64(fuel)
		a.res += int64(res)
		a.destruct += int64(dest)
	}

	// Crystal mining: chance proportional to efficiency, requires crystal tech
	if sec.Crystals > 0 && race.HasDiscovery(model.DiscCrystal) && success(int(sec.Eff)) {
		a.crystals++
		sec.Crystals--
	}

	pinf := &planet.Info[idx]

	// Adjust mobilization toward planetary target
	if sec.Mobilization < pinf.MobSet {
		if pinf.Resource+uint16(a.res) > 0 {
			sec.Mobilization++
			a.res -= int64(roundRand(float64(s.cfg.MobCost)))
		}
	} else if sec.Mobilization > pinf.MobSet {
		sec.Mobilization--
	}
	a.avgMob += int64(sec.Mobilization)

	// Efficiency growth
	if sec.Eff < 100 {
		taxFactor := 100.0 - float64(planet.Info[idx].Tax)
		chance := roundRand(taxFactor * race.Likes[sec.Condition])
		if success(chance) {
			sec.Eff += uint8(roundRand(race.Metabolism))
			if sec.Eff >= 100 {
				plate(sec)
			}
		}
	} else {
		plate(sec)
	}

	// Fertility growth
	if sec.Condition != model.SectorWasted && race.Fertilize > 0 && sec.Fert < 100 {
		if intRand(0, 100) < int(race.Fertilize) {
			sec.Fert++
		}
	}
	if sec.Fert > 100 {
		sec.Fert = 100
	}

	// Natural repair of wasted sectors
	if sec.Condition == model.SectorWasted && success(s.cfg.NaturalRepair) {
		sec.Condition = sec.Type
	}

	// Population growth / starvation
	maxSup := maxSupport(race, sec, compat[idx], int(planet.Conditions[model.AtmosToxic]))
	diff := int(sec.Popn) - maxSup
	var delta int
	if diff < 0 {
		if int(sec.Popn) >= int(race.NumberSexes) {
			delta = roundRand(float64(-diff) * race.BirthRate)
		}
	} else {
		delta = -intRand(0, min2i(2*diff, int(sec.Popn)))
	}
	sec.Popn = uint16(int(sec.Popn) + delta)

	// Troop maintenance cost
	if sec.Troops > 0 && s.races[idx] != nil {
		govIdx := int(star.Governor[idx])
		s.races[idx].Governor[govIdx].Maintain += uint64(s.cfg.UpdateTroopCost) * uint64(sec.Troops)
	} else if sec.Popn == 0 {
		sec.Owner = 0
	}
}

// sectorSpread migrates population from one sector to adjacent sectors.
// Mirrors spread() in dosector.c.
func (s *State) sectorSpread(planet *model.Planet, sec *model.Sector,
	x, y int, compat []float64, totCapt *int64, claims *int) {

	if sec.Owner == 0 {
		return
	}
	if planet.SlavedTo != 0 && planet.SlavedTo != sec.Owner {
		return // enslaved planet: colonists don't move
	}
	idx := sec.Owner - 1
	if s.races[idx] == nil {
		return
	}
	race := s.races[idx]

	// Number of people willing to move
	people := roundRand(race.Adventurism*float64(sec.Popn)*
		(100.0-float64(sec.Fert))/100.0) - int(race.NumberSexes)

	checks := roundRand(6.0 * race.Adventurism)
	for people > 0 && checks > 0 {
		j := intRand(0, 7)
		dx := xAdj[j]
		dy := yAdj[j]
		s.migrate2(planet, x+dx, y+dy, sec, &people, compat, totCapt, claims)
		checks--
	}
}

var xAdj = [8]int{-1, 0, 1, -1, 1, -1, 0, 1}
var yAdj = [8]int{1, 1, 1, 0, 0, -1, -1, -1}

// migrate2 attempts to move people from ps into the sector at (xd, yd).
// Mirrors Migrate2() in dosector.c.
func (s *State) migrate2(planet *model.Planet, xd, yd int, ps *model.Sector,
	people *int, compat []float64, totCapt *int64, claims *int) {

	if yd > int(planet.MaxY)-1 || yd < 0 {
		return
	}
	if xd < 0 {
		xd = int(planet.MaxX) - 1
	} else if xd > int(planet.MaxX)-1 {
		xd = 0
	}

	pd := planet.SectorAt(xd, yd)
	if pd.Owner == 0 {
		idx := ps.Owner - 1
		if s.races[idx] == nil {
			return
		}
		move := int(float64(*people) * compat[idx] *
			s.races[idx].Likes[pd.Condition] / 100.0)
		if move <= 0 {
			return
		}
		*people -= move
		pd.Popn += uint16(move)
		ps.Popn -= uint16(move)
		pd.Owner = ps.Owner
		*totCapt++
		*claims = int(ps.Owner)
	}
}

// exploreSector marks a sector and its neighbours as explored by player p.
// Mirrors explore() in dosector.c.
func (s *State) exploreSector(planet *model.Planet, sec *model.Sector,
	x, y, playerNum int, sectInfo [][]sectInfoCell) {

	mx := int(planet.MaxX)
	my := int(planet.MaxY)

	if sectInfo[x][y].explored != 0 {
		// Expand from already-explored cell
		sectInfo[posMod(x-1, mx)][y].explored = uint8(playerNum)
		sectInfo[posMod(x+1, mx)][y].explored = uint8(playerNum)
		if y == 0 {
			sectInfo[x][1].explored = uint8(playerNum)
		} else if y == my-1 {
			sectInfo[x][y-1].explored = uint8(playerNum)
		} else {
			sectInfo[x][y-1].explored = uint8(playerNum)
			sectInfo[x][y+1].explored = uint8(playerNum)
		}
	} else if sec.Owner == uint8(playerNum) {
		sectInfo[x][y].explored = uint8(playerNum)
	}
}

// plate converts a sector to PLATED once efficiency reaches 100.
// Mirrors plate() in dosector.c.
func plate(s *model.Sector) {
	s.Eff = 100
	if s.Condition != model.SectorGas {
		s.Condition = model.SectorPlated
	}
}

// ---- Ship device actions -------------------------------------------------

// terraform runs one move-and-terraform cycle for a terraformer device.
// Mirrors terraform() in doplanet.c.
func (s *State) terraform(ship *model.Ship, planet *model.Planet) {
	if !s.moveShipOnPlanet(ship, planet) {
		return
	}
	sec := planet.SectorAt(int(ship.LandX), int(ship.LandY))
	if s.races[ship.Owner-1] == nil {
		return
	}
	race := s.races[ship.Owner-1]

	if sec.Condition != uint8(race.LikesBest) &&
		sec.Condition != model.SectorGas &&
		success(int(100-ship.Damage)*int(ship.Popn)/int(ship.MaxCrew)) {
		sec.Condition = uint8(race.LikesBest)
		sec.Eff = 0
		sec.Mobilization = 0
		sec.Popn = 0
		sec.Troops = 0
		sec.Owner = 0
		ship.Fuel -= float64(s.cfg.FuelCostTerra)
		if intRand(0, 1) == 0 && planet.Conditions[model.AtmosToxic] < 100 {
			planet.Conditions[model.AtmosToxic]++
		}
		if ship.Fuel < float64(s.cfg.FuelCostTerra) && !ship.Notified {
			ship.Notified = true
			s.msgOOF(ship)
		}
	} else if sec.Condition == uint8(race.LikesBest) {
		s.pushTelegram(int(ship.Owner), int(ship.Governor),
			fmt.Sprintf(" T%d is full of zealots!!!", ship.Number))
	} else if sec.Condition == model.SectorGas {
		s.pushTelegram(int(ship.Owner), int(ship.Governor),
			fmt.Sprintf(" T%d is trying to terraform gas.", ship.Number))
	}
}

// plow runs one move-and-plow cycle to increase sector fertility.
// Mirrors plow() in doplanet.c.
func (s *State) plow(ship *model.Ship, planet *model.Planet) {
	if !s.moveShipOnPlanet(ship, planet) {
		return
	}
	if s.races[ship.Owner-1] == nil {
		return
	}
	race := s.races[ship.Owner-1]
	sec := planet.SectorAt(int(ship.LandX), int(ship.LandY))

	if race.Likes[sec.Condition] > 0 && sec.Fert < 100 {
		if ship.Fuel < float64(s.cfg.FuelCostPlow) {
			if !ship.Notified {
				ship.Notified = true
				s.msgOOF(ship)
			}
			return
		}
		adjust := roundRand(10 * (0.01 * (100.0 - float64(ship.Damage)) *
			float64(ship.Popn) / float64(ship.MaxCrew)))
		sec.Fert = uint8(min2i(100, int(sec.Fert)+adjust))
		if sec.Fert >= 100 {
			s.pushTelegram(int(ship.Owner), int(ship.Governor),
				fmt.Sprintf(" K%d is full of zealots!!!", ship.Number))
		}
		ship.Fuel -= float64(s.cfg.FuelCostPlow)
		if intRand(0, 1) == 0 && planet.Conditions[model.AtmosToxic] < 100 {
			planet.Conditions[model.AtmosToxic]++
		}
	}
}

// doDome raises the efficiency of the sector the dome is landed on.
// Mirrors do_dome() in doplanet.c.
func (s *State) doDome(ship *model.Ship, planet *model.Planet) {
	sec := planet.SectorAt(int(ship.LandX), int(ship.LandY))
	if sec.Eff >= 100 {
		s.pushTelegram(int(ship.Owner), int(ship.Governor),
			fmt.Sprintf(" Y%d is full of zealots!!!", ship.Number))
		return
	}
	adjust := roundRand(0.05 * (100.0 - float64(ship.Damage)) *
		float64(ship.Popn) / float64(ship.MaxCrew))
	sec.Eff += uint8(adjust)
	if sec.Eff > 100 {
		sec.Eff = 100
	}
	if ship.Resource >= uint16(s.cfg.ResCostDome) {
		ship.Resource -= uint16(s.cfg.ResCostDome)
	}
}

// doQuarry strip-mines the current sector, generating resources.
// Mirrors do_quarry() in doplanet.c.
func (s *State) doQuarry(ship *model.Ship, planet *model.Planet, accum []prodAccum) {
	sec := planet.SectorAt(int(ship.LandX), int(ship.LandY))
	if ship.Fuel < float64(s.cfg.FuelCostQuarry) {
		if !ship.Notified {
			s.msgOOF(ship)
		}
		ship.Notified = true
		return
	}
	sec.Condition = model.SectorWasted
	if s.races[ship.Owner-1] == nil {
		return
	}
	prod := roundRand(s.races[ship.Owner-1].Metabolism *
		float64(ship.Popn) / float64(ship.MaxCrew))
	ship.Fuel -= float64(s.cfg.FuelCostQuarry)
	accum[ship.Owner-1].res += int64(prod)
	tox := intRand(0, intRand(0, prod))
	newTox := int(planet.Conditions[model.AtmosToxic]) + tox
	if newTox > 100 {
		newTox = 100
	}
	planet.Conditions[model.AtmosToxic] = int16(newTox)
	if int(sec.Fert) >= prod {
		sec.Fert -= uint8(prod)
	} else {
		sec.Fert = 0
	}
}

// doBerserker causes a berserker to bombard its target planet.
// Mirrors do_berserker() in doplanet.c.
func (s *State) doBerserker(ship *model.Ship, planet *model.Planet) {
	if ship.WhatDest != model.LevelPlan || ship.WhatOrbits != model.LevelPlan {
		return
	}
	if landed(ship) {
		return
	}
	if ship.StOrbits != ship.DestStar || ship.PNumOrbits != ship.DestPNum {
		return
	}

	var longBuf, shortBuf string
	s.shootShipToPlanet(ship, planet,
		int(ship.Destruct),
		intRand(0, int(planet.MaxX)-1),
		intRand(0, int(planet.MaxY)-1),
		1, 0, 1,
		&longBuf, &shortBuf)
	if longBuf != "" {
		s.postCombat(longBuf)
	}
}

// planetDoVN is implemented in vn.go.

// ---- Resource recovery on conquered planets ------------------------------

// doRecover redistributes stockpiles from defeated players to victors.
// Mirrors do_recover() in doplanet.c.
func (s *State) doRecover(planet *model.Planet, star *model.Star, starNum, planetNum int) {
	numRaces := s.numRaces()

	var ownerBits uint64 // bit i set → player i+1 owns sectors here
	var stolenRes, stolenDes, stolenFuel, stolenXtal int
	allBuddies := true
	ownerCount := 0

	for i := 0; i < numRaces; i++ {
		if planet.Info[i].NumSectsOwned > 0 {
			ownerCount++
			ownerBits |= 1 << uint(i)
			// Check if all co-owners are allied with each other
			for j := 0; j < i && allBuddies; j++ {
				if ownerBits&(1<<uint(j)) != 0 {
					ri := s.races[i]
					rj := s.races[j]
					if ri == nil || rj == nil ||
						!ri.IsAllied(j+1) || !rj.IsAllied(i+1) {
						allBuddies = false
					}
				}
			}
		} else if i != 0 { // Can't steal from God (player 1)
			stolenRes += int(planet.Info[i].Resource)
			stolenDes += int(planet.Info[i].Destruct)
			stolenFuel += int(planet.Info[i].Fuel)
			stolenXtal += int(planet.Info[i].Crystals)
		}
	}

	if !allBuddies || ownerCount == 0 ||
		(stolenRes+stolenDes+stolenFuel+stolenXtal == 0) {
		return
	}

	// Distribute loot among victors.
	shares := ownerCount
	var givenRes, givenDes, givenFuel, givenXtal int

	for i := 0; i < numRaces && shares > 1; i++ {
		if ownerBits&(1<<uint(i)) == 0 {
			continue
		}
		res := roundRand(float64(stolenRes) / float64(shares))
		if res+givenRes > stolenRes {
			res = stolenRes - givenRes
		}
		des := roundRand(float64(stolenDes) / float64(shares))
		if des+givenDes > stolenDes {
			des = stolenDes - givenDes
		}
		fuel := roundRand(float64(stolenFuel) / float64(shares))
		if fuel+givenFuel > stolenFuel {
			fuel = stolenFuel - givenFuel
		}
		xtal := roundRand(float64(stolenXtal) / float64(shares))
		if xtal+givenXtal > stolenXtal {
			xtal = stolenXtal - givenXtal
		}
		planet.Info[i].Resource += uint16(res)
		givenRes += res
		planet.Info[i].Destruct += uint16(des)
		givenDes += des
		planet.Info[i].Fuel += uint16(fuel)
		givenFuel += fuel
		planet.Info[i].Crystals += uint16(xtal)
		givenXtal += xtal
		shares--

		msg := fmt.Sprintf("Recovery Report: Planet /%s/%s\n%-14.14s %5d %5d %5d %5d",
			star.Name, star.PlanetNames[planetNum],
			s.races[i].Name, res, des, fuel, xtal)
		for j := 0; j < numRaces; j++ {
			if ownerBits&(1<<uint(j)) != 0 {
				s.pushTelegram(j+1, int(star.Governor[j]), msg)
			}
		}
	}

	// Remainder goes to the last victor.
	for i := 0; i < numRaces; i++ {
		if ownerBits&(1<<uint(i)) == 0 {
			continue
		}
		res := stolenRes - givenRes
		des := stolenDes - givenDes
		fuel := stolenFuel - givenFuel
		xtal := stolenXtal - givenXtal
		planet.Info[i].Resource += uint16(res)
		planet.Info[i].Destruct += uint16(des)
		planet.Info[i].Fuel += uint16(fuel)
		planet.Info[i].Crystals += uint16(xtal)

		summary := fmt.Sprintf("%-14.14s %5d %5d %5d %5d\nTotal: %5d %5d %5d %5d",
			s.races[i].Name, res, des, fuel, xtal,
			stolenRes, stolenDes, stolenFuel, stolenXtal)
		for j := 0; j < numRaces; j++ {
			if ownerBits&(1<<uint(j)) != 0 {
				s.pushTelegram(j+1, int(star.Governor[j]), summary)
			}
		}
		break
	}

	// Zero out defeated players' stockpiles.
	for i := 1; i < numRaces; i++ { // skip player 1 (God)
		if ownerBits&(1<<uint(i)) == 0 {
			planet.Info[i].Resource = 0
			planet.Info[i].Destruct = 0
			planet.Info[i].Fuel = 0
			planet.Info[i].Crystals = 0
		}
	}
}

// ---- Toxic waste container auto-build ------------------------------------

// buildToxWC auto-constructs a toxic waste container when toxicity exceeds
// the player's threshold. Mirrors the inline code in doplanet.c.
func (s *State) buildToxWC(planet *model.Planet, star *model.Star,
	playerNum, governor, starNum, planetNum int) {

	idx := playerNum - 1
	cost := s.shipcostForType(model.OtypeToxWC, s.races[idx])
	if planet.Info[idx].Resource < uint16(cost) {
		return
	}
	planet.Info[idx].Resource -= uint16(cost)

	shipNum, err := s.store.AllocShipNum()
	if err != nil {
		return
	}

	ship := &model.Ship{
		Number:      shipNum,
		Type:        model.OtypeToxWC,
		Owner:       uint8(playerNum),
		Governor:    uint8(governor),
		Alive:       true,
		Active:      true,
		Docked:      true,
		WhatOrbits:  model.LevelPlan,
		WhatDest:    model.LevelPlan,
		StOrbits:    uint8(starNum),
		PNumOrbits:  uint8(planetNum),
		DestStar:    uint8(starNum),
		DestPNum:    uint8(planetNum),
		XPos:        star.XPos + planet.XPos,
		YPos:        star.YPos + planet.YPos,
		LandX:       uint8(intRand(0, int(planet.MaxX)-1)),
		LandY:       uint8(intRand(0, int(planet.MaxY)-1)),
		Armor:       uint8(model.Shipdata[model.OtypeToxWC][model.AbilArmor]),
		MaxCrew:     uint16(model.Shipdata[model.OtypeToxWC][model.AbilMaxCrew]),
		MaxResource: uint16(model.Shipdata[model.OtypeToxWC][model.AbilCargo]),
		MaxFuel:     uint16(model.Shipdata[model.OtypeToxWC][model.AbilFuelCap]),
		MaxDestruct: uint16(model.Shipdata[model.OtypeToxWC][model.AbilDestCap]),
		MaxSpeed:    uint16(model.Shipdata[model.OtypeToxWC][model.AbilSpeed]),
		Name:        fmt.Sprintf("Scum%04d", shipNum),
	}
	t := int(planet.Conditions[model.AtmosToxic])
	if t > s.cfg.ToxMax {
		t = s.cfg.ToxMax
	}
	planet.Conditions[model.AtmosToxic] -= int16(t)
	ship.Special.Waste = &model.WasteSpecial{Toxic: uint8(t)}
	ship.Mass = 1.0
	ship.BaseMass = 1.0

	s.ships[shipNum] = ship
	s.insertShipPlan(planet, ship)
}

// ---- Planet utility functions --------------------------------------------

// maxSupport returns the maximum population a sector can sustain.
// Mirrors maxsupport() in max.c.
func maxSupport(race *model.Race, sec *model.Sector, compat float64, toxic int) int {
	if race.Likes[sec.Condition] == 0 {
		return 0
	}
	a := (float64(sec.Eff) + 1.0) * float64(sec.Fert)
	b := 0.01 * compat
	return int(a * b * 0.01 * (100.0 - float64(toxic)))
}

// compatibility returns the environmental compatibility of a race with a
// planet (0–100 scale). Mirrors compatibility() in max.c.
func compatibility(planet *model.Planet, race *model.Race) float64 {
	tempDiff := 0.1 * float64(planet.Conditions[model.AtmosTemp]-race.Conditions[model.AtmosTemp])
	sum := 1.0 - math.Abs(tempDiff)/100.0

	atmos := 1.0
	for i := model.AtmosTemp + 1; i <= model.AtmosOther; i++ {
		diff := float64(planet.Conditions[i] - race.Conditions[i])
		atmos *= 1.0 - math.Abs(diff)/100.0
	}
	sum *= atmos
	sum *= 100.0 - float64(planet.Conditions[model.AtmosToxic])

	if sum < 0 {
		return 0
	}
	return sum
}

// estProduction estimates the production of a sector (for autoreport).
// Mirrors est_production() in doplanet.c.
func estProduction(sec *model.Sector, race *model.Race) float64 {
	if race == nil {
		return 0
	}
	return race.Metabolism * float64(sec.Eff) * float64(sec.Eff) / 200.0
}

// planetGuns computes the number of planetary guns from total mob points.
// From the comment in vars.h: "number of planet guns (mob/5)".
func planetGuns(mobPoints uint64) uint8 {
	g := mobPoints / 5
	if g > 255 {
		return 255
	}
	return uint8(g)
}

// techProd computes the technology production from investment and population.
// Mirrors tech_prod() in tech.c.
// Formula: TECH_INVEST * log10(investment * (popn/10000) + 1)
// where TECH_INVEST = 0.01 (from tweakables.h).
func techProd(invest, popn int) float64 {
	if invest == 0 || popn == 0 {
		return 0
	}
	scale := float64(popn) / 10000.0
	return 0.01 * math.Log10(float64(invest)*scale+1.0)
}

// shipCost computes the base build cost of a ship type for a given race.
// Now delegates to the full implementation in build.go.
func (s *State) shipCostByType(stype model.ShipType, race *model.Race) int {
	return s.shipcostForType(stype, race)
}

// ---- Sector iteration helpers -------------------------------------------

// permuteSects returns a Fisher-Yates-shuffled list of all sector coordinates
// on the planet. Mirrors PermuteSects() + Getxysect() from perm.c.
func permuteSects(planet *model.Planet) []sectorCoord {
	total := int(planet.MaxX) * int(planet.MaxY)
	coords := make([]sectorCoord, total)
	for i, x, y := 0, 0, 0; i < total; i++ {
		coords[i] = sectorCoord{x, y}
		x++
		if x >= int(planet.MaxX) {
			x = 0
			y++
		}
	}
	for i := total - 1; i > 0; i-- {
		j := intRand(0, i)
		coords[i], coords[j] = coords[j], coords[i]
	}
	return coords
}

// moveShipOnPlanet advances a terraformer/plow along its programmed path.
// Mirrors moveship_onplanet() in doplanet.c.
func (s *State) moveShipOnPlanet(ship *model.Ship, planet *model.Planet) bool {
	if ship.Special.Terraform == nil {
		return false
	}
	idx := int(ship.Special.Terraform.Index)
	if idx >= len(ship.Class) {
		return false
	}
	ch := ship.Class[idx]
	if ch == 's' {
		ship.On = false
		return false
	}
	if ch == 'c' {
		ship.Special.Terraform.Index = 0
		idx = 0
		ch = ship.Class[0]
	}

	x, y, bounced := getMove(ch, int(ship.LandX), int(ship.LandY), planet)
	if y >= int(planet.MaxY) {
		bounced = true
		y -= 2
	} else if y < 0 {
		bounced = true
		y = 1
	}
	if planet.MaxY == 1 {
		y = 0
	}

	if idx+1 < len(ship.Class) && ship.Class[idx+1] != 0 {
		ship.Special.Terraform.Index++
		if idx+2 >= len(ship.Class) && !ship.Notified {
			ship.Notified = true
			s.pushTelegram(int(ship.Owner), int(ship.Governor),
				fmt.Sprintf("%s is out of orders at /%s/%s.",
					shipDisplayName(ship),
					s.starName(int(ship.StOrbits)),
					s.planetName(int(ship.StOrbits), int(ship.PNumOrbits))))
		}
	} else if bounced {
		// Reverse direction on pole bounce
		cur := ship.Class[idx]
		if cur > '5' {
			ship.Class = replaceChar(ship.Class, idx, cur-6)
		} else {
			ship.Class = replaceChar(ship.Class, idx, cur+6)
		}
	}

	ship.LandX = uint8(x)
	ship.LandY = uint8(y)
	return true
}

// getMove interprets a movement character and returns the new (x, y) position.
// Mirrors get_move() from move.c.
func getMove(ch byte, x, y int, planet *model.Planet) (int, int, bool) {
	// Direction characters: 1=SW, 2=S, 3=SE, 4=W, 6=E, 7=NW, 8=N, 9=NE
	switch ch {
	case '1':
		x--; y++
	case '2':
		y++
	case '3':
		x++; y++
	case '4':
		x--
	case '6':
		x++
	case '7':
		x--; y--
	case '8':
		y--
	case '9':
		x++; y--
	}
	// Wrap x around planet
	if x < 0 {
		x = int(planet.MaxX) - 1
	} else if x >= int(planet.MaxX) {
		x = 0
	}
	return x, y, false
}

// ---- Ship orbit list management -----------------------------------------

// insertShipPlan inserts a ship into the planet's orbit chain.
// Mirrors insert_sh_plan() in GB_server.c.
func (s *State) insertShipPlan(planet *model.Planet, ship *model.Ship) {
	ship.NextShip = planet.Ships
	planet.Ships = ship.Number
}

// ---- Ship utility helpers -----------------------------------------------

// landed returns true if the ship is physically on a planet surface.
func landed(ship *model.Ship) bool {
	return ship.Docked && ship.WhatOrbits == model.LevelPlan
}

// shipMaxFuel returns a ship's maximum fuel capacity, using the type table
// for factories (mirrors the Max_fuel macro in ships.h).
func shipMaxFuel(ship *model.Ship) float64 {
	if ship.Type == model.OtypeFactory {
		return float64(model.Shipdata[ship.Type][model.AbilFuelCap])
	}
	return float64(ship.MaxFuel)
}

// ---- Telegram helpers ---------------------------------------------------

// pushTelegram delivers a telegram to a player/governor inbox.
// If the governor is online the message is sent immediately; otherwise it is
// stored in the DB for later retrieval with the `read` command.
func (s *State) pushTelegram(playerNum, governor int, text string) {
	// Try live delivery first (mirrors warn() in GB_server.c).
	delivered := false
	if s.registry != nil {
		delivered = s.registry.Notify(playerNum, governor, text)
		if !delivered {
			delivered = s.registry.Notify(playerNum, 0, text)
		}
	}
	if !delivered {
		_ = s.store.PushTelegram(playerNum, governor, timestampedMsg(text))
	}
}

// pushTelegramRace delivers a telegram to every active governor of a race.
// Mirrors push_telegram_race() in tele.c.
func (s *State) pushTelegramRace(playerNum int, text string) {
	race := s.raceByNum(playerNum)
	if race == nil {
		return
	}
	for j := 0; j <= model.MaxGovernors; j++ {
		if race.Governor[j].Active {
			s.pushTelegram(playerNum, j, text)
		}
	}
}

// post appends a timestamped entry to one of the four news streams.
// Mirrors post() in tele.c / teleg_send.c.
// newsType is one of model.NewsDeclaration, NewsTransfer, NewsCombat, NewsAnnounce.
func (s *State) post(newsType uint8, msg string) {
	// Replace C-convention separators.
	out := make([]byte, 0, len(msg))
	for i := 0; i < len(msg); i++ {
		switch msg[i] {
		case ';':
			out = append(out, '\n')
		case '|':
			out = append(out, '\t')
		default:
			out = append(out, msg[i])
		}
	}
	_, _ = s.store.AppendNews(newsType, timestampedMsg(string(out)))
}

// timestampedMsg prepends a "MM/DD HH:MM:SS " timestamp to a message,
// mirroring the sprintf in push_telegram / post in tele.c.
func timestampedMsg(text string) string {
	now := time.Now()
	return fmt.Sprintf("%2d/%2d %02d:%02d:%02d %s",
		now.Month(), now.Day(),
		now.Hour(), now.Minute(), now.Second(),
		text)
}

// msgOOF sends an "out of fuel" notification for a ship.
func (s *State) msgOOF(ship *model.Ship) {
	s.pushTelegram(int(ship.Owner), int(ship.Governor),
		fmt.Sprintf("Ship %d is out of fuel at %s.",
			ship.Number, s.shipLocation(ship)))
}

// ---- Miscellaneous helpers ----------------------------------------------

// numRaces returns the count of enrolled races.
func (s *State) numRaces() int {
	n := 0
	for _, r := range s.races {
		if r != nil {
			n++
		}
	}
	return n
}

// posMod returns a non-negative modulus (mirrors the mod() macro in tweakables.h).
func posMod(a, b int) int {
	r := a % b
	if r < 0 {
		r += b
	}
	return r
}

// min2i returns the smaller of two ints.
func min2i(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// starName returns the name of a star by number.
func (s *State) starName(starNum int) string {
	if starNum >= 0 && starNum < len(s.stars) && s.stars[starNum] != nil {
		return s.stars[starNum].Name
	}
	return "?"
}

// planetName returns the name of a planet.
func (s *State) planetName(starNum, planetNum int) string {
	if starNum >= 0 && starNum < len(s.stars) && s.stars[starNum] != nil &&
		planetNum < model.MaxPlanets {
		return s.stars[starNum].PlanetNames[planetNum]
	}
	return "?"
}

// shipLocation returns a human-readable location string for a ship.
func (s *State) shipLocation(ship *model.Ship) string {
	switch ship.WhatOrbits {
	case model.LevelStar:
		return fmt.Sprintf("/%s", s.starName(int(ship.StOrbits)))
	case model.LevelPlan:
		return fmt.Sprintf("/%s/%s",
			s.starName(int(ship.StOrbits)),
			s.planetName(int(ship.StOrbits), int(ship.PNumOrbits)))
	default:
		return fmt.Sprintf("/(%.0f,%.0f)", ship.XPos, ship.YPos)
	}
}

// shipDisplayName returns the short letter+number name for a ship.
func shipDisplayName(ship *model.Ship) string {
	if int(ship.Type) < len(model.ShipLetters) {
		return fmt.Sprintf("%c%d", model.ShipLetters[ship.Type], ship.Number)
	}
	return fmt.Sprintf("?%d", ship.Number)
}

// replaceChar returns a new string with the character at index i replaced.
func replaceChar(s string, i int, c byte) string {
	b := []byte(s)
	if i >= 0 && i < len(b) {
		b[i] = c
	}
	return string(b)
}
