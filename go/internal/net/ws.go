package net

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Allow all origins for now; restrict in production.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleWS handles GET /ws — upgrades to WebSocket and runs the player loop.
func (srv *Server) handleWS(w http.ResponseWriter, r *http.Request) {
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

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}

	sess := newSession(conn)
	srv.registry.register(sess)
	srv.registry.setAuth(sess, claims.PlayerNum, claims.Governor, claims.God)

	defer func() {
		srv.registry.unregister(sess)
		sess.Close()
		srv.game.PlayerDisconnect(claims.PlayerNum, claims.Governor)
	}()

	sess.Send(srv.game.WelcomeMessage(claims.PlayerNum, claims.Governor))

	// Read loop: one goroutine per player, blocks on conn.ReadMessage().
	// There is no separate write goroutine; writes are serialized by sess.mu.
	conn.SetReadDeadline(time.Time{}) // no deadline — game is long-running
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Periodic ping to detect dead connections (replaces DISCONNECT_TIME check).
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if !sess.IsActive() {
				return
			}
			sess.mu.Lock()
			err := sess.conn.WriteMessage(websocket.PingMessage, nil)
			sess.mu.Unlock()
			if err != nil {
				sess.Close()
				return
			}
		}
	}()

	for {
		msgType, raw, err := conn.ReadMessage()
		if err != nil {
			// Connection closed or error — exit read loop.
			return
		}
		if msgType != websocket.TextMessage {
			continue
		}
		text := strings.TrimSpace(string(raw))
		if text == "" {
			continue
		}

		// Dispatch command to the game state actor.
		// The game actor will call sess.Send / registry.Notify etc. to respond.
		srv.game.HandleCommand(claims.PlayerNum, claims.Governor, text, sess)
	}
}
