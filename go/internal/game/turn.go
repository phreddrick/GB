package game

import (
	"fmt"
	"log"
	"math"
	"time"

	"github.com/phreddrick/GB/go/internal/model"
)

// doUpdate performs a full game update (production, combat, etc.).
// Corresponds to do_update() + do_turn(update=1) in doturn.c / GB_server.c.
// Called exclusively by the actor goroutine.
func (s *State) doUpdate() {
	s.numUpdates++
	log.Printf("Starting update %d", s.numUpdates)
	s.shout(fmt.Sprintf("\n*** DOING UPDATE %d ***\n", s.numUpdates))

	updateMins := s.cfg.DefaultUpdateMinutes
	s.nextUpdateTime = s.nextUpdateTime.Add(time.Duration(updateMins) * time.Minute)

	segMins := updateMins / s.cfg.MovesPerUpdate
	if segMins < 1 {
		segMins = 1
	}
	s.numSegments = 0
	s.nextSegmentTime = s.nextUpdateTime.Add(-time.Duration(segMins*(s.cfg.MovesPerUpdate-1)) * time.Minute)

	s.doTurn(true)

	s.shout(fmt.Sprintf("\n*** UPDATE %d COMPLETE ***\n", s.numUpdates))
	log.Printf("Update %d complete", s.numUpdates)

	s.persistTiming()
}

// doSegment performs a movement segment between updates.
// Corresponds to do_segment() + do_turn(update=0) in GB_server.c.
// Called exclusively by the actor goroutine.
func (s *State) doSegment() {
	if s.numSegments >= s.cfg.MovesPerUpdate-1 {
		// Already fired all segments for this update cycle; skip.
		return
	}
	s.numSegments++
	log.Printf("Starting segment %d/%d", s.numSegments, s.cfg.MovesPerUpdate-1)
	s.shout(fmt.Sprintf("\n*** DOING MOVEMENT SEGMENT %d/%d ***\n",
		s.numSegments, s.cfg.MovesPerUpdate-1))

	segMins := s.cfg.DefaultUpdateMinutes / s.cfg.MovesPerUpdate
	if segMins < 1 {
		segMins = 1
	}
	s.nextSegmentTime = s.nextSegmentTime.Add(time.Duration(segMins) * time.Minute)

	s.doTurn(false)

	s.shout(fmt.Sprintf("\n*** MOVEMENT %d DONE ***\n", s.numSegments))
	log.Printf("Segment %d complete", s.numSegments)

	s.persistTiming()
}

