package net

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/phreddrick/GB/go/internal/engine"
)

const (
	pongWait     = 60 * time.Second
	pingInterval = 30 * time.Second
	// commandWait bounds how long a session's command worker waits for the
	// actor to START one command (it is queued behind a turn, for example).
	// A command still queued at the deadline is canceled and never runs, and
	// the player is told so; a command that has started always completes.
	commandWait = 2 * time.Minute
	// commandQueueLen bounds the commands one session may have waiting for
	// its worker. Further commands are refused with a message, not queued.
	commandQueueLen = 32
	// welcomeWait bounds the welcome request at session start.
	welcomeWait = 10 * time.Second
)

// wsTiming holds the WebSocket timing parameters; tests shorten them.
type wsTiming struct {
	pongWait, pingInterval, commandWait time.Duration
	commandQueue                        int
}

func defaultWSTiming() wsTiming {
	return wsTiming{pongWait: pongWait, pingInterval: pingInterval, commandWait: commandWait, commandQueue: commandQueueLen}
}

// newUpgrader builds the WebSocket upgrader with the origin policy:
//   - no Origin header (non-browser clients such as gb-client): allowed;
//   - Origin equal to the request's own host (same-origin): allowed;
//   - Origin in the configured allow-list (exact scheme://host[:port]): allowed;
//   - anything else: refused with 403 before the upgrade.
func (srv *Server) newUpgrader() websocket.Upgrader {
	allowed := map[string]bool{}
	for _, o := range srv.cfg.WSAllowedOrigins {
		allowed[strings.ToLower(strings.TrimRight(o, "/"))] = true
	}
	return websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			u, err := url.Parse(origin)
			if err != nil {
				return false
			}
			if strings.EqualFold(u.Host, r.Host) {
				return true
			}
			return allowed[strings.ToLower(strings.TrimRight(origin, "/"))]
		},
	}
}

func newSessionID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "s-unknown"
	}
	return hex.EncodeToString(b[:])
}

// handleWS handles GET /ws — upgrades to WebSocket and runs the player loop.
func (srv *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if srv.draining.Load() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}
	// Authenticate before upgrading.
	tokenStr := tokenFromRequest(r)
	if tokenStr == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}
	claims, err := srv.parseToken(tokenStr)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	conn, err := srv.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote the HTTP error (403 for a refused origin).
		srv.metrics.WSRejected.Inc("upgrade")
		return
	}

	tm := srv.timing
	ident := Identity{PlayerNum: claims.PlayerNum, Governor: claims.Governor, God: claims.God}
	sess := newSession(conn, newSessionID(), ident, srv.cfg.WSOutboundQueue,
		time.Duration(srv.cfg.WSWriteTimeoutSeconds)*time.Second, tm.pingInterval)
	log := srv.logger.With(slog.String("session_id", sess.ID), slog.Int("player", claims.PlayerNum), slog.Int("governor", claims.Governor))
	if err := srv.registry.register(sess); err != nil {
		// Shutdown closed the registry after this upgrade began: close with
		// 1012 like every other session instead of serving it.
		sess.CloseForRestart()
		srv.metrics.WSRejected.Inc("shutdown")
		log.Info("session refused: server shutting down", slog.String("op", "ws.open"))
		return
	}
	log.Info("session opened", slog.String("op", "ws.open"))

	// One command worker per session (GB1-04). The read loop below only
	// reads frames, so pong control frames keep being processed (and the
	// read deadline refreshed) while a command or a turn runs for minutes.
	// The worker runs this session's commands one at a time, in arrival
	// order, from a bounded queue; it is the only goroutine of this session
	// that waits for the actor.
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	cmds := make(chan string, tm.commandQueue)
	workerDone := make(chan struct{})
	go srv.commandWorker(workerCtx, sess, claims.PlayerNum, claims.Governor, cmds, workerDone)

	defer func() {
		srv.registry.unregister(sess)
		sess.Close()
		// Cancel the worker: a command still queued on the actor is canceled
		// and never runs; one already running completes (it cannot be
		// interrupted safely), so the wait below is bounded by the actor.
		cancelWorker()
		<-workerDone
		reason := "closed"
		if sess.Dropped() {
			reason = "overflow"
			srv.metrics.WSDropped.Inc("overflow")
		}
		log.Info("session closed", slog.String("op", "ws.close"), slog.String("reason", reason))
		srv.game.PlayerDisconnect(claims.PlayerNum, claims.Governor)
	}()

	// Read-side limits: frame size, and a deadline refreshed by pongs and
	// by any inbound message (the writer goroutine pings every pingInterval).
	conn.SetReadLimit(srv.cfg.WSMaxMessageBytes)
	_ = conn.SetReadDeadline(time.Now().Add(tm.pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(tm.pongWait))
	})

	for {
		msgType, raw, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, websocket.ErrReadLimit) {
				srv.metrics.WSDropped.Inc("frame_too_big")
			}
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(tm.pongWait))
		if msgType != websocket.TextMessage {
			continue
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			continue
		}
		if !sess.IsActive() {
			return
		}
		select {
		case cmds <- text:
		default:
			srv.metrics.WSCommandsRejected.Inc("queue_full")
			_ = sess.Send(fmt.Sprintf("\n*** Too many commands waiting (limit %d); this command was not run. ***\n", tm.commandQueue))
		}
	}
}

// commandWorker sends the welcome line, then runs the session's commands in
// order until ctx ends (session closed). It never writes to the WebSocket
// itself: output goes through sess, whose writer goroutine owns the
// connection.
func (srv *Server) commandWorker(ctx context.Context, sess *Session, playerNum, governor int, cmds <-chan string, done chan<- struct{}) {
	defer close(done)
	wctx, cancel := context.WithTimeout(ctx, welcomeWait)
	_ = sess.Send(srv.game.WelcomeMessage(wctx, playerNum, governor))
	cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case text := <-cmds:
			if ctx.Err() != nil {
				// select picks randomly among ready cases; a closed session
				// never starts another command.
				return
			}
			cctx, cancel := context.WithTimeout(ctx, srv.timing.commandWait)
			err := srv.game.HandleCommand(cctx, playerNum, governor, text, sess)
			cancel()
			if err != nil && ctx.Err() == nil {
				_ = sess.Send(commandFailureLine(err))
			}
		}
	}
}

// commandFailureLine tells the player precisely what happened to a command
// that did not complete normally.
func commandFailureLine(err error) string {
	switch {
	case errors.Is(err, engine.ErrNotExecuted):
		return "\n*** The server is busy (a turn is running); your command was NOT run. Please try again. ***\n"
	case errors.Is(err, engine.ErrEngineStopped):
		return "\n*** The server is shutting down; your command was NOT run. ***\n"
	}
	return "\n*** The server could not confirm your command; it may not have run. ***\n"
}
