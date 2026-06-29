package game

// commands_order.go implements cmdOrder and cmdRoute.
//
// C sources ported: order.c
//   cmdOrder        — order(): list or set ship orders
//   giveOrders      — give_orders(): set one ship's orders from subcommand args
//   dispOrders      — DispOrders(): display current orders for one ship
//   dispOrdersHeader — DispOrdersHeader(): column header line
//   mkExplAimedAt   — mk_expl_aimed_at(): mark target as explored when aiming
//   cmdRoute        — route(): manage merchant shipping routes

import (
	"fmt"
	"math"
	"strings"

	"github.com/gbgame/gb/internal/model"
	"github.com/gbgame/gb/internal/net"
)

// cmdOrder handles "order [<ships> [<subcommand> ...]]".
//
// Modes:
//   order                  — list orders for all own ships
//   order <ships>          — list orders for matching ships
//   order <ships> <sub>... — set and then list orders for matching ships
//
// Mirrors order() in order.c.
func (s *State) cmdOrder(p, g int, args []string, sess net.Responder) {
	s.dispOrdersHeader(sess)

	if len(args) == 0 {
		// List all ships.
		for _, ship := range s.ships {
			if ship == nil || !ship.Alive || int(ship.Owner) != p {
				continue
			}
			if !authorized(g, ship) {
				continue
			}
			s.dispOrders(p, g, ship, sess)
		}
		return
	}

	ships := s.shipsBySpec(p, g, args[0])
	for _, ship := range ships {
		if !ship.Alive || int(ship.Owner) != p {
			continue
		}
		if len(args) > 1 {
			s.giveOrders(p, g, args[1:], ship, sess)
		}
		s.dispOrders(p, g, ship, sess)
	}
}

