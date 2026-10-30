// Package admin implements the Phase 1 transitional administrative
// interface: a small HTTP listener, bound to loopback by default, that
// authenticates every request with a static bearer token (GB_ADMIN_TOKEN,
// constant-time compare) and forwards typed operations to the engine. The
// player-facing listener never serves these routes. The Phase 4b identity
// model replaces the static token.
//
// Routes (all under /admin/v1/, JSON):
//
//	GET  /status         engine status (no secrets, no player data)
//	POST /update         run a full update now
//	POST /segment        run a movement segment now
//	POST /quiesce        pause turns and mutating commands
//	POST /resume         resume a quiesced engine
//	POST /universe/init  {"seed":N,"stars":N,"min_planets":N,"max_planets":N}
//
// Unauthenticated on this listener: GET /livez, /readyz (same handlers as
// the player listener) and GET /metrics.
//
// Kubernetes (Phase 2): the listener is reached through
// `kubectl port-forward` to the pod, so it may stay on loopback inside the
// container network namespace or bind 0.0.0.0 on a port that is not on the
// Service or Ingress; either way the token is required. Nothing here
// configures Kubernetes.
package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/engine"
	"github.com/phreddrick/GB/go/internal/platform/metrics"
	"github.com/phreddrick/GB/go/internal/universe"
)

// Engine is what the admin listener needs from the game actor. *game.State
// satisfies it.
type Engine interface {
	Status() engine.Status
	TriggerUpdate(ctx context.Context) error
	TriggerSegment(ctx context.Context) error
	Quiesce(ctx context.Context) error
	Resume(ctx context.Context) error
	InitUniverse(ctx context.Context, params universe.Params, seed int64) (*db.UniverseRecord, error)
}

// Options configure the listener.
type Options struct {
	Addr     string // host:port
	Token    string // static bearer token (validated by config)
	Engine   Engine
	Logger   *slog.Logger
	Registry *metrics.Registry // exposed at /metrics
	Livez    http.HandlerFunc  // shared with the player listener
	Readyz   http.HandlerFunc
	// OpTimeout bounds update/segment/init requests on the server side.
	OpTimeout time.Duration
}

// Server is the admin HTTP listener.
type Server struct {
	opts      Options
	tokenHash [32]byte
	http      *http.Server
	listener  net.Listener
	logger    *slog.Logger
}

// New builds the listener; it does not bind until Listen/Serve.
func New(opts Options) (*Server, error) {
	if opts.Engine == nil {
		return nil, errors.New("admin: engine is required")
	}
	if strings.TrimSpace(opts.Token) == "" {
		return nil, errors.New("admin: token is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.OpTimeout == 0 {
		opts.OpTimeout = 10 * time.Minute
	}
	s := &Server{opts: opts, tokenHash: sha256.Sum256([]byte(opts.Token)), logger: opts.Logger.With("component", "admin-http")}

	mux := http.NewServeMux()
	if opts.Livez != nil {
		mux.HandleFunc("/livez", opts.Livez)
	}
	if opts.Readyz != nil {
		mux.HandleFunc("/readyz", opts.Readyz)
	}
	if opts.Registry != nil {
		mux.HandleFunc("/metrics", s.handleMetrics)
	}
	mux.Handle("/admin/v1/status", s.auth(http.MethodGet, s.handleStatus))
	mux.Handle("/admin/v1/update", s.auth(http.MethodPost, s.op("update", func(ctx context.Context, _ *http.Request) (any, error) {
		return nil, opts.Engine.TriggerUpdate(ctx)
	})))
	mux.Handle("/admin/v1/segment", s.auth(http.MethodPost, s.op("segment", func(ctx context.Context, _ *http.Request) (any, error) {
		return nil, opts.Engine.TriggerSegment(ctx)
	})))
	mux.Handle("/admin/v1/quiesce", s.auth(http.MethodPost, s.op("quiesce", func(ctx context.Context, _ *http.Request) (any, error) {
		return nil, opts.Engine.Quiesce(ctx)
	})))
	mux.Handle("/admin/v1/resume", s.auth(http.MethodPost, s.op("resume", func(ctx context.Context, _ *http.Request) (any, error) {
		return nil, opts.Engine.Resume(ctx)
	})))
	mux.Handle("/admin/v1/universe/init", s.auth(http.MethodPost, s.op("universe.init", s.universeInit)))

	s.http = &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 14,
		ErrorLog:          slog.NewLogLogger(opts.Logger.Handler(), slog.LevelWarn),
	}
	return s, nil
}

