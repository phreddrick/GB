package game

// combat.go implements the core ship-vs-ship, ship-vs-planet, and
// planet-vs-ship fire resolution engine, plus ship capture logic.
//
// Sources ported:
//   shootblast.c – shoot_ship_to_ship(), shoot_planet_to_ship(),
//                  shoot_ship_to_planet(), do_radiation(), do_damage(),
//                  do_critical_hits(), do_collateral(), hit_odds(),
//                  Num_hits(), CEW_hit(), gun_range(), tele_range(),
//                  current_caliber(), getdefense(), p_factor(),
//                  mutate_sector(), ship_disposition(), planet_guns()
//   fire.c       – retal_strength(), check_overload(),
//                  check_retal_strength(), laser_on(), adjacent()
//   capture.c    – capture_stuff() (command in commands.go)
//   shlmisc.c    – morale_factor()

import (
	"fmt"
	"math"

	"github.com/phreddrick/GB/go/internal/model"
)

// ---- Sector defense values -------------------------------------------------

// defenseData is the defense multiplier for each sector condition type.
// Mirrors Defensedata[] in move.c: indexed by sector condition (0–8).
// 0=ocean/plains/..., 7=plated(4), 8=wasted(0).
var defenseData = [9]int{1, 1, 3, 2, 2, 3, 2, 4, 0}

// Caliber constants (mirrors LIGHT/MEDIUM/HEAVY/NONE in ships.h)
const (
	caliberNone   = 0
	caliberLight  = 1
	caliberMedium = 2
	caliberHeavy  = 3
)

// ---- Math helpers ----------------------------------------------------------

// moraleFactor converts a morale difference to a combat multiplier.
// Mirrors morale_factor() in shlmisc.c.
func moraleFactor(x float64) float64 {
	return math.Atan(x/10000.0)/math.Pi + 0.5
}

// pFactor computes the penetration probability factor between tech levels.
// Mirrors p_factor() in shootblast.c.
func pFactor(attacker, defender float64) float64 {
	return (2.0 / math.Pi) * math.Atan(5.0*(attacker+1.0)/(defender+1.0))
}

// gunRange returns the maximum firing range for a ship.
// mode 0 = use ship tech, mode 1 = use race tech.
// Mirrors gun_range() in shootblast.c.
func (s *State) gunRange(race *model.Race, ship *model.Ship, mode int) float64 {
	if mode == 1 && race != nil {
		return logscale(int(race.Tech+1.0)) * s.cfg.SystemSize
	}
	return logscale(int(ship.Tech+1.0)) * s.cfg.SystemSize
}

// teleRange returns the telescope observation range.
// Mirrors tele_range() in shootblast.c.
func teleRange(shipType model.ShipType, tech float64) float64 {
	if shipType == model.OtypeGtele {
		return math.Log1p(tech)*400 + model.SystemGravConst/375
	}
	return math.Log1p(tech)*1500 + model.SystemGravConst/100
}

// hitOdds computes the per-gun hit probability (0–100) and a range factor.
// Mirrors hit_odds() in shootblast.c.
func hitOdds(rng float64, factor *int, tech float64, fdam, fevade, tevade,
	fspeed, tspeed, body, caliber, defense int) int {
	if caliber == caliberNone {
		*factor = 0
		return 0
	}
	a := math.Log10(1.0+tech) * 80.0 * math.Pow(float64(body), 0.33333)
	b := 72.0 / ((2.0 + float64(tevade)) * (2.0 + float64(fevade)) *
		(18.0 + float64(tspeed) + float64(fspeed)))
	c := a * b / float64(caliber)
	*factor = int(c * (1.0 - float64(fdam)/100.0))
	odds := 0
	if *factor > 0 {
		odds = int(float64(*factor*100) / float64(*factor+int(rng)))
	}
	odds = int(float64(odds) * (1.0 - 0.1*float64(defense)))
	return odds
}

// numHits rolls hit dice and returns how many shots connect.
// Mirrors Num_hits() in shootblast.c.
func numHits(dist float64, focus, guns int, tech float64, fdam,
	fevade, tevade, fspeed, tspeed, body, caliber, defense int) (hits, hitProb int) {
	var factor int
	prob := hitOdds(dist, &factor, tech, fdam, fevade, tevade, fspeed, tspeed,
		body, caliber, defense)
	if focus != 0 {
		if success(prob * prob / 100) {
			hits = guns
		}
		hitProb = prob * prob / 100
	} else {
		for i := 0; i < guns; i++ {
			if success(prob) {
				hits++
			}
		}
		hitProb = prob
	}
	return hits, hitProb
}

