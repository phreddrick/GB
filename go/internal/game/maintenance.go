package game

import (
	"log/slog"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
)

// Durable maintenance mode (CR-43, ADR-0010).
//
// An operator quiesce used to live only in the mode atomic: a restart, and
// therefore every Kubernetes Pod replacement, silently returned the engine
// to SERVING and armed the turn timer. The quiesce is now recorded in the
// store (db.MaintenanceRecord) before the mode changes, read back at startup
// (state.go New) and removed by Resume in the same transaction as the
// resumed schedule. The in-memory view below is what status and the probes
// report; the store is the truth about what a restart will do.

// maintenanceState is the actor's view of the operator quiesce, kept under
// statusMu because Status and Health read it from other goroutines.
type maintenanceState struct {
	active  bool      // an operator quiesce is in effect (mode QUIESCED by Quiesce or restored at start)
	durable bool      // the store holds the maintenance record
	setAt   time.Time // the record's set_at (or the attempt time when not durable)
	reason  string
	err     string // why the record could not be written (durable=false)
}

func (m maintenanceState) snapshot() *MaintenanceStatus {
	if !m.active {
		return nil
	}
	st := &MaintenanceStatus{Durable: m.durable, Reason: m.reason, Error: m.err}
	if !m.setAt.IsZero() {
		t := m.setAt
		st.SetAt = &t
	}
	return st
}

// setMaintenance records an operator quiesce. durable says whether rec is
// in the store; writeErr (when not durable) says why it is not.
func (s *State) setMaintenance(rec *db.MaintenanceRecord, durable bool, writeErr error) {
	m := maintenanceState{active: true, durable: durable}
	if rec != nil {
		m.setAt = rec.SetAt
		m.reason = rec.Reason
	}
	if writeErr != nil {
		m.err = writeErr.Error()
	}
	s.statusMu.Lock()
	s.maint = m
	s.statusMu.Unlock()
	s.metrics.MaintenanceDurable.Set(boolGauge(durable))
}

// clearMaintenance forgets the operator quiesce (after Resume has removed
// the record durably).
func (s *State) clearMaintenance() {
	s.statusMu.Lock()
	s.maint = maintenanceState{}
	s.statusMu.Unlock()
	s.metrics.MaintenanceDurable.Set(0)
}

// maintenanceDurable reports whether the store is known to hold the record.
func (s *State) maintenanceDurable() bool {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	return s.maint.active && s.maint.durable
}

// adoptPersistedSchedule loads the schedule exactly as the store holds it
// without applying the overdue policy and without writing anything: the
// startup path of a store that carries a maintenance record. Overdue times
// are left in the past on purpose; Resume reschedules them (no catch-up)
// and persists the result. A store without a schedule record (never seen in
// practice: Quiesce requires SERVING, which requires a persisted schedule)
// gets an in-memory fresh schedule that Resume persists.
func (s *State) adoptPersistedSchedule(now time.Time) error {
	rec, err := s.store.GetSchedule()
	if err != nil {
		return err
	}
	if rec == nil {
		s.sched = s.freshSchedule(now)
		s.logger.Warn("maintenance record without a schedule record: fresh schedule held in memory until resume", slog.String("op", "schedule.restore"))
	} else {
		s.sched = schedule{
			nextUpdate:  rec.NextUpdate,
			nextSegment: rec.NextSegment,
			numUpdates:  rec.NumUpdates,
			numSegments: rec.NumSegments,
		}
		s.logger.Info("schedule loaded as persisted (quiesced; overdue turns are rescheduled at resume)", slog.String("op", "schedule.restore"),
			slog.Int("num_updates", s.sched.numUpdates), slog.Int("num_segments", s.sched.numSegments),
			slog.Time("next_update", s.sched.nextUpdate), slog.Time("next_segment", s.sched.nextSegment))
	}
	s.publishSchedule()
	return nil
}

func boolGauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
