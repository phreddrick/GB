package game

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/commandrules"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// Classify the ENTIRE invocation before the legacy loop can run a prefix. Valid
// wholly nonhostile routes retain their existing implementation and limitations.
func (s *State) handleGroundAssault(p, g int, args []string, sess net.Responder, what int) bool {
	refuse := func(text string) bool { _ = sess.Send(text + "; no changes made.\n"); return true }
	if p < 1 || p > model.MaxPlayers || g < 0 || g > model.MaxGovernors {
		return refuse("Invalid movement authority")
	}
	scope := s.dirs[p-1][g]
	if scope.Level != model.LevelPlan {
		return refuse("Wrong scope — change to planet level first")
	}
	planet := s.planets[scope.StarNum][scope.PlanetNum]
	if planet == nil || planet.MaxX == 0 || planet.MaxY == 0 || len(planet.Sectors) != int(planet.MaxX)*int(planet.MaxY) {
		s.halt("ground movement has inconsistent planet; no write attempted", "")
		return refuse("Inconsistent movement world")
	}
	if len(args) < 2 || len(args) > 3 {
		return refuse("Usage: move/deploy x,y directions [amount]")
	}
	xy := strings.Split(args[0], ",")
	if len(xy) != 2 {
		return refuse("Invalid origin coordinates")
	}
	x, e := strconv.ParseUint(xy[0], 10, 8)
	if e != nil {
		return refuse("Invalid origin coordinates")
	}
	y, e := strconv.ParseUint(xy[1], 10, 8)
	if e != nil || x >= uint64(planet.MaxX) || y >= uint64(planet.MaxY) {
		return refuse("Invalid origin coordinates")
	}
	if len(args[1]) == 0 {
		return refuse("Invalid movement direction")
	}
	cx, cy := int(x), int(y)
	hostile := false
	for _, direction := range []byte(args[1]) {
		nx, ny, ok := getMoveDir(direction, cx, cy, planet)
		if !ok || ny < 0 || ny >= int(planet.MaxY) || nx < 0 || nx >= int(planet.MaxX) {
			return refuse("Invalid movement route")
		}
		target := planet.SectorAt(nx, ny)
		hostile = hostile || (target.Owner != 0 && int(target.Owner) != p)
		cx, cy = nx, ny
	}
	if !hostile {
		return false
	}
	if len(args[1]) != 1 {
		return refuse("Multistep hostile movement is deferred")
	}
	r := commandrules.AssaultRequest{X: int(x), Y: int(y), Direction: args[1][0], Military: what == moveMIL, MoveFactor: s.cfg.MoveFactor}
	origin := planet.SectorAt(r.X, r.Y)
	r.Quantity = uint32(origin.Popn)
	if r.Military {
		r.Quantity = uint32(origin.Troops)
	}
	if len(args) == 3 {
		n, e := strconv.ParseUint(args[2], 10, 16)
		if e != nil || n == 0 {
			return refuse("Assault requires a positive representable quantity")
		}
		r.Quantity = uint32(n)
	}
	s.runGroundAssault(p, g, r, sess)
	return true
}