// cewHitOdds computes the hit probability for a CEW at distance.
// Mirrors cew_hit_odds() in shootblast.c.
func cewHitOdds(dist float64, cewRange int) int {
	factor := (dist + 1.0) / (float64(cewRange) + 1.0)
	return int(100.0 * math.Exp(-50.0*(factor-1.0)*(factor-1.0)))
}

// cewHit returns 1 if the CEW scores a hit.
// Mirrors CEW_hit() in shootblast.c.
func cewHit(dist float64, cewRange int) int {
	if success(cewHitOdds(dist, cewRange)) {
		return 1
	}
	return 0
}

// ---- Ship combat helpers ---------------------------------------------------

// shipDisposition extracts the evade/speed/body values for hit calculations.
// Mirrors ship_disposition() in shootblast.c.
func shipDisposition(ship *model.Ship) (evade, speed, body int) {
	body = int(ship.Size)
	if ship.Active && !ship.Docked &&
		(ship.WhatDest != model.LevelUniv || ship.Navigate.On) {
		if ship.Protect.Evade {
			evade = 1
		}
		speed = int(ship.Speed)
	}
	return
}

// getDefense returns the sector defense bonus for a landed ship.
// Mirrors getdefense() in shootblast.c.
func (s *State) getDefense(ship *model.Ship) int {
	if !landed(ship) {
		return 0
	}
	planet := s.planets[ship.StOrbits][ship.PNumOrbits]
	if planet == nil {
		return 0
	}
	sec := planet.SectorAt(int(ship.LandX), int(ship.LandY))
	if sec == nil {
		return 0
	}
	cond := int(sec.Condition)
	if cond < 0 || cond >= len(defenseData) {
		return 0
	}
	return 2 * defenseData[cond]
}

// currentCaliber returns the effective weapon caliber for a ship.
// Mirrors current_caliber() in shootblast.c.
// Replaces the stub in ship.go.
func (s *State) currentCaliber(ship *model.Ship) int {
	if ship.Laser > 0 && ship.FireLaser > 0 {
		return caliberLight
	}
	switch ship.Type {
	case model.StypeMine:
		return caliberLight
	case model.StypeMissile:
		return caliberHeavy
	}
	switch ship.Guns {
	case model.GunPrimary:
		return int(ship.PrimType)
	case model.GunSecondary:
		return int(ship.SecType)
	}
	return caliberNone
}

// retalStrength returns the maximum retaliation strength of a ship.
// Mirrors retal_strength() in fire.c.
// Replaces the stub in ship.go.
func (s *State) retalStrength(ship *model.Ship) int {
	if !ship.Alive {
		return 0
	}
	// Land-based ships with no speed can't retaliate unless landed.
	if model.Shipdata[ship.Type][model.AbilSpeed] == 0 && !landed(ship) {
		return 0
	}
	if ship.Popn == 0 && ship.Type != model.OtypeBers {
		return 0
	}

	var avail int
	switch ship.Guns {
	case model.GunPrimary:
		if ship.Type == model.StypeFighter || ship.Type == model.OtypeAFV || ship.Type == model.OtypeBers {
			avail = int(ship.Primary)
		} else {
			avail = min2i(int(ship.Popn), int(ship.Primary))
		}
	case model.GunSecondary:
		if ship.Type == model.StypeFighter || ship.Type == model.OtypeAFV || ship.Type == model.OtypeBers {
			avail = int(ship.Secondary)
		} else {
			avail = min2i(int(ship.Popn), int(ship.Secondary))
		}
	default:
		return 0
	}
	avail = min2i(int(ship.Retaliate), avail)
	return min2i(int(ship.Destruct), avail)
}

// laserOn returns true if the ship has an active laser crystal.
// Mirrors laser_on() in fire.c.
func laserOn(ship *model.Ship) bool {
	return ship.Laser > 0 && ship.FireLaser > 0
}

