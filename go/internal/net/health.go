package net

import (
	"bufio"
	"net/http"
	"time"

	"github.com/phreddrick/GB/go/internal/engine"
)

// bufioReadWriter is the type returned by http.Hijacker.
type bufioReadWriter = bufio.ReadWriter

// Probe responses (migration plan 11.5).
//
//	/livez  200 while the actor heartbeats or a running turn makes progress;
//	        503 when the actor is stuck or stopped. HALTED is alive (restarting
//	        would hide the state and would not help).
//	/readyz 200 in SERVING, TURN_IN_PROGRESS, UNINITIALIZED (admin only;
//	        game_ready=false) and QUIESCED (operator maintenance; the process
//	        is healthy and the store validated, game_ready=false, CR-43);
//	        503 in STARTING, HALTED (including CORRUPT_STORE), STOPPED and
//	        while shutdown is draining.
//
// /readyz is the orchestrator's process-readiness signal: a quiesced Pod
// stays Ready so a StatefulSet rolling update can replace it, and the
// Service keeps routing to it (players connecting then get the paused
// refusal from the actor). Gameplay readiness is the body's game_ready and
// the admin status, never the HTTP code. Both probes are served by the HTTP
// goroutine from atomics and never wait on the actor queue. Neither includes
// secrets or player data.

type probeBody struct {
	OK          bool   `json:"ok"`
	Mode        string `json:"mode"`
	GameReady   bool   `json:"game_ready"`
	Draining    bool   `json:"draining,omitempty"`
	HaltReason  string `json:"halt_reason,omitempty"`
	TurnRunning bool   `json:"turn_running,omitempty"`
	// Maintenance is true while an operator quiesce is in effect;
	// MaintenanceDurable says whether a restart would start QUIESCED again.
	Maintenance        bool   `json:"maintenance,omitempty"`
	MaintenanceDurable bool   `json:"maintenance_durable,omitempty"`
	LastHeartbeat      string `json:"last_heartbeat,omitempty"`
	LastProgress       string `json:"last_progress,omitempty"`
}

func probeFromHealth(h engine.Health, draining bool) probeBody {
	b := probeBody{
		Mode:        h.Mode.String(),
		GameReady:   h.GameReady && !draining,
		Draining:    draining,
		HaltReason:  h.HaltReason,
		TurnRunning: h.TurnRunning,

		Maintenance:        h.Maintenance,
		MaintenanceDurable: h.MaintenanceDurable,
	}
	if !h.LastHeartbeat.IsZero() {
		b.LastHeartbeat = h.LastHeartbeat.UTC().Format(time.RFC3339)
	}
	if !h.LastProgress.IsZero() {
		b.LastProgress = h.LastProgress.UTC().Format(time.RFC3339)
	}
	return b
}

// HandleLivez serves the liveness probe.
func (srv *Server) HandleLivez(w http.ResponseWriter, r *http.Request) {
	h := srv.game.Health(time.Now())
	b := probeFromHealth(h, srv.draining.Load())
	b.OK = h.Alive
	code := http.StatusOK
	if !h.Alive {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, b)
}

// HandleReadyz serves the readiness probe.
func (srv *Server) HandleReadyz(w http.ResponseWriter, r *http.Request) {
	h := srv.game.Health(time.Now())
	draining := srv.draining.Load()
	b := probeFromHealth(h, draining)
	b.OK = h.Ready && !draining
	code := http.StatusOK
	if !b.OK {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, b)
}
