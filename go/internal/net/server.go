package net

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/phreddrick/GB/go/internal/config"
	"github.com/phreddrick/GB/go/internal/engine"
	"github.com/phreddrick/GB/go/internal/enroll"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
)

// GameIface is the interface the network layer uses to interact with game
// logic. It is satisfied by *game.State. Every call that reaches the actor
// takes a context that bounds the wait.
type GameIface interface {
	// Authentication (runs on the actor).
	Authenticate(ctx context.Context, raceName, password string, governor int) (AuthResult, error)

	// Command dispatch (replaces process_command in GB_server.c).
	HandleCommand(ctx context.Context, playerNum, governor int, cmd string, sess Responder) error

	// Player lifecycle.
	PlayerDisconnect(playerNum, governor int)
	WelcomeMessage(ctx context.Context, playerNum, governor int) string

	// Enrollment through the actor (CR-03); disabled by default (Decision 9).
	Enroll(ctx context.Context, req *enroll.Request) (enroll.Response, error)

	// Operating state for health endpoints and status (never blocks).
	Health(now time.Time) engine.Health
	Status() engine.Status
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

// Metrics are the transport families.
type Metrics struct {
	Sessions   *metrics.Gauge   // gb_sessions_connected
	Logins     *metrics.Counter // gb_logins_total{result}
	WSDropped  *metrics.Counter // gb_ws_dropped_sessions_total{reason}
	WSRejected *metrics.Counter // gb_ws_rejected_total{reason}
	// WSCommandsRejected counts commands refused at the session queue.
	WSCommandsRejected *metrics.Counter // gb_ws_commands_rejected_total{reason}
	HTTP               *metrics.Counter // gb_http_requests_total{route,code}
}

// NewMetrics registers the transport families on r.
func NewMetrics(r *metrics.Registry) *Metrics {
	return &Metrics{
		Sessions:   metrics.NewGauge(r, "gb_sessions_connected", "Open WebSocket sessions."),
		Logins:     metrics.NewCounter(r, "gb_logins_total", "Login attempts by result.", "result"),
		WSDropped:  metrics.NewCounter(r, "gb_ws_dropped_sessions_total", "Sessions closed by the server by reason.", "reason"),
		WSRejected: metrics.NewCounter(r, "gb_ws_rejected_total", "WebSocket upgrades refused by reason.", "reason"),
		WSCommandsRejected: metrics.NewCounter(r, "gb_ws_commands_rejected_total",
			"Player commands refused before reaching the engine, by reason.", "reason"),
		HTTP: metrics.NewCounter(r, "gb_http_requests_total", "HTTP requests by route and status code.", "route", "code"),
	}
}

// Server is the player-facing HTTP/WebSocket server.
type Server struct {
	cfg      *config.Config
	game     GameIface
	registry *SessionRegistry
	http     *http.Server
	logger   *slog.Logger
	metrics  *Metrics
	upgrader websocket.Upgrader
	draining atomic.Bool
	listener net.Listener
	timing   wsTiming
}

// Options configure New.
type Options struct {
	Logger   *slog.Logger
	Registry *metrics.Registry
}

// New creates a Server bound to the given config and game interface. It
// does not listen until Serve is called.
func New(cfg *config.Config, g GameIface, opts Options) *Server {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Registry == nil {
		opts.Registry = metrics.NewRegistry()
	}
	srv := &Server{
		cfg:     cfg,
		game:    g,
		logger:  opts.Logger.With("component", "player-http"),
		metrics: NewMetrics(opts.Registry),
		timing:  defaultWSTiming(),
	}
	srv.registry = newRegistry(func(n int) { srv.metrics.Sessions.Set(float64(n)) })
	srv.upgrader = srv.newUpgrader()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", srv.handleWS)
	mux.HandleFunc("/auth/login", srv.handleLogin)
	mux.HandleFunc("/status", srv.handleStatus)
	mux.HandleFunc("/livez", srv.HandleLivez)
	mux.HandleFunc("/readyz", srv.HandleReadyz)
	mux.HandleFunc("/api/enroll", srv.handleEnroll)
	mux.HandleFunc("/enroll", srv.handleEnrollPage)
	// The pre-Phase-1 password-triggered endpoints are gone (CR-04); make the
	// removal explicit instead of a silent 404.
	mux.HandleFunc("/admin/", srv.handleRemovedAdmin)

	srv.http = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           srv.instrument(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second, // does not apply to hijacked WebSocket connections
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
		ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelWarn),
	}
	return srv
}

// Handler returns the player mux (for httptest).
func (srv *Server) Handler() http.Handler { return srv.http.Handler }

// Registry returns the session registry (used by game logic to notify players).
func (srv *Server) Registry() *SessionRegistry { return srv.registry }