// giveOrders processes a subcommand and updates the ship's orders.
// Mirrors give_orders() in order.c.
func (s *State) giveOrders(p, g int, args []string, ship *model.Ship, sess net.Responder) {
	if len(args) == 0 {
		return
	}

	if !ship.Active {
		sendf(sess, "%s is irradiated (%d); it cannot be given orders.\n",
			shipDisplayName(ship), ship.Rad)
		return
	}
	if ship.Type != model.OtypeTransDev && ship.Popn == 0 && shipMaxCrew(ship) > 0 {
		sendf(sess, "%s has no crew and is not a robotic ship.\n", shipDisplayName(ship))
		return
	}

	sub := strings.ToLower(args[0])

	switch {
	case matchCmd(sub, "defense"):
		if !ship.CanBombard(model.Shipdata) {
			sess.Send("That ship cannot be assigned those orders.\n")
			return
		}
		if len(args) > 1 && matchCmd(strings.ToLower(args[1]), "off") {
			ship.Protect.Planet = false
		} else {
			ship.Protect.Planet = true
		}

	case matchCmd(sub, "scatter"):
		if ship.Type != model.StypeMissile {
			sess.Send("Only missiles can be given this order.\n")
			return
		}
		if ship.Special.Impact == nil {
			ship.Special.Impact = &model.ImpactSpecial{}
		}
		ship.Special.Impact.Scatter = 1

	case matchCmd(sub, "impact"):
		if ship.Type != model.StypeMissile {
			sess.Send("Only missiles can be designated for this.\n")
			return
		}
		if len(args) < 2 {
			sess.Send("Usage: order <ship> impact <x,y>\n")
			return
		}
		var x, y int
		fmt.Sscanf(args[1], "%d,%d", &x, &y)
		if ship.Special.Impact == nil {
			ship.Special.Impact = &model.ImpactSpecial{}
		}
		ship.Special.Impact.X = uint8(x)
		ship.Special.Impact.Y = uint8(y)
		ship.Special.Impact.Scatter = 0

	case matchCmd(sub, "jump"):
		if !ship.HyperDrive.Has {
			sess.Send("This ship does not have hyper drive capability.\n")
			return
		}
		if ship.Docked {
			sess.Send("That ship is docked. Use 'launch' or 'undock' first.\n")
			return
		}
		if len(args) > 1 && matchCmd(strings.ToLower(args[1]), "off") {
			ship.HyperDrive.On = false
		} else {
			if ship.WhatDest != model.LevelStar && ship.WhatDest != model.LevelPlan {
				sess.Send("Destination must be star or planet.\n")
				return
			}
			ship.HyperDrive.On = true
			ship.Navigate.On = false
			if ship.Mounted {
				ship.HyperDrive.Charge = 1
				ship.HyperDrive.Ready = true
			}
		}

	case matchCmd(sub, "protect"):
		if !ship.CanBombard(model.Shipdata) {
			sess.Send("That ship cannot protect.\n")
			return
		}
		j := uint32(0)
		if len(args) > 1 {
			target := args[1]
			if len(target) > 0 && target[0] == '#' {
				target = target[1:]
			}
			fmt.Sscanf(target, "%d", &j)
		}
		if j == ship.Number {
			sess.Send("You can't do that.\n")
			return
		}
		if j == 0 {
			ship.Protect.On = false
		} else {
			ship.Protect.On = true
			ship.Protect.Ship = uint16(j)
		}

	case matchCmd(sub, "navigate"):
		if len(args) >= 3 {
			var bearing, turns int
			fmt.Sscanf(args[1], "%d", &bearing)
			fmt.Sscanf(args[2], "%d", &turns)
			ship.Navigate.On = true
			ship.Navigate.Bearing = uint16(bearing)
			ship.Navigate.Turns = uint16(turns)
		} else {
			ship.Navigate.On = false
		}
		if ship.HyperDrive.On {
			ship.HyperDrive.On = false
		}

	case matchCmd(sub, "switch"):
		if ship.Type == model.OtypeFactory {
			sess.Send("Use \"on\" to bring factory online.\n")
			return
		}
		if !hasSwitch(ship) {
			sess.Send("That ship does not have an on/off setting.\n")
			return
		}
		if ship.WhatOrbits == model.LevelShip {
			sess.Send("That ship is being transported.\n")
			return
		}
		ship.On = !ship.On
		if ship.On {
			switch ship.Type {
			case model.StypeMine:
				sess.Send("Mine armed and ready.\n")
			case model.OtypeTransDev:
				sess.Send("Transporter ready to receive.\n")
			}
		} else {
			switch ship.Type {
			case model.StypeMine:
				sess.Send("Mine disarmed.\n")
			case model.OtypeTransDev:
				sess.Send("No longer receiving.\n")
			}
		}

	case matchCmd(sub, "destination"):
		if !speedRating(ship) {
			sess.Send("That ship cannot be launched.\n")
			return
		}
		if ship.Docked {
			sess.Send("That ship is docked; use undock or launch first.\n")
			return
		}
		if len(args) < 2 {
			sess.Send("Usage: order <ship> destination <place>\n")
			return
		}
		place := s.getplace(p, g, args[1], true)
		if place.Err {
			return
		}
		if place.Level == model.LevelShip {
			target := s.ships[place.ShipNo]
			if target == nil || !s.followable(ship, target) {
				sess.Send("Warning: that ship is out of range.\n")
				return
			}
			ship.DestShipNo = place.ShipNo
			ship.WhatDest = model.LevelShip
		} else {
			// Anti-cheat: must have explored the star system (unless same star or setting star dest).
			if place.Level != model.LevelUniv &&
				int(ship.StOrbits) != int(place.StarNum) &&
				place.Level != model.LevelStar {
				st := s.stars[place.StarNum]
				if st != nil && !st.IsExplored(p) {
					sess.Send("You haven't explored this system.\n")
					return
				}
			}
			ship.WhatDest = place.Level
			ship.DestStar = place.StarNum
			ship.DestPNum = place.PlanetNum
		}

	case matchCmd(sub, "evade"):
		if shipMaxCrew(ship) > 0 && speedRating(ship) {
			if len(args) > 1 && matchCmd(strings.ToLower(args[1]), "on") {
				ship.Protect.Evade = true
			} else {
				ship.Protect.Evade = false
			}
		}

	case matchCmd(sub, "bombard"):
		if ship.Type == model.OtypeOMCL {
			return
		}
		if !ship.CanBombard(model.Shipdata) {
			sess.Send("This type of ship cannot be set to retaliate.\n")
			return
		}
		if len(args) > 1 && matchCmd(strings.ToLower(args[1]), "off") {
			ship.Bombard = false
		} else {
			ship.Bombard = true
		}

	case matchCmd(sub, "retaliate"):
		if ship.Type == model.OtypeOMCL {
			return
		}
		if !ship.CanBombard(model.Shipdata) {
			sess.Send("This type of ship cannot be set to retaliate.\n")
			return
		}
		if len(args) > 1 && matchCmd(strings.ToLower(args[1]), "off") {
			ship.Protect.Self = false
		} else {
			ship.Protect.Self = true
		}

	case matchCmd(sub, "focus"):
		if ship.Laser == 0 {
			sess.Send("No laser.\n")
			return
		}
		if len(args) > 1 && matchCmd(strings.ToLower(args[1]), "on") {
			ship.Focus = 1
		} else {
			ship.Focus = 0
		}

	case matchCmd(sub, "laser"):
		if ship.Laser == 0 {
			sess.Send("This ship is not equipped with combat lasers.\n")
			return
		}
		if !ship.CanBombard(model.Shipdata) {
			sess.Send("This type of ship cannot be set to retaliate.\n")
			return
		}
		if !ship.Mounted {
			sess.Send("You do not have a crystal mounted.\n")
			return
		}
		if len(args) > 1 && matchCmd(strings.ToLower(args[1]), "on") {
			strength := 0
			if len(args) > 2 {
				fmt.Sscanf(args[2], "%d", &strength)
			}
			ship.FireLaser = uint8(strength)
		} else {
			ship.FireLaser = 0
		}

	case matchCmd(sub, "merchant"):
		if len(args) > 1 && matchCmd(strings.ToLower(args[1]), "off") {
			ship.Merchant = 0
		} else {
			j := 0
			if len(args) > 1 {
				fmt.Sscanf(args[1], "%d", &j)
			}
			if j < 0 || j > model.MaxRoutes {
				sess.Send("Bad route number.\n")
				return
			}
			ship.Merchant = uint8(j)
		}

	case matchCmd(sub, "speed"):
		if !speedRating(ship) {
			sess.Send("This ship does not have a speed rating.\n")
			return
		}
		if len(args) < 2 {
			sess.Send("Usage: order <ship> speed <n>\n")
			return
		}
		j := 0
		fmt.Sscanf(args[1], "%d", &j)
		if j < 0 {
			sess.Send("Specify a positive speed.\n")
			return
		}
		maxSpeed := int(model.Shipdata[ship.Type][model.AbilSpeed])
		if j > maxSpeed {
			j = maxSpeed
		}
		ship.Speed = uint8(j)

	case matchCmd(sub, "salvo"):
		if !ship.CanBombard(model.Shipdata) {
			sess.Send("This ship cannot be set to retaliate.\n")
			return
		}
		if len(args) < 2 {
			sess.Send("Usage: order <ship> salvo <n>\n")
			return
		}
		j := 0
		fmt.Sscanf(args[1], "%d", &j)
		if j < 0 {
			sess.Send("Specify a positive number of guns.\n")
			return
		}
		switch ship.Guns {
		case model.GunPrimary:
			if j > int(ship.Primary) {
				j = int(ship.Primary)
			}
		case model.GunSecondary:
			if j > int(ship.Secondary) {
				j = int(ship.Secondary)
			}
		case model.GunNone:
			j = 0
		}
		ship.Retaliate = uint8(j)

	case matchCmd(sub, "primary"):
		if ship.Primary == 0 {
			sess.Send("This ship does not have primary guns.\n")
			return
		}
		if len(args) < 2 {
			ship.Guns = uint8(model.GunPrimary)
			if ship.Retaliate > ship.Primary {
				ship.Retaliate = ship.Primary
			}
		} else {
			j := 0
			fmt.Sscanf(args[1], "%d", &j)
			if j < 0 {
				sess.Send("Specify a nonnegative number of guns.\n")
				return
			}
			if j > int(ship.Primary) {
				j = int(ship.Primary)
			}
			ship.Retaliate = uint8(j)
			ship.Guns = uint8(model.GunPrimary)
		}

	case matchCmd(sub, "secondary"):
		if ship.Secondary == 0 {
			sess.Send("This ship does not have secondary guns.\n")
			return
		}
		if len(args) < 2 {
			ship.Guns = uint8(model.GunSecondary)
			if ship.Retaliate > ship.Secondary {
				ship.Retaliate = ship.Secondary
			}
		} else {
			j := 0
			fmt.Sscanf(args[1], "%d", &j)
			if j < 0 {
				sess.Send("Specify a nonnegative number of guns.\n")
				return
			}
			if j > int(ship.Secondary) {
				j = int(ship.Secondary)
			}
			ship.Retaliate = uint8(j)
			ship.Guns = uint8(model.GunSecondary)
		}

	case matchCmd(sub, "explosive"):
		switch ship.Type {
		case model.StypeMine, model.OtypeGR:
			ship.Mode = false
		}

	case matchCmd(sub, "radiative"):
		switch ship.Type {
		case model.StypeMine, model.OtypeGR:
			ship.Mode = true
		}

	case matchCmd(sub, "move"):
		if ship.Type != model.OtypeTerra && ship.Type != model.OtypePlow {
			sess.Send("That ship is not a terraformer or a space plow.\n")
			return
		}
		if len(args) < 2 {
			// Default: move south.
			ship.Class = "5"
			break
		}
		moveStr := args[1]
		const maxMoves = 31 // SHIP_NAMESIZE - 1
		truncated := false
		for i := 0; i < len(moveStr); i++ {
			if i == maxMoves {
				sendf(sess, "Warning: that is more than %d moves.\n", maxMoves)
				sess.Send("These move orders have been truncated.\n")
				moveStr = moveStr[:i]
				truncated = true
				break
			}
			c := moveStr[i]
			if c == 'c' || c == 's' {
				if i == 0 && c == 'c' {
					sess.Send("Cycling move orders can not be empty!\n")
					return
				}
				if i < len(moveStr)-1 {
					sendf(sess, "Warning: '%c' should be the last character in the move order.\n", c)
					sess.Send("These move orders have been truncated.\n")
					moveStr = moveStr[:i+1]
					truncated = true
					break
				}
			} else if c < '1' || c > '9' {
				sendf(sess, "'%c' is not a valid move direction.\n", c)
				return
			}
		}
		if !truncated && len(moveStr) == 0 {
			moveStr = "5"
		}
		ship.Class = moveStr
		if ship.Special.Terraform == nil {
			ship.Special.Terraform = &model.TerraformSpecial{}
		}
		ship.Special.Terraform.Index = 0

	case matchCmd(sub, "trigger"):
		if ship.Type != model.StypeMine {
			sess.Send("This ship cannot be assigned a trigger radius.\n")
			return
		}
		if ship.Special.Trigger == nil {
			ship.Special.Trigger = &model.TriggerSpecial{}
		}
		r := 0
		if len(args) > 1 {
			fmt.Sscanf(args[1], "%d", &r)
		}
		if r < 0 {
			r = 0
		}
		ship.Special.Trigger.Radius = uint16(r)

	case matchCmd(sub, "transport"):
		if ship.Type != model.OtypeTransDev {
			sess.Send("This ship is not a transporter.\n")
			return
		}
		t := 0
		if len(args) > 1 {
			fmt.Sscanf(args[1], "%d", &t)
		}
		if ship.Special.Transport == nil {
			ship.Special.Transport = &model.TransportSpecial{}
		}
		if uint32(t) == ship.Number {
			sess.Send("A transporter cannot transport to itself.")
			ship.Special.Transport.Target = 0
		} else {
			ship.Special.Transport.Target = uint16(t)
			sendf(sess, "Target ship is %d.\n", t)
		}

	case matchCmd(sub, "aim"):
		if !canAim(ship) {
			sess.Send("You can't aim that kind of ship.\n")
			return
		}
		if ship.Type != model.OtypeGtele && ship.Type != model.OtypeTract &&
			ship.Fuel < s.cfg.FuelManeuver {
			sendf(sess, "Not enough maneuvering fuel (%.2f).\n", s.cfg.FuelManeuver)
			return
		}
		if ship.Type == model.StypeMirror && ship.Docked {
			sess.Send("docked; use undock or launch first.\n")
			return
		}
		if len(args) < 2 {
			sess.Send("Usage: order <ship> aim <place>\n")
			return
		}
		place := s.getplace(p, g, args[1], true)
		if place.Err {
			sess.Send("Error in destination.\n")
			return
		}
		if ship.Special.AimedAt == nil {
			ship.Special.AimedAt = &model.AimedAtSpecial{}
		}
		ship.Special.AimedAt.Level = place.Level
		ship.Special.AimedAt.PlanetNum = place.PlanetNum
		ship.Special.AimedAt.StarNum = place.StarNum
		ship.Special.AimedAt.ShipNo = uint16(place.ShipNo)
		if ship.Type != model.OtypeTract && ship.Type != model.OtypeGtele {
			s.useFuel(ship, s.cfg.FuelManeuver)
		}
		if ship.Type == model.OtypeGtele || ship.Type == model.OtypeStele {
			s.mkExplAimedAt(p, g, ship, sess)
		}
		sendf(sess, "Aimed at %s\n", s.prinAimedAt(p, g, ship))

	case matchCmd(sub, "intensity"):
		if ship.Type != model.StypeMirror {
			return
		}
		if ship.Special.AimedAt == nil {
			ship.Special.AimedAt = &model.AimedAtSpecial{}
		}
		val := 0
		if len(args) > 1 {
			fmt.Sscanf(args[1], "%d", &val)
		}
		if val < 0 {
			val = 0
		}
		if val > 100 {
			val = 100
		}
		ship.Special.AimedAt.Intensity = int8(val)

	case matchCmd(sub, "on"):
		if !hasSwitch(ship) {
			sess.Send("This ship does not have an on/off setting.\n")
			return
		}
		if ship.Damage > 0 && ship.Type != model.OtypeFactory {
			sess.Send("Damaged ships cannot be activated.\n")
			return
		}
		if ship.On {
			sess.Send("This ship is already activated.\n")
			return
		}
		if ship.Type == model.OtypeFactory {
			if err := s.activateFactory(p, g, ship, sess); err != nil {
				return
			}
		}
		ship.On = true

	case matchCmd(sub, "off"):
		if ship.Type == model.OtypeFactory && ship.On {
			sess.Send("You can't deactivate a factory once it's online. Consider using 'scrap'.\n")
			return
		}
		ship.On = false

	default:
		sendf(sess, "Unknown order '%s'.\n", sub)
		return
	}

	ship.Notified = false
	s.saveShip(ship)
}

