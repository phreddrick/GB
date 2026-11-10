package game

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
)

// dumpProposal owns the expected/proposed values. Original addresses are kept
// separately and checked before persistence and before any installation.
type dumpProposal struct {
	records db.CommandRecords
	stars   []*model.Star
	planets []*model.Planet
}

func (d *dumpProposal) owns(s *State) bool {
	if len(d.stars) != len(d.records.Stars) || len(d.planets) != len(d.records.Planets) {
		return false
	}
	for i, edit := range d.records.Stars {
		if s.stars[int(edit.Expected.Number)] != d.stars[i] || !reflect.DeepEqual(d.stars[i], edit.Expected) {
			return false
		}
	}
	for i, edit := range d.records.Planets {
		p := edit.Expected
		if s.planets[p.StarNum][p.PlanetNum] != d.planets[i] || !reflect.DeepEqual(d.planets[i], p) {
			return false
		}
	}
	return true
}

// commitDump runs only on the actor. No external callback runs during the
// checked, pointer-preserving assignments. A committed reporting error still
// installs the whole proposal, then halts; uncertain writes never install.
func (s *State) commitDump(d *dumpProposal, sess net.Responder) (ok bool) {
	outcome := db.CommandNotCommitted
	defer func() {
		if recover() != nil {
			s.halt(fmt.Sprintf("dump %s; installation/reporting panic; operator reconciliation required; no shutdown flush", outcome), "")
			_ = sess.Send("Dump persistence requires operator reconciliation; the engine has halted.\n")
			ok = false
		}
	}()
	if s.Mode() != ModeServing {
		_ = sess.Send(refusalMessage(s.Mode()))
		return false
	}
	if !d.owns(s) {
		s.halt("dump actor identities changed; no write attempted; operator reconciliation required", "")
		_ = sess.Send("Dump refused: actor records disagree; the engine has halted.\n")
		return false
	}
	var e error
	outcome, e = s.store.CommitCommandRecords(d.records)
	if outcome == db.CommandCommitted {
		if s.testHooks != nil && s.testHooks.beforeCommandInstall != nil {
			s.testHooks.beforeCommandInstall()
		}
		if !d.owns(s) {
			s.halt("dump COMMITTED; actor installation preconditions failed; operator reconciliation required; no shutdown flush", "")
			_ = sess.Send("Dump records committed; actor reconciliation required; the engine has halted.\n")
			return false
		}
		for i, edit := range d.records.Stars {
			*d.stars[i] = *edit.Proposed
		}
		for i, edit := range d.records.Planets {
			*d.planets[i] = *edit.Proposed
		}
		if e == nil {
			return true
		}
	}
	// Even confirmed aborts halt conservatively: stale preimages or storage faults
	// must not allow a later turn/shutdown to write an unverified actor world.
	s.halt(fmt.Sprintf("dump %s; command persistence failed; operator reconciliation required; no shutdown flush", outcome), "")
	if outcome == db.CommandCommitted {
		_ = sess.Send("Dump records committed; reporting failed; the engine has halted.\n")
	} else {
		_ = sess.Send(fmt.Sprintf("Dump %s; no success confirmed; the engine has halted for operator reconciliation.\n", outcome))
	}
	return false
}

// dumpExploration replaces only cmdDump's coupled operation; it does not stage
// unrelated handlers. The success/notification texts are bounded local effects.
func (s *State) dumpExploration(p, g int, args []string, sess net.Responder) {
	dir := s.dirs[p-1][g]
	apStar := s.stars[int(dir.StarNum)]
	if apStar == nil {
		_ = sess.Send("You are not in a star system.\n")
		return
	}
	if len(args) < 1 {
		_ = sess.Send("Dump to whom?\n")
		return
	}
	target := s.getPlayerArg(args[0])
	src, dst := s.raceByNum(p), s.raceByNum(target)
	if target == 0 || src == nil || dst == nil {
		_ = sess.Send("No such player.\n")
		return
	}
	if src.Guest || dst.Guest {
		_ = sess.Send("Cheater!\n")
		return
	}
	if g != 0 {
		_ = sess.Send("Only leaders are allowed to use dump.\n")
		return
	}
	if !s.enoughAP(p, g, apStar.AP[p-1], 1) {
		return
	}
	selected := map[int]bool{}
	if len(args) == 1 {
		for n, st := range s.stars {
			if st != nil && st.IsExplored(p) {
				selected[n] = true
			}
		}
	} else {
		for _, arg := range args[1:] {
			sn := -1
			for n, st := range s.stars {
				if st != nil && strings.EqualFold(st.Name, arg) {
					sn = n
					break
				}
			}
			if sn < 0 {
				n, e := strconv.Atoi(arg)
				if e == nil && n >= 1 && n <= len(s.stars) {
					sn = n - 1
				}
			}
			if sn < 0 || sn >= len(s.stars) || s.stars[sn] == nil {
				_ = sess.Send("Invalid dump star.\n")
				return
			}
			if s.stars[sn].IsExplored(p) {
				selected[sn] = true
			}
		}
	}
	d := &dumpProposal{records: db.CommandRecords{Player: p, Recipient: target, APStar: dir.StarNum}}
	// Stable ordering bounds the proposal, fault stages and without-prefix success.
	numbers := make([]int, 0, len(selected)+1)
	for n := range selected {
		numbers = append(numbers, n)
	}
	if !selected[int(dir.StarNum)] {
		numbers = append(numbers, int(dir.StarNum))
	}
	sort.Ints(numbers)
	for _, n := range numbers {
		st := s.stars[n]
		if st == nil || int(st.Number) != n || int(st.NumPlanets) > model.MaxPlanets {
			s.halt("dump has invalid actor star identity; operator reconciliation required", "")
			_ = sess.Send("Dump refused: inconsistent world; the engine has halted.\n")
			return
		}
		before, next := *st, *st
		if selected[n] {
			next.SetExplored(target)
		}
		if n == int(dir.StarNum) {
			next.AP[p-1]--
		}
		d.records.Stars = append(d.records.Stars, db.CommandStar{Expected: &before, Proposed: &next, Share: selected[n]})
		d.stars = append(d.stars, st)
		if !selected[n] {
			continue
		}
		for j := uint8(0); j < st.NumPlanets; j++ {
			pl := s.planets[st.Number][j]
			if pl == nil || pl.StarNum != st.Number || pl.PlanetNum != j {
				s.halt("dump has missing or inconsistent actor planet; operator reconciliation required", "")
				_ = sess.Send("Dump refused: inconsistent world; the engine has halted.\n")
				return
			}
			before, next := planetEdit(pl), planetEdit(pl)
			if before.Info[p-1].Explored {
				next.Info[target-1].Explored = true
			}
			d.records.Planets = append(d.records.Planets, db.CommandPlanet{Expected: before, Proposed: next})
			d.planets = append(d.planets, pl)
		}
	}
	if !s.commitDump(d, sess) {
		return
	}
	// Delivery is best-effort after the complete confirmed installation. A panic
	// here cannot roll back the committed records; do not obscure that outcome.
	defer func() {
		if recover() != nil {
			s.halt("dump COMMITTED; output delivery failed; nothing rolled back; no shutdown flush", "")
		}
	}()
	s.warnRace(target, fmt.Sprintf("%s [%d] has given you exploration data.\n", src.Name, p))
	_ = sess.Send("Exploration Data transferred.\n")
}
