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

// classifyMaintenanceWriteFailure reads the store back after PutMaintenance
// reported err and returns a sentence saying what the next start will do.
// It also leaves the in-memory durability claim false in every case: a
// record that is visible after a failed transaction has not been confirmed
// durable on disk (bbolt may have written the meta page before a failing
// fsync), so a repeat quiesce is required to confirm it.
func (s *State) classifyMaintenanceWriteFailure(rec *db.MaintenanceRecord, err error) string {
	got, rerr := s.store.GetMaintenance()
	switch {
	case rerr != nil:
		return "write failed and the store could not be read back (" + rerr.Error() + "): whether the next start is QUIESCED or SERVING is unknown"
	case got == nil:
		return "write failed and the store holds no maintenance record: the next start would be SERVING"
	case got.SetAt.Equal(rec.SetAt):
		return "write reported an error but the record is visible in the store: a restart would likely start QUIESCED, but durability on disk is not confirmed"
	default:
		return "write failed; the store holds an older maintenance record (set " + got.SetAt.UTC().Format(time.RFC3339) + "): the next start would be QUIESCED from that record"
	}
}

// classifyResumeFailure reads the store back after
// ClearMaintenanceWithSchedule reported an error for the record want and
// updates the maintenance view to what the store shows: if the transaction
// is visible (schedule saved_at equals the resume's), the record is gone and
// the next start is SERVING, so the durability claim is withdrawn; if the
// store still holds the previous pair, the previous claim stands; if it
// cannot be read, the outcome is reported as unknown.
func (s *State) classifyResumeFailure(want *db.ScheduleRecord) string {
	m, merr := s.store.GetMaintenance()
	sc, serr := s.store.GetSchedule()
	switch {
	case merr != nil || serr != nil:
		s.statusMu.Lock()
		if s.maint.active {
			s.maint.durable = false
			s.maint.err = "resume failed and the store could not be read back: whether the next start is QUIESCED or SERVING is unknown"
		}
		s.statusMu.Unlock()
		s.metrics.MaintenanceDurable.Set(0)
		return "store read-back failed: next start unknown"
	case sc != nil && sc.SavedAt.Equal(want.SavedAt) && m == nil:
		s.statusMu.Lock()
		s.maint = maintenanceState{active: true, durable: false, setAt: s.maint.setAt, reason: s.maint.reason,
			err: "resume transaction reported an error but the store shows it applied (maintenance record removed, schedule saved " + sc.SavedAt.UTC().Format(time.RFC3339) + "): the next start will be SERVING"}
		s.statusMu.Unlock()
		s.metrics.MaintenanceDurable.Set(0)
		return "the store shows the resume applied (record removed, schedule written): the next start will be SERVING"
	case m != nil && (sc == nil || !sc.SavedAt.Equal(want.SavedAt)):
		return "the store still holds the maintenance record and the previous schedule: the next start will be QUIESCED"
	default:
		s.statusMu.Lock()
		if s.maint.active {
			s.maint.durable = false
			s.maint.err = "resume failed and the store is in an unexpected state (record present=" + boolStr(m != nil) + ", schedule applied=" + boolStr(sc != nil && sc.SavedAt.Equal(want.SavedAt)) + "): the next start cannot be predicted from here"
		}
		s.statusMu.Unlock()
		s.metrics.MaintenanceDurable.Set(0)
		return "unexpected store state after the failed resume: next start unknown"
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func boolGauge(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