// activateFactory handles the resource cost of bringing a factory online.
// Returns non-nil error if activation fails (caller should not set On=true).
func (s *State) activateFactory(p, g int, ship *model.Ship, sess net.Responder) error {
	if ship.WhatOrbits == model.LevelShip {
		// Factory on a habitat.
		carrier := s.ships[ship.DestShipNo]
		if carrier == nil {
			sess.Send("Carrier ship not found.\n")
			return fmt.Errorf("carrier not found")
		}
		if carrier.Type != model.StypeHabitat {
			sess.Send("The factory is currently being transported.\n")
			return fmt.Errorf("not a habitat")
		}
		const habFactOnCost = 4   // HAB_FACT_ON_COST from tweakables.h
		const habFactSize = 0.2  // HAB_FACT_SIZE from tweakables.h
		onCost := uint16(habFactOnCost * int(ship.BuildCost))
		hangerNeeded := (1 + int(habFactSize*float64(s.shipSize(ship)))) -
			(int(carrier.MaxHanger-carrier.Hanger) + int(ship.Size))
		if carrier.Resource < onCost {
			sendf(sess, "You don't have %d resources on Habitat #%d to activate this factory.\n",
				onCost, ship.DestShipNo)
			return fmt.Errorf("insufficient resources")
		}
		if hangerNeeded > 0 {
			sendf(sess, "Not enough hanger space free on Habitat #%d. Need %d more.\n",
				ship.DestShipNo, hangerNeeded)
			return fmt.Errorf("insufficient hanger")
		}
		carrier.Resource -= onCost
		carrier.Hanger -= ship.Size
		ship.Size = uint16(1 + int(habFactSize*float64(s.shipSize(ship))))
		carrier.Hanger += ship.Size
		s.saveShip(carrier)
		sendf(sess, "Factory activated at a cost of %d resources.\n", onCost)
	} else if !landed(ship) {
		sess.Send("You cannot activate the factory here.\n")
		return fmt.Errorf("not landed")
	} else {
		planet, err := s.store.GetPlanet(ship.DestStar, ship.DestPNum)
		if err != nil || planet == nil {
			sess.Send("Planet not found.\n")
			return fmt.Errorf("planet not found")
		}
		onCost := uint16(2 * int(ship.BuildCost))
		if planet.Info[p-1].Resource < onCost {
			sendf(sess, "You don't have %d resources on the planet to activate this factory.\n", onCost)
			return fmt.Errorf("insufficient resources")
		}
		planet.Info[p-1].Resource -= onCost
		if err := s.store.PutPlanet(planet); err != nil {
			s.logError("PutPlanet", err)
		}
		sendf(sess, "Factory activated at a cost of %d resources.\n", onCost)
	}
	return nil
}

