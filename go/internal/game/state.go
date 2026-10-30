// Package game implements the core game state actor and command dispatch.
// All mutation of game state happens inside the actor goroutine, serializing
// access without fine-grained locking.
//
// Phase 1 lifecycle (lifecycle.go, admin.go, schedule.go): the actor also
// processes typed administrative requests, recovers panics by halting,
// restores and persists the turn schedule, publishes an operating mode and a
// heartbeat for health probes, and shuts down in an orderly sequence. The
// persistence model is unchanged: a turn is still flushed in many bbolt
// transactions, so a crash or kill during a flush can leave a mixed-turn
// store (CR-18, Phase 3a).
package game

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/model"
	"github.com/phreddrick/GB/go/internal/net"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
)

// Registry is the session registry interface the game uses to push output.
type Registry interface {
	NotifyRace(playerNum int, text string)
	Notify(playerNum, governor int, text string) bool
	Broadcast(senderPlayerNum int, text string)
	Shout(text string)
	ConnectedPlayers() []net.SessionInfo
}

// State is the central game state. It owns all in-memory game data and
// serializes mutations through a single goroutine (actor model).
// The networking layer calls methods on State from per-player goroutines;
// those methods communicate with the actor via channels.
type State struct {
	cfg     *config.Config
	store   *db.Store
	logger  *slog.Logger
	metrics *Metrics
	now     func() time.Time

	// Broadcaster: set by the server after construction.
	registry Registry

	// In-memory game data (owned exclusively by the actor goroutine).
	// Populated at startup from the DB and refreshed at turn start.
	stars       []*model.Star
	planets     map[uint8]map[uint8]*model.Planet
	races       []*model.Race // index 0 = player 1
	ships       map[uint32]*model.Ship
	sdata       *model.StarData
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

	// Turn schedule (actor-owned; schedule.go).
	sched schedule

	// Inboxes (other goroutines -> actor goroutine).
	cmdCh   chan *command      // player commands
	adminCh chan *adminRequest // typed administrative and lifecycle requests

	// Lifecycle (lifecycle.go).
	modeVal      atomic.Int32  // Mode
	draining     atomic.Bool   // set by Stop before the actor sees stopCh
	heartbeatNs  atomic.Int64  // last actor heartbeat (unix nanoseconds)
	progress     atomic.Uint64 // turn progress counter
	progressNs   atomic.Int64  // last progress advance (unix nanoseconds)
	turnStartNs  atomic.Int64  // start of the running turn, 0 when idle
	stopCh       chan struct{}
	stopOnce     sync.Once
	stopped      chan struct{} // closed when the actor goroutine has exited
	shutdownErr  error         // set by the actor during shutdown, read after <-stopped
	statusMu     sync.Mutex
	status       statusData
	haltReason   string // under statusMu
	haltStack    string // under statusMu
	testHooks    *testHooks
	loopStarted  bool
	startupError error
}

type command struct {
	playerNum int
	governor  int
	text      string
	sess      net.Responder
	state     atomic.Int32  // reqQueued -> reqStarted (actor) or reqCanceled (caller); lifecycle.go
	done      chan struct{} // closed by the actor after running or skipping the command
	enqueued  time.Time
}

// Options configure New. Zero values select the defaults.
type Options struct {
	Logger   *slog.Logger      // default: slog.Default()
	Registry *metrics.Registry // default: a private registry
	Now      func() time.Time  // default: time.Now
	// TestTurnDelay, when positive, makes every turn sleep this long (in
	// small slices that advance the progress counter) before processing.
	// It exists only so the shutdown tests can observe a turn in flight;
	// gb-server honours it solely when GB_TEST_HOOKS=1.
	TestTurnDelay time.Duration
}

