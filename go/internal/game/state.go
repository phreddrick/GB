// Package game implements the core game state actor and command dispatch.
// All mutation of game state happens inside the actor goroutine, serializing
// access without fine-grained locking.
package game

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gbgame/gb/internal/config"
	"github.com/gbgame/gb/internal/db"
	"github.com/gbgame/gb/internal/model"
	"github.com/gbgame/gb/internal/net"
)

// State is the central game state. It owns all in-memory game data and
// serializes mutations through a single goroutine (actor model).
// The networking layer calls methods on State from per-player goroutines;
// those methods communicate with the actor via channels.
type State struct {
	cfg   *config.Config
	store *db.Store

	// Broadcaster: set by the server after construction.
	registry interface {
		NotifyRace(playerNum int, text string)
		Notify(playerNum, governor int, text string) bool
		Broadcast(senderPlayerNum int, text string)
		Shout(text string)
		ConnectedPlayers() []net.SessionInfo
	}

	// In-memory game data (owned exclusively by the actor goroutine).
	// Populated at startup from the DB and refreshed at turn start.
	mu      sync.RWMutex // protects read-only access from REST handlers
	stars   []*model.Star
	planets map[uint8]map[uint8]*model.Planet
	races   []*model.Race   // index 0 = player 1
	ships   map[uint32]*model.Ship
	sdata   *model.StarData
	power       [model.MaxPlayers]model.Power
	blocks      [model.MaxPlayers]model.Block
	powerBlocks model.PowerBlocks

	// Directories: Dir[playerNum-1][governor]
	dirs [model.MaxPlayers][model.MaxGovernors + 1]model.Directory

	// Per-star/planet scratch data reset at the start of each doTurn.
	// Indexed [starNum][planetNum].
	stinfo [model.NumStars][model.MaxPlanets]StarPlanetInfo

	// VN brain: transient per-turn accumulators, mirrors struct vnbrain in doturn.h.
	// Reset at the start of each update, accumulated during the race pre-pass.
	vnTotalMad uint16 // total VN kills so far in the game
	vnMostMad  uint8  // player number most targeted by berserkers (1-based)

	// Ground-assault counters: groundAssaults[attacker-1][defender-1][starNum].
	// Incremented by cmdWalk / cmdUnload when a player assaults enemy sectors.
	// Posted to news at the start of each doTurn.
	groundAssaults [model.MaxPlayers][model.MaxPlayers][model.NumStars]uint16

	// Timing
	nextUpdateTime  time.Time
	nextSegmentTime time.Time
	numUpdates      int
	numSegments     int

	// Command inbox (player goroutines → actor goroutine)
	cmdCh chan *command

	// Turn in progress: block player commands while update/segment runs
	turnDone chan struct{}
	inTurn   bool
}

type command struct {
	playerNum int
	governor  int
	text      string
	sess      net.Responder
	done      chan struct{}
}

// New creates a State, loads data from the DB, and starts the actor goroutine.
func New(cfg *config.Config, store *db.Store) (*State, error) {
	s := &State{
		cfg:   cfg,
		store: store,
		cmdCh: make(chan *command, 256),
	}
	if err := s.loadAll(); err != nil {
		return nil, fmt.Errorf("load game state: %w", err)
	}
	s.scheduleTimers()
	go s.actorLoop()
	return s, nil
}

// SetRegistry wires up the session registry so game logic can push notifications.
func (s *State) SetRegistry(r interface {
	NotifyRace(playerNum int, text string)
	Notify(playerNum, governor int, text string) bool
	Broadcast(senderPlayerNum int, text string)
	Shout(text string)
	ConnectedPlayers() []net.SessionInfo
}) {
	s.registry = r
}

