// gb-server is the Galactic Bloodshed game server (Go port).
//
// Configuration comes from defaults, an optional JSON file (-config or
// GB_CONFIG) and the environment (GB_ADDR, GB_ADMIN_ADDR, GB_DB_PATH,
// GB_JWT_SECRET, GB_ADMIN_TOKEN, ...; see internal/config/env.go). The
// server refuses to start with a missing or placeholder JWT secret or admin
// token, validates the configuration before opening the database, never
// regenerates an existing universe, and shuts down in an orderly sequence
// on SIGINT or SIGTERM.
//
// Exit status:
//
//	0 orderly shutdown after a signal, state flushed
//	1 configuration or startup error, including a listener that cannot bind
//	  (nothing was served)
//	2 shutdown completed while the engine was HALTED: in-memory state was
//	  discarded, not flushed; the store may already contain partially
//	  applied turn changes (CR-18)
//	3 shutdown timed out while a turn was running: the store may contain a
//	  partially applied turn (CR-18)
//	4 flush or database close reported an error during shutdown
//	5 a listener failed while serving (for example a missing TLS
//	  certificate or key); the shutdown sequence still ran and its own
//	  result is logged as shutdown_exit_code, but the process exits 5
//	6 forced exit: a second SIGINT/SIGTERM arrived during shutdown; the
//	  process exits at once without waiting for the actor or closing the
//	  store, and no clean-shutdown guarantee applies (a turn in progress
//	  may leave partially applied changes, CR-18)
//
// Codes 5 and 6 take precedence over the shutdown result: cleanup never
// turns a failure into a success (GB1-05).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/phreddrick/GB/go/internal/admin"
	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/engine"
	"github.com/phreddrick/GB/go/internal/game"
	gbnet "github.com/phreddrick/GB/go/internal/net"
	"github.com/phreddrick/GB/go/internal/platform/logging"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
)

const (
	exitOK             = 0
	exitConfig         = 1
	exitHaltedNoFlush  = 2
	exitShutdownTimout = 3
	exitFlushError     = 4
	exitListenerFailed = 5
	exitForced         = 6
)

func main() {
	os.Exit(run())
}

func run() int {
	cfgPath := flag.String("config", "", "path to JSON config file (overrides GB_CONFIG)")
	flag.Parse()

	// 1. Configuration: load and validate before anything is opened.
	cfg, warnings, err := config.Load(*cfgPath, config.LookupEnv(os.LookupEnv))
	if err != nil {
		fmt.Fprintf(os.Stderr, "gb-server: %v\n", err)
		return exitConfig
	}
	logger := logging.New(os.Stdout, logging.ParseLevel(cfg.LogLevel), "gb-server")
	for _, w := range warnings {
		logger.Warn(w, slog.String("op", "config"))
	}
	logger.Info("configuration loaded", slog.String("op", "config"),
		slog.String("addr", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)), slog.String("admin_addr", cfg.AdminAddr),
		slog.String("db_path", cfg.DBPath), slog.Int("update_minutes", cfg.DefaultUpdateMinutes),
		slog.Int("moves_per_update", cfg.MovesPerUpdate), slog.Bool("enroll_preview", cfg.EnrollPreview),
		slog.Int("shutdown_timeout_s", cfg.ShutdownTimeoutSeconds))

	registry := metrics.NewRegistry()

	// 2. Store and engine.
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		logger.Error("open database", slog.String("op", "startup"), slog.String("db_path", cfg.DBPath), slog.String("err", err.Error()))
		return exitConfig
	}
	gameOpts := game.Options{Logger: logger, Registry: registry}
	if os.Getenv("GB_TEST_HOOKS") == "1" {
		// Test-only: used by the end-to-end shutdown tests. Never set in
		// production configuration.
		if d, err := time.ParseDuration(os.Getenv("GB_TEST_TURN_DELAY")); err == nil && d > 0 {
			gameOpts.TestTurnDelay = d
		}
	}
	gameState, err := game.New(cfg, store, gameOpts)
	if err != nil {
		logger.Error("init game state", slog.String("op", "startup"), slog.String("err", err.Error()))
		_ = store.Close()
		return exitConfig
	}

	// 3. Listeners.
	srv := gbnet.New(cfg, gameState, gbnet.Options{Logger: logger, Registry: registry})
	gameState.SetRegistry(srv.Registry())
	if _, err := srv.Listen(); err != nil {
		logger.Error("player listener", slog.String("op", "startup"), slog.String("err", err.Error()))
		_ = gameState.Stop(context.Background())
		_ = store.Close()
		return exitConfig
	}
	var adm *admin.Server
	if cfg.AdminAddr != "" {
		adm, err = admin.New(admin.Options{
			Addr: cfg.AdminAddr, Token: cfg.AdminToken, Engine: gameState, Logger: logger,
			Registry: registry, Livez: srv.HandleLivez, Readyz: srv.HandleReadyz,
		})
		if err == nil {
			_, err = adm.Listen()
		}
		if err != nil {
			logger.Error("admin listener", slog.String("op", "startup"), slog.String("err", err.Error()))
			_ = gameState.Stop(context.Background())
			_ = store.Close()
			return exitConfig
		}
	} else {
		logger.Warn("admin listener disabled: updates can only run on schedule and the universe cannot be initialised through this process", slog.String("op", "startup"))
	}

	serveErr := make(chan error, 2)
	go func() { serveErr <- srv.Serve() }()
	if adm != nil {
		go func() { serveErr <- adm.Serve() }()
	}

	// 4. Wait for a signal or a listener failure. The signal channel stays
	// registered for the whole shutdown so that a second signal is handled
	// explicitly (exit 6) instead of killing the process by signal.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	listenerFailed := false
	select {
	case sig := <-sigs:
		logger.Info("shutdown signal received", slog.String("op", "shutdown"), slog.String("signal", sig.String()))
	case err := <-serveErr:
		listenerFailed = true
		logger.Error("listener failed; shutting down", slog.String("op", "serve"), slog.String("err", fmt.Sprint(err)))
	}

	// 5. Orderly shutdown within the configured budget, unless a second
	// signal forces an immediate exit.
	done := make(chan int, 1)
	go func() { done <- shutdown(logger, cfg, srv, adm, gameState, store) }()
	var code int
	select {
	case code = <-done:
	case sig := <-sigs:
		logger.Error("second signal during shutdown: forced exit without waiting for the engine or closing the store; no clean-shutdown guarantee, the store may contain partially applied turn changes (CR-18)",
			slog.String("op", "shutdown"), slog.String("signal", sig.String()), slog.Int("exit_code", exitForced))
		return exitForced
	}
	if listenerFailed {
		logger.Error("exiting with failure because a listener failed", slog.String("op", "shutdown"),
			slog.Int("exit_code", exitListenerFailed), slog.Int("shutdown_exit_code", code))
		return exitListenerFailed
	}
	return code
}

