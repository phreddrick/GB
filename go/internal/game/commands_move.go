package game

// commands_move.go implements population/troop movement and AFV walking.
//
// C sources ported: move.c
//   cmdMove      — move_popn(Playernum, Governor, CIV)
//   cmdDeploy    — move_popn(Playernum, Governor, MIL)
//   cmdWalk      — walk()
//   getMoveDir   — get_move()
//   groundAttack         — ground_attack()
//   mechDefend           — mech_defend()
//   mechAttackPeople     — mech_attack_people()
//   peopleAttackMech     — people_attack_mech()

import (
	"fmt"
	"math"
	"strconv"

	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// afvFuelCost matches AFV_FUEL_COST in tweakables.h.
const afvFuelCost = 1.0

// sectorNames mirrors Desnames[] for human-readable sector display in combat messages.
var sectorNames = [9]string{
	"sea", "land", "mountain", "gas", "ice", "forest", "desert", "plated", "wasted",
}

// moveCIV / moveMIL distinguish civilian vs troop movement (mirrors C's CIV/MIL).
const (
	moveCIV = 0
	moveMIL = 1
)

// getMoveDir maps a direction character to destination coordinates.
// X wraps around at planet.MaxX. Returns false for unrecognised chars.
// Mirrors get_move() in move.c.
func getMoveDir(direction byte, x, y int, planet *model.Planet) (x2, y2 int, ok bool) {
	maxX := int(planet.MaxX)
	switch direction {
	case '1', 'b': // SW
		x2, y2 = x-1, y+1
		if x2 == -1 {
			x2 = maxX - 1
		}
	case '2', 'k': // S
		x2, y2 = x, y+1
	case '3', 'n': // SE
		x2, y2 = x+1, y+1
		if x2 == maxX {
			x2 = 0
		}
	case '4', 'h': // W
		x2, y2 = x-1, y
		if x2 == -1 {
			x2 = maxX - 1
		}
	case '6', 'l': // E
		x2, y2 = x+1, y
		if x2 == maxX {
			x2 = 0
		}
	case '7', 'y': // NW
		x2, y2 = x-1, y-1
		if x2 == -1 {
			x2 = maxX - 1
		}
	case '8', 'j': // N
		x2, y2 = x, y-1
	case '9', 'u': // NE
		x2, y2 = x+1, y-1
		if x2 == maxX {
			x2 = 0
		}
	default:
		return x, y, false
	}
	return x2, y2, true
}

// groundAttack resolves one round of ground combat between attacker and defender.
// Mirrors ground_attack() in move.c.
//
// people — attacker count, modified in place (survivors after combat)
// what   — moveCIV or moveMIL
// civ / mil — defender civilian/troop counts, modified in place
// def1/def2 — defenseData[sector.Condition] for attacker's / defender's sector
// alikes/dlikes — race.Likes[cond] for attacker / defender
// Outputs: astrength / dstrength (for display), and three casualty counts.
func groundAttack(
	race, alien *model.Race,
	people *int, what int,
	civ, mil *uint16,
	def1, def2 int,
	alikes, dlikes float64,
	astrength, dstrength *float64,
	casualties, casualties2, casualties3 *int,
) {
	attackMult := 1.0
	if what == moveMIL {
		attackMult = 10.0
	}

	*astrength = float64(*people) * float64(race.Fighters) * attackMult *
		(alikes + 1.0) * float64(def1+1) *
		moraleFactor(float64(race.Morale-alien.Morale))

	*dstrength = float64((int(*civ)+int(*mil)*10)*int(alien.Fighters)) *
		(dlikes + 1.0) * float64(def2+1) *
		moraleFactor(float64(alien.Morale-race.Morale))

	casualtyScale := intMin(*people*int(race.Fighters)*int(attackMult),
		(int(*civ)+int(*mil)*10)*int(alien.Fighters))

	// Attacker casualties.
	*casualties = intRand(0, roundRand(float64(casualtyScale/int(attackMult))**dstrength / *astrength))
	if *casualties > *people {
		*casualties = *people
	}
	*people -= *casualties

	// Defender civilian casualties.
	*casualties2 = intRand(0, roundRand(float64(casualtyScale)**astrength / *dstrength))
	if *casualties2 > int(*civ) {
		*casualties2 = int(*civ)
	}
	*civ -= uint16(*casualties2)

	// Defender troop casualties.
	*casualties3 = intRand(0, roundRand(float64(casualtyScale/10)**astrength / *dstrength))
	if *casualties3 > int(*mil) {
		*casualties3 = int(*mil)
	}
	*mil -= uint16(*casualties3)
}

// intMin returns the smaller of two ints.
func intMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// mechDefend fires defending AFVs at the incoming group before it reaches
// (x2,y2). Mirrors mech_defend() in move.c.
// people is decremented by deaths; zero means attack must be aborted.
func (s *State) mechDefend(
	p, g int,
	people *int, what int,
	planet *model.Planet,
	x, y int, sect *model.Sector,
	x2, y2 int, sect2 *model.Sector,
	sess net.Responder,
) {
	race := s.raceByNum(p)
	if race == nil {
		return
	}

	civ, mil := 0, 0
	if what == moveCIV {
		civ = *people
	} else {
		mil = *people
	}

	sh := int(planet.Ships)
	for sh != 0 && (civ+mil) > 0 {
		ship := s.ships[uint32(sh)]
		if ship == nil {
			break
		}
		next := int(ship.NextShip)

		if int(ship.Owner) != p &&
			ship.Type == model.OtypeAFV &&
			landed(ship) &&
			s.retalStrength(ship) > 0 &&
			int(ship.LandX) == x2 && int(ship.LandY) == y2 {

			alien := s.raceByNum(int(ship.Owner))
			if alien != nil && (!race.IsAllied(int(ship.Owner)) || !alien.IsAllied(p)) {
				oldGov := int(s.stars[ship.StOrbits].Governor[ship.Owner-1])
				for (civ+mil) > 0 && s.retalStrength(ship) > 0 {
					lmsg, _ := s.mechAttackPeople(ship, &civ, &mil, alien, race, sect2, x2, y2, true)
					sess.Send(lmsg)
					s.warn(int(ship.Owner), oldGov, lmsg)
					if civ+mil > 0 {
						lmsg2, _ := s.peopleAttackMech(ship, civ, mil, race, alien, sect2, x2, y2)
						sess.Send(lmsg2)
						s.warn(int(ship.Owner), oldGov, lmsg2)
					}
				}
				s.ships[uint32(sh)] = ship
			}
		}
		sh = next
	}
	*people = civ + mil
}

// mechAttackPeople fires a single AFV volley at a people group.
// Returns (longMsg, shortMsg). Mirrors mech_attack_people() in move.c.
//
// race  = AFV owner's race
// alien = people owner's race
func (s *State) mechAttackPeople(
	ship *model.Ship,
	civ, mil *int,
	race, alien *model.Race,
	sect *model.Sector,
	x, y int,
	ignore bool,
) (longMsg, shortMsg string) {
	strength := s.retalStrength(ship)
	oldCiv := *civ
	oldMil := *mil

	astrength := s.cfg.MechAttack * float64(ship.Tech) * float64(strength) *
		(float64(ship.Armor) + 1.0) *
		0.01 * (100.0 - float64(ship.Damage)) *
		0.01 * (float64(race.Likes[sect.Condition]) + 1.0) *
		moraleFactor(float64(race.Morale-alien.Morale))

	dstrength := float64(10*oldMil*int(alien.Fighters)+oldCiv) *
		0.01 * float64(alien.Tech) *
		0.01 * (float64(alien.Likes[sect.Condition]) + 1.0) *
		(float64(defenseData[sect.Condition]) + 1.0) *
		moraleFactor(float64(alien.Morale-race.Morale))

	ammo := strength
	if ignore {
		a := int(math.Log10(dstrength+1)) - 1
		if a < 0 {
			a = 0
		}
		if a < strength {
			ammo = a
		}
	}
	s.useDestruct(ship, ammo)

	casCiv := intRand(0, roundRand(float64(oldCiv)*astrength/dstrength))
	if casCiv > oldCiv {
		casCiv = oldCiv
	}
	casMil := intRand(0, roundRand(float64(oldMil)*astrength/dstrength))
	if casMil > oldMil {
		casMil = oldMil
	}
	*civ -= casCiv
	*mil -= casMil

	word := "attacked"
	if *civ+*mil == 0 {
		word = "slaughtered"
	}
	short := fmt.Sprintf("%s: %s %s %s [%d]\n",
		s.dispShipLocFull(ship), shipDisplayName(ship), word,
		alien.Name, alien.PlayerNum)
	long := short +
		fmt.Sprintf("\tBattle at %d,%d %s: %d guns fired on %d civ/%d mil\n",
			x, y, sectorNames[sect.Condition], strength, oldCiv, oldMil) +
		fmt.Sprintf("\tAttack: %.3f   Defense: %.3f.\n", astrength, dstrength) +
		fmt.Sprintf("\t%d civ/%d mil killed.\n", casCiv, casMil)
	return long, short
}

// peopleAttackMech fires a people group against an AFV.
// Returns (longMsg, shortMsg). Mirrors people_attack_mech() in move.c.
//
// race  = people owner's race
// alien = AFV owner's race
func (s *State) peopleAttackMech(
	ship *model.Ship,
	civ, mil int,
	race, alien *model.Race,
	sect *model.Sector,
	x, y int,
) (longMsg, shortMsg string) {
	strength := s.retalStrength(ship)

	dstrength := s.cfg.MechAttack * float64(ship.Tech) * float64(strength) *
		(float64(ship.Armor) + 1.0) *
		0.01 * (100.0 - float64(ship.Damage)) *
		0.01 * (float64(alien.Likes[sect.Condition]) + 1.0) *
		moraleFactor(float64(alien.Morale-race.Morale))

	astrength := float64(10*mil*int(race.Fighters)+civ) *
		0.01 * float64(race.Tech) *
		0.01 * (float64(race.Likes[sect.Condition]) + 1.0) *
		(float64(defenseData[sect.Condition]) + 1.0) *
		moraleFactor(float64(race.Morale-alien.Morale))

	ammo := int(math.Log10(astrength+1)) - 1
	if ammo < 0 {
		ammo = 0
	}
	if ammo > strength {
		ammo = strength
	}
	s.useDestruct(ship, ammo)

	damage := intRand(0, roundRand(100.0*astrength/dstrength))
	if damage > 100 {
		damage = 100
	}
	ship.Damage += uint8(damage)
	if ship.Damage >= 100 {
		ship.Damage = 100
		s.killShip(int(race.PlayerNum), ship)
	}

	var casCiv, casMil, pdam, sdam int
	doCollateral(ship, damage, &casCiv, &casMil, &pdam, &sdam)

	status := "attacked"
	if !ship.Alive {
		status = "DESTROYED"
	}
	short := fmt.Sprintf("%s: %s [%d] %s %s\n",
		s.dispShipLocFull(ship), race.Name, race.PlayerNum, status, shipDisplayName(ship))
	long := short +
		fmt.Sprintf("\tBattle at %d,%d %s: %d civ/%d mil assault %s\n",
			x, y, sectorNames[sect.Condition], civ, mil, model.ShipNames[ship.Type]) +
		fmt.Sprintf("\tAttack: %.3f   Defense: %.3f.\n", astrength, dstrength) +
		fmt.Sprintf("\t%d%% damage inflicted for a total of %d%%\n", damage, ship.Damage) +
		fmt.Sprintf("\t%d civ/%d mil killed   %d prim/%d sec guns knocked out\n",
			casCiv, casMil, pdam, sdam)
	return long, short
}

// movePopn is the shared implementation for cmdMove (CIV) and cmdDeploy (MIL).
// Mirrors move_popn() in move.c.
func (s *State) movePopn(p, g int, args []string, sess net.Responder, what int) {
	dir := s.dirs[p-1][g]
	if int(dir.Level) != model.LevelPlan {
		sess.Send("Wrong scope — change to planet level first.\n")
		return
	}

	star := s.stars[dir.StarNum]
	if star == nil || !controlStar(p, g, star) {
		sess.Send("You are not authorized to do that here.\n")
		return
	}

	planet := s.planet(int(dir.StarNum), int(dir.PlanetNum))
	if planet == nil {
		sess.Send("Internal error: planet not found.\n")
		return
	}

	if planet.SlavedTo > 0 && int(planet.SlavedTo) != p {
		sess.Send("That planet has been enslaved!\n")
		return
	}

	if len(args) < 2 {
		if what == moveCIV {
			sess.Send("Usage: move x,y directions [amount]\n")
		} else {
			sess.Send("Usage: deploy x,y directions [amount]\n")
		}
		return
	}

	// Parse origin "x,y".
	x, y := -1, -1
	fmt.Sscanf(args[0], "%d,%d", &x, &y)
	if x < 0 || y < 0 || x > int(planet.MaxX)-1 || y > int(planet.MaxY)-1 {
		sess.Send("Origin coordinates illegal.\n")
		return
	}

	directions := args[1]

	// Optional amount argument.
	amountArg := -1
	if len(args) >= 3 {
		if n, err := strconv.Atoi(args[2]); err == nil {
			amountArg = n
		}
	}

	n := 0
	done := false
	for !done {
		sect := planet.SectorAt(x, y)
		if int(sect.Owner) != p {
			sendf(sess, "You don't own sector %d,%d!\n", x, y)
			_ = s.store.PutPlanet(planet)
			return
		}

		if n >= len(directions) {
			sess.Send("Finished.\n")
			_ = s.store.PutPlanet(planet)
			return
		}
		dirChar := directions[n]
		n++

		x2, y2, ok := getMoveDir(dirChar, x, y, planet)
		if !ok {
			sess.Send("Finished.\n")
			_ = s.store.PutPlanet(planet)
			return
		}

		if x2 < 0 || y2 < 0 || x2 > int(planet.MaxX)-1 || y2 > int(planet.MaxY)-1 {
			sendf(sess, "Illegal coordinates %d,%d.\n", x2, y2)
			_ = s.store.PutPlanet(planet)
			return
		}
		if !adjacent(x, y, x2, y2, planet) {
			sess.Send("Illegal move — to adjacent sectors only!\n")
			return
		}

		sect2 := planet.SectorAt(x2, y2)

		// Determine number of people to move.
		people := 0
		if amountArg >= 0 {
			people = amountArg
			if people < 0 {
				if what == moveCIV {
					people = int(sect.Popn) + people
				} else {
					people = int(sect.Troops) + people
				}
			}
		} else {
			if what == moveCIV {
				people = int(sect.Popn)
			} else {
				people = int(sect.Troops)
			}
		}

		if (what == moveCIV && people > int(sect.Popn)) ||
			(what == moveMIL && people > int(sect.Troops)) ||
			people <= 0 {
			if what == moveCIV {
				sendf(sess, "Bad value — %d civilians in [%d,%d]\n", sect.Popn, x, y)
			} else {
				sendf(sess, "Bad value — %d troops in [%d,%d]\n", sect.Troops, x, y)
			}
			_ = s.store.PutPlanet(planet)
			return
		}

		if what == moveCIV {
			sendf(sess, "%d population moved.\n", people)
		} else {
			sendf(sess, "%d troops moved.\n", people)
		}

		// Check for defending AFVs at the destination.
		s.mechDefend(p, g, &people, what, planet, x, y, sect, x2, y2, sect2, sess)
		if people == 0 {
			_ = s.store.PutPlanet(planet)
			sess.Send("Attack aborted.\n")
			return
		}

		assault := int(sect2.Owner) != 0 && int(sect2.Owner) != p

		// AP cost mirrors the C formula.
		apCost := 0
		if what == moveCIV {
			apCost = int(s.cfg.MoveFactor)*
				(int(math.Log(1.0+float64(people)))+boolInt(assault)) + 1
		} else {
			apCost = int(s.cfg.MoveFactor)*
				(int(math.Log10(1.0+float64(people)))+boolInt(assault)) + 1
		}

		if !s.enoughAP(p, g, star.AP[p-1], apCost) {
			_ = s.store.PutPlanet(planet)
			return
		}

		if assault {
			Race := s.raceByNum(p)
			alien := s.raceByNum(int(sect2.Owner))
			if Race == nil || alien == nil {
				return
			}

			// Record ground assault for turn-end reporting.
			if p >= 1 && p <= model.MaxPlayers && int(sect2.Owner) >= 1 && int(sect2.Owner) <= model.MaxPlayers {
				s.groundAssaults[p-1][sect2.Owner-1][dir.StarNum]++
			}

			// Both races learn a bit from the encounter.
			if alien.Translate[p-1] < 100 {
				alien.Translate[p-1] = int8(intMin(int(alien.Translate[p-1])+5, 100))
			}
			if Race.Translate[sect2.Owner-1] < 100 {
				Race.Translate[sect2.Owner-1] = int8(intMin(int(Race.Translate[sect2.Owner-1])+5, 100))
			}

			old2Owner := int(sect2.Owner)
			old2Gov := int(star.Governor[sect2.Owner-1])

			if what == moveCIV {
				if int(sect.Popn) >= people {
					sect.Popn -= uint16(people)
				} else {
					sect.Popn = 0
				}
				sendf(sess, "%d civ assault %d civ/%d mil\n", people, sect2.Popn, sect2.Troops)
			} else {
				if int(sect.Troops) >= people {
					sect.Troops -= uint16(people)
				} else {
					sect.Troops = 0
				}
				sendf(sess, "%d mil assault %d civ/%d mil\n", people, sect2.Popn, sect2.Troops)
			}

			oldPeople := people
			old2Popn := int(sect2.Popn)
			old3Popn := int(sect2.Troops)

			var astr, dstr float64
			var cas, cas2, cas3 int
			groundAttack(Race, alien, &people, what,
				&sect2.Popn, &sect2.Troops,
				defenseData[sect.Condition], defenseData[sect2.Condition],
				float64(Race.Likes[sect.Condition]),
				float64(alien.Likes[sect2.Condition]),
				&astr, &dstr, &cas, &cas2, &cas3)

			sendf(sess, "Attack: %.2f   Defense: %.2f.\n", astr, dstr)

			victResult := "DEFEAT"
			if int(sect2.Owner) == p {
				victResult = "VICTORY"
			}
			telegramBuf := fmt.Sprintf("/%s/%s: %s [%d] %c(%d,%d) assaults %s [%d] %c(%d,%d) %s\n",
				star.Name, star.PlanetNames[dir.PlanetNum],
				Race.Name, p, sectorSymbol(int(sect.Condition)), x, y,
				alien.Name, alien.PlayerNum,
				sectorSymbol(int(sect2.Condition)), x2, y2,
				victResult)

			if int(sect2.Owner) == p { // victory
				sess.Send("VICTORY! The sector is yours!\n")
				telegramBuf += "Sector CAPTURED!\n"

				absorbed := 0
				if Race.Absorb {
					absorbed = intRand(0, old2Popn+old3Popn)
					sendf(sess, "%d alien bodies absorbed.\n", absorbed)
					s.warn(old2Owner, old2Gov,
						fmt.Sprintf("Metamorphs have absorbed %d bodies!!!\n", absorbed))
				}
				if people > 0 {
					if what == moveCIV {
						sendf(sess, "%d civilians move in.\n", people)
					} else {
						sendf(sess, "%d troops move in.\n", people)
					}
				}
				if what == moveCIV {
					sect2.Popn = uint16(people + absorbed)
				} else {
					sect2.Popn = uint16(absorbed)
					sect2.Troops = uint16(people)
				}
				planet.Info[p-1].MobPoints += uint64(sect2.Mobilization)
				planet.Info[old2Owner-1].MobPoints -= uint64(sect2.Mobilization)
				adjustMorale(Race, alien, int(alien.Fighters))

			} else { // repulsed
				sess.Send("The invasion was repulsed; try again.\n")
				telegramBuf += "You fought them off!\n"

				absorbed := 0
				if alien.Absorb {
					absorbed = intRand(0, oldPeople-people)
					s.warn(old2Owner, old2Gov,
						fmt.Sprintf("%d alien bodies absorbed.\n", absorbed))
					sendf(sess, "Metamorphs have absorbed %d bodies!!!\n", absorbed)
					sect2.Popn += uint16(absorbed)
				}
				if what == moveCIV {
					sect.Popn += uint16(people)
				} else {
					sect.Troops += uint16(people)
				}
				adjustMorale(alien, Race, int(Race.Fighters))
				done = true
			}

			if int(sect.Popn)+int(sect.Troops) == 0 {
				telegramBuf += "You killed all of them!\n"
				Race.Translate[old2Owner-1] = int8(intMin(int(Race.Translate[old2Owner-1])+5, 100))
			}
			if people == 0 {
				sess.Send("Oh no! They killed your party to the last man!\n")
				alien.Translate[p-1] = int8(intMin(int(alien.Translate[p-1])+5, 100))
			}
			_ = s.store.PutRace(alien)
			_ = s.store.PutRace(Race)

			whatWord := "civ"
			if what == moveMIL {
				whatWord = "mil"
			}
			telegramBuf += fmt.Sprintf("Casualties: You: %d civ/%d mil, Them: %d %s\n",
				cas2, cas3, cas, whatWord)
			s.warn(old2Owner, old2Gov, telegramBuf)
			sendf(sess, "Casualties: You: %d %s, Them: %d civ/%d mil\n",
				cas, whatWord, cas2, cas3)

		} else {
			// Friendly / empty sector — just move in.
			if what == moveCIV {
				sect.Popn -= uint16(people)
				sect2.Popn += uint16(people)
			} else {
				sect.Troops -= uint16(people)
				sect2.Troops += uint16(people)
			}
			if sect2.Owner == 0 {
				planet.Info[p-1].MobPoints += uint64(sect2.Mobilization)
			}
			sect2.Owner = uint8(p)
		}

		if int(sect.Popn)+int(sect.Troops) == 0 {
			planet.Info[p-1].MobPoints -= uint64(sect.Mobilization)
			sect.Owner = 0
		}
		if int(sect2.Popn)+int(sect2.Troops) == 0 {
			sect2.Owner = 0
			done = true
		}

		s.deductAPs(p, g, apCost, int(dir.StarNum), false)
		x, y = x2, y2
	}

	_ = s.store.PutPlanet(planet)
	sess.Send("Finished.\n")
}

// boolInt converts a bool to 0 or 1.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// sectorSymbol returns the single-character display symbol for a sector condition.
// Mirrors Dessymbols[] in C.
func sectorSymbol(cond int) byte {
	const sym = ".LMGIFDSW"
	if cond < 0 || cond >= len(sym) {
		return '?'
	}
	return sym[cond]
}

// cmdMove moves civilians between sectors (and assaults alien-owned ones).
// Syntax: move x,y directions [amount]
func (s *State) cmdMove(p, g int, args []string, sess net.Responder) {
	s.movePopn(p, g, args, sess, moveCIV)
}

// cmdDeploy moves troops between sectors (and assaults alien-owned ones).
// Syntax: deploy x,y directions [amount]
func (s *State) cmdDeploy(p, g int, args []string, sess net.Responder) {
	s.movePopn(p, g, args, sess, moveMIL)
}

// cmdWalk moves an AFV one sector across a planet surface.
// Syntax: walk #shipno direction
// Mirrors walk() in move.c.
func (s *State) cmdWalk(p, g int, args []string, sess net.Responder) {
	if len(args) < 2 {
		sess.Send("Usage: walk #ship direction\n")
		return
	}

	// Parse ship number (optional leading '#').
	shipArg := args[0]
	if len(shipArg) > 0 && shipArg[0] == '#' {
		shipArg = shipArg[1:]
	}
	shipno, err := strconv.Atoi(shipArg)
	if err != nil || shipno <= 0 {
		sess.Send("No such ship.\n")
		return
	}
	ship := s.ships[uint32(shipno)]
	if ship == nil {
		sess.Send("No such ship.\n")
		return
	}
	if int(ship.Owner) != p || !authorized(g, ship) {
		sess.Send("You do not control this ship.\n")
		return
	}
	if ship.Type != model.OtypeAFV {
		sess.Send("This ship doesn't walk!\n")
		return
	}
	if !landed(ship) {
		sess.Send("This ship is not landed on a planet.\n")
		return
	}
	if ship.Popn == 0 {
		sess.Send("No crew.\n")
		return
	}
	if ship.Fuel < afvFuelCost {
		sendf(sess, "You don't have %.1f fuel to move it.\n", afvFuelCost)
		return
	}

	star := s.stars[ship.StOrbits]
	if star == nil {
		sess.Send("Internal error: star not found.\n")
		return
	}
	if !s.enoughAP(p, g, star.AP[p-1], 1) {
		return
	}

	planet := s.planet(int(ship.StOrbits), int(ship.PNumOrbits))
	if planet == nil {
		sess.Send("Internal error: planet not found.\n")
		return
	}

	Race := s.raceByNum(p)
	if Race == nil {
		sess.Send("Internal error: race not found.\n")
		return
	}

	x2, y2, ok := getMoveDir(args[1][0], int(ship.LandX), int(ship.LandY), planet)
	if !ok {
		sess.Send("Illegal move.\n")
		return
	}
	if x2 < 0 || y2 < 0 || x2 > int(planet.MaxX)-1 || y2 > int(planet.MaxY)-1 {
		sendf(sess, "Illegal coordinates %d,%d.\n", x2, y2)
		return
	}

	sect := planet.SectorAt(x2, y2)

	if Race.Likes[sect.Condition] == 0 {
		sess.Send("Your ships cannot walk into that sector type!\n")
		return
	}

	// Fight hostile AFVs parked at the destination.
	sh := int(planet.Ships)
	for sh != 0 && ship.Alive {
		ship2 := s.ships[uint32(sh)]
		if ship2 == nil {
			break
		}
		next := int(ship2.NextShip)

		if int(ship2.Owner) != p &&
			ship2.Type == model.OtypeAFV &&
			landed(ship2) &&
			s.retalStrength(ship2) > 0 &&
			int(ship2.LandX) == x2 && int(ship2.LandY) == y2 {

			alien := s.raceByNum(int(ship2.Owner))
			if alien != nil && (!Race.IsAllied(int(ship2.Owner)) || !alien.IsAllied(p)) {
				oldGov := int(star.Governor[ship2.Owner-1])
				for s.retalStrength(ship2) > 0 && s.retalStrength(ship) > 0 {
					var lmsg, smsg string
					s.shootShipToShip(ship2, ship, s.retalStrength(ship2), 0, 0, &lmsg, &smsg)
					s.useDestruct(ship2, s.retalStrength(ship2))
					sess.Send(lmsg)
					s.warn(int(ship2.Owner), oldGov, lmsg)
					if !ship2.Alive {
						s.post(model.NewsCombat, smsg)
					}
					s.notifyStar(p, g, int(ship2.Owner), int(ship.StOrbits), smsg)

					if s.retalStrength(ship) > 0 {
						var lmsg2, smsg2 string
						s.shootShipToShip(ship, ship2, s.retalStrength(ship), 0, 1, &lmsg2, &smsg2)
						s.useDestruct(ship, s.retalStrength(ship))
						sess.Send(lmsg2)
						s.warn(int(ship2.Owner), oldGov, lmsg2)
						if !ship2.Alive {
							s.post(model.NewsCombat, smsg2)
						}
						s.notifyStar(p, g, int(ship2.Owner), int(ship.StOrbits), smsg2)
					}
				}
				s.ships[uint32(sh)] = ship2
			}
		}
		sh = next
	}

	// Fight people in the sector if hostile.
	if ship.Popn > 0 && ship.Alive && sect.Owner != 0 && int(sect.Owner) != p {
		oldOwner := int(sect.Owner)
		oldGov := int(star.Governor[sect.Owner-1])
		alien := s.raceByNum(oldOwner)
		if alien != nil && (!Race.IsAllied(oldOwner) || !alien.IsAllied(p)) {
			if s.retalStrength(ship) == 0 {
				sess.Send("You have nothing to attack with!\n")
				return
			}
			civ := int(sect.Popn)
			mil := int(sect.Troops)
			for (civ+mil) > 0 && s.retalStrength(ship) > 0 {
				lmsg, smsg := s.mechAttackPeople(ship, &civ, &mil, Race, alien, sect, x2, y2, false)
				sess.Send(lmsg)
				s.warn(alien.PlayerNum, oldGov, lmsg)
				s.notifyStar(p, g, oldOwner, int(ship.StOrbits), smsg)
				s.post(model.NewsCombat, smsg)

				lmsg2, smsg2 := s.peopleAttackMech(ship, civ, mil, alien, Race, sect, x2, y2)
				sess.Send(lmsg2)
				s.warn(alien.PlayerNum, oldGov, lmsg2)
				s.notifyStar(p, g, oldOwner, int(ship.StOrbits), smsg2)
				if !ship.Alive {
					s.post(model.NewsCombat, smsg2)
				}
			}
			sect.Popn = uint16(civ)
			sect.Troops = uint16(mil)
			if sect.Popn+sect.Troops == 0 {
				planet.Info[sect.Owner-1].MobPoints -= uint64(sect.Mobilization)
				sect.Owner = 0
			}
			_ = s.store.PutRace(alien)
			_ = s.store.PutRace(Race)
		}
		_ = s.store.PutPlanet(planet)
	}

	// Move if sector is now friendly/empty.
	succ := ship.Alive && (int(sect.Owner) == p || Race.IsAllied(int(sect.Owner)) || sect.Owner == 0)
	if succ && ship.Popn > 0 {
		msg := fmt.Sprintf("%s moving from %d,%d to %d,%d on %s.\n",
			shipDisplayName(ship),
			ship.LandX, ship.LandY, x2, y2,
			s.dispShipLocFull(ship))
		ship.LandX = uint8(x2)
		ship.LandY = uint8(y2)
		s.useFuel(ship, afvFuelCost)
		// Notify all other races that own sectors on this planet.
		for i := 1; i <= len(s.races); i++ {
			if i != p && planet.Info[i-1].NumSectsOwned > 0 {
				s.notify(i, int(star.Governor[i-1]), msg)
			}
		}
	}
	s.ships[uint32(shipno)] = ship
	s.deductAPs(p, g, 1, int(ship.StOrbits), false)
}

// warn sends a message to a player/governor — live if connected, else stored telegram.
// Mirrors warn() in GB_server.c.
func (s *State) warn(playerNum, governor int, text string) {
	s.pushTelegram(playerNum, governor, text)
}