// hasSwitch returns true if the ship type has an on/off switch.
// Mirrors has_switch(s) macro in ships.h.
func hasSwitch(ship *model.Ship) bool {
	return model.Shipdata[ship.Type][model.AbilHasSwitch] != 0
}

// checkRetalStrength sets *strength to the ship's available retaliation.
// Mirrors check_retal_strength() in fire.c.
func (s *State) checkRetalStrength(ship *model.Ship, strength *int) {
	*strength = 0
	if !ship.Active || !ship.Alive {
		return
	}
	if laserOn(ship) {
		*strength = min2i(int(ship.FireLaser), int(ship.Fuel)/2)
	} else {
		*strength = s.retalStrength(ship)
	}
}

// checkOverload checks for crystal overload when firing lasers or CEWs.
// May kill the ship or zero out strength on critical failure.
// Mirrors check_overload() in fire.c.
func (s *State) checkOverload(ship *model.Ship, cew int, strength *int) {
	if !laserOn(ship) && cew == 0 {
		return
	}
	techSafe := (1.0 - 0.01*float64(ship.Damage)) * ship.Tech / 2.0
	if intRand(0, *strength) > int(techSafe) {
		// Ship destroyed by matter-antimatter explosion.
		msg := fmt.Sprintf("%s: Matter-antimatter EXPLOSION from overloaded crystal on %s\n",
			s.shipLocation(ship), shipDisplayName(ship))
		s.killShip(int(ship.Owner), ship)
		*strength = 0
		s.pushTelegram(int(ship.Owner), int(ship.Governor), msg)
		s.postCombat(msg)
		s.notifyStar(int(ship.Owner), int(ship.Governor), 0, int(ship.StOrbits), msg)
	} else if intRand(0, *strength) > int((1.0-0.01*float64(ship.Damage))*ship.Tech/4.0) {
		// Crystal damaged.
		msg := fmt.Sprintf("%s: Crystal damaged from overloading on %s.\n",
			s.shipLocation(ship), shipDisplayName(ship))
		ship.FireLaser = 0
		ship.Mounted = false
		*strength = 0
		s.pushTelegram(int(ship.Owner), int(ship.Governor), msg)
	}
}