// shutdown runs the documented sequence: stop accepting new work, close
// sessions with 1012, stop the actor (letting a running turn finish within
// the budget), flush, close the store. Errors are reported, never hidden.
func shutdown(logger *slog.Logger, cfg *config.Config, srv *gbnet.Server, adm *admin.Server, gameState *game.State, store *db.Store) int {
	budget := time.Duration(cfg.ShutdownTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	start := time.Now()

	// Player and admin listeners stop accepting; WebSocket sessions get 1012.
	if err := srv.Shutdown(ctx); err != nil {
		logger.Warn("player listener shutdown", slog.String("op", "shutdown"), slog.String("err", err.Error()))
	}
	if adm != nil {
		if err := adm.Shutdown(ctx); err != nil {
			logger.Warn("admin listener shutdown", slog.String("op", "shutdown"), slog.String("err", err.Error()))
		}
	}

	code := exitOK
	stopErr := gameState.Stop(ctx)
	switch {
	case stopErr == nil:
	case errors.Is(stopErr, engine.ErrShutdownTimeout):
		logger.Error("actor did not stop within the shutdown budget; exiting with the store possibly mid-flush (CR-18, not crash-consistent)",
			slog.String("op", "shutdown"), slog.Int64("budget_ms", budget.Milliseconds()))
		// Do not close the store under the actor; the OS reclaims the file lock.
		return exitShutdownTimout
	case errors.Is(stopErr, engine.ErrHaltedNotFlushed):
		logger.Error("engine was halted; in-memory state discarded, not flushed; the store may already contain partially applied turn changes (CR-18)", slog.String("op", "shutdown"))
		code = exitHaltedNoFlush
	default:
		logger.Error("flush during shutdown failed", slog.String("op", "shutdown"), slog.String("err", stopErr.Error()))
		code = exitFlushError
	}

	if err := store.Close(); err != nil {
		logger.Error("database close failed", slog.String("op", "shutdown"), slog.String("err", err.Error()))
		if code == exitOK {
			code = exitFlushError
		}
	}
	logger.Info("shutdown complete", slog.String("op", "shutdown"), slog.Int("exit_code", code), slog.Int64("duration_ms", time.Since(start).Milliseconds()))
	return code
}