// doTurn executes one game turn.
// If update is true, performs full production + movement; otherwise movement only.
// Corresponds to do_turn() in doturn.c.
func (s *State) doTurn(update bool) {
	// Reload ships from DB to pick up any changes made by player commands
	// since the last turn. Stars and races are kept in memory.
	var err error
	s.ships, err = s.store.AllShips()
	if err != nil {
		log.Printf("doTurn: AllShips: %v", err)
		return
	}

	// Post and clear ground-assault counters accumulated since the last turn.
	s.outputGroundAttacks()

	if update {
		// Reset per-turn accumulators.
		s.power = [model.MaxPlayers]model.Power{}
		// Reset per-turn stinfo scratch data.
		s.stinfo = [model.NumStars][model.MaxPlanets]StarPlanetInfo{}
		// Reset VN brain accumulators.
		s.vnTotalMad = 0
		s.vnMostMad = 0

		// Accumulate VN brain from sdata hit-list, and reset per-race counters.
		// Mirrors the race pre-pass in doturn.c lines 109-138.
		if s.sdata != nil {
			for i, r := range s.races {
				if r == nil {
					continue
				}
				s.vnTotalMad += s.sdata.VNHitList[i]
				if s.vnMostMad == 0 ||
					s.sdata.VNHitList[s.vnMostMad-1] <= s.sdata.VNHitList[i] {
					s.vnMostMad = uint8(i + 1)
				}
			}
		}

		// Reset per-race counters that are recalculated each update.
		for _, r := range s.races {
			if r == nil {
				continue
			}
			r.ControlledPlanets = 0
			r.PlanetPoints = 0
			for j := 0; j <= model.MaxGovernors; j++ {
				if r.Governor[j].Active {
					r.Governor[j].CostTech = 0
					r.Governor[j].Income = 0
				}
			}
		}
	}

	// --- Per-star / per-planet processing ---
	// planetCount counts non-asteroid planets (mirrors Planet_count in C).
	planetCount := 0
	for i, star := range s.stars {
		if star == nil {
			continue
		}
		starPlanets := s.planets[uint8(i)]

		if update {
			s.fixStability(star)
		}

		for pnum := uint8(0); pnum < star.NumPlanets; pnum++ {
			planet := starPlanets[pnum]
			if planet == nil {
				continue
			}
			if planet.Type != model.PlanetAsteroid {
				planetCount++
			}
			if update {
				s.movePlanet(star, planet, i, int(pnum))
			}
			s.doPlanet(star, planet, i, int(pnum), update)
		}
	}

	// --- Mine pre-pass: trigger auto-detonation for armed mines ---
	// Corresponds to `for(i=1;i<=Num_ships;i++) domine(i,0)` in doturn.c.
	for shipNo, ship := range s.ships {
		if ship != nil && ship.Alive && ship.Type == model.StypeMine {
			s.doMine(shipNo, false)
		}
	}

	// --- Per-ship processing: slower ships first (speed 0..9) ---
	for speed := 0; speed <= 9; speed++ {
		for _, ship := range s.ships {
			if ship == nil || !ship.Alive {
				continue
			}
			if int(ship.Speed) != speed {
				continue
			}
			s.doShip(ship, update)
			// Missiles not aimed at a planet fire now (planet-aimed ones fire later).
			if ship.Type == model.StypeMissile && !s.shipAttacksPlanet(ship) {
				s.doMissile(ship)
			}
		}
	}

	// --- Rebuild orbit chains ---
	// Clear all nextship/ships pointers, then re-insert every alive ship.
	// Mirrors the orbit rebuild block in do_turn() in doturn.c.
	s.rebuildOrbitChains()

	// --- ABM pass: ABMs defend their planet ---
	for _, ship := range s.ships {
		if ship != nil && ship.Alive && ship.Type == model.OtypeABM {
			s.doABM(ship)
		}
	}

	// --- Missile attack-planet pass ---
	for _, ship := range s.ships {
		if ship != nil && ship.Alive && ship.Type == model.StypeMissile && s.shipAttacksPlanet(ship) {
			s.doMissile(ship)
		}
	}

	if update {
		// --- Post-ship: per-star accounting ---
		// Compute starpopns[star][player-1] and starnumships[star][player-1]
		// from in-memory planet and ship data (mirrors accumulation done by
		// doplanet/doship in the C version).
		type starAccum struct {
			popn  [model.MaxPlayers]uint64
			ships [model.MaxPlayers]uint16
		}
		starAcc := make([]starAccum, len(s.stars))

		// Accumulate from planets: per-player popn from Info[].
		for starIdx := range s.stars {
			byPlanet := s.planets[uint8(starIdx)]
			for _, planet := range byPlanet {
				if planet == nil {
					continue
				}
				for j := 0; j < model.MaxPlayers; j++ {
					if planet.Info[j].Popn > 0 {
						starAcc[starIdx].popn[j] += uint64(planet.Info[j].Popn)
					}
				}
			}
		}

		// Accumulate from ships.
		for _, ship := range s.ships {
			if ship == nil || !ship.Alive {
				continue
			}
			if ship.WhatOrbits == model.LevelUniv {
				continue
			}
			starIdx := int(ship.StOrbits)
			if starIdx < 0 || starIdx >= len(s.stars) {
				continue
			}
			ownerIdx := int(ship.Owner) - 1
			if ownerIdx < 0 || ownerIdx >= model.MaxPlayers {
				continue
			}
			starAcc[starIdx].ships[ownerIdx]++
			starAcc[starIdx].popn[ownerIdx] += uint64(ship.Popn)
		}

		// Per-star: update inhabited bits, APs, block systems_owned.
		for starIdx, star := range s.stars {
			if star == nil {
				continue
			}

			// inhabited[star] bitmask — which players have any sectors.
			var starInhab uint64
			byPlanet := s.planets[uint8(starIdx)]
			for _, planet := range byPlanet {
				if planet == nil {
					continue
				}
				for j := 0; j < model.MaxPlayers; j++ {
					if planet.Info[j].NumSectsOwned > 0 {
						starInhab |= 1 << uint(j)
						star.SetInhabited(j + 1)
					}
				}
			}

			for i := 0; i < model.MaxPlayers; i++ {
				// Update inhabited bit based on starpopns.
				if starAcc[starIdx].popn[i] > 0 {
					star.SetInhabited(i + 1)
				} else {
					star.ClearInhabited(i + 1)
				}

				if star.IsInhabited(i + 1) {
					aps := int(star.AP[i]) + s.apAdd(
						int(starAcc[starIdx].ships[i]),
						int(starAcc[starIdx].popn[i]),
						s.races[i])
					if aps > 255 { // LIMIT_APs
						aps = 255
					}
					star.AP[i] = uint8(aps)
				}
			}

			// Block systems_owned: a block "owns" a star if all its members
			// that inhabit the star are block members.
			// Mirrors the inhabited/dummy logic in doturn.c lines 333-336.
			if starInhab != 0 {
				for i := 0; i < len(s.races); i++ {
					if i >= model.MaxPlayers {
						break
					}
					bl := s.blocks[i]
					members := bl.Invite & bl.Pledge
					// block owns the star if every inhabiting player is a member
					if members != 0 && (starInhab&^members) == 0 {
						bl.SystemsOwned++
						s.blocks[i] = bl
					}
				}
			}
		}

		// --- Sdata AP update ---
		// Add planet_points APs to universe-level sdata, for governed races.
		for i := 0; i < len(s.races); i++ {
			r := s.races[i]
			if r == nil {
				continue
			}
			// Reset block systems_owned before recount (done above already; VPs set below).
			s.blocks[i].VPs = 0 // will be set below

			if s.governed(r) {
				aps := int(s.sdata.AP[i]) + int(r.PlanetPoints)
				if aps > 255 {
					aps = 255
				}
				s.sdata.AP[i] = uint8(aps)
			}
		}

		// --- Victory score calculation ---
		type victCalc struct {
			numSects int64
			shipCost int64
			shipTech int64
			res      int64
			des      int64
			fuel     int64
			money    int64
			morale   int64
		}
		vcs := make([]victCalc, model.MaxPlayers)

		for i := 0; i < model.MaxPlayers; i++ {
			if i < len(s.races) && s.races[i] != nil {
				vcs[i].morale = s.races[i].Morale
				vcs[i].money = int64(s.races[i].Governor[0].Money)
				for j := 1; j <= model.MaxGovernors; j++ {
					if s.races[i].Governor[j].Active {
						vcs[i].money += int64(s.races[i].Governor[j].Money)
					}
				}
			}
		}

		// Accumulate sectors, resources from planets.
		for _, byPlanet := range s.planets {
			for _, planet := range byPlanet {
				if planet == nil {
					continue
				}
				for j := 0; j < model.MaxPlayers; j++ {
					pi := &planet.Info[j]
					if !pi.Explored {
						continue
					}
					vcs[j].numSects += int64(pi.NumSectsOwned)
					vcs[j].res += int64(pi.Resource)
					vcs[j].des += int64(pi.Destruct)
					vcs[j].fuel += int64(pi.Fuel)
				}
			}
		}

		// Accumulate ship costs and tech.
		for _, ship := range s.ships {
			if ship == nil || !ship.Alive {
				continue
			}
			idx := int(ship.Owner) - 1
			if idx < 0 || idx >= model.MaxPlayers {
				continue
			}
			vcs[idx].shipCost += int64(ship.BuildCost)
			vcs[idx].shipTech += int64(ship.Tech)
			vcs[idx].res += int64(ship.Resource)
			vcs[idx].des += int64(ship.Destruct)
			vcs[idx].fuel += int64(ship.Fuel)
		}

		// Compute victory_score for each race.
		// Mirrors doturn.c lines 402-415.
		const (
			victSect    = 1000
			victShip    = 333
			victTech    = 0.10
			victRes     = 100
			victFuel    = 15
			victMoney   = 5
			victDivisor = 10000
		)
		for i := 0; i < len(s.races); i++ {
			r := s.races[i]
			if r == nil {
				continue
			}
			vc := &vcs[i]
			raw := victSect*vc.numSects +
				victShip*(vc.shipCost+int64(victTech*float64(vc.shipTech))) +
				victRes*(vc.res+vc.des) +
				victFuel*vc.fuel +
				victMoney*vc.money
			raw /= victDivisor
			raw = int64(moraleFactor(float64(vc.morale)) * float64(raw))
			r.VictoryScore = uint64(raw)
		}

		// --- Per-race post-update ---
		const (
			victoryPercent = 10 // from tweakables.h
			victoryUpdates = 5  // from tweakables.h
			mesoPop        = 20000.0
		)
		for i := 0; i < len(s.races); i++ {
			r := s.races[i]
			if r == nil {
				continue
			}

			// Collective intelligence update.
			if r.CollectiveIQ {
				x := (2.0 / math.Pi) * math.Atan(float64(s.power[i].Popn)/mesoPop)
				r.IQ = uint8(float64(r.IQLimit) * x * x)
			}

			r.Tech += float64(r.IQ) / 100.0
			r.Morale += int64(s.power[i].PlanetsOwned)
			s.makeDiscoveries(r)
			r.Turn++

			// Victory turn tracking.
			threshold := uint16(planetCount) * victoryPercent / 100
			if r.ControlledPlanets >= threshold {
				r.VictoryTurns++
			} else {
				r.VictoryTurns = 0
			}

			// If a race controls >50% of VICTORY_PERCENT threshold, everyone
			// gets 100% translation of them.
			halfThreshold := uint16(planetCount) * victoryPercent / 200
			if r.ControlledPlanets >= halfThreshold {
				for j := 0; j < len(s.races); j++ {
					if s.races[j] != nil {
						s.races[j].Translate[i] = 100
					}
				}
			}

			// Block VPs = 10 * systems_owned.
			s.blocks[i].VPs = uint64(s.blocks[i].SystemsOwned) * 10
		}

		// --- Handle victory ---
		s.handleVictory(planetCount, victoryPercent, victoryUpdates)

		// --- Power money: accumulate from governor accounts ---
		for i := 0; i < len(s.races); i++ {
			r := s.races[i]
			if r == nil {
				continue
			}
			s.power[i].Money = r.Governor[0].Money
			for j := 1; j <= model.MaxGovernors; j++ {
				if r.Governor[j].Active {
					s.power[i].Money += r.Governor[j].Money
				}
			}
		}

		s.computePowerBlocks()
		s.flushAll()

		// Persist power and blocks.
		if err := s.store.PutPower(s.power); err != nil {
			log.Printf("doTurn: PutPower: %v", err)
		}
		if err := s.store.PutBlocks(s.blocks); err != nil {
			log.Printf("doTurn: PutBlocks: %v", err)
		}
		if err := s.store.PutPowerBlocks(&s.powerBlocks); err != nil {
			log.Printf("doTurn: PutPowerBlocks: %v", err)
		}
		if err := s.store.PutStarData(s.sdata); err != nil {
			log.Printf("doTurn: PutStarData: %v", err)
		}
	} else {
		// Segment-only flush.
		s.flushAll()
	}

	// Notify all races.
	for _, r := range s.races {
		if r == nil {
			continue
		}
		if update {
			s.notifyRace(r.PlayerNum, "Finished with update.\n")
		} else {
			s.notifyRace(r.PlayerNum, "Finished with movement segment.\n")
		}
	}
}