// crypto draws are isolated from the global turn/command RNG. Rejection sampling
// in crypto/rand.Int avoids modulo bias. No persistent seed/replay is claimed.
func secureAssaultDraw(need commandrules.AssaultDraw) (commandrules.AssaultDraw, error) {
	if need.Kind == "round" {
		var b [8]byte
		if _, e := rand.Read(b[:]); e != nil {
			return need, e
		}
		need.Fraction = float64(binary.BigEndian.Uint64(b[:])>>11) / (1 << 53)
		return need, nil
	}
	if need.Kind != "integer" {
		return need, errors.New("invalid assault draw kind")
	}
	n, e := rand.Int(rand.Reader, new(big.Int).SetUint64(uint64(need.Bound)+1))
	if e != nil {
		return need, e
	}
	v := n.Uint64()
	if v > math.MaxUint32 {
		return need, errors.New("invalid assault integer draw")
	}
	need.Integer = uint32(v)
	return need, nil
}
func (s *State) groundAssaultStage(p, g int, r commandrules.AssaultRequest) (*commandWorkspace, db.GroundAssault, string, error) {
	var in db.GroundAssault
	w := newCommandWorkspace(s)
	w.scope = &workspaceScope{p, g, s.dirs[p-1][g]}
	planet, e := w.planet(w.scope.before.StarNum, w.scope.before.PlanetNum)
	if e != nil {
		return nil, in, "", e
	}
	star, e := w.star(planet.StarNum)
	if e != nil {
		return nil, in, "", e
	}
	a, e := w.race(p)
	if e != nil {
		return nil, in, "", e
	}
	x, y, ok := commandrules.AssaultDestination(planet, r)
	if !ok {
		return nil, in, "Invalid assault destination.\n", nil
	}
	d, e := w.race(int(planet.SectorAt(x, y).Owner))
	if e != nil {
		return nil, in, "", e
	}
	input := commandrules.AssaultInput{Player: p, Governor: g, Scope: w.scope.before, Request: r, Planet: w.planets[[2]uint8{planet.StarNum, planet.PlanetNum}].before, Star: w.stars[star.Number].before, Attacker: a, Defender: d}
	seen := map[uint32]bool{}
	for n := planet.Ships; n != 0; {
		if seen[n] {
			return nil, in, "", errors.New("cyclic planet ship chain")
		}
		seen[n] = true
		sh, e := w.ship(n)
		if e != nil {
			return nil, in, "", e
		}
		input.Ships = append(input.Ships, w.ships[n].before)
		if int(sh.Owner) != p && int(sh.Owner) != d.PlayerNum {
			_, already := w.races[int(sh.Owner)]
			race, e := w.race(int(sh.Owner))
			if e != nil {
				return nil, in, "", e
			}
			if !already {
				input.Races = append(input.Races, race)
			}
		}
		n = sh.NextShip
	}
	if e := commandrules.ValidateAssault(input); e != nil {
		if errors.Is(e, commandrules.ErrAssaultMechanical) {
			return nil, in, "Mechanical ground assaults are deferred; no changes made.\n", nil
		}
		return nil, in, "Invalid/unauthorized assault, inconsistent accounting, or insufficient AP; no changes made.\n", nil
	}
	in.Input = input
	in.At = s.now()
	draw := s.assaultRandom
	if draw == nil {
		draw = secureAssaultDraw
	}
	// A bounded human assault uses at most seven round/integer draws. A missing
	// transcript requests the next draw; all previous outcomes are replayed, never
	// rerolled. Storage rejection never re-enters this preparation loop.
	for {
		change, e := commandrules.PlanAssault(input, in.Draws)
		if e == nil {
			in.Proposed = change
			break
		}
		var need *commandrules.AssaultNeedDraw
		if !errors.As(e, &need) {
			return nil, in, "Assault arithmetic/outcome is invalid; no changes made.\n", nil
		}
		if len(in.Draws) >= 7 {
			return nil, in, "", errors.New("unbounded assault transcript")
		}
		value, e := draw(need.Draw)
		if e != nil {
			return nil, in, "Unable to prepare assault randomness; no changes made.\n", nil
		}
		in.Draws = append(in.Draws, value)
	}
	c := in.Proposed
	text := "The invasion was repulsed; try again.\n"
	if c.Outcome == "VICTORY" {
		text = "VICTORY! The sector is yours!\n"
	} else if c.Outcome == "MUTUAL DESTRUCTION" {
		text = "Mutual destruction; neither side wins.\n"
	} else if c.Outcome != "REPULSE" {
		text = "Unopposed occupation; the sector is yours.\n"
	}
	if c.AttackStrength > 0 {
		_ = w.effects.Send(fmt.Sprintf("Attack: %.2f   Defense: %.2f.\n", c.AttackStrength, c.DefenseStrength))
	}
	_ = w.effects.Send(text)
	_ = w.effects.Send(fmt.Sprintf("Casualties: You: %d; Them: %d civ/%d mil. Absorbed: %d. AP: %d.\n", c.AttackerLoss, c.CivilianLoss, c.MilitaryLoss, c.Absorbed, c.APCost))
	if w.effects.failed {
		return nil, in, "Assault response exceeds staging limit; no changes made.\n", nil
	}
	if _, _, e := db.AssaultMessages(in); e != nil {
		return nil, in, "Assault reporting exceeds staging limit; no changes made.\n", nil
	}
	return w, in, "", nil
}

