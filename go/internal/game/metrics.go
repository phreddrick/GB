package game

import (
	"github.com/phreddrick/GB/go/internal/platform/metrics"
)

// Metrics are the engine's Prometheus families. Label values are drawn from
// fixed sets (canonical command names, modes, admin operation names and
// result classes); no player or session identifiers are ever labels.
type Metrics struct {
	Commands      *metrics.Counter   // gb_commands_total{type,result}
	CommandPanics *metrics.Counter   // gb_command_panics_total
	TurnPanics    *metrics.Counter   // gb_turn_panics_total
	TurnDuration  *metrics.Histogram // gb_turn_duration_seconds{kind}
	TurnsTotal    *metrics.Counter   // gb_turns_total{kind,result}
	TurnProgress  *metrics.Counter   // gb_turn_progress
	EngineMode    *metrics.Gauge     // gb_engine_mode{mode}
	AdminOps      *metrics.Counter   // gb_admin_operations_total{op,result}
	Canceled      *metrics.Counter   // gb_requests_canceled_total{op}: canceled while queued, never run
	QueueDepth    *metrics.Gauge     // gb_actor_queue_depth
	NextUpdate    *metrics.Gauge     // gb_schedule_next_update_timestamp_seconds
	NextSegment   *metrics.Gauge     // gb_schedule_next_segment_timestamp_seconds
	Updates       *metrics.Gauge     // gb_schedule_updates (counter value restored from the store)
	Segments      *metrics.Gauge     // gb_schedule_segments
	MissedTurns   *metrics.Counter   // gb_schedule_missed_turns_total{kind}
	WorldStars    *metrics.Gauge     // gb_world_stars
	WorldRaces    *metrics.Gauge     // gb_world_races
	WorldShips    *metrics.Gauge     // gb_world_ships
	Enrollments   *metrics.Counter   // gb_enrollments_total{result}
}

// NewMetrics registers the engine families on r.
func NewMetrics(r *metrics.Registry) *Metrics {
	return &Metrics{
		Commands:      metrics.NewCounter(r, "gb_commands_total", "Player commands by canonical type and result.", "type", "result"),
		CommandPanics: metrics.NewCounter(r, "gb_command_panics_total", "Panics recovered in command handlers (each halts the engine)."),
		TurnPanics:    metrics.NewCounter(r, "gb_turn_panics_total", "Panics recovered in turn processing (each halts the engine)."),
		TurnDuration:  metrics.NewHistogram(r, "gb_turn_duration_seconds", "Turn duration by kind.", metrics.DefaultDurationBuckets, "kind"),
		TurnsTotal:    metrics.NewCounter(r, "gb_turns_total", "Turns by kind and result.", "kind", "result"),
		TurnProgress:  metrics.NewCounter(r, "gb_turn_progress", "Progress counter advanced per entity during turns (liveness watchdog)."),
		EngineMode:    metrics.NewGauge(r, "gb_engine_mode", "1 for the current engine mode, 0 otherwise.", "mode"),
		AdminOps:      metrics.NewCounter(r, "gb_admin_operations_total", "Administrative operations by name and result.", "op", "result"),
		Canceled:      metrics.NewCounter(r, "gb_requests_canceled_total", "Actor requests canceled by their caller while queued (never executed), by operation.", "op"),
		QueueDepth:    metrics.NewGauge(r, "gb_actor_queue_depth", "Player commands waiting for the actor."),
		NextUpdate:    metrics.NewGauge(r, "gb_schedule_next_update_timestamp_seconds", "Unix time of the next scheduled update."),
		NextSegment:   metrics.NewGauge(r, "gb_schedule_next_segment_timestamp_seconds", "Unix time of the next scheduled movement segment."),
		Updates:       metrics.NewGauge(r, "gb_schedule_updates", "Updates performed so far (restored across restarts)."),
		Segments:      metrics.NewGauge(r, "gb_schedule_segments", "Segments performed in the current update cycle."),
		MissedTurns:   metrics.NewCounter(r, "gb_schedule_missed_turns_total", "Scheduled turns found overdue at startup and skipped per the no-catch-up policy.", "kind"),
		WorldStars:    metrics.NewGauge(r, "gb_world_stars", "Stars loaded."),
		WorldRaces:    metrics.NewGauge(r, "gb_world_races", "Races loaded."),
		WorldShips:    metrics.NewGauge(r, "gb_world_ships", "Ships loaded."),
		Enrollments:   metrics.NewCounter(r, "gb_enrollments_total", "Enrollment requests by result.", "result"),
	}
}

// setMode updates the mode gauge so exactly one mode reads 1.
func (m *Metrics) setMode(mode Mode) {
	if m == nil {
		return
	}
	for _, x := range AllModes {
		v := 0.0
		if x == mode {
			v = 1
		}
		m.EngineMode.Set(v, x.String())
	}
}
