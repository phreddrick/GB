package game

import (
	"bytes"
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

// storeSnapshot is what the two maintenance writes compare against: the raw
// bytes of the maintenance and schedule records as the store held them
// before the transaction, and whether the record present then had been
// confirmed durable (written by a successful transaction of this process,
// or read at startup).
type storeSnapshot struct {
	maintenance, schedule []byte
	priorConfirmed        bool
	readErr               error
}

func (s *State) snapshotMetaForWrite() storeSnapshot {
	snap := storeSnapshot{priorConfirmed: s.maintenanceDurable()}
	var err error
	if snap.maintenance, err = s.store.GetMeta(db.KeyMaintenance); err != nil {
		snap.readErr = err
		return snap
	}
	if snap.schedule, err = s.store.GetMeta(db.KeySchedule); err != nil {
		snap.readErr = err
	}
	return snap
}

// classifyMaintenanceWriteFailure reads the store back after PutMaintenance
// reported err for the record whose canonical bytes are intended, and
// returns a sentence saying what the next start will do, as far as the
// store can show it. A record that is visible after a failed transaction
// has NOT been confirmed durable (bbolt may have written the meta page
// before a failing fsync), so the sentence promises QUIESCED only when a
// record that an earlier successful transaction confirmed (prior) is still
// there; otherwise it says "likely" and the caller's durability claim stays
// false until a repeat quiesce succeeds.
func (s *State) classifyMaintenanceWriteFailure(intended []byte, prior storeSnapshot) string {
	raw, rerr := s.store.GetMeta(db.KeyMaintenance)
	switch {
	case rerr != nil:
		return "write failed and the store could not be read back (" + rerr.Error() + "): whether the next start is QUIESCED or SERVING is unknown"
	case raw == nil:
		return "write failed and the store holds no maintenance record: the next start would be SERVING"
	case bytes.Equal(raw, intended):
		return "write reported an error but this record is visible in the store: a restart would likely start QUIESCED, but durability on disk is not confirmed; re-run quiesce to confirm"
	case prior.priorConfirmed && bytes.Equal(raw, prior.maintenance):
		return "write failed; the store still holds the previously confirmed maintenance record: the next start would be QUIESCED from it"
	default:
		return "write failed; the store holds a maintenance record that was never confirmed durable by this process: a restart would likely start QUIESCED, but it is not guaranteed; re-run quiesce to confirm"
	}
}

// classifyResumeFailure reads the store back after
// ClearMaintenanceWithSchedule reported an error for the transaction that
// intended to remove the record and store the schedule bytes intendedSched,
// compares with the snapshot taken before the transaction, updates the
// in-memory durability claim to the most pessimistic reading the store
// supports, and returns the sentence that goes into the halt reason and
// the error. Visibility after a failed transaction never counts as
// confirmed durability: an applied-but-reported-failed resume may still be
// lost by a crash, in which case the previous record reappears.
func (s *State) classifyResumeFailure(intendedSched []byte, before storeSnapshot) string {
	withdraw := func(reason string) {
		s.statusMu.Lock()
		if s.maint.active {
			s.maint.durable = false
			s.maint.err = reason
		}
		s.statusMu.Unlock()
		s.metrics.MaintenanceDurable.Set(0)
	}
	recRaw, merr := s.store.GetMeta(db.KeyMaintenance)
	schedRaw, serr := s.store.GetMeta(db.KeySchedule)
	switch {
	case before.readErr != nil || merr != nil || serr != nil:
		msg := "resume failed and the store could not be read back: whether the next start is QUIESCED or SERVING is unknown"
		withdraw(msg)
		return msg
	case recRaw == nil && bytes.Equal(schedRaw, intendedSched):
		msg := "resume transaction reported an error but the store shows it applied (maintenance record removed, resumed schedule written); durability on disk is not confirmed: the next start is SERVING if the change persisted, otherwise QUIESCED from the previous record"
		if before.maintenance == nil {
			msg = "resume transaction reported an error; the store holds no maintenance record (the quiesce was in memory only) and the resumed schedule is visible: the next start would be SERVING"
		}
		withdraw(msg)
		return msg
	case bytes.Equal(recRaw, before.maintenance) && bytes.Equal(schedRaw, before.schedule):
		switch {
		case recRaw == nil:
			msg := "the store is unchanged and holds no maintenance record (the quiesce was in memory only): the next start would be SERVING"
			withdraw(msg)
			return msg
		case before.priorConfirmed:
			return "the store is unchanged and still holds the confirmed maintenance record and the previous schedule: the next start will be QUIESCED"
		default:
			msg := "the store is unchanged, but its maintenance record was never confirmed durable by this process: the next start is likely QUIESCED, not guaranteed"
			withdraw(msg)
			return msg
		}
	default:
		msg := "the store is in an unexpected state after the failed resume (maintenance record present=" + boolStr(recRaw != nil) + ", resumed schedule visible=" + boolStr(bytes.Equal(schedRaw, intendedSched)) + "): the next start cannot be predicted from here"
		withdraw(msg)
		return msg
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