// New creates a State, loads data from the DB, restores the schedule and
// starts the actor goroutine. A store without a universe yields a State in
// ModeUninitialized that serves administrative requests only.
func New(cfg *config.Config, store *db.Store, opts Options) (*State, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Registry == nil {
		opts.Registry = metrics.NewRegistry()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &State{
		cfg:     cfg,
		store:   store,
		logger:  opts.Logger.With("component", "engine"),
		metrics: NewMetrics(opts.Registry),
		now:     opts.Now,
		cmdCh:   make(chan *command, 256),
		adminCh: make(chan *adminRequest, 16),
		stopCh:  make(chan struct{}),
		stopped: make(chan struct{}),
	}
	if opts.TestTurnDelay > 0 {
		delay := opts.TestTurnDelay
		s.testHooks = &testHooks{beforeTurn: func(turnKind) {
			deadline := time.Now().Add(delay)
			for time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
				s.Progress()
			}
		}}
		opts.Logger.Warn("TEST HOOK ACTIVE: every turn is delayed", slog.Duration("delay", delay))
	}
	s.setMode(ModeStarting)
	s.beat()
	if err := s.loadAll(); err != nil {
		return nil, fmt.Errorf("load game state: %w", err)
	}
	initialized, err := store.IsInitialized()
	if err != nil {
		return nil, fmt.Errorf("check universe: %w", err)
	}
	if initialized {
		if err := s.restoreSchedule(s.now()); err != nil {
			return nil, err
		}
		s.setMode(ModeServing)
	} else {
		s.logger.Warn("store holds no universe; waiting for administrative initialisation", slog.String("mode", ModeUninitialized.String()))
		s.setMode(ModeUninitialized)
	}
	s.loopStarted = true
	go s.actorLoop()
	return s, nil
}

// SetRegistry wires up the session registry so game logic can push notifications.
func (s *State) SetRegistry(r Registry) {
	s.registry = r
}

// Metrics returns the engine metric families (for tests and status).
func (s *State) Metrics() *Metrics { return s.metrics }

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

	rec, err := s.store.GetUniverseRecord()
	if err != nil {
		return fmt.Errorf("load universe record: %w", err)
	}
	s.publishWorld(rec)
	return nil
}

// publishWorld refreshes the world part of the status snapshot and gauges.
func (s *State) publishWorld(rec *db.UniverseRecord) {
	stars, races := 0, 0
	for _, st := range s.stars {
		if st != nil {
			stars++
		}
	}
	for _, r := range s.races {
		if r != nil {
			races++
		}
	}
	s.statusMu.Lock()
	s.status.universe = rec
	s.status.stars = stars
	s.status.races = races
	s.status.ships = len(s.ships)
	s.statusMu.Unlock()
	s.metrics.WorldStars.Set(float64(stars))
	s.metrics.WorldRaces.Set(float64(races))
	s.metrics.WorldShips.Set(float64(len(s.ships)))
}

