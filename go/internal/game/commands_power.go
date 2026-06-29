package game

// commands_power.go implements cmdPower and cmdBlock.
//
// C sources ported: power.c, prof.c (Estimate_i, round_perc),
//                   victory.c (create_victory_list, victory_sort)
//
//   cmdPower — display the galactic power report, optionally for one player
//   cmdBlock — display alliance block memberships / block power report
//
// The commands share two helpers:
//   estimateI   — format an integer with translation-accuracy fuzzing
//   roundPerc   — round data to the nearest translate-bucket
//   victoryList — return races sorted by victory score (descending)

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gbgame/gb/internal/model"
	"github.com/gbgame/gb/internal/net"
)

// ---- Helpers (from prof.c) ------------------------------------------------

// roundPerc rounds data to the nearest bucket determined by the viewer's
// translation accuracy for race p.  Mirrors round_perc() in prof.c.
//
// translate = 100 → exact value
// translate = 0   → rounds to nearest 101
func roundPerc(data int, viewer *model.Race, p int) int {
	k := 101 - int(viewer.Translate[p-1])
	if k < 1 {
		k = 1
	}
	return (data / k) * k
}

// estimateI formats an integer as a human-readable string, fuzzing it
// according to the viewer's translation accuracy for race p.
// Returns "?" when translate ≤ 10.  Mirrors Estimate_i() in prof.c.
func estimateI(data int, viewer *model.Race, p int) string {
	if int(viewer.Translate[p-1]) <= 10 {
		return "?"
	}
	est := roundPerc(data, viewer, p)
	abs := est
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs < 1000:
		return fmt.Sprintf("%d", est)
	case abs < 10000:
		return fmt.Sprintf("%.1fK", float64(est)/1000.0)
	case abs < 1_000_000:
		return fmt.Sprintf("%.0fK", float64(est)/1000.0)
	default:
		return fmt.Sprintf("%.1fM", float64(est)/1_000_000.0)
	}
}

// ---- Victory list (from victory.c) ----------------------------------------

// victoryEntry is one row in the victory-sorted race list.
type victoryEntry struct {
	raceNum  int
	rawScore int64
	noCount  bool // true → sort to end (God/Guest/dissolved)
}

// victoryList returns all active races sorted by victory_score descending.
// God/Guest/dissolved races are pushed to the end.
// Mirrors create_victory_list() + victory_sort() in victory.c.
func (s *State) victoryList() []victoryEntry {
	entries := make([]victoryEntry, 0, len(s.races))
	for i, r := range s.races {
		if r == nil {
			continue
		}
		noCount := r.God || r.Guest || r.Dissolved
		entries = append(entries, victoryEntry{
			raceNum:  i + 1,
			rawScore: int64(r.VictoryScore),
			noCount:  noCount,
		})
	}
	sort.SliceStable(entries, func(a, b int) bool {
		ea, eb := entries[a], entries[b]
		if ea.noCount != eb.noCount {
			return !ea.noCount // normal races first
		}
		return ea.rawScore > eb.rawScore // higher score first
	})
	return entries
}

// ---- getPlayer: resolve a name-or-number argument to a player number -------

// getPlayerArg resolves a command argument (player number or name) to a
// player number, or 0 if not found.  Mirrors GetPlayer() in GB_server.c.
func (s *State) getPlayerArg(arg string) int {
	// Try numeric first.
	n := 0
	fmt.Sscanf(arg, "%d", &n)
	if n >= 1 && n <= len(s.races) && s.races[n-1] != nil {
		return n
	}
	// Name prefix match.
	for i, r := range s.races {
		if r != nil && strings.EqualFold(r.Name, arg) {
			return i + 1
		}
	}
	// Prefix match.
	for i, r := range s.races {
		if r != nil && strings.HasPrefix(strings.ToLower(r.Name), strings.ToLower(arg)) {
			return i + 1
		}
	}
	return 0
}

// ---- cmdPower ---------------------------------------------------------------

