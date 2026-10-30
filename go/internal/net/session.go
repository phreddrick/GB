// Package net implements the HTTP/WebSocket server and per-player session state.
package net

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Session holds per-player connection state.
// This replaces struct descriptor_data and the descriptor_list in GB_server.c.
//
// Two goroutines serve a session: the read loop (handleWS) and a writer
// goroutine that owns every write to the WebSocket. Send only enqueues, so
// the game actor never blocks on a client's TCP window (CR-23): a client
// whose queue fills is disconnected, like the C server's MAX_OUTPUT drop.
type Session struct {
	// Identity. Set once by newSession from the verified token, before the
	// session is published in the registry, and never written again; the
	// registry's mutex orders those writes before every reader (GB1-02).
	PlayerNum int
	Governor  int
	God       bool
	ID        string // short random id for logs (no token material)

	conn         *websocket.Conn
	outbound     chan outMsg
	done         chan struct{} // closed when the session is closed
	closeOnce    sync.Once
	closeReason  atomic.Pointer[string]
	writeTimeout time.Duration
	pingInterval time.Duration
	dropped      atomic.Bool // closed because the outbound queue overflowed
}

type outMsg struct {
	kind int // websocket.TextMessage or CloseMessage
	data []byte
}

// ErrSessionClosed is returned by Send after the session closed.
var ErrSessionClosed = errors.New("session closed")

// ErrSessionOverflow is returned when the outbound queue is full; the
// session is closed as a consequence.
var ErrSessionOverflow = errors.New("session outbound queue overflow")

// Identity is the authenticated identity a session is created with.
type Identity struct {
	PlayerNum int
	Governor  int
	God       bool
}

// newSession creates a Session for an accepted WebSocket connection, with
// its identity already in place, and starts its writer goroutine.
func newSession(conn *websocket.Conn, id string, ident Identity, queue int, writeTimeout, pingInterval time.Duration) *Session {
	s := &Session{
		PlayerNum:    ident.PlayerNum,
		Governor:     ident.Governor,
		God:          ident.God,
		conn:         conn,
		ID:           id,
		outbound:     make(chan outMsg, queue),
		done:         make(chan struct{}),
		writeTimeout: writeTimeout,
		pingInterval: pingInterval,
	}
	go s.writeLoop()
	return s
}

// writeLoop is the only goroutine that writes to conn.
func (s *Session) writeLoop() {
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	defer func() { _ = s.conn.Close() }()
	for {
		select {
		case <-s.done:
			// Best-effort close frame with the recorded reason.
			reason := websocket.FormatCloseMessage(websocket.CloseGoingAway, "")
			if r := s.closeReason.Load(); r != nil {
				reason = []byte(*r)
			}
			_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
			_ = s.conn.WriteMessage(websocket.CloseMessage, reason)
			return
		case m := <-s.outbound:
			_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
			if err := s.conn.WriteMessage(m.kind, m.data); err != nil {
				s.closeWith(websocket.CloseAbnormalClosure, "write failed")
				return
			}
		case <-ticker.C:
			_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
			if err := s.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				s.closeWith(websocket.CloseAbnormalClosure, "ping failed")
				return
			}
		}
	}
}

// Send queues a text message for the player. Safe to call from any
// goroutine; never blocks. A full queue closes the session and returns
// ErrSessionOverflow.
func (s *Session) Send(text string) error {
	return s.enqueue(websocket.TextMessage, []byte(text))
}

// SendJSON queues a structured JSON message for the player.
func (s *Session) SendJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.enqueue(websocket.TextMessage, data)
}

func (s *Session) enqueue(kind int, data []byte) error {
	select {
	case <-s.done:
		return ErrSessionClosed
	default:
	}
	select {
	case s.outbound <- outMsg{kind: kind, data: data}:
		return nil
	case <-s.done:
		return ErrSessionClosed
	default:
		s.dropped.Store(true)
		s.closeWith(websocket.ClosePolicyViolation, "output queue overflow (slow client)")
		return ErrSessionOverflow
	}
}

// Close closes the session with a "going away" frame. Idempotent.
func (s *Session) Close() { s.closeWith(websocket.CloseGoingAway, "") }

// CloseForRestart closes the session with 1012 (service restart), used during
// server shutdown.
func (s *Session) CloseForRestart() {
	s.closeWith(websocket.CloseServiceRestart, "server shutting down")
}

func (s *Session) closeWith(code int, text string) {
	s.closeOnce.Do(func() {
		msg := string(websocket.FormatCloseMessage(code, text))
		s.closeReason.Store(&msg)
		close(s.done)
	})
}

// IsActive returns whether the session is still open.
func (s *Session) IsActive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// Done is closed when the session closes.
func (s *Session) Done() <-chan struct{} { return s.done }

// Dropped reports whether the session was closed for output overflow.
func (s *Session) Dropped() bool { return s.dropped.Load() }

// SessionRegistry tracks all live sessions.
// Replaces the descriptor_list linked list in GB_server.c.
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[int][]*Session // playerNum → []Session (one per governor)
	all      []*Session
	closed   bool        // set by CloseAll; later registrations are refused
	onChange func(n int) // gauge hook
}

// ErrRegistryClosed is returned by register once shutdown has closed the
// registry.
var ErrRegistryClosed = errors.New("session registry closed: server shutting down")

func newRegistry(onChange func(n int)) *SessionRegistry {
	return &SessionRegistry{
		sessions: make(map[int][]*Session),
		onChange: onChange,
	}
}

// register publishes a fully initialised session (identity included) in one
// step: there is no window in which a broadcast can see it without its
// identity. It refuses once CloseAll has run, so a session that finishes
// its upgrade during shutdown is never left open and unregistered.
func (r *SessionRegistry) register(s *Session) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRegistryClosed
	}
	r.all = append(r.all, s)
	if s.PlayerNum > 0 {
		r.sessions[s.PlayerNum] = append(r.sessions[s.PlayerNum], s)
	}
	n := len(r.all)
	r.mu.Unlock()
	if r.onChange != nil {
		r.onChange(n)
	}
	return nil
}

func (r *SessionRegistry) unregister(s *Session) {
	r.mu.Lock()
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
	n := len(r.all)
	r.mu.Unlock()
	if r.onChange != nil {
		r.onChange(n)
	}
}

// Count returns the number of open sessions.
func (r *SessionRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.all)
}

// CloseAll closes every session for a server restart (close code 1012).
func (r *SessionRegistry) CloseAll() {
	r.mu.Lock()
	r.closed = true
	all := append([]*Session(nil), r.all...)
	r.mu.Unlock()
	for _, s := range all {
		s.CloseForRestart()
	}
}

// NotifyRace sends a text message to all governors of a given race.
// Corresponds to notify_race() in GB_server.c.
func (r *SessionRegistry) NotifyRace(playerNum int, text string) {
	r.mu.RLock()
	sessions := append([]*Session(nil), r.sessions[playerNum]...)
	r.mu.RUnlock()
	for _, s := range sessions {
		_ = s.Send(text)
	}
}

// Notify sends a text message to a specific governor of a race.
// Returns true if a connected session was found and the message was queued.
// Corresponds to notify() in GB_server.c.
func (r *SessionRegistry) Notify(playerNum, governor int, text string) bool {
	r.mu.RLock()
	sessions := append([]*Session(nil), r.sessions[playerNum]...)
	r.mu.RUnlock()
	for _, s := range sessions {
		if s.Governor == governor {
			_ = s.Send(text)
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
			_ = s.Send(text)
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
		_ = s.Send(text)
	}
}

// ConnectedPlayers returns the player numbers for the WHO display.
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
