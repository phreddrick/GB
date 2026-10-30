package game

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
)

// schedule is the actor-owned turn schedule. A copy is kept in the status
// snapshot for HTTP handlers.
type schedule struct {
	nextUpdate  time.Time
	nextSegment time.Time
	numUpdates  int
	numSegments int
}

func (s *State) updateInterval() time.Duration {
	return time.Duration(s.cfg.DefaultUpdateMinutes) * time.Minute
}

func (s *State) segmentInterval() time.Duration {
	segMins := s.cfg.DefaultUpdateMinutes / s.cfg.MovesPerUpdate
	if segMins < 1 {
		segMins = 1
	}
	return time.Duration(segMins) * time.Minute
}

// freshSchedule returns the schedule for a universe that has never run.
func (s *State) freshSchedule(now time.Time) schedule {
	return schedule{
		nextUpdate:  now.Add(s.updateInterval()),
		nextSegment: now.Add(s.segmentInterval()),
	}
}

// restoreSchedule loads the persisted schedule (CR-21) or creates a fresh one,
// applies the Phase 1 overdue policy, and persists the result.
//
// Overdue policy (conservative, documented; the final policy is a Phase 3
// decision): turns that fell due while the server was down are NOT run
// automatically. An overdue next-update time is moved to now plus one update
// interval, an overdue next-segment time to now plus one segment interval,
// the counters are kept, and the number of missed turns is logged and
// counted. An operator who wants the missed update runs `gbctl update`.
func (s *State) restoreSchedule(now time.Time) error {
	rec, err := s.store.GetSchedule()
	if err != nil {
		return fmt.Errorf("read schedule: %w", err)
	}
	if rec == nil {
		s.sched = s.freshSchedule(now)
		s.logger.Info("schedule initialised", slog.String("op", "schedule.init"),
			slog.Time("next_update", s.sched.nextUpdate), slog.Time("next_segment", s.sched.nextSegment))
		return s.persistSchedule(now)
	}
	s.sched = schedule{
		nextUpdate:  rec.NextUpdate,
		nextSegment: rec.NextSegment,
		numUpdates:  rec.NumUpdates,
		numSegments: rec.NumSegments,
	}
	missedUpdates := 0
	if !now.Before(s.sched.nextUpdate) {
		missedUpdates = int(now.Sub(s.sched.nextUpdate)/s.updateInterval()) + 1
		s.sched.nextUpdate = now.Add(s.updateInterval())
		s.sched.nextSegment = now.Add(s.segmentInterval())
		s.sched.numSegments = 0
	} else if !now.Before(s.sched.nextSegment) {
		s.sched.nextSegment = now.Add(s.segmentInterval())
		if s.sched.nextSegment.After(s.sched.nextUpdate) {
			s.sched.nextSegment = s.sched.nextUpdate
		}
	}
	if missedUpdates > 0 {
		s.logger.Warn("schedule restored with overdue update; not running missed turns (Phase 1 no-catch-up policy; use gbctl update)",
			slog.String("op", "schedule.restore"), slog.Int("missed_updates", missedUpdates),
			slog.Time("recorded_next_update", rec.NextUpdate), slog.Time("next_update", s.sched.nextUpdate))
		s.metrics.MissedTurns.Add(float64(missedUpdates), "update")
	} else {
		s.logger.Info("schedule restored", slog.String("op", "schedule.restore"),
			slog.Int("num_updates", s.sched.numUpdates), slog.Int("num_segments", s.sched.numSegments),
			slog.Time("next_update", s.sched.nextUpdate), slog.Time("next_segment", s.sched.nextSegment))
	}
	if rec.UpdateMinutes != s.cfg.DefaultUpdateMinutes || rec.MovesPerUpdate != s.cfg.MovesPerUpdate {
		s.logger.Warn("update interval configuration changed since the schedule was saved; the recorded next times are kept",
			slog.Int("saved_update_minutes", rec.UpdateMinutes), slog.Int("update_minutes", s.cfg.DefaultUpdateMinutes),
			slog.Int("saved_moves_per_update", rec.MovesPerUpdate), slog.Int("moves_per_update", s.cfg.MovesPerUpdate))
	}
	return s.persistSchedule(now)
}

// persistSchedule writes the actor's schedule and, only after the write
// succeeded, refreshes the status snapshot and gauges, so status never
// advertises a schedule the store does not hold (GB1-03). Called after every
// turn, after restore and during orderly shutdown.
func (s *State) persistSchedule(now time.Time) error {
	if err := s.writeSchedule(s.sched, now); err != nil {
		return err
	}
	s.publishSchedule()
	return nil
}

// writeSchedule stores sc without touching the actor's schedule or the
// status snapshot. Callers that must not change state on failure (Resume)
// write a candidate schedule first and adopt it only on success.
func (s *State) writeSchedule(sc schedule, now time.Time) error {
	rec := &db.ScheduleRecord{
		NextUpdate:     sc.nextUpdate,
		NextSegment:    sc.nextSegment,
		NumUpdates:     sc.numUpdates,
		NumSegments:    sc.numSegments,
		UpdateMinutes:  s.cfg.DefaultUpdateMinutes,
		MovesPerUpdate: s.cfg.MovesPerUpdate,
		SavedAt:        now,
	}
	if err := s.store.PutSchedule(rec); err != nil {
		return fmt.Errorf("persist schedule: %w", err)
	}
	return nil
}

// publishSchedule copies the actor-owned schedule into the status snapshot
// and the gauges.
func (s *State) publishSchedule() {
	s.statusMu.Lock()
	s.status.sched = s.sched
	s.statusMu.Unlock()
	s.metrics.NextUpdate.Set(float64(s.sched.nextUpdate.Unix()))
	s.metrics.NextSegment.Set(float64(s.sched.nextSegment.Unix()))
	s.metrics.Updates.Set(float64(s.sched.numUpdates))
	s.metrics.Segments.Set(float64(s.sched.numSegments))
}

// nextWake returns the earlier of the two scheduled times.
func (s *State) nextWake() time.Time {
	if s.sched.nextUpdate.Before(s.sched.nextSegment) {
		return s.sched.nextUpdate
	}
	return s.sched.nextSegment
}