// flushAll writes all in-memory state back to the DB.
// Called at the end of a full update or segment.
func (s *State) flushAll() {
	if err := s.store.PutShipsBatch(s.ships); err != nil {
		log.Printf("flushAll: PutShipsBatch: %v", err)
	}
	for _, star := range s.stars {
		if star == nil {
			continue
		}
		if err := s.store.PutStar(star); err != nil {
			log.Printf("flushAll: PutStar %d: %v", star.Number, err)
		}
	}
	for _, byPlanet := range s.planets {
		for _, planet := range byPlanet {
			if err := s.store.PutPlanet(planet); err != nil {
				log.Printf("flushAll: PutPlanet: %v", err)
			}
		}
	}
	for _, race := range s.races {
		if race == nil {
			continue
		}
		if err := s.store.PutRace(race); err != nil {
			log.Printf("flushAll: PutRace %d: %v", race.PlayerNum, err)
		}
	}
}

// persistTiming saves update/segment timestamps to the DB for crash recovery.
func (s *State) persistTiming() {
	encode := func(t time.Time) []byte {
		unix := t.Unix()
		b := make([]byte, 8)
		for i := 0; i < 8; i++ {
			b[i] = byte(unix)
			unix >>= 8
		}
		return b
	}
	_ = s.store.PutMeta([]byte("update_time"), encode(s.nextUpdateTime))
	_ = s.store.PutMeta([]byte("segment_time"), encode(s.nextSegmentTime))
}

