package game

// commands.go contains stub implementations of player commands not yet ported.
// Each function signature mirrors the pattern used in the C source:
//   func (s *State) cmdFoo(playerNum, governor int, args []string, sess net.Responder)
//
// Full implementations will be added in subsequent phases of the port.
// The stubs emit a "not yet implemented" message so the server is runnable
// without crashing.

// Implemented in other files:
//   cmdBuild      — build.go
//   cmdUpgrade    — build.go
//   cmdMake       — build.go
//   cmdMod        — build.go
//   cmdSell       — build.go
//   cmdBid        — build.go
//   cmdCS         — commands_cs.go
//   cmdExamine    — commands_tech.go
//   cmdTech       — commands_tech.go
//   cmdDeclare    — commands_tech.go
//   cmdRelation   — commands_tech.go
//   cmdLand       — commands_nav.go
//   cmdLaunch     — commands_nav.go
//   cmdScrap      — commands_nav.go
//   cmdCapture    — commands_nav.go
//   cmdCapital    — commands_misc.go
//   cmdZoom       — commands_misc.go
//   cmdOrbit      — commands_misc.go
//   cmdAutoreport — commands_misc.go
//   cmdMobilize   — commands_misc.go
//   cmdDissolve   — commands_misc.go
//   cmdEnslave    — commands_misc.go
//   cmdFuel       — commands_misc.go
//   cmdAnalysis   — commands_misc.go
//   cmdExplore    — commands_misc.go
//   cmdToggle     — commands_profile.go
//   cmdProfile    — commands_profile.go
//   cmdName       — commands_profile.go
//   cmdMax        — commands_profile.go
//   cmdSurvey     — commands_profile.go
//   cmdMap        — commands_profile.go
//   cmdVictory    — commands_profile.go
//   cmdRST        — commands_profile.go
//   cmdDock       — commands_profile.go
//   cmdFire       — commands_fire.go
//   cmdBombard    — commands_fire.go
//   cmdDefend     — commands_fire.go
//   cmdDetonate   — commands_fire.go
//   cmdCEW        — commands_fire.go
//   cmdPower      — commands_power.go
//   cmdBlock      — commands_power.go
//   cmdTelegram   — commands_teleg.go
//   cmdRead       — commands_teleg.go
//   cmdMove       — commands_move.go
//   cmdDeploy     — commands_move.go
//   cmdWalk       — commands_move.go

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/phreddrick/GB/go/internal/net"
)

func (s *State) cmdWho(p, g int, args []string, sess net.Responder) {
	if s.registry == nil {
		sess.Send("No other players connected.\n")
		return
	}
	players := s.registry.ConnectedPlayers()
	if len(players) == 0 {
		sess.Send("No other players connected.\n")
		return
	}
	sess.Send(" #  Name                 Governor\n")
	for _, info := range players {
		race := s.raceByNum(info.PlayerNum)
		name := "(unknown)"
		if race != nil {
			name = race.Name
		}
		godStr := ""
		if info.God {
			godStr = " [God]"
		}
		sess.Send(fmt.Sprintf("%2d  %-20s  %d%s\n", info.PlayerNum, name, info.Governor, godStr))
	}
}

func (s *State) cmdQuit(p, g int, args []string, sess net.Responder) {
	sess.Send("\n*** Thank you for playing Galactic Bloodshed ***\n")
	// The WebSocket goroutine will detect the closed connection.
	type closer interface{ Close() }
	if c, ok := sess.(closer); ok {
		c.Close()
	}
}

func (s *State) cmdHelp(p, g int, args []string, sess net.Responder) {
	// With an argument, look for <arg>.txt; otherwise fall back to help.txt.
	filename := "help.txt"
	if len(args) > 0 {
		// Sanitise to prevent path traversal — only allow plain names.
		base := filepath.Base(args[0])
		if base != "." && base != ".." {
			filename = base + ".txt"
		}
	}
	path := filepath.Join(s.cfg.DocsDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if len(args) > 0 {
			sess.Send(fmt.Sprintf("No help available for '%s'.\n", args[0]))
		} else {
			sess.Send("Help file not found.\n")
		}
		return
	}
	sess.Send(string(data))
}

func (s *State) cmdAnnounce(p, g int, args []string, sess net.Responder) {
	if len(args) == 0 {
		sess.Send("Usage: announce <message>\n")
		return
	}
	race := s.raceByNum(p)
	name := "Unknown"
	if race != nil {
		name = race.Name
	}
	msg := fmt.Sprintf("[%s announces]: %s\n", name, joinArgs(args))
	s.shout(msg)
}

func (s *State) cmdBroadcast(p, g int, args []string, sess net.Responder) {
	if len(args) == 0 {
		sess.Send("Usage: broadcast <message>\n")
		return
	}
	race := s.raceByNum(p)
	name := "Unknown"
	if race != nil {
		name = race.Name
	}
	msg := fmt.Sprintf("[%s broadcasts]: %s\n", name, joinArgs(args))
	if s.registry != nil {
		s.registry.Broadcast(p, msg)
	}
	sess.Send(msg)
}

func (s *State) cmdShout(p, g int, args []string, sess net.Responder) {
	s.cmdAnnounce(p, g, args, sess)
}

// cmdSurvey is implemented in commands_profile.go.
// cmdBuild is implemented in build.go.
// cmdMap is implemented in commands_profile.go.

// cmdMove, cmdDeploy, cmdWalk are implemented in commands_move.go.

// cmdOrder is implemented in commands_order.go.
// cmdRoute is implemented in commands_order.go.

// cmdFire, cmdBombard, cmdDefend, cmdDetonate, cmdCEW are implemented in commands_fire.go.

//   cmdLoad      — commands_load.go
//   cmdUnload    — commands_load.go
//   cmdJettison  — commands_load.go
//   cmdDump      — commands_load.go
//   cmdTransfer  — commands_load.go
//   cmdMount     — commands_load.go
//   cmdUnmount   — commands_load.go

// cmdPower and cmdBlock are implemented in commands_power.go.

// cmdTelegram is implemented in commands_teleg.go.
// cmdRead is implemented in commands_teleg.go.

// cmdName, cmdProfile, cmdToggle, cmdDock, cmdMax are implemented in commands_profile.go.

// cmdVictory and cmdRST are implemented in commands_profile.go.

// joinArgs concatenates command arguments with spaces.
func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}