// dispOrdersHeader prints the column header for the orders display.
// Mirrors DispOrdersHeader() in order.c.
func (s *State) dispOrdersHeader(sess net.Responder) {
	sess.Send("    #       name       sp orbits     destin     options\n")
}

// dispOrders displays the current orders for one ship.
// Mirrors DispOrders() in order.c.
func (s *State) dispOrders(p, g int, ship *model.Ship, sess net.Responder) {
	if int(ship.Owner) != p || !authorized(g, ship) || !ship.Alive {
		return
	}

	// Destination string.
	var destStr string
	if ship.Docked {
		if ship.WhatDest == model.LevelShip {
			destStr = fmt.Sprintf("D#%d", ship.DestShipNo)
		} else {
			destStr = fmt.Sprintf("L%2d,%-2d", ship.LandX, ship.LandY)
		}
	} else {
		destStr = s.prinShipDest(p, g, ship)
	}

	// Hyperdrive indicator character.
	hyperChar := ' '
	if ship.HyperDrive.Has {
		if ship.Mounted {
			hyperChar = '+'
		} else {
			hyperChar = '*'
		}
	}

	line := fmt.Sprintf("%5d %c %14.14s %c%1d %-10s %-10.10s",
		ship.Number, model.ShipLetters[ship.Type], ship.Name,
		hyperChar, ship.Speed,
		s.dispShipLocBrief(ship),
		destStr)

	if ship.HyperDrive.On {
		readyStr := "charging"
		if ship.HyperDrive.Ready {
			readyStr = "ready"
		}
		line += fmt.Sprintf("/jump %s %d", readyStr, ship.HyperDrive.Charge)
	}
	if ship.Protect.Self {
		line += "/retal"
	}
	if ship.Guns == uint8(model.GunPrimary) {
		line += gunTypeStr("/lgt", "/med", "/hvy", ship.PrimType) + " primary"
	} else if ship.Guns == uint8(model.GunSecondary) {
		line += gunTypeStr("/lgt", "/med", "/hvy", ship.SecType) + " secndry"
	}
	if ship.FireLaser != 0 {
		line += fmt.Sprintf("/laser %d", ship.FireLaser)
	}
	if ship.Focus != 0 {
		line += "/focus"
	}
	if ship.Retaliate != 0 {
		line += fmt.Sprintf("/salvo %d", ship.Retaliate)
	}
	if ship.Protect.Planet {
		line += "/defense"
	}
	if ship.Protect.On {
		line += fmt.Sprintf("/prot %d", ship.Protect.Ship)
	}
	if ship.Navigate.On {
		line += fmt.Sprintf("/nav %d (%d)", ship.Navigate.Bearing, ship.Navigate.Turns)
	}
	if ship.Merchant != 0 {
		line += fmt.Sprintf("/merchant %d", ship.Merchant)
	}
	if hasSwitch(ship) {
		if ship.On {
			line += "/on"
		} else {
			line += "/off"
		}
	}
	if ship.Protect.Evade {
		line += "/evade"
	}
	if ship.Bombard {
		line += "/bomb"
	}
	if ship.Type == model.StypeMine || ship.Type == model.OtypeGR {
		if ship.Mode {
			line += "/radiate"
		} else {
			line += "/explode"
		}
	}
	if (ship.Type == model.OtypeTerra || ship.Type == model.OtypePlow) &&
		ship.Special.Terraform != nil {
		idx := int(ship.Special.Terraform.Index)
		cls := ship.Class
		suffix := ""
		if idx < len(cls) {
			suffix = cls[idx:]
		}
		// If it ends with 'c' (cycle), show the full cycle from current index.
		if len(suffix) > 0 && suffix[len(suffix)-1] == 'c' {
			line += fmt.Sprintf("/move %s", suffix)
		} else {
			line += fmt.Sprintf("/move %s", suffix)
		}
	}
	if ship.Type == model.StypeMissile && ship.WhatDest == model.LevelPlan &&
		ship.Special.Impact != nil {
		if ship.Special.Impact.Scatter != 0 {
			line += "/scatter"
		} else {
			line += fmt.Sprintf("/impact %d,%d",
				ship.Special.Impact.X, ship.Special.Impact.Y)
		}
	}
	if ship.Type == model.StypeMine && ship.Special.Trigger != nil {
		line += fmt.Sprintf("/trigger %d", ship.Special.Trigger.Radius)
	}
	if ship.Type == model.OtypeTransDev && ship.Special.Transport != nil {
		line += fmt.Sprintf("/target %d", ship.Special.Transport.Target)
	}
	if ship.Type == model.StypeMirror && ship.Special.AimedAt != nil {
		line += fmt.Sprintf("/aim %s/int %d",
			s.prinAimedAt(p, g, ship),
			ship.Special.AimedAt.Intensity)
	}

	sess.Send(line + "\n")

	// Hyperdrive fuel estimate.
	if ship.HyperDrive.On && int(ship.DestStar) < len(s.stars) && s.stars[ship.DestStar] != nil {
		dst := s.stars[ship.DestStar]
		d := math.Sqrt(distSq(ship.XPos, ship.YPos, dst.XPos, dst.YPos))
		distFac := s.cfg.HyperDistFactor * (ship.Tech + 100.0)
		var fuse float64
		if ship.Mounted && d > distFac {
			fuse = s.cfg.HyperDriveFuelUse * math.Sqrt(ship.Mass) * (d / distFac)
		} else {
			fuse = s.cfg.HyperDriveFuelUse * math.Sqrt(ship.Mass) * (d / distFac) * (d / distFac)
		}
		sendf(sess, "  *** distance %.0f - jump will cost %.1ff ***\n", d, fuse)
		if ship.Fuel < fuse {
			sess.Send("Your ship cannot carry enough fuel to do this jump.\n")
		}
	}
}