// rebuildOrbitChains clears all nextship/ships linked-list pointers and
// re-inserts every alive ship into the appropriate orbit list.
// Mirrors the orbit rebuild block in do_turn() in doturn.c.
func (s *State) rebuildOrbitChains() {
	// Clear all pointers.
	for _, ship := range s.ships {
		if ship == nil {
			continue
		}
		ship.NextShip = 0
		ship.Ships = 0
	}
	if s.sdata != nil {
		s.sdata.Ships = 0
	}
	for _, star := range s.stars {
		if star != nil {
			star.Ships = 0
		}
	}
	for _, byPlanet := range s.planets {
		for _, planet := range byPlanet {
			if planet != nil {
				planet.Ships = 0
			}
		}
	}

	// Re-insert each alive ship (reverse order to match C prepend behaviour).
	// Build a sorted list first so we iterate in decreasing ship number order.
	nums := make([]uint32, 0, len(s.ships))
	for n := range s.ships {
		nums = append(nums, n)
	}
	// Sort descending.
	for i := 0; i < len(nums); i++ {
		for j := i + 1; j < len(nums); j++ {
			if nums[j] > nums[i] {
				nums[i], nums[j] = nums[j], nums[i]
			}
		}
	}

	for _, n := range nums {
		ship := s.ships[n]
		if ship == nil || !ship.Alive {
			continue
		}
		switch ship.WhatOrbits {
		case model.LevelUniv:
			if s.sdata != nil {
				ship.NextShip = s.sdata.Ships
				s.sdata.Ships = ship.Number
			}
		case model.LevelStar:
			if int(ship.StOrbits) < len(s.stars) && s.stars[ship.StOrbits] != nil {
				star := s.stars[ship.StOrbits]
				ship.NextShip = star.Ships
				star.Ships = ship.Number
			}
		case model.LevelPlan:
			starMap := s.planets[ship.StOrbits]
			if starMap != nil {
				planet := starMap[ship.PNumOrbits]
				if planet != nil {
					ship.NextShip = planet.Ships
					planet.Ships = ship.Number
				}
			}
		case model.LevelShip:
			parent := s.ships[ship.DestShipNo]
			if parent != nil {
				ship.NextShip = parent.Ships
				parent.Ships = ship.Number
			}
		}
	}
}

