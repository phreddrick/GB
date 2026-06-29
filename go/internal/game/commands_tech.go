package game

// commands_tech.go — tech investment, examine, and declare commands.
// Mirrors tech.c, examine.c, and declare.c from the C source.

import (
	"fmt"
	"math"
	"strings"

	"github.com/gbgame/gb/internal/model"
	"github.com/gbgame/gb/internal/net"
)

// ---- cmdTech -------------------------------------------------------------

// cmdTech sets the tech investment level on a planet.
// Usage: tech [amount]
// Mirrors technology() in tech.c.
func (s *State) cmdTech(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]
	if dir.Level != model.LevelPlan {
		sess.Send(fmt.Sprintf("scope must be a planet (%d).\n", dir.Level))
		return
	}
	if int(dir.StarNum) >= len(s.stars) || s.stars[dir.StarNum] == nil {
		sess.Send("tech: bad star.\n")
		return
	}
	star := s.stars[dir.StarNum]
	if !controlStar(p, g, star) {
		sess.Send("You are not authorized to do that here.\n")
		return
	}
	if !s.enoughAP(p, g, star.AP[p-1], 1) {
		return
	}

	planet := s.planet(int(dir.StarNum), int(dir.PlanetNum))
	if planet == nil {
		sess.Send("tech: planet not found.\n")
		return
	}

	if len(args) == 0 {
		inv := int(planet.Info[p-1].TechInvest)
		popn := int(planet.Info[p-1].Popn)
		sess.Send(fmt.Sprintf("Current investment : %d    Technology production/update: %.3f\n",
			inv, techProdCalc(inv, popn, s.cfg.TechInvest)))
		return
	}

	invest := atoi(args[0])
	if invest < 0 {
		sess.Send("Illegal value.\n")
		return
	}

	planet.Info[p-1].TechInvest = uint16(invest)
	s.savePlanet(planet)
	s.deductAPs(p, g, 1, int(dir.StarNum), false)

	sess.Send(fmt.Sprintf("   New (ideal) tech production: %.3f (this planet)\n",
		techProdCalc(invest, int(planet.Info[p-1].Popn), s.cfg.TechInvest)))
}

// techProdCalc computes the tech production for a given investment, population,
// and TECH_INVEST constant. Mirrors tech_prod() in tech.c.
func techProdCalc(investment, popn int, techInvest float64) float64 {
	scale := float64(popn) / 10000.0
	return techInvest * math.Log10(float64(investment)*scale+1.0)
}

// ---- cmdExamine ----------------------------------------------------------

// cmdExamine shows information about a ship type.
// Usage: examine <type-letter>
// Mirrors examine.c — reads ship type data from the ship data table.
func (s *State) cmdExamine(p, g int, args []string, sess net.Responder) {
	if len(args) == 0 {
		sess.Send("Usage: examine <type-letter>\n")
		return
	}

	letter := args[0]
	if len(letter) != 1 {
		sess.Send("examine: specify a single type letter.\n")
		return
	}

	var stype model.ShipType = model.NumShipTypes
	for i := model.ShipType(0); i < model.NumShipTypes; i++ {
		if int(i) < len(model.ShipLetters) &&
			strings.EqualFold(string(model.ShipLetters[i]), letter) {
			stype = i
			break
		}
	}
	if stype == model.NumShipTypes {
		sess.Send(fmt.Sprintf("examine: unknown type '%s'.\n", letter))
		return
	}

	name := ""
	if int(stype) < len(model.ShipNames) {
		name = model.ShipNames[stype]
	}
	sess.Send(fmt.Sprintf("=== %c - %s ===\n", model.ShipLetters[stype], name))

	tbl := model.Shipdata[stype]
	sess.Send(fmt.Sprintf("  Tech required   : %d\n", tbl[model.AbilTech]))
	sess.Send(fmt.Sprintf("  Cargo capacity  : %d\n", tbl[model.AbilCargo]))
	sess.Send(fmt.Sprintf("  Hanger capacity : %d\n", tbl[model.AbilHanger]))
	sess.Send(fmt.Sprintf("  Destruct cap.   : %d\n", tbl[model.AbilDestCap]))
	sess.Send(fmt.Sprintf("  Guns            : %d\n", tbl[model.AbilGuns]))
	sess.Send(fmt.Sprintf("  Primary         : %d\n", tbl[model.AbilPrimary]))
	sess.Send(fmt.Sprintf("  Secondary       : %d\n", tbl[model.AbilSecondary]))
	sess.Send(fmt.Sprintf("  Fuel capacity   : %d\n", tbl[model.AbilFuelCap]))
	sess.Send(fmt.Sprintf("  Max crew        : %d\n", tbl[model.AbilMaxCrew]))
	sess.Send(fmt.Sprintf("  Armor           : %d\n", tbl[model.AbilArmor]))
	sess.Send(fmt.Sprintf("  Cost            : %d\n", tbl[model.AbilCost]))
	sess.Send(fmt.Sprintf("  Max speed       : %d\n", tbl[model.AbilSpeed]))
	sess.Send(fmt.Sprintf("  Can land        : %d\n", tbl[model.AbilCanLand]))
	sess.Send(fmt.Sprintf("  Can build       : %d\n", tbl[model.AbilBuild]))
	sess.Send(fmt.Sprintf("  Constructible   : %d\n", tbl[model.AbilConstruct]))
}