// loadAll reads all game state from the DB into memory.
func (s *State) loadAll() error {
	var err error

	s.stars, err = s.store.AllStars()
	if err != nil {
		return fmt.Errorf("load stars: %w", err)
	}

	s.planets, err = s.store.AllPlanets()
	if err != nil {
		return fmt.Errorf("load planets: %w", err)
	}

	s.races, err = s.store.AllRaces()
	if err != nil {
		return fmt.Errorf("load races: %w", err)
	}

	s.ships, err = s.store.AllShips()
	if err != nil {
		return fmt.Errorf("load ships: %w", err)
	}

	s.sdata, err = s.store.GetStarData()
	if err != nil {
		return fmt.Errorf("load stardata: %w", err)
	}
	if s.sdata == nil {
		s.sdata = &model.StarData{}
	}

	s.power, err = s.store.GetPower()
	if err != nil {
		return fmt.Errorf("load power: %w", err)
	}

	s.blocks, err = s.store.GetBlocks()
	if err != nil {
		return fmt.Errorf("load blocks: %w", err)
	}

	pb, err := s.store.GetPowerBlocks()
	if err != nil {
		return fmt.Errorf("load power blocks: %w", err)
	}
	s.powerBlocks = *pb

	return nil
}

// scheduleTimers restores timing from DB metadata or initialises fresh timers.
func (s *State) scheduleTimers() {
	updateMins := s.cfg.DefaultUpdateMinutes
	segMins := updateMins / s.cfg.MovesPerUpdate
	if segMins < 1 {
		segMins = 1
	}

	now := time.Now()
	// In a real server, restore from s.store.GetMeta(KeyUpdateTime) here.
	s.nextUpdateTime = now.Add(time.Duration(updateMins) * time.Minute)
	s.nextSegmentTime = now.Add(time.Duration(segMins) * time.Minute)
}

// actorLoop is the single goroutine that processes commands and timed events.
// All game state mutations happen here — no other goroutine touches s.stars etc.
func (s *State) actorLoop() {
	for {
		now := time.Now()

		// Work out the next timer fire so we can sleep efficiently.
		nextWake := s.nextSegmentTime
		if s.nextUpdateTime.Before(nextWake) {
			nextWake = s.nextUpdateTime
		}
		sleep := time.Until(nextWake)
		if sleep < 0 {
			sleep = 0
		}

		timer := time.NewTimer(sleep)
		select {
		case cmd := <-s.cmdCh:
			timer.Stop()
			s.dispatchCommand(cmd)

		case <-timer.C:
			now = time.Now()
			if !now.Before(s.nextUpdateTime) {
				s.doUpdate()
			} else if !now.Before(s.nextSegmentTime) {
				s.doSegment()
			}
		}
	}
}

// HandleCommand is called by a player's WebSocket goroutine to submit a command.
// It enqueues the command and blocks until the actor processes it.
func (s *State) HandleCommand(playerNum, governor int, text string, sess net.Responder) {
	cmd := &command{
		playerNum: playerNum,
		governor:  governor,
		text:      text,
		sess:      sess,
		done:      make(chan struct{}),
	}
	s.cmdCh <- cmd
	<-cmd.done
}

