package game

import (
	"context"
	"errors"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
)

// EnrollmentLookup is private read-only diagnosis. It never clears uncertainty,
// creates an intent or equates absence with a previous transaction's rollback.
func (s *State) EnrollmentLookup(ctx context.Context, req EnrollmentRequest) (enrollmentapi.View, error) {
	return call(s, ctx, adminOp("enroll.status"), func() (enrollmentapi.View, error) {
		view := enrollmentapi.View{State: enrollmentapi.Unknown, Outcome: enrollmentapi.Unknown}
		policy := s.enrollmentPolicy
		if policy == nil || !enrollmentID(req.RequestID, 64) || !enrollmentID(req.CallerReference, 128) || req.GameID != policy.GameID {
			view.State = enrollmentapi.NotPermitted
			return view, ErrEnrollmentDisabled
		}
		auth, e := policy.Authorize(req)
		if e != nil || !enrollmentID(auth.CallerID, 128) || auth.GameID != policy.GameID {
			return view, enrollmentFailure("UNAUTHORIZED_ENROLLMENT", db.EnrollmentNotCommitted)
		}
		q := enrollmentapi.Query{RequestID: req.RequestID, GameID: req.GameID, AccountID: auth.CallerID}
		view = enrollmentapi.NewView(q)
		rec, e := s.store.GetPlacement(auth.CallerID, req.RequestID)
		if e != nil {
			var failure *db.EnrollmentError
			if errors.As(e, &failure) && failure.Code == db.EnrollmentConflict {
				view.State = enrollmentapi.Conflict
				view.Outcome = string(db.EnrollmentNotCommitted)
				return view, nil
			}
			view.State = enrollmentapi.Unknown
			return view, nil
		}
		if s.store.EnrollmentWriteUncertain() {
			view.State = enrollmentapi.Reconciliation
			view.Outcome = string(db.EnrollmentUncertain)
			return view, nil
		}
		if s.Mode() == ModeHalted {
			view.State = enrollmentapi.Reconciliation
			return view, nil
		}
		view.Permitted = s.Mode() == ModeServing && !s.draining.Load()
		if rec == nil {
			view.State = enrollmentapi.NoKnownPlacement
			return view, nil
		}
		consistent, pending := s.enrollmentWorldState()
		if pending {
			view.State = enrollmentapi.NotPermitted
			view.Permitted = false
			return view, nil
		}
		if !consistent || !s.enrollmentReplayConsistent(rec) {
			view.State = enrollmentapi.Reconciliation
			view.Permitted = false
			return view, nil
		}
		view.State = enrollmentapi.Confirmed
		view.Outcome = string(db.EnrollmentCommitted)
		view.Placement = EnrollmentPlacement(rec)
		return view, nil
	})
}
func EnrollmentPlacement(rec *db.PlacementRecord) *enrollmentapi.Placement {
	if rec == nil {
		return nil
	}
	return &enrollmentapi.Placement{Player: rec.Player, Star: rec.Star, Planet: rec.Planet, CapitalX: rec.CapitalX, CapitalY: rec.CapitalY, GovernmentShip: rec.GovernmentShip, CreatedAt: rec.CreatedAt}
}