// prinAimedAt returns a string representation of what the ship is aimed at.
// Mirrors prin_aimed_at() in order.c.
func (s *State) prinAimedAt(p, g int, ship *model.Ship) string {
	if ship.Special.AimedAt == nil {
		return "(none)"
	}
	aa := ship.Special.AimedAt
	place := model.Place{
		Level:     aa.Level,
		StarNum:   aa.StarNum,
		PlanetNum: aa.PlanetNum,
		ShipNo:    uint32(aa.ShipNo),
	}
	return s.dispPlace(p, g, place)
}

// prinShipDest returns a string representation of a ship's navigation destination.
// Mirrors prin_ship_dest() in order.c.
func (s *State) prinShipDest(p, g int, ship *model.Ship) string {
	place := model.Place{
		Level:     ship.WhatDest,
		StarNum:   ship.DestStar,
		PlanetNum: ship.DestPNum,
		ShipNo:    ship.DestShipNo,
	}
	return s.dispPlace(p, g, place)
}

// dispShipLocBrief returns a brief location string (e.g. "/Sol" or "/Sol/Earth").
// Mirrors Dispshiploc_brief() in getplace.c.
func (s *State) dispShipLocBrief(ship *model.Ship) string {
	switch ship.WhatOrbits {
	case model.LevelStar:
		return fmt.Sprintf("/%s", s.starName(int(ship.StOrbits)))
	case model.LevelPlan:
		return fmt.Sprintf("/%s/%s",
			s.starName(int(ship.StOrbits)),
			s.planetName(int(ship.StOrbits), int(ship.PNumOrbits)))
	case model.LevelShip:
		return fmt.Sprintf("#%d", ship.DestShipNo)
	default:
		return "/"
	}
}