// dispatchCommand routes a command string to the appropriate handler function.
// This replaces the 140-command switch in process_command() in GB_server.c.
func (s *State) dispatchCommand(cmd *command) {
	defer close(cmd.done)

	text := strings.TrimSpace(cmd.text)
	if text == "" {
		return
	}

	parts := strings.Fields(text)
	verb := strings.ToLower(parts[0])
	args := parts[1:]

	// Convenience shorthands
	p, g, sess := cmd.playerNum, cmd.governor, cmd.sess

	// Command dispatch table — prefix matching (mirrors process_command).
	// Each handler is responsible for calling sess.Send() with its output.
	// TODO: implement each handler in its own file under internal/game/cmd/.
	switch {
	case matchCmd(verb, "who"):
		s.cmdWho(p, g, args, sess)
	case matchCmd(verb, "quit"):
		s.cmdQuit(p, g, args, sess)
	case matchCmd(verb, "help"):
		s.cmdHelp(p, g, args, sess)
	case matchCmd(verb, "announce"):
		s.cmdAnnounce(p, g, args, sess)
	case matchCmd(verb, "broadcast"):
		s.cmdBroadcast(p, g, args, sess)
	case matchCmd(verb, "shout"):
		s.cmdShout(p, g, args, sess)
	case matchCmd(verb, "examine"):
		s.cmdExamine(p, g, args, sess)
	case matchCmd(verb, "survey"):
		s.cmdSurvey(p, g, args, sess)
	case matchCmd(verb, "build"):
		s.cmdBuild(p, g, args, sess)
	case matchCmd(verb, "upgrade"):
		s.cmdUpgrade(p, g, args, sess)
	case matchCmd(verb, "make"):
		s.cmdMake(p, g, args, sess)
	case matchCmd(verb, "mod"):
		s.cmdMod(p, g, args, sess)
	case matchCmd(verb, "sell"):
		s.cmdSell(p, g, args, sess)
	case matchCmd(verb, "bid"):
		s.cmdBid(p, g, args, sess)
	case matchCmd(verb, "map"):
		s.cmdMap(p, g, args, sess)
	case matchCmd(verb, "move"):
		s.cmdMove(p, g, args, sess)
	case matchCmd(verb, "deploy"):
		s.cmdDeploy(p, g, args, sess)
	case matchCmd(verb, "walk"):
		s.cmdWalk(p, g, args, sess)
	case matchCmd(verb, "order"):
		s.cmdOrder(p, g, args, sess)
	case matchCmd(verb, "fire"):
		s.cmdFire(p, g, args, sess)
	case matchCmd(verb, "bombard"):
		s.cmdBombard(p, g, args, sess)
	case matchCmd(verb, "defend"):
		s.cmdDefend(p, g, args, sess)
	case matchCmd(verb, "detonate"):
		s.cmdDetonate(p, g, args, sess)
	case matchCmd(verb, "cew"):
		s.cmdCEW(p, g, args, sess)
	case matchCmd(verb, "land"):
		s.cmdLand(p, g, args, sess)
	case matchCmd(verb, "launch"):
		s.cmdLaunch(p, g, args, sess)
	case matchCmd(verb, "load"):
		s.cmdLoad(p, g, args, sess)
	case matchCmd(verb, "unload"):
		s.cmdUnload(p, g, args, sess)
	case matchCmd(verb, "jettison"):
		s.cmdJettison(p, g, args, sess)
	case matchCmd(verb, "dump"):
		s.cmdDump(p, g, args, sess)
	case matchCmd(verb, "transfer"):
		s.cmdTransfer(p, g, args, sess)
	case matchCmd(verb, "mount"):
		s.cmdMount(p, g, args, sess)
	case matchCmd(verb, "unmount"):
		s.cmdUnmount(p, g, args, sess)
	case matchCmd(verb, "route"):
		s.cmdRoute(p, g, args, sess)
	case matchCmd(verb, "scrap"):
		s.cmdScrap(p, g, args, sess)
	case matchCmd(verb, "declare"):
		s.cmdDeclare(p, g, args, sess)
	case matchCmd(verb, "tech"):
		s.cmdTech(p, g, args, sess)
	case matchCmd(verb, "power"):
		s.cmdPower(p, g, args, sess)
	case matchCmd(verb, "block"):
		s.cmdBlock(p, g, args, sess)
	case matchCmd(verb, "telegram"), matchCmd(verb, "send"):
		s.cmdTelegram(p, g, args, sess)
	case matchCmd(verb, "read"):
		s.cmdRead(p, g, args, sess)
	case matchCmd(verb, "name"):
		s.cmdName(p, g, args, sess)
	case matchCmd(verb, "profile"):
		s.cmdProfile(p, g, args, sess)
	case matchCmd(verb, "toggle"):
		s.cmdToggle(p, g, args, sess)
	case matchCmd(verb, "capture"):
		s.cmdCapture(p, g, args, sess)
	case matchCmd(verb, "cs"):
		s.cmdCS(p, g, args, sess)
	case matchCmd(verb, "dock"):
		s.cmdDock(p, g, args, sess)
	case matchCmd(verb, "orbit"):
		s.cmdOrbit(p, g, args, sess)
	case matchCmd(verb, "zoom"):
		s.cmdZoom(p, g, args, sess)
	case matchCmd(verb, "analysis"):
		s.cmdAnalysis(p, g, args, sess)
	case matchCmd(verb, "autoreport"):
		s.cmdAutoreport(p, g, args, sess)
	case matchCmd(verb, "max"):
		s.cmdMax(p, g, args, sess)
	case matchCmd(verb, "relation"):
		s.cmdRelation(p, g, args, sess)
	case matchCmd(verb, "enslave"):
		s.cmdEnslave(p, g, args, sess)
	case matchCmd(verb, "capital"):
		s.cmdCapital(p, g, args, sess)
	case matchCmd(verb, "mobilize"):
		s.cmdMobilize(p, g, args, sess)
	case matchCmd(verb, "dissolve"):
		s.cmdDissolve(p, g, args, sess)
	case matchCmd(verb, "fuel"):
		s.cmdFuel(p, g, args, sess)
	case matchCmd(verb, "victory"):
		s.cmdVictory(p, g, args, sess)
	case matchCmd(verb, "rst"):
		s.cmdRST(p, g, args, sess)
	case matchCmd(verb, "explore"):
		s.cmdExplore(p, g, args, sess)
	default:
		sess.Send(fmt.Sprintf("Unknown command '%s'. Type 'help' for a list of commands.\n", verb))
	}
}