// adjacent returns true if (fx,fy) and (tx,ty) are adjacent on the planet map,
// accounting for x-wrap. Mirrors adjacent() in fire.c.
func adjacent(fx, fy, tx, ty int, planet *model.Planet) bool {
	if abs(fy-ty) > 1 {
		return false
	}
	dx := abs(fx - tx)
	if dx <= 1 {
		return true
	}
	// X-wrap: one is at 0, other at Maxx-1.
	if (fx == int(planet.MaxX)-1 && tx == 0) || (fx == 0 && tx == int(planet.MaxX)-1) {
		return true
	}
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ---- Damage application ----------------------------------------------------

// doRadiation applies radiation damage to a ship.
// Mirrors do_radiation() in shootblast.c.
func doRadiation(ship *model.Ship, tech float64, strength, hits int, weapon string, msg *string) int {
	fac := (2.0 / math.Pi) * math.Atan(5.0*(tech+1.0)/(ship.Tech+1.0))
	arm := int(ship.EffArmor(model.Shipdata)) - hits/5
	if arm < 0 {
		arm = 0
	}
	body := shipBody(ship)

	r := 1.0
	for i := 0; i < arm; i++ {
		r *= fac
	}
	penetrate := 0
	for i := 0; i < hits; i++ {
		if doubleRand() <= r {
			penetrate++
		}
	}

	dosage := roundRand(40.0 * float64(penetrate) / float64(max1(body, 1)))
	if dosage > 100 {
		dosage = 100
	}
	if dosage > int(ship.Rad) {
		ship.Rad = uint8(dosage)
	}
	if success(int(ship.Rad)) {
		ship.Active = false
	}

	*msg += fmt.Sprintf("\tAttack: %d %s\n\t  Hits: %d\n", strength, weapon, hits)
	*msg += fmt.Sprintf("\t   Rad: %d%% for a total of %d%%\n", dosage, ship.Rad)
	return dosage
}

// doDamage applies weapon damage to a ship and returns damage dealt.
// Mirrors do_damage() in shootblast.c.
func (s *State) doDamage(who int, ship *model.Ship, tech float64, strength, hits,
	defense, caliber int, rng float64, weapon string, msg *string) int {
	fac := pFactor(tech, ship.Tech)
	arm := int(ship.EffArmor(model.Shipdata)) + defense - hits/5
	if arm < 0 {
		arm = 0
	}
	body := math.Sqrt(0.1 * float64(max1(shipBody(ship), 1)))

	r := 1.0
	for i := 0; i < arm; i++ {
		r *= fac
	}
	penetrate := 0
	for i := 0; i < hits; i++ {
		if doubleRand() <= r {
			penetrate++
		}
	}

	damage := roundRand(s.cfg.ShipDamage * float64(caliber) * float64(penetrate) / body)
	var crithits, critdam int
	var critmsg string
	doCriticalHits(penetrate, ship, &crithits, &critdam, caliber, &critmsg)
	if crithits > 0 {
		damage += critdam
	}
	if damage > 100 {
		damage = 100
	}
	ship.Damage += uint8(damage)
	if ship.Damage > 100 {
		ship.Damage = 100
	}

	var casualties, casualties1, primgdmg, secgdmg int
	doCollateral(ship, damage, &casualties, &casualties1, &primgdmg, &secgdmg)

	// Clamp laser strength to safe level after damage.
	if ship.FireLaser > 0 {
		safe := int((1.0 - 0.01*float64(ship.Damage)) * ship.Tech / 4.0)
		if int(ship.FireLaser) > safe {
			ship.FireLaser = uint8(safe)
		}
	}

	*msg += fmt.Sprintf("\tAttack: %d %s at a range of %.0f\n", strength, weapon, rng)
	if penetrate > 0 {
		*msg += fmt.Sprintf("\t\t%d penetrations  eff armor=%d defense=%d prob=%.3f\n",
			penetrate, arm, defense, r)
	}
	if crithits > 0 {
		*msg += fmt.Sprintf("\t\t%d CRITICAL hits do %d%% damage\n", crithits, critdam)
		*msg += critmsg
	}
	if damage > 0 {
		*msg += fmt.Sprintf("\tDamage: %d%% damage for a total of %d%%\n",
			damage, ship.Damage)
	}
	if primgdmg > 0 || secgdmg > 0 {
		*msg += fmt.Sprintf("\t Other: %d primary/%d secondary guns destroyed\n",
			primgdmg, secgdmg)
	}
	if casualties > 0 || casualties1 > 0 {
		*msg += fmt.Sprintf("\tKilled: %d civ + %d mil casualties\n",
			casualties, casualties1)
	}

	if ship.Damage >= 100 {
		s.killShip(who, ship)
	}
	ship.BuildCost = uint16(s.shipCostFull(ship))
	return damage
}

// doCriticalHits randomly applies critical damage to ship systems.
// Mirrors do_critical_hits() in shootblast.c.
func doCriticalHits(penetrate int, ship *model.Ship, crithits, critdam *int,
	caliber int, critmsg *string) {
	*critdam = 0
	*crithits = 0
	effSize := max1(shipBody(ship)/caliber, 1)
	for i := 0; i < penetrate; i++ {
		if intRand(0, effSize-1) == 0 {
			*crithits++
			*critdam += intRand(0, 100)
		}
	}
	if *critdam > 100 {
		*critdam = 100
	}

	*critmsg = "\t\tSpecial systems damage: "
	if ship.CEW > 0 && success(*critdam) {
		*critmsg += "CEW "
		ship.CEW = 0
	}
	if ship.Laser > 0 && success(*critdam) {
		*critmsg += "Laser "
		ship.Laser = 0
	}
	if ship.Cloak > 0 && success(*critdam) {
		*critmsg += "Cloak "
		ship.Cloak = 0
	}
	if ship.HyperDrive.Has && success(*critdam) {
		*critmsg += "Hyper-drive "
		ship.HyperDrive.Has = false
	}
	if ship.MaxSpeed > 0 && success(*critdam) {
		ship.Speed = 0
		ship.MaxSpeed = uint16(intRand(0, int(ship.MaxSpeed)-1))
		*critmsg += fmt.Sprintf("Speed=%d ", ship.MaxSpeed)
	}
	if ship.Armor > 0 && success(*critdam) {
		ship.Armor = uint8(intRand(0, int(ship.Armor)-1))
		*critmsg += fmt.Sprintf("Armor=%d ", ship.Armor)
	}
	*critmsg += "\n"
}

// doCollateral applies collateral crew/gun losses from a hit.
// Mirrors do_collateral() in shootblast.c.
func doCollateral(ship *model.Ship, damage int,
	casualties, casualties1, primgundamage, secgundamage *int) {
	*casualties = 0
	*casualties1 = 0
	*primgundamage = 0
	*secgundamage = 0

	for i := 0; i < int(ship.Popn); i++ {
		if success(damage) {
			*casualties++
		}
	}
	ship.Popn -= uint16(*casualties)

	for i := 0; i < int(ship.Troops); i++ {
		if success(damage) {
			*casualties1++
		}
	}
	ship.Troops -= uint16(*casualties1)

	for i := 0; i < int(ship.Primary); i++ {
		if success(damage) {
			*primgundamage++
		}
	}
	ship.Primary -= uint8(*primgundamage)

	for i := 0; i < int(ship.Secondary); i++ {
		if success(damage) {
			*secgundamage++
		}
	}
	ship.Secondary -= uint8(*secgundamage)

	if ship.Primary == 0 {
		ship.PrimType = 0
	}
	if ship.Secondary == 0 {
		ship.SecType = 0
	}
}

// mutateSector randomly changes a bombed sector's condition back to its type.
// Mirrors mutate_sector() in shootblast.c.
func mutateSector(sec *model.Sector) {
	cond := int(sec.Condition)
	if cond >= 0 && cond < len(defenseData) &&
		intRand(0, 6) >= defenseData[cond] {
		sec.Condition = sec.Type
	}
}

// ---- Core fire functions ---------------------------------------------------

// shootShipToShip fires from one ship at another.
// Returns damage dealt, or -1 if the attack is illegal.
// Mirrors shoot_ship_to_ship() in shootblast.c.
// Replaces the stub in ship.go.
func (s *State) shootShipToShip(from, to *model.Ship, strength, cew, ignore int,
	longMsg, shortMsg *string) int {

	if strength <= 0 {
		return -1
	}
	if (!from.Alive && ignore == 0) || !to.Alive {
		return -1
	}
	if from.WhatOrbits == model.LevelShip || from.WhatOrbits == model.LevelUniv {
		return -1
	}
	if to.WhatOrbits == model.LevelShip || to.WhatOrbits == model.LevelUniv {
		return -1
	}
	if from.StOrbits != to.StOrbits {
		return -1
	}
	if hasSwitch(from) && !from.On {
		return -1
	}

	dist := 0.0
	if from.Type == model.StypeMissile {
		dist = 0.0
	} else {
		dist = math.Hypot(from.XPos-to.XPos, from.YPos-to.YPos)
		if from.Type == model.StypeMine {
			dist = dist * dist / 200.0
		}
	}

	if dist > s.gunRange(nil, from, 0) {
		return -1
	}

	caliber := s.currentCaliber(from)
	fevade, fspeed, _ := shipDisposition(from)
	tevade, tspeed, tbody := shipDisposition(to)
	defense := s.getDefense(to)

	focus := 0
	if laserOn(from) && from.Focus > 0 {
		focus = 1
	}

	var hits int
	if cew > 0 {
		hits = cewHit(dist, int(from.CEWRange))
	} else {
		hits, _ = numHits(dist, focus, strength, from.Tech,
			int(from.Damage), fevade, tevade, fspeed, tspeed, tbody, caliber, defense)
	}

	var dmgMsg string
	var damage int
	if from.Mode {
		// Radiation weapon
		damage = doRadiation(to, from.Tech, strength, hits, "radiation", &dmgMsg)
	} else {
		var weapon string
		if cew > 0 {
			weapon = "strength CEW"
		} else if laserOn(from) {
			if from.Focus > 0 {
				weapon = "strength focused laser"
			} else {
				weapon = "strength laser"
			}
		} else {
			switch caliber {
			case caliberLight:
				weapon = "light guns"
			case caliberMedium:
				weapon = "medium guns"
			case caliberHeavy:
				weapon = "heavy guns"
			default:
				weapon = "pea-shooter"
				return -1
			}
		}
		damage = s.doDamage(int(from.Owner), to, from.Tech,
			strength, hits, defense, caliber, dist, weapon, &dmgMsg)
	}

	aliveStr := "attacked"
	if !to.Alive {
		aliveStr = "DESTROYED"
	}
	*shortMsg = fmt.Sprintf("%s: %s %s %s\n",
		s.shipLocation(to), shipDisplayName(from), aliveStr, shipDisplayName(to))
	*longMsg = *shortMsg + dmgMsg
	return damage
}

// shootShipToPlanet fires a ship's weapons at a planet sector and surroundings.
// Returns sectors destroyed, or -1 if illegal.
// Mirrors shoot_ship_to_planet() in shootblast.c.
// Replaces the stub in ship.go.
func (s *State) shootShipToPlanet(ship *model.Ship, pl *model.Planet,
	strength, x, y, getMap, ignore, caliber int,
	longMsg, shortMsg *string) int {

	if strength <= 0 {
		return -1
	}
	if !ship.Alive && ignore == 0 {
		return -1
	}
	if hasSwitch(ship) && !ship.On {
		return -1
	}
	if ship.WhatOrbits != model.LevelPlan {
		return -1
	}
	if x < 0 || x >= int(pl.MaxX) || y < 0 || y >= int(pl.MaxY) {
		return -1
	}

	// Determine caliber if not provided.
	if caliber == 0 {
		if ship.FireLaser > 0 {
			caliber = caliberLight
		} else {
			switch ship.Guns {
			case model.GunPrimary:
				caliber = int(ship.PrimType)
			case model.GunSecondary:
				caliber = int(ship.SecType)
			default:
				caliber = caliberLight
			}
		}
	}

	r := 0.4 * float64(strength)
	numdest := 0
	// nuked[i] = true means player i+1 had sectors damaged.
	var nuked [model.MaxPlayers]bool

	numSectors := int(pl.MaxX) * int(pl.MaxY)
	sumMob := make([]int, model.MaxPlayers)

	for y2 := 0; y2 < int(pl.MaxY); y2++ {
		for x2 := 0; x2 < int(pl.MaxX); x2++ {
			dx := x2 - x
			if dx < 0 {
				dx = -dx
			}
			// X-wrap distance.
			dxWrap := int(pl.MaxX) - 1 - dx
			if dxWrap < dx {
				dx = dxWrap
			}
			dy := y2 - y
			if dy < 0 {
				dy = -dy
			}
			d := math.Sqrt(float64(dx*dx + dy*dy))
			sec := pl.SectorAt(x2, y2)
			if sec == nil {
				continue
			}

			if d <= r {
				cond := int(sec.Condition)
				if cond < 0 || cond >= len(defenseData) {
					cond = 0
				}
				plated := 0
				if cond == model.SectorPlated {
					plated = 1
				}
				fac := s.cfg.SectorDamage * float64(strength) * float64(caliber) / (d + 1.0)

				if sec.Owner > 0 {
					if sec.Popn > 0 {
						kills := intRand(0, int(fac/10.0)*int(sec.Popn)) / (1 + plated)
						if kills > int(sec.Popn) {
							sec.Popn = 0
						} else {
							sec.Popn -= uint16(kills)
						}
					}
					if sec.Troops > 0 && fac > 5.0*float64(defenseData[cond]) {
						kills := intRand(0, int(fac/20.0)*int(sec.Troops)) / (1 + plated)
						if kills > int(sec.Troops) {
							sec.Troops = 0
						} else {
							sec.Troops -= uint16(kills)
						}
					}
					if sec.Popn == 0 && sec.Troops == 0 {
						sec.Owner = 0
					}
				}

				if fac >= 5.0 && intRand(0, 10) == 0 {
					mutateSector(sec)
				}

				defRoll := 0
				if cond < len(defenseData) {
					defRoll = defenseData[cond] * intRand(0, 10)
				}
				if roundRand(fac) > defRoll {
					if sec.Owner > 0 {
						nuked[sec.Owner-1] = true
					}
					sec.Popn = 0
					sec.Troops = uint16(intRand(0, int(sec.Troops)))
					if sec.Troops == 0 {
						sec.Owner = 0
					}
					sec.Eff = 0
					sec.Resource = uint16(int(sec.Resource) / (int(fac) + 1))
					sec.Mobilization = 0
					sec.Fert = 0
					sec.Crystals = uint8(intRand(0, int(sec.Crystals)))
					sec.Condition = model.SectorWasted
					numdest++
				} else {
					sec.Fert = uint8(max1(0, int(sec.Fert)-int(fac)))
					sec.Eff = uint8(max1(0, int(sec.Eff)-int(fac)))
					sec.Mobilization = uint8(max1(0, int(sec.Mobilization)-int(fac)))
					sec.Resource = uint16(max1(0, int(sec.Resource)-int(fac)))
				}
			}
			if sec.Owner > 0 {
				sumMob[sec.Owner-1] += int(sec.Mobilization)
			}
		}
	}

	for i := 0; i < model.MaxPlayers; i++ {
		pl.Info[i].MobPoints = uint64(sumMob[i])
		if numSectors > 0 {
			pl.Info[i].ComRead = uint8(sumMob[i] / numSectors)
		}
		pl.Info[i].Guns = planetGuns(uint64(sumMob[i]))
	}

	// Toxicity increase.
	tox := pl.Conditions[model.AtmosToxic]
	pl.Conditions[model.AtmosToxic] += int16(float64(100-tox) *
		(float64(numdest) / float64(numSectors)))

	oldOwner := 0
	target := pl.SectorAt(x, y)
	if target != nil {
		oldOwner = int(target.Owner)
	}
	*shortMsg = fmt.Sprintf("%s bombards %s [%d]\n",
		shipDisplayName(ship), s.shipLocation(ship), oldOwner)
	*longMsg = *shortMsg + fmt.Sprintf("\t%d sectors destroyed\n", numdest)

	// Write nuked flags into planet.Info for callers that telegram affected races.
	for i := 0; i < model.MaxPlayers; i++ {
		if nuked[i] {
			pl.Info[i].Explored = true // reuse Explored as "was hit this turn"
		}
	}

	return numdest
}

// shootPlanetToShip fires a planet's guns at an orbiting ship.
// Returns damage dealt, or -1 if illegal.
// Only active when the Defense feature flag is set.
// Mirrors shoot_planet_to_ship() in shootblast.c.
func (s *State) shootPlanetToShip(race *model.Race, planet *model.Planet,
	ship *model.Ship, strength int, longMsg, shortMsg *string) int {

	if strength <= 0 || !ship.Alive {
		return -1
	}
	if ship.WhatOrbits != model.LevelPlan {
		return -1
	}

	evade, speed, body := shipDisposition(ship)
	hits, _ := numHits(0.0, 0, strength, race.Tech, 0,
		evade, 0, speed, 0, body, caliberMedium, 1)

	var dmgMsg string
	damage := s.doDamage(race.PlayerNum, ship, race.Tech, strength, hits, 0,
		caliberMedium, 0.0, "medium guns", &dmgMsg)

	aliveStr := "attacked"
	if !ship.Alive {
		aliveStr = "DESTROYED"
	}
	*shortMsg = fmt.Sprintf("%s [%d] %s %s\n",
		s.shipLocation(ship), race.PlayerNum, aliveStr, shipDisplayName(ship))
	*longMsg = *shortMsg + dmgMsg
	return damage
}

// ---- Capture helpers -------------------------------------------------------

// captureStuff recursively transfers all ships docked inside a captured ship
// to the new owner. Mirrors capture_stuff() in capture.c.
func (s *State) captureStuff(ship *model.Ship) {
	sh := ship.Ships
	for sh != 0 {
		child := s.ships[sh]
		if child == nil {
			break
		}
		s.captureStuff(child)
		child.Owner = ship.Owner
		child.Governor = ship.Governor
		s.pushTelegram(int(child.Owner), int(child.Governor),
			fmt.Sprintf("%s CAPTURED!\n", shipDisplayName(child)))
		sh = child.NextShip
	}
}

// ---- Utility ---------------------------------------------------------------

// shipCostFull computes the effective build cost of a ship for record-keeping.
// Mirrors the Cost(s) macro in ships.h: factories use 2*build_cost*on + table.
func (s *State) shipCostFull(ship *model.Ship) int {
	if ship.Type == model.OtypeFactory {
		on := 0
		if ship.On {
			on = 1
		}
		return 2*int(ship.BuildCost)*on +
			int(model.Shipdata[ship.Type][model.AbilCost])
	}
	return int(ship.BuildCost)
}

// max1 returns the larger of two ints (avoids clash with built-in max).
func max1(a, b int) int {
	if a > b {
		return a
	}
	return b
}