// computePowerBlocks aggregates per-player Power[] totals into PowerBlocks.
// Corresponds to compute_power_blocks() in GB_server.c.
func (s *State) computePowerBlocks() {
	pb := &s.powerBlocks
	pb.Time = time.Now().Format(time.RFC1123)

	n := len(s.races)
	for i := 0; i < n && i < model.MaxPlayers; i++ {
		bl := s.blocks[i]
		members := bl.Invite & bl.Pledge // full members: both invited and pledged

		pb.Members[i] = 0
		pb.SectorsOwned[i] = 0
		pb.Money[i] = 0
		pb.Popn[i] = 0
		pb.ShipsOwned[i] = 0
		pb.Resource[i] = 0
		pb.Fuel[i] = 0
		pb.Destruct[i] = 0
		pb.SystemsOwned[i] = bl.SystemsOwned
		pb.VPs[i] = uint16(bl.VPs)

		for j := 0; j < n && j < model.MaxPlayers; j++ {
			if members&(1<<uint(j)) != 0 {
				pb.Members[i]++
				pb.SectorsOwned[i] += s.power[j].SectorsOwned
				pb.Money[i] += s.power[j].Money
				pb.Popn[i] += s.power[j].Popn
				pb.ShipsOwned[i] += s.power[j].ShipsOwned
				pb.Resource[i] += s.power[j].Resource
				pb.Fuel[i] += s.power[j].Fuel
				pb.Destruct[i] += s.power[j].Destruct
			}
		}
	}
}