// Handler returns the mux (for httptest).
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Listen binds the listener and returns its address.
func (s *Server) Listen() (net.Addr, error) {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return nil, fmt.Errorf("admin listen %s: %w", s.opts.Addr, err)
	}
	s.listener = ln
	return ln.Addr(), nil
}

// Serve serves until Shutdown; nil after an orderly shutdown.
func (s *Server) Serve() error {
	if s.listener == nil {
		if _, err := s.Listen(); err != nil {
			return err
		}
	}
	s.logger.Info("admin listener", slog.String("op", "listen"), slog.String("addr", s.listener.Addr().String()))
	err := s.http.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops the listener.
func (s *Server) Shutdown(ctx context.Context) error { return s.http.Shutdown(ctx) }

// TokenMatches compares a presented token with the configured one in
// constant time (both are hashed first so the comparison length never
// depends on the secret).
func (s *Server) TokenMatches(presented string) bool {
	h := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(h[:], s.tokenHash[:]) == 1
}

// auth enforces the method and the bearer token.
func (s *Server) auth(method string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, prefix) || !s.TokenMatches(strings.TrimPrefix(h, prefix)) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gb-admin"`)
			writeErr(w, http.StatusUnauthorized, "missing or invalid admin token")
			s.logger.Warn("admin auth failed", slog.String("op", "admin.auth"), slog.String("remote", r.RemoteAddr), slog.String("path", r.URL.Path))
			return
		}
		next(w, r)
	})
}

// Response is the JSON envelope of every admin reply.
type Response struct {
	OK     bool           `json:"ok"`
	Op     string         `json:"op,omitempty"`
	Error  string         `json:"error,omitempty"`
	Status *engine.Status `json:"status,omitempty"`
	Result any            `json:"result,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, Response{OK: false, Error: msg})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	st := s.opts.Engine.Status()
	writeJSON(w, http.StatusOK, Response{OK: true, Op: "status", Status: &st})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = s.opts.Registry.WriteTo(w)
}

// op wraps an engine operation: bounded context, error-to-status mapping,
// status included in the reply.
func (s *Server) op(name string, fn func(ctx context.Context, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.opts.OpTimeout)
		defer cancel()
		start := time.Now()
		result, err := fn(ctx, r)
		st := s.opts.Engine.Status()
		code := StatusCode(err)
		resp := Response{OK: err == nil, Op: name, Status: &st, Result: result}
		if err != nil {
			resp.Error = err.Error()
		}
		s.logger.Info("admin request", slog.String("op", "admin."+name), slog.Int("code", code),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()), slog.String("remote", r.RemoteAddr))
		writeJSON(w, code, resp)
	}
}

// StatusCode maps engine errors to HTTP codes:
//
//	200 success; 400 bad request; 409 refused by state (halted, not serving,
//	already initialised); 503 busy or stopped; 500 other.
func StatusCode(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, errBadRequest):
		return http.StatusBadRequest
	case errors.Is(err, engine.ErrHalted), errors.Is(err, engine.ErrNotServing), errors.Is(err, universe.ErrAlreadyInitialized):
		return http.StatusConflict
	case errors.Is(err, engine.ErrEngineBusy), errors.Is(err, engine.ErrEngineStopped), errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

var errBadRequest = errors.New("bad request")

// InitRequest is the body of POST /admin/v1/universe/init.
type InitRequest struct {
	Seed       int64 `json:"seed"`
	Stars      int   `json:"stars"`
	MinPlanets int   `json:"min_planets"`
	MaxPlanets int   `json:"max_planets"`
}

func (s *Server) universeInit(ctx context.Context, r *http.Request) (any, error) {
	var req InitRequest
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("%w: %v", errBadRequest, err)
	}
	if req.Seed == 0 {
		return nil, fmt.Errorf("%w: seed is required and must be non-zero (record it: the same seed regenerates the same universe)", errBadRequest)
	}
	params := universe.Params{Stars: req.Stars, MinPlanets: req.MinPlanets, MaxPlanets: req.MaxPlanets}
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", errBadRequest, err)
	}
	return s.opts.Engine.InitUniverse(ctx, params, req.Seed)
}
