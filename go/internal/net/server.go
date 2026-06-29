package net

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gbgame/gb/internal/config"
)

// GameIface is the interface the network layer uses to interact with game logic.
// It is satisfied by *game.State. Defined here to avoid an import cycle.
type GameIface interface {
	// Authentication
	Authenticate(raceName, password string, governor int) AuthResult

	// Command dispatch (replaces process_command in GB_server.c)
	HandleCommand(playerNum, governor int, cmd string, sess Responder)

	// Player lifecycle
	PlayerDisconnect(playerNum, governor int)
	WelcomeMessage(playerNum, governor int) string

	// Triggered updates (replaces @@update / @@segment commands)
	TriggerUpdate(password string) bool
	TriggerSegment(password string) bool
}

// AuthResult is returned by GameIface.Authenticate.
type AuthResult struct {
	OK        bool
	PlayerNum int
	God       bool
}

// Responder is the per-session send interface.
// Allows game logic to send messages without importing the net package.
type Responder interface {
	Send(text string) error
	SendJSON(v interface{}) error
}

// Server is the HTTP/WebSocket server.
type Server struct {
	cfg      *config.Config
	game     GameIface
	registry *SessionRegistry
	http     *http.Server
}

// New creates a Server bound to the given config and game interface.
func New(cfg *config.Config, game GameIface) *Server {
	srv := &Server{
		cfg:      cfg,
		game:     game,
		registry: newRegistry(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.handleWS)
	mux.HandleFunc("/auth/login", srv.handleLogin)
	mux.HandleFunc("/status", srv.handleStatus)
	mux.HandleFunc("/admin/update", srv.handleAdminUpdate)
	mux.HandleFunc("/admin/segment", srv.handleAdminSegment)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv.http = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return srv
}

// Run starts the server and blocks until a shutdown signal is received.
func (srv *Server) Run() error {
	addr := srv.http.Addr
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	errCh := make(chan error, 1)
	go func() {
		if srv.cfg.TLSCert != "" && srv.cfg.TLSKey != "" {
			log.Printf("GB server listening on https://%s", addr)
			errCh <- srv.http.ListenAndServeTLS(srv.cfg.TLSCert, srv.cfg.TLSKey)
		} else {
			log.Printf("GB server listening on http://%s (no TLS)", addr)
			errCh <- srv.http.ListenAndServe()
		}
	}()

	select {
	case err := <-errCh:
		if err != http.ErrServerClosed {
			return err
		}
	case sig := <-quit:
		log.Printf("received signal %v — shutting down", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.http.Shutdown(ctx)
}

// Registry returns the session registry (used by game logic to notify players).
func (srv *Server) Registry() *SessionRegistry {
	return srv.registry
}

// --- REST handlers ---

// handleStatus handles GET /status — returns connected players and basic stats.
func (srv *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	players := srv.registry.ConnectedPlayers()
	writeJSON(w, map[string]interface{}{
		"connected": len(players),
		"players":   players,
	})
}

// handleAdminUpdate handles POST /admin/update — triggers a game update.
// Body: {"password": "..."}
func (srv *Server) handleAdminUpdate(w http.ResponseWriter, r *http.Request) {
	if !srv.cfg.ExternalTrigger {
		http.Error(w, "external trigger disabled", http.StatusForbidden)
		return
	}
	var req struct{ Password string `json:"password"` }
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !srv.game.TriggerUpdate(req.Password) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]string{"status": "update triggered"})
}

// handleAdminSegment handles POST /admin/segment — triggers a movement segment.
func (srv *Server) handleAdminSegment(w http.ResponseWriter, r *http.Request) {
	if !srv.cfg.ExternalTrigger {
		http.Error(w, "external trigger disabled", http.StatusForbidden)
		return
	}
	var req struct{ Password string `json:"password"` }
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !srv.game.TriggerSegment(req.Password) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]string{"status": "segment triggered"})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}

func decodeJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