// fixStability adjusts a star's stability / nova stage at the start of an update.
// Corresponds to fix_stability() in doturn.c.
func (s *State) fixStability(star *model.Star) {
	if star.NovaStage > 0 {
		if star.NovaStage > 14 {
			// Nova is over.
			star.Stability = 20
			star.NovaStage = 0
			msg := fmt.Sprintf("Notice\n\n  Scientists report that star %s\nis no longer undergoing nova.\n", star.Name)
			for i := range s.races {
				if s.races[i] != nil {
					s.notifyRace(s.races[i].PlayerNum, msg)
				}
			}
		} else {
			star.NovaStage++
		}
	} else if star.Stability > 20 {
		a := intRand(-1, 3)
		if int(star.Stability)+a > 100 {
			star.Stability = 100
			star.NovaStage = 1
			msg := fmt.Sprintf("***** BULLETIN! ******\n\n  Scientists report that star %s\nis undergoing nova.\n", star.Name)
			for i := range s.races {
				if s.races[i] != nil {
					s.notifyRace(s.races[i].PlayerNum, msg)
				}
			}
		} else {
			newStab := int(star.Stability) + a
			if newStab < 0 {
				newStab = 0
			}
			star.Stability = uint8(newStab)
		}
	} else {
		a := intRand(-1, 1)
		newStab := int(star.Stability) + a
		if newStab < 0 {
			newStab = 0
		}
		star.Stability = uint8(newStab)
	}
}

// apAdd computes how many APs to add for a player in a star system.
// Corresponds to APadd() in doturn.c.
// sh = number of ships, popn = population, r = race.
func (s *State) apAdd(sh, popn int, r *model.Race) int {
	aps := roundRand(float64(sh)/10.0 + 5.0*math.Log10(1.0+float64(popn)))
	if s.governed(r) {
		return aps
	}
	return roundRand(float64(aps) / 20.0)
}

// governed returns true if the race has an active government ship docked on
// a planet or star.  Corresponds to governed() in doturn.c.
func (s *State) governed(r *model.Race) bool {
	if r == nil || r.GovShip == 0 {
		return false
	}
	gov := s.ships[r.GovShip]
	if gov == nil || !gov.Alive || !gov.Docked {
		return false
	}
	if gov.WhatDest == model.LevelPlan || gov.WhatDest == model.LevelStar {
		return true
	}
	// Gov ship docked on a Habitat that is itself on a planet or star.
	if gov.WhatOrbits == model.LevelShip {
		hab := s.ships[gov.DestShipNo]
		if hab != nil && hab.Type == model.StypeHabitat &&
			(hab.WhatOrbits == model.LevelPlan || hab.WhatOrbits == model.LevelStar) {
			return true
		}
	}
	return false
}