// cmdPower displays the galactic power report.
//
// Syntax (mirrors power() in power.c):
//   power          — all races sorted by VP
//   power <player> — detail line for one race
func (s *State) cmdPower(p, g int, args []string, sess net.Responder) {
	Race := s.raceByNum(p)
	if Race == nil {
		sess.Send("Internal error: your race not found.\n")
		return
	}

	sess.Send("         ========== Galactic Bloodshed Power Report ==========\n")

	rankCol := ""
	if len(args) == 0 {
		rankCol = "rank"
	}

	vnCol := "know"
	if Race.God {
		vnCol = "VNs"
	}
	sendf(sess, "%s  #  Name               VP  mil  civ cash ship pl  res fuel dest morl %s\n",
		rankCol, vnCol)

	if len(args) == 0 {
		// Ranked list — all races the viewer knows about (translate ≥ 10).
		for rank, entry := range s.victoryList() {
			r := s.raceByNum(entry.raceNum)
			if r == nil || r.Dissolved {
				continue
			}
			if int(Race.Translate[entry.raceNum-1]) < 10 {
				continue
			}
			s.powerOutputLine(sess, Race, r, entry.raceNum, rank+1)
		}
	} else {
		// Single-race detail.
		target := s.getPlayerArg(args[0])
		if target == 0 {
			sess.Send("No such player.\n")
			return
		}
		r := s.raceByNum(target)
		if r == nil {
			sess.Send("No such player.\n")
			return
		}
		s.powerOutputLine(sess, Race, r, target, 0)
	}
}

// powerOutputLine formats one row of the power report and sends it.
// rank=0 means no rank prefix.  Mirrors prepare_output_line() in power.c.
func (s *State) powerOutputLine(sess net.Responder, viewer, r *model.Race, i, rank int) {
	pw := s.power[i-1]

	rankStr := ""
	if rank > 0 {
		rankStr = fmt.Sprintf("%2d ", rank)
	}

	// Relationship indicators: +allied, -at war, space neutral.
	relAB := " "
	if viewer.IsAllied(i) {
		relAB = "+"
	} else if viewer.IsAtWar(i) {
		relAB = "-"
	}
	relBA := " "
	if r.IsAllied(viewer.PlayerNum) {
		relBA = "+"
	} else if r.IsAtWar(viewer.PlayerNum) {
		relBA = "-"
	}

	line := rankStr + fmt.Sprintf("[%2d]%s%s%-15.15s %5s",
		i, relAB, relBA, r.Name,
		estimateI(int(r.VictoryScore), viewer, i))
	line += fmt.Sprintf("%5s", estimateI(int(pw.Troops), viewer, i))
	line += fmt.Sprintf("%5s", estimateI(int(pw.Popn), viewer, i))
	line += fmt.Sprintf("%5s", estimateI(int(pw.Money), viewer, i))
	line += fmt.Sprintf("%5s", estimateI(int(pw.ShipsOwned), viewer, i))
	line += fmt.Sprintf("%3s", estimateI(int(pw.PlanetsOwned), viewer, i))
	line += fmt.Sprintf("%5s", estimateI(int(pw.Resource), viewer, i))
	line += fmt.Sprintf("%5s", estimateI(int(pw.Fuel), viewer, i))
	line += fmt.Sprintf("%5s", estimateI(int(pw.Destruct), viewer, i))
	line += fmt.Sprintf("%5s", estimateI(int(r.Morale), viewer, i))

	if viewer.God {
		vnHits := 0
		if s.sdata != nil {
			vnHits = int(s.sdata.VNHitList[i-1])
		}
		line += fmt.Sprintf(" %3d\n", vnHits)
	} else {
		line += fmt.Sprintf(" %3d%%\n", viewer.Translate[i-1])
	}

	sess.Send(line)
}

// ---- cmdBlock ---------------------------------------------------------------