// dispPlace converts a Place to a display string.
// Mirrors Dispplace() in getplace.c.
func (s *State) dispPlace(p, g int, place model.Place) string {
	switch place.Level {
	case model.LevelStar:
		return fmt.Sprintf("/%s", s.starName(int(place.StarNum)))
	case model.LevelPlan:
		return fmt.Sprintf("/%s/%s",
			s.starName(int(place.StarNum)),
			s.planetName(int(place.StarNum), int(place.PlanetNum)))
	case model.LevelShip:
		return fmt.Sprintf("#%d", place.ShipNo)
	default:
		return "/"
	}
}

// mkExplAimedAt marks the aimed-at location as explored by the player.
// Used by survey telescope (OtypeStele) and gravity telescope (OtypeGtele).
// Mirrors mk_expl_aimed_at() in order.c.
func (s *State) mkExplAimedAt(p, g int, ship *model.Ship, sess net.Responder) {
	if ship.Special.AimedAt == nil {
		return
	}
	aa := ship.Special.AimedAt
	snum := int(aa.StarNum)

	switch aa.Level {
	case model.LevelUniv:
		sess.Send("There is nothing out here to aim at.")

	case model.LevelStar:
		st := s.stars[snum]
		if st == nil {
			return
		}
		sendf(sess, "Star %s ", s.prinAimedAt(p, g, ship))
		d := dist(ship.XPos, ship.YPos, st.XPos, st.YPos)
		rng := teleRange(ship.Type, ship.Tech)
		if d <= rng {
			st.SetExplored(p)
			if err := s.store.PutStar(st); err != nil {
				s.logError("PutStar", err)
			}
			sendf(sess, "Surveyed, distance %g.\n", d)
		} else {
			sendf(sess, "Too far to see (%g, max %g).\n", d, rng)
		}

	case model.LevelPlan:
		st := s.stars[snum]
		if st == nil {
			return
		}
		pl, err := s.store.GetPlanet(uint8(snum), aa.PlanetNum)
		if err != nil || pl == nil {
			return
		}
		sendf(sess, "Planet %s ", s.prinAimedAt(p, g, ship))
		d := dist(ship.XPos, ship.YPos, st.XPos+pl.XPos, st.YPos+pl.YPos)
		rng := teleRange(ship.Type, ship.Tech)
		if d <= rng {
			st.SetExplored(p)
			pl.Info[p-1].Explored = true
			if err := s.store.PutPlanet(pl); err != nil {
				s.logError("PutPlanet", err)
			}
			sendf(sess, "Surveyed, distance %g.\n", d)
		} else {
			sendf(sess, "Too far to see (%g, max %g).\n", d, rng)
		}

	case model.LevelShip:
		sess.Send("You can't see anything of use there.\n")
	}
}