// matchCmd returns true if input is a case-insensitive prefix of command.
// Mirrors the match() prefix-matching function in GB_server.c.
func matchCmd(input, command string) bool {
	if len(input) == 0 || len(input) > len(command) {
		return false
	}
	return strings.EqualFold(command[:len(input)], input)
}

// --- net.GameIface implementation ---

// Authenticate validates race credentials and returns auth info.
func (s *State) Authenticate(raceName, password string, governor int) net.AuthResult {
	for _, race := range s.races {
		if race == nil {
			continue
		}
		if !strings.EqualFold(race.Name, raceName) {
			continue
		}
		if err := bcrypt.CompareHashAndPassword([]byte(race.Password), []byte(password)); err != nil {
			return net.AuthResult{}
		}
		if governor < 0 || governor > int(race.Governors) {
			return net.AuthResult{}
		}
		gov := race.Governor[governor]
		if gov.Password != "" {
			if err := bcrypt.CompareHashAndPassword([]byte(gov.Password), []byte(password)); err != nil {
				return net.AuthResult{}
			}
		}
		return net.AuthResult{OK: true, PlayerNum: race.PlayerNum, God: race.God}
	}
	return net.AuthResult{}
}

// PlayerDisconnect is called when a WebSocket connection closes.
func (s *State) PlayerDisconnect(playerNum, governor int) {
	log.Printf("player %d governor %d disconnected", playerNum, governor)
}

// WelcomeMessage returns the greeting shown on login.
func (s *State) WelcomeMessage(playerNum, governor int) string {
	race := s.raceByNum(playerNum)
	if race == nil {
		return "\nWelcome to Galactic Bloodshed\n"
	}
	msg := fmt.Sprintf("\nWelcome back, %s.\n", race.Name)
	// Append a telegram-waiting notice if there are pending messages.
	if has, err := s.store.HasTelegrams(playerNum, governor); err == nil && has {
		msg += "You have telegram(s) waiting. Use 'read' to read them.\n"
	}
	return msg
}

// TriggerUpdate fires a game update if the correct password is supplied.
func (s *State) TriggerUpdate(password string) bool {
	if s.cfg.ExternalTrigger && password == s.cfg.UpdatePassword {
		s.cmdCh <- &command{text: "@@update"}
		return true
	}
	return false
}

// TriggerSegment fires a movement segment if the correct password is supplied.
func (s *State) TriggerSegment(password string) bool {
	if s.cfg.ExternalTrigger && password == s.cfg.SegmentPassword {
		s.cmdCh <- &command{text: "@@segment"}
		return true
	}
	return false
}

// --- Internal helpers ---

func (s *State) raceByNum(playerNum int) *model.Race {
	if playerNum < 1 || playerNum > model.MaxPlayers {
		return nil
	}
	return s.races[playerNum-1]
}

// notify sends text to a specific governor.
func (s *State) notify(playerNum, governor int, text string) {
	if s.registry != nil {
		s.registry.Notify(playerNum, governor, text)
	}
}

// notifyRace sends text to all governors of a race.
func (s *State) notifyRace(playerNum int, text string) {
	if s.registry != nil {
		s.registry.NotifyRace(playerNum, text)
	}
}

// shout sends text to all connected players.
func (s *State) shout(text string) {
	if s.registry != nil {
		s.registry.Shout(text)
	}
}