// cmdBlock displays alliance block information.
//
// Syntax (mirrors block() in power.c):
//   block                    — list all blocks with VPs
//   block <player>           — show members and stats of that player's block
//   block player <player>    — show block memberships for that player
func (s *State) cmdBlock(p, g int, args []string, sess net.Responder) {
	Race := s.raceByNum(p)
	if Race == nil {
		sess.Send("Internal error: your race not found.\n")
		return
	}

	n := len(s.races)

	if len(args) >= 2 && strings.EqualFold(args[0], "player") {
		// --- "block player <name>" ---
		target := s.getPlayerArg(args[1])
		if target == 0 {
			sess.Send("No such player.\n")
			return
		}
		r := s.raceByNum(target)
		if r == nil {
			sess.Send("No such player.\n")
			return
		}

		// Member of (both pledged and invited).
		sendf(sess, "Race #%d [%s] is a member of", target, r.Name)
		found := false
		for i := 1; i <= n; i++ {
			bl := s.blocks[i-1]
			if bl.Invite&(1<<uint(target-1)) != 0 && bl.Pledge&(1<<uint(target-1)) != 0 {
				if !found {
					sess.Send(" ")
				} else {
					sess.Send(", ")
				}
				sendf(sess, "%d", i)
				found = true
			}
		}
		if !found {
			sess.Send(" no blocks")
		}
		sess.Send("\n")

		// Invited but not pledged.
		sendf(sess, "Race #%d [%s] has been invited to join", target, r.Name)
		found = false
		for i := 1; i <= n; i++ {
			bl := s.blocks[i-1]
			if bl.Invite&(1<<uint(target-1)) != 0 && bl.Pledge&(1<<uint(target-1)) == 0 {
				if !found {
					sess.Send(" ")
				} else {
					sess.Send(", ")
				}
				sendf(sess, "%d", i)
				found = true
			}
		}
		if !found {
			sess.Send(" no blocks")
		}
		sess.Send("\n")

		// Pledged but not invited.
		sendf(sess, "Race #%d [%s] has pledged", target, r.Name)
		found = false
		for i := 1; i <= n; i++ {
			bl := s.blocks[i-1]
			if bl.Pledge&(1<<uint(target-1)) != 0 && bl.Invite&(1<<uint(target-1)) == 0 {
				if !found {
					sess.Send(" ")
				} else {
					sess.Send(", ")
				}
				sendf(sess, "%d", i)
				found = true
			}
		}
		if !found {
			sess.Send(" no blocks")
		}
		sess.Send("\n")

	} else if len(args) >= 1 {
		// --- "block <player>" — show that block's member list ---
		target := s.getPlayerArg(args[0])
		if target == 0 {
			sess.Send("No such player.\n")
			return
		}
		bl := s.blocks[target-1]
		members := bl.Invite & bl.Pledge // both flags set = full member

		sendf(sess, "         ========== %s Power Report ==========\n", bl.Name)
		sendf(sess, "         \t       %-64.64s\n", bl.Motto)
		sess.Send("  #  Name              troops  pop  money ship  plan  res fuel dest know\n")

		for i := 1; i <= n; i++ {
			if members&(1<<uint(i-1)) == 0 {
				continue
			}
			r := s.raceByNum(i)
			if r == nil || r.Dissolved {
				continue
			}
			pw := s.power[i-1]
			line := fmt.Sprintf("%2d %-20.20s ", i, r.Name)
			line += fmt.Sprintf("%5s", estimateI(int(pw.Troops), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pw.Popn), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pw.Money), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pw.ShipsOwned), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pw.PlanetsOwned), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pw.Resource), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pw.Fuel), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pw.Destruct), Race, i))
			line += fmt.Sprintf(" %3d%%\n", Race.Translate[i-1])
			sess.Send(line)
		}

	} else {
		// --- bare "block" — list all blocks with VPs ---
		pb := s.powerBlocks
		sendf(sess, "         ========== Alliance Blocks as of %s ==========\n", pb.Time)
		sess.Send(" #  Name             memb money popn ship  sys  res fuel dest  VPs know\n")

		for i := 1; i <= n; i++ {
			if pb.VPs[i-1] == 0 {
				continue
			}
			bl := s.blocks[i-1]
			line := fmt.Sprintf("%2d %-19.19s%3d", i, bl.Name, pb.Members[i-1])
			line += fmt.Sprintf("%5s", estimateI(int(pb.Money[i-1]), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pb.Popn[i-1]), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pb.ShipsOwned[i-1]), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pb.SystemsOwned[i-1]), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pb.Resource[i-1]), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pb.Fuel[i-1]), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pb.Destruct[i-1]), Race, i))
			line += fmt.Sprintf("%5s", estimateI(int(pb.VPs[i-1]), Race, i))
			line += fmt.Sprintf(" %3d%%\n", Race.Translate[i-1])
			sess.Send(line)
		}
	}
}