func (s *State) runGroundAssault(p, g int, r commandrules.AssaultRequest, sess net.Responder) (result commandCompletion) {
	outcome := db.CommandNotCommitted
	result = commandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("ground assault %s; command panic; operator reconciliation required; no shutdown flush", outcome), "")
			func() {
				defer func() { _ = recover() }()
				_ = sess.Send(fmt.Sprintf("Ground assault %s; reconciliation required; engine halted.\n", outcome))
			}()
			switch outcome {
			case db.CommandCommitted:
				result = commandCommittedError
			case db.CommandUncertain:
				result = commandUncertain
			}
		}
	}()
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return commandRejected
	}
	w, in, validation, e := s.groundAssaultStage(p, g, r)
	if e != nil {
		s.halt("ground assault has inconsistent actor records; no write attempted; reconciliation required", "")
		_ = sess.Send("Ground assault refused: inconsistent world; engine halted.\n")
		return commandNotCommitted
	}
	if validation != "" {
		_ = sess.Send(validation)
		return commandValidationFailed
	}
	if !w.owns() {
		s.halt("ground assault actor preimages changed; no write attempted", "")
		return commandNotCommitted
	}
	outcome, e = s.store.CommitGroundAssault(in)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !w.owns() {
			s.halt("ground assault COMMITTED; installation identities changed; no shutdown flush", "")
			_ = sess.Send("Ground assault COMMITTED; reconciliation required; engine halted.\n")
			return commandCommittedError
		}
		// No callbacks/allocations between these assignments; all identities were
		// checked together. Preserve the actor records' addresses and sector slice.
		pl := w.planets[[2]uint8{in.Input.Planet.StarNum, in.Input.Planet.PlanetNum}].actor
		sectors := pl.Sectors
		copy(sectors, in.Proposed.Planet.Sectors)
		*pl = *in.Proposed.Planet
		pl.Sectors = sectors
		*w.races[p].actor = *in.Proposed.Attacker
		*w.races[in.Input.Defender.PlayerNum].actor = *in.Proposed.Defender
		*w.stars[in.Input.Star.Number].actor = *in.Proposed.Star
		if s.testHooks != nil && s.testHooks.afterCommandInstall != nil {
			s.testHooks.afterCommandInstall()
		}
		if e == nil {
			_, tele, _ := db.AssaultMessages(in)
			// Never call warn/pushTelegram: the inbox is already committed. A failed
			// online delivery must not create a second inbox write.
			if s.registry != nil {
				delivered := s.registry.Notify(tele.Player, tele.Governor, tele.Text)
				if !delivered && tele.Governor != 0 {
					s.registry.Notify(tele.Player, 0, tele.Text)
				}
			}
			if e = w.effects.release(sess); e == nil {
				return commandCommitted
			}
		}
	}
	s.halt(fmt.Sprintf("ground assault %s; persistence or response failed; reconciliation required; no shutdown flush", outcome), "")
	_ = sess.Send(fmt.Sprintf("Ground assault %s; reconciliation required; engine halted.\n", outcome))
	if outcome == db.CommandCommitted {
		return commandCommittedError
	}
	if outcome == db.CommandUncertain {
		return commandUncertain
	}
	return commandNotCommitted
}
