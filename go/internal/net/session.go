// Package net implements the HTTP/WebSocket server and per-player session state.
package net

import (
	"sync"

	"github.com/gorilla/websocket"
)

// Session holds per-player connection state.
// This replaces struct descriptor_data and the descriptor_list in GB_server.c.
//
// One goroutine per Session handles I/O; game state mutations are sent to the
// central game state actor via the server's command channel.
type Session struct {
	mu sync.Mutex

	// Identity (set after authentication)
	PlayerNum int
	Governor  int
	God       bool

	// WebSocket connection
	conn *websocket.Conn

	// Rate limiting (mirrors the quota field in descriptor_data)
	quota int

	// Whether this session is still active
	active bool
}

// newSession creates a Session for an accepted WebSocket connection.
func newSession(conn *websocket.Conn) *Session {
	return &Session{
		conn:   conn,
		active: true,
	}
}

// Send writes a text message to the player.
// Safe to call from any goroutine.
func (s *Session) Send(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return nil
	}
	return s.conn.WriteMessage(websocket.TextMessage, []byte(text))
}

// SendJSON writes a structured JSON message to the player.
// Safe to call from any goroutine.
func (s *Session) SendJSON(v interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return nil
	}
	return s.conn.WriteJSON(v)
}

// Close marks the session as inactive and closes the WebSocket.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		s.active = false
		s.conn.Close()
	}
}

// IsActive returns whether the session is still connected.
func (s *Session) IsActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// SessionRegistry tracks all live sessions.
// Replaces the descriptor_list linked list in GB_server.c.
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[int][]*Session // playerNum → []Session (one per governor)
	all      []*Session
}

func newRegistry() *SessionRegistry {
	return &SessionRegistry{
		sessions: make(map[int][]*Session),
	}
}

func (r *SessionRegistry) register(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.all = append(r.all, s)
}

func (r *SessionRegistry) unregister(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, sess := range r.all {
		if sess == s {
			r.all = append(r.all[:i], r.all[i+1:]...)
			break
		}
	}
	if s.PlayerNum > 0 {
		govList := r.sessions[s.PlayerNum]
		for i, sess := range govList {
			if sess == s {
				r.sessions[s.PlayerNum] = append(govList[:i], govList[i+1:]...)
				break
			}
		}
	}
}

func (r *SessionRegistry) setAuth(s *Session, playerNum, governor int, god bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s.PlayerNum = playerNum
	s.Governor = governor
	s.God = god
	r.sessions[playerNum] = append(r.sessions[playerNum], s)
}

// NotifyRace sends a text message to all governors of a given race.
// Corresponds to notify_race() in GB_server.c.
func (r *SessionRegistry) NotifyRace(playerNum int, text string) {
	r.mu.RLock()
	sessions := append([]*Session(nil), r.sessions[playerNum]...)
	r.mu.RUnlock()
	for _, s := range sessions {
		s.Send(text)
	}
}

// Notify sends a text message to a specific governor of a race.
// Returns true if a connected session was found and the message was delivered.
// Corresponds to notify() in GB_server.c.
func (r *SessionRegistry) Notify(playerNum, governor int, text string) bool {
	r.mu.RLock()
	sessions := append([]*Session(nil), r.sessions[playerNum]...)
	r.mu.RUnlock()
	for _, s := range sessions {
		if s.Governor == governor {
			s.Send(text)
			return true
		}
	}
	return false
}

// Broadcast sends a text message to all connected players except the sender.
// Corresponds to d_broadcast() in GB_server.c.
func (r *SessionRegistry) Broadcast(senderPlayerNum int, text string) {
	r.mu.RLock()
	all := append([]*Session(nil), r.all...)
	r.mu.RUnlock()
	for _, s := range all {
		if s.PlayerNum != senderPlayerNum && s.PlayerNum > 0 {
			s.Send(text)
		}
	}
}

// Shout sends a text message to all connected players.
// Corresponds to d_shout() in GB_server.c.
func (r *SessionRegistry) Shout(text string) {
	r.mu.RLock()
	all := append([]*Session(nil), r.all...)
	r.mu.RUnlock()
	for _, s := range all {
		s.Send(text)
	}
}

// ConnectedPlayers returns the player numbers and names for the WHO display.
func (r *SessionRegistry) ConnectedPlayers() []SessionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []SessionInfo
	for _, s := range r.all {
		if s.PlayerNum > 0 {
			out = append(out, SessionInfo{
				PlayerNum: s.PlayerNum,
				Governor:  s.Governor,
				God:       s.God,
			})
		}
	}
	return out
}

// SessionInfo is a snapshot of one connected session.
type SessionInfo struct {
	PlayerNum int  `json:"player_num"`
	Governor  int  `json:"governor"`
	God       bool `json:"god"`
}