// canAim returns true if this ship type can be aimed.
// Mirrors can_aim() — steles, gravity teles, tractor beams, mirrors, and weapons.
func canAim(ship *model.Ship) bool {
	switch ship.Type {
	case model.OtypeStele, model.OtypeGtele, model.OtypeTract, model.StypeMirror:
		return true
	}
	return ship.CanBombard(model.Shipdata)
}

// gunTypeStr returns the gun weight prefix based on gun type code.
func gunTypeStr(lgt, med, hvy string, gunType uint8) string {
	const (
		GunLight  = 1
		GunMedium = 2
		GunHeavy  = 3
	)
	switch gunType {
	case GunLight:
		return lgt
	case GunMedium:
		return med
	case GunHeavy:
		return hvy
	default:
		return "/none"
	}
}

// cmdRoute handles "route [<n> [<subcommand> <arg>]]".
//
// Modes:
//   route                     — list all active routes for current planet
//   route <n>                 — show one route
//   route <n> activate        — activate route
//   route <n> deactivate      — deactivate route
//   route <n> <place>         — set destination
//   route <n> land <x,y>      — set landing sector
//   route <n> load <fdxr>     — set load commodities
//   route <n> unload <fdxr>   — set unload commodities
//
// Mirrors route() in order.c.
func (s *State) cmdRoute(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]

	if dir.Level != model.LevelPlan {
		sess.Send("You have to 'cs' to a planet to examine routes.\n")
		return
	}

	planet, err := s.store.GetPlanet(dir.StarNum, dir.PlanetNum)
	if err != nil || planet == nil {
		sess.Send("Planet data not found.\n")
		return
	}

	if len(args) == 0 {
		// List all active routes.
		for i := 1; i <= model.MaxRoutes; i++ {
			r := planet.Info[p-1].Route[i-1]
			if !r.Set {
				continue
			}
			st := s.stars[r.DestStar]
			destName := "(unknown)"
			if st != nil && int(r.DestPlanet) < len(st.PlanetNames) {
				destName = fmt.Sprintf("%s/%s", st.Name, st.PlanetNames[r.DestPlanet])
			}
			line := fmt.Sprintf("%2d  land %2d,%2d   ", i, r.X, r.Y)
			line += "load: "
			line += routeCommodStr(r.Load)
			line += "  unload: "
			line += routeCommodStr(r.Unload)
			line += fmt.Sprintf("  -> %s\n", destName)
			sess.Send(line)
		}
		sess.Send("Done.\n")
		return
	}

	i := 0
	fmt.Sscanf(args[0], "%d", &i)
	if i < 1 || i > model.MaxRoutes {
		sess.Send("Bad route number.\n")
		return
	}

	if len(args) == 1 {
		// Show one route.
		r := planet.Info[p-1].Route[i-1]
		if r.Set {
			st := s.stars[r.DestStar]
			destName := "(unknown)"
			if st != nil && int(r.DestPlanet) < len(st.PlanetNames) {
				destName = fmt.Sprintf("%s/%s", st.Name, st.PlanetNames[r.DestPlanet])
			}
			line := fmt.Sprintf("%2d  land %2d,%2d   ", i, r.X, r.Y)
			if r.Load != 0 {
				line += "load: " + routeCommodStr(r.Load)
			}
			if r.Unload != 0 {
				line += "  unload: " + routeCommodStr(r.Unload)
			}
			line += fmt.Sprintf("  ->  %s\n", destName)
			sess.Send(line)
		}
		sess.Send("Done.\n")
		return
	}

	// Route modification.
	sub := strings.ToLower(args[1])
	switch {
	case matchCmd(sub, "activate"):
		planet.Info[p-1].Route[i-1].Set = true
		sess.Send("Set.\n")
	case matchCmd(sub, "deactivate"):
		planet.Info[p-1].Route[i-1].Set = false
		sess.Send("Set.\n")
	case matchCmd(sub, "land"):
		if len(args) < 3 {
			sess.Send("Usage: route <n> land <x,y>\n")
			return
		}
		var x, y int
		fmt.Sscanf(args[2], "%d,%d", &x, &y)
		maxX := int(planet.MaxX) - 1
		maxY := int(planet.MaxY) - 1
		if x < 0 || x > maxX || y < 0 || y > maxY {
			sess.Send("Bad sector coordinates.\n")
			return
		}
		planet.Info[p-1].Route[i-1].X = uint8(x)
		planet.Info[p-1].Route[i-1].Y = uint8(y)
		sess.Send("Set.\n")
	case matchCmd(sub, "load"):
		if len(args) < 3 {
			sess.Send("Usage: route <n> load <fdxr>\n")
			return
		}
		planet.Info[p-1].Route[i-1].Load = parseCommodMask(args[2])
		sess.Send("Set.\n")
	case matchCmd(sub, "unload"):
		if len(args) < 3 {
			sess.Send("Usage: route <n> unload <fdxr>\n")
			return
		}
		planet.Info[p-1].Route[i-1].Unload = parseCommodMask(args[2])
		sess.Send("Set.\n")
	default:
		// Treat as a destination place.
		place := s.getplace(p, g, args[1], true)
		if place.Err {
			sess.Send("Illegal destination.\n")
			return
		}
		if place.Level != model.LevelPlan {
			sess.Send("You have to designate a planet.\n")
			return
		}
		planet.Info[p-1].Route[i-1].DestStar = place.StarNum
		planet.Info[p-1].Route[i-1].DestPlanet = place.PlanetNum
		sess.Send("Set.\n")
	}

	if err := s.store.PutPlanet(planet); err != nil {
		s.logError("PutPlanet", err)
	}
}

// routeCommodStr converts a commodity bitmask to a display string (e.g. "f dr").
func routeCommodStr(mask uint8) string {
	var b strings.Builder
	if mask&uint8(model.MFuel) != 0 {
		b.WriteByte('f')
	} else {
		b.WriteByte(' ')
	}
	if mask&uint8(model.MDestruct) != 0 {
		b.WriteByte('d')
	} else {
		b.WriteByte(' ')
	}
	if mask&uint8(model.MResources) != 0 {
		b.WriteByte('r')
	} else {
		b.WriteByte(' ')
	}
	if mask&uint8(model.MCrystals) != 0 {
		b.WriteByte('x')
	} else {
		b.WriteByte(' ')
	}
	return b.String()
}

// parseCommodMask converts a string like "fdr" to a commodity bitmask.
func parseCommodMask(s string) uint8 {
	var mask uint8
	for _, c := range s {
		switch c {
		case 'f':
			mask |= uint8(model.MFuel)
		case 'd':
			mask |= uint8(model.MDestruct)
		case 'r':
			mask |= uint8(model.MResources)
		case 'x':
			mask |= uint8(model.MCrystals)
		}
	}
	return mask
}