// actorLoop is the single goroutine that processes commands, administrative
// requests and timed events. All game state mutations happen here.
func (s *State) actorLoop() {
	defer close(s.stopped)
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()

	for {
		s.beat()
		s.metrics.QueueDepth.Set(float64(len(s.cmdCh)))

		// Scheduled turns only fire while serving.
		var timerC <-chan time.Time
		var timer *time.Timer
		if s.Mode() == ModeServing {
			sleep := s.nextWake().Sub(s.now())
			if sleep < 0 {
				sleep = 0
			}
			timer = time.NewTimer(sleep)
			timerC = timer.C
		}

		select {
		case <-s.stopCh:
			stopTimer(timer)
			s.shutdownErr = s.orderlyShutdown()
			return

		case req := <-s.adminCh:
			stopTimer(timer)
			s.handleAdmin(req)

		case cmd := <-s.cmdCh:
			stopTimer(timer)
			s.handlePlayerCommand(cmd)

		case <-heartbeat.C:
			stopTimer(timer)

		case <-timerC:
			now := s.now()
			switch {
			case !now.Before(s.sched.nextUpdate):
				// Errors are logged, counted and reflected in the mode by
				// runTurn itself; a scheduled turn has no caller to return to.
				_ = s.runTurn(turnUpdate, "schedule")
			case !now.Before(s.sched.nextSegment):
				_ = s.runTurn(turnSegment, "schedule")
			}
		}
	}
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

// HandleCommand is called by a player's WebSocket goroutine to submit a
// command. It enqueues the command and blocks until the actor processes it,
// the context ends, or the engine stops. A command refused before dispatch
// (engine not serving) is answered on sess with an explanatory line.
func (s *State) HandleCommand(ctx context.Context, playerNum, governor int, text string, sess net.Responder) error {
	if sess == nil {
		return errors.New("HandleCommand: nil responder")
	}
	cmd := &command{
		playerNum: playerNum,
		governor:  governor,
		text:      text,
		sess:      sess,
		done:      make(chan struct{}),
		enqueued:  s.now(),
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: command not accepted: %w (%w)", ErrEngineBusy, ErrNotExecuted, err)
	}
	select {
	case s.cmdCh <- cmd:
	case <-ctx.Done():
		return fmt.Errorf("%w: command not accepted (queue full): %w (%w)", ErrEngineBusy, ErrNotExecuted, ctx.Err())
	case <-s.stopped:
		return ErrEngineStopped
	}
	select {
	case <-cmd.done:
		return nil
	case <-ctx.Done():
		// Same tie-break as admin requests (lifecycle.go): a command canceled
		// while queued is skipped by the actor and never runs later; a
		// command that already started runs to completion, and the caller
		// waits for it so that a session's commands never overlap or reorder.
		if cmd.state.CompareAndSwap(reqQueued, reqCanceled) {
			s.metrics.Canceled.Inc("command")
			return fmt.Errorf("%w: command canceled while queued: %w (%w)", ErrEngineBusy, ErrNotExecuted, ctx.Err())
		}
		select {
		case <-cmd.done:
			return nil
		case <-s.stopped:
			return ErrEngineStopped
		}
	case <-s.stopped:
		if cmd.state.CompareAndSwap(reqQueued, reqCanceled) {
			return ErrEngineStopped
		}
		<-cmd.done
		return nil
	}
}

// handlePlayerCommand runs one player command on the actor goroutine with
// mode checks, panic isolation and metrics.
func (s *State) handlePlayerCommand(cmd *command) {
	defer close(cmd.done)
	if !cmd.state.CompareAndSwap(reqQueued, reqStarted) {
		// Canceled by the waiting session before it started (GB1-01): the
		// player was told it did not run, so it must not run now.
		return
	}
	verb := canonicalVerb(cmd.text)
	mode := s.Mode()
	if s.draining.Load() && mode != ModeHalted {
		mode = ModeQuiesced
	}
	if !mode.AcceptsCommands() && !safeWhileNotServing[verb] {
		_ = cmd.sess.Send(refusalMessage(mode))
		s.metrics.Commands.Inc(verb, "refused")
		return
	}
	start := s.now()
	panicked := s.guard("command", func() { s.dispatchCommand(cmd) }, func(r any) {
		_ = cmd.sess.Send("\n*** Internal error while processing your command. The server has halted for safety; an operator must restart it. ***\n")
		s.metrics.CommandPanics.Inc()
	})
	result := "ok"
	if panicked {
		result = "panic"
	} else if verb == "unknown" {
		result = "unknown"
	}
	s.metrics.Commands.Inc(verb, result)
	if d := s.now().Sub(start); d > time.Second {
		s.logger.Warn("slow command", slog.String("op", "command"), slog.String("type", verb),
			slog.Int("player", cmd.playerNum), slog.Int("governor", cmd.governor), slog.Int64("duration_ms", d.Milliseconds()))
	}
}

func refusalMessage(mode Mode) string {
	switch mode {
	case ModeHalted:
		return "\n*** The server is HALTED after an internal error; commands are refused until an operator restarts it. ***\n"
	case ModeQuiesced:
		return "\n*** The server is paused or shutting down; commands are not accepted right now. ***\n"
	case ModeTurnInProgress:
		return "\n*** A turn is in progress; try again in a moment. ***\n"
	case ModeUninitialized:
		return "\n*** The universe has not been initialised yet. ***\n"
	}
	return "\n*** The server is not accepting commands (" + mode.String() + "). ***\n"
}

// safeWhileNotServing lists verbs that read no world state and may run while
// the engine is quiesced or halted.
var safeWhileNotServing = map[string]bool{"who": true, "help": true, "quit": true}

// commandNames is the dispatch order of dispatchCommand (prefix matching,
// first match wins); canonicalVerb must agree with it so that the metrics
// label is the command actually run.
var commandNames = []string{
	"who", "quit", "help", "announce", "broadcast", "shout", "examine", "survey",
	"build", "upgrade", "make", "mod", "sell", "bid", "map", "move", "deploy",
	"walk", "order", "fire", "bombard", "defend", "detonate", "cew", "land",
	"launch", "load", "unload", "jettison", "dump", "transfer", "mount",
	"unmount", "route", "scrap", "declare", "tech", "power", "block", "telegram",
	"send", "read", "name", "profile", "toggle", "capture", "cs", "dock", "orbit",
	"zoom", "analysis", "autoreport", "max", "relation", "enslave", "capital",
	"mobilize", "dissolve", "fuel", "victory", "rst", "explore",
}

// canonicalVerb returns the bounded command name a text would dispatch to.
func canonicalVerb(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "empty"
	}
	verb := strings.ToLower(fields[0])
	for _, name := range commandNames {
		if matchCmd(verb, name) {
			if name == "send" {
				return "telegram"
			}
			return name
		}
	}
	return "unknown"
}