// ---- cmdDeclare ----------------------------------------------------------

// cmdDeclare handles diplomatic declarations: war, peace, neutral, allied.
// Usage: declare <war|allied|neutral|peace> <race>
// Mirrors declare() in declare.c.
func (s *State) cmdDeclare(p, g int, args []string, sess net.Responder) {
	if len(args) < 2 {
		sess.Send("Usage: declare <war|allied|neutral|peace> <race>\n")
		return
	}

	declType := strings.ToLower(args[0])
	targetName := args[1]

	// Find target race
	var target *model.Race
	for _, r := range s.races {
		if r == nil {
			continue
		}
		if strings.EqualFold(r.Name, targetName) {
			target = r
			break
		}
	}
	if target == nil {
		sess.Send(fmt.Sprintf("declare: unknown race '%s'.\n", targetName))
		return
	}
	if target.PlayerNum == p {
		sess.Send("declare: can't declare against yourself.\n")
		return
	}

	race := s.raceByNum(p)
	if race == nil {
		return
	}
	if g != 0 {
		sess.Send("Only the governor-0 (race governor) can make diplomatic declarations.\n")
		return
	}

	targetBit := uint64(1) << uint(target.PlayerNum-1)

	switch declType {
	case "war":
		race.AtWar |= targetBit
		race.Allied &^= targetBit
		sess.Send(fmt.Sprintf("You have declared war on %s.\n", target.Name))
		s.notifyRace(target.PlayerNum,
			fmt.Sprintf("%s has declared WAR on you!\n", race.Name))
	case "allied", "alliance":
		race.Allied |= targetBit
		race.AtWar &^= targetBit
		sess.Send(fmt.Sprintf("You have declared alliance with %s.\n", target.Name))
		s.notifyRace(target.PlayerNum,
			fmt.Sprintf("%s has declared alliance with you.\n", race.Name))
	case "neutral", "peace":
		race.AtWar &^= targetBit
		race.Allied &^= targetBit
		sess.Send(fmt.Sprintf("You have declared neutral/peace with %s.\n", target.Name))
		s.notifyRace(target.PlayerNum,
			fmt.Sprintf("%s has declared neutrality/peace with you.\n", race.Name))
	default:
		sess.Send("declare: use 'war', 'allied', 'neutral', or 'peace'.\n")
		return
	}

	_ = s.store.PutRace(race)
}

// cmdRelation shows or sets diplomatic relations.
// Usage: relation [race]
func (s *State) cmdRelation(p, g int, args []string, sess net.Responder) {
	race := s.raceByNum(p)
	if race == nil {
		return
	}

	if len(args) == 0 {
		// Show all diplomatic relations
		sess.Send("Diplomatic status:\n")
		for _, r := range s.races {
			if r == nil || r.PlayerNum == p {
				continue
			}
			bit := uint64(1) << uint(r.PlayerNum-1)
			status := "neutral"
			if race.AtWar&bit != 0 {
				status = "WAR"
			} else if race.Allied&bit != 0 {
				status = "allied"
			}
			sess.Send(fmt.Sprintf("  %-20s : %s\n", r.Name, status))
		}
		return
	}

	// Show relation with a specific race
	targetName := args[0]
	var target *model.Race
	for _, r := range s.races {
		if r == nil {
			continue
		}
		if strings.EqualFold(r.Name, targetName) {
			target = r
			break
		}
	}
	if target == nil {
		sess.Send(fmt.Sprintf("relation: unknown race '%s'.\n", targetName))
		return
	}

	bit := uint64(1) << uint(target.PlayerNum-1)
	if race.AtWar&bit != 0 {
		sess.Send(fmt.Sprintf("You are at WAR with %s.\n", target.Name))
	} else if race.Allied&bit != 0 {
		sess.Send(fmt.Sprintf("You are ALLIED with %s.\n", target.Name))
	} else {
		sess.Send(fmt.Sprintf("You are NEUTRAL with %s.\n", target.Name))
	}
}