// Listen binds the player listener and returns its address (the port may be
// 0 in tests).
func (srv *Server) Listen() (net.Addr, error) {
	ln, err := net.Listen("tcp", srv.http.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", srv.http.Addr, err)
	}
	srv.listener = ln
	return ln.Addr(), nil
}

// Serve serves on the listener from Listen (or binds now) until Shutdown.
// It returns nil after an orderly shutdown.
func (srv *Server) Serve() error {
	if srv.listener == nil {
		if _, err := srv.Listen(); err != nil {
			return err
		}
	}
	var err error
	if srv.cfg.TLSCert != "" && srv.cfg.TLSKey != "" {
		srv.logger.Info("player listener", slog.String("op", "listen"), slog.String("addr", srv.listener.Addr().String()), slog.Bool("tls", true))
		err = srv.http.ServeTLS(srv.listener, srv.cfg.TLSCert, srv.cfg.TLSKey)
	} else {
		srv.logger.Info("player listener", slog.String("op", "listen"), slog.String("addr", srv.listener.Addr().String()), slog.Bool("tls", false))
		err = srv.http.Serve(srv.listener)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops accepting connections, closes every WebSocket session with
// a 1012 close frame, and waits for in-flight HTTP requests up to ctx.
func (srv *Server) Shutdown(ctx context.Context) error {
	srv.draining.Store(true)
	srv.registry.CloseAll()
	return srv.http.Shutdown(ctx)
}

// instrument counts requests per route (bounded: the registered patterns).
func (srv *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := r.URL.Path
		switch route {
		case "/ws", "/auth/login", "/status", "/livez", "/readyz", "/api/enroll", "/enroll":
		default:
			route = "other"
		}
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		srv.metrics.HTTP.Inc(route, fmt.Sprint(rec.code))
	})
}

// statusRecorder captures the status code and still supports hijacking for
// the WebSocket upgrade.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Hijack() (net.Conn, *bufioReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	s.code = http.StatusSwitchingProtocols
	c, rw, err := h.Hijack()
	return c, rw, err
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// --- REST handlers ---

// handleStatus handles GET /status — public operating summary. It exposes
// the engine mode, schedule and session count, never player identities or
// secrets.
func (srv *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st := srv.game.Status()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"mode":         st.Mode,
		"connected":    srv.registry.Count(),
		"next_update":  st.NextUpdate,
		"next_segment": st.NextSegment,
		"num_updates":  st.NumUpdates,
		"num_segments": st.NumSegments,
		"stars":        st.Stars,
		"races":        st.Races,
	})
}

// handleRemovedAdmin answers the old /admin/update and /admin/segment paths.
func (srv *Server) handleRemovedAdmin(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "the password-triggered admin endpoints were removed (CR-04); use gbctl against the admin listener", http.StatusGone)
}

// handleEnrollPage serves the enrollment form, or the disabled notice.
func (srv *Server) handleEnrollPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !srv.cfg.EnrollPreview {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, enrollmentDisabledHTML, engine.ErrEnrollmentDisabled.Error())
		return
	}
	_, _ = w.Write([]byte(enrollmentHTML))
}

// handleEnroll handles POST /api/enroll through the actor. With the preview
// disabled (default) it returns 403 and touches nothing.
func (srv *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !srv.cfg.EnrollPreview {
		writeJSON(w, http.StatusForbidden, enroll.Response{Success: false, Message: engine.ErrEnrollmentDisabled.Error()})
		return
	}
	var req enroll.Request
	if err := decodeJSON(r, &req, 16<<10); err != nil {
		writeJSON(w, http.StatusBadRequest, enroll.Response{Success: false, Message: "Invalid request", Errors: []string{err.Error()}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := srv.game.Enroll(ctx, &req)
	switch {
	case errors.Is(err, engine.ErrEnrollmentDisabled):
		writeJSON(w, http.StatusForbidden, resp)
	case errors.Is(err, engine.ErrNotServing), errors.Is(err, engine.ErrEngineBusy):
		writeJSON(w, http.StatusServiceUnavailable, resp)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, enroll.Response{Success: false, Message: "Enrollment failed"})
	case !resp.Success:
		writeJSON(w, http.StatusUnprocessableEntity, resp)
	default:
		writeJSON(w, http.StatusOK, resp)
	}
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writeJSON", slog.String("err", err.Error()))
	}
}

func decodeJSON(r *http.Request, v interface{}, limit int64) error {
	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	return dec.Decode(v)
}

const enrollmentDisabledHTML = `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><title>Enrollment disabled</title></head>
<body style="font-family:monospace;background:#000;color:#0f0;padding:2em"><h1>Enrollment is disabled</h1>
<p>%s</p><p>Test races are created offline with <code>racegen</code>.</p></body></html>`