// dispatchCommand routes a command string to the appropriate handler function.
// This replaces the 140-command switch in process_command() in GB_server.c.
func (s *State) dispatchCommand(cmd *command) {
	text := strings.TrimSpace(cmd.text)
	if text == "" {
		return
	}

	parts := strings.Fields(text)
	verb := strings.ToLower(parts[0])
	args := parts[1:]

	// Convenience shorthands
	p, g, sess := cmd.playerNum, cmd.governor, cmd.sess

	// Test-only fault injection (set from _test.go files in this package).
	if s.testHooks != nil && s.testHooks.panicVerb != "" && verb == s.testHooks.panicVerb {
		panic("injected handler panic (test)")
	}

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
		_ = sess.Send(fmt.Sprintf("Unknown command '%s'. Type 'help' for a list of commands.\n", verb))
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

// Authenticate validates race credentials. It runs on the actor goroutine
// (typed request) so that it never reads race records concurrently with a
// turn (interim mitigation of CR-20; Phase 3a adds a read snapshot). The
// caller's context bounds the wait.
func (s *State) Authenticate(ctx context.Context, raceName, password string, governor int) (net.AuthResult, error) {
	// The result is returned through the reply channel only (GB1-01): a
	// caller that timed out never reads memory the actor is still writing.
	return call(s, ctx, opAuthenticate, func() (net.AuthResult, error) {
		return s.authenticate(raceName, password, governor), nil
	})
}

// authenticate is the actor-side credential check.
func (s *State) authenticate(raceName, password string, governor int) net.AuthResult {
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
	s.logger.Info("player disconnected", slog.String("op", "disconnect"), slog.Int("player", playerNum), slog.Int("governor", governor))
}

// WelcomeMessage returns the greeting shown on login (through the actor).
// If the actor does not answer in time the generic greeting is returned;
// the request is read-only, so a canceled or abandoned one changes nothing.
func (s *State) WelcomeMessage(ctx context.Context, playerNum, governor int) string {
	const generic = "\nWelcome to Galactic Bloodshed\n"
	msg, err := call(s, ctx, opWelcome, func() (string, error) {
		race := s.raceByNum(playerNum)
		if race == nil {
			return generic, nil
		}
		m := fmt.Sprintf("\nWelcome back, %s.\n", race.Name)
		if has, err := s.store.HasTelegrams(playerNum, governor); err == nil && has {
			m += "You have telegram(s) waiting. Use 'read' to read them.\n"
		}
		return m, nil
	})
	if err != nil || msg == "" {
		return generic
	}
	return msg
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