// makeDiscoveries checks tech thresholds and notifies the race of new
// technology discoveries.  Corresponds to make_discoveries() in doturn.c.
func (s *State) makeDiscoveries(r *model.Race) {
	type discovery struct {
		idx     int
		techReq float64
		name    string
	}
	discoveries := []discovery{
		{model.DiscHyperDrive, model.TechHyperDrive, "HYPERDRIVE"},
		{model.DiscLaser, model.TechLaser, "LASER"},
		{model.DiscCEW, model.TechCEW, "CEW"},
		{model.DiscVN, model.TechVN, "VN"},
		{model.DiscTractorBeam, model.TechTractorBeam, "TRACTOR BEAM"},
		{model.DiscTransporter, model.TechTransporter, "TRANSPORTER"},
		{model.DiscAVPM, model.TechAVPM, "AVPM"},
		{model.DiscCloak, model.TechCloak, "CLOAK"},
		{model.DiscWormhole, model.TechWormhole, "WORMHOLE"},
		{model.DiscCrystal, model.TechCrystal, "CRYSTAL"},
	}
	for _, d := range discoveries {
		if !r.HasDiscovery(d.idx) && r.Tech >= d.techReq {
			r.Discoveries[d.idx] = 1
			s.notifyRace(r.PlayerNum,
				fmt.Sprintf("You have discovered %s technology.\n", d.name))
		}
	}
}

// handleVictory checks victory conditions and notifies all races if the game
// is over.  Corresponds to handle_victory() in doturn.c.
func (s *State) handleVictory(planetCount, victoryPercent, victoryUpdates int) {
	if !s.cfg.Victory {
		return
	}

	const bigWinner = 1
	const littleWinner = 2

	type winInfo struct {
		category int
		name     string
		num      int
	}
	wins := make([]winInfo, len(s.races))
	gameOver := 0

	for i, r := range s.races {
		if r == nil {
			continue
		}
		threshold := uint16(planetCount) * uint16(victoryPercent) / 100
		if r.ControlledPlanets >= threshold {
			wins[i] = winInfo{littleWinner, r.Name, r.PlayerNum}
		}
		if r.VictoryTurns >= uint16(victoryUpdates) {
			gameOver++
			wins[i] = winInfo{bigWinner, r.Name, r.PlayerNum}
		}
	}

	if gameOver == 0 {
		return
	}

	for _, r := range s.races {
		if r == nil {
			continue
		}
		s.notifyRace(r.PlayerNum, "*** Attention ***\n")
		s.notifyRace(r.PlayerNum, "This game of Galactic Bloodshed is now *over*\n")
		if gameOver == 1 {
			s.notifyRace(r.PlayerNum, "The big winner is\n")
		} else {
			s.notifyRace(r.PlayerNum, "The big winners are\n")
		}
		for _, w := range wins {
			if w.category == bigWinner {
				s.notifyRace(r.PlayerNum, fmt.Sprintf("*** [%2d] %-30.30s ***\n", w.num, w.name))
			}
		}
		s.notifyRace(r.PlayerNum, "Lesser winners:\n")
		for _, w := range wins {
			if w.category == littleWinner {
				s.notifyRace(r.PlayerNum, fmt.Sprintf("+++ [%2d] %-30.30s +++\n", w.num, w.name))
			}
		}
	}
}

// shipAttacksPlanet returns true if a missile ship is aimed at a planet.
// Mirrors attack_planet() in doturn.c.
func (s *State) shipAttacksPlanet(ship *model.Ship) bool {
	return ship.WhatDest == model.LevelPlan
}

// outputGroundAttacks posts news entries for all inter-race ground assaults
// accumulated since the last turn, then clears the counters.
// Mirrors output_ground_attacks() in doturn.c.
func (s *State) outputGroundAttacks() {
	for i := 0; i < model.MaxPlayers; i++ {
		if i >= len(s.races) || s.races[i] == nil {
			continue
		}
		for j := 0; j < model.MaxPlayers; j++ {
			if j >= len(s.races) || s.races[j] == nil {
				continue
			}
			for star := 0; star < model.NumStars; star++ {
				n := s.groundAssaults[i][j][star]
				if n == 0 {
					continue
				}
				starName := "?"
				if star < len(s.stars) && s.stars[star] != nil {
					starName = s.stars[star].Name
				}
				msg := fmt.Sprintf("%s: %s [%d] assaults %s [%d] %d time(s).\n",
					starName,
					s.races[i].Name, i+1,
					s.races[j].Name, j+1,
					n)
				s.post(model.NewsCombat, msg)
				s.groundAssaults[i][j][star] = 0
			}
		}
	}
}
