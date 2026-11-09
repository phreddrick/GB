package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/engine"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"github.com/phreddrick/GB/go/internal/game"
)

type enrollmentEngine interface {
	EnrollTrusted(context.Context, game.EnrollmentRequest) (game.EnrollmentResult, error)
	EnrollmentLookup(context.Context, game.EnrollmentRequest) (enrollmentapi.View, error)
}

func (s *Server) enrollmentReply(w http.ResponseWriter, code int, op, reason string, view *enrollmentapi.View, validation any) {
	status := s.opts.Engine.Status()
	var result any
	if view != nil {
		result = view
	}
	response := Response{OK: code/100 == 2, Op: op, Status: &status, Result: result, Code: reason, Validation: validation}
	if response.OK {
		response.Code = ""
	} else {
		response.Error = reason
	}
	writeJSON(w, code, response)
}
func (s *Server) handleEnrollment(w http.ResponseWriter, r *http.Request) {
	const op = "enroll"
	if s.opts.Enrollment == nil {
		s.enrollmentReply(w, http.StatusForbidden, op, "ENROLLMENT_DISABLED", nil, nil)
		return
	}
	var req enrollmentapi.OperatorRequest
	if enrollmentapi.DecodeBody(r.Body, &req) != nil || !req.Query().Valid() || !enrollmentapi.ID(req.CredentialVersion, 128) {
		s.enrollmentReply(w, http.StatusBadRequest, op, "INVALID_REQUEST", nil, nil)
		return
	}
	view := enrollmentapi.NewView(req.Query())
	view.IntentDigest = enrollmentapi.IntentDigest(req)
	reference, e := s.opts.Enrollment.BindVersion(req.Query(), req.CredentialVersion)
	if e != nil {
		s.enrollmentReply(w, http.StatusForbidden, op, "ENROLLMENT_NOT_AUTHORIZED", &view, nil)
		return
	}
	actor, ok := s.opts.Engine.(enrollmentEngine)
	if !ok {
		s.enrollmentReply(w, http.StatusForbidden, op, "ENROLLMENT_DISABLED", &view, nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.opts.OpTimeout)
	defer cancel()
	result, e := actor.EnrollTrusted(ctx, game.EnrollmentRequest{RequestID: req.RequestID, CallerReference: reference, GameID: req.GameID, Spec: req.Spec, God: req.God, Guest: req.Guest})
	view.Permitted = s.opts.Engine.Status().Mode == "SERVING" && !s.opts.Engine.Status().Draining
	if e == nil && result.Outcome == db.EnrollmentCommitted && result.Placement.Player > 0 {
		view.State = enrollmentapi.Confirmed
		view.Outcome = string(result.Outcome)
		view.Replay = result.Replay
		view.Placement = game.EnrollmentPlacement(&result.Placement)
		s.enrollmentReply(w, http.StatusOK, op, "", &view, nil)
		return
	}
	code, reason := http.StatusInternalServerError, "OUTCOME_UNKNOWN"
	var validation any
	var failure *game.EnrollmentFailure
	var placementFailure *game.PlacementError
	switch {
	case errors.As(e, &failure):
		reason = failure.Code
		view.Outcome = string(failure.Outcome)
		view.State = enrollmentapi.NotPermitted
		if len(failure.Validation) > 0 {
			validation = failure.Validation
			code = http.StatusUnprocessableEntity
		} else {
			code = http.StatusConflict
		}
		switch failure.Outcome {
		case db.EnrollmentUncertain:
			view.State = enrollmentapi.Reconciliation
			code = http.StatusInternalServerError
		case db.EnrollmentCommitted:
			code = http.StatusInternalServerError
			view.State = enrollmentapi.Reconciliation
			if result.Placement.Player > 0 && result.Outcome == db.EnrollmentCommitted {
				view.State = enrollmentapi.Confirmed
				view.Placement = game.EnrollmentPlacement(&result.Placement)
			}
		}
		if failure.Code == "UNAUTHORIZED_ENROLLMENT" || failure.Code == "INVALID_IDENTITY" {
			code = http.StatusForbidden
		}
		if failure.Code == "REQUEST_CONFLICT" || failure.Code == "ACCOUNT_ALREADY_PLACED" || failure.Code == "NAME_TAKEN" {
			view.State = enrollmentapi.Conflict
		}
		if failure.Code == "ENROLLMENT_RECONCILIATION_REQUIRED" {
			view.Placement = nil
			view.Permitted = false
			view.State = enrollmentapi.Reconciliation
			code = http.StatusInternalServerError
		}
	case errors.As(e, &placementFailure):
		code = http.StatusConflict
		reason = string(placementFailure.Code)
		view.State = enrollmentapi.NotPermitted
		view.Outcome = string(db.EnrollmentNotCommitted)
	case errors.Is(e, engine.ErrNotExecuted):
		code = http.StatusServiceUnavailable
		reason = "NOT_EXECUTED"
		view.State = enrollmentapi.NotPermitted
		view.Outcome = string(db.EnrollmentNotCommitted)
	case errors.Is(e, engine.ErrNotServing):
		code = http.StatusConflict
		reason = "NOT_SERVING"
		view.State = enrollmentapi.NotPermitted
		view.Outcome = string(db.EnrollmentNotCommitted)
	case errors.Is(e, engine.ErrEnrollmentDisabled):
		code = http.StatusForbidden
		reason = "ENROLLMENT_DISABLED"
		view.State = enrollmentapi.NotPermitted
		view.Outcome = string(db.EnrollmentNotCommitted)
	case errors.Is(e, engine.ErrStillRunning), errors.Is(e, context.DeadlineExceeded), errors.Is(e, engine.ErrEngineStopped):
		code = http.StatusServiceUnavailable
	}
	s.enrollmentReply(w, code, op, reason, &view, validation)
}
func (s *Server) handleEnrollmentStatus(w http.ResponseWriter, r *http.Request) {
	const op = "enroll.status"
	if s.opts.Enrollment == nil {
		s.enrollmentReply(w, http.StatusForbidden, op, "ENROLLMENT_DISABLED", nil, nil)
		return
	}
	query := r.URL.Query()
	q := enrollmentapi.Query{RequestID: strings.TrimPrefix(r.URL.Path, "/admin/v1/enroll/"), GameID: query.Get("game_id"), AccountID: query.Get("account_id")}
	if !q.Valid() || len(query) != 2 || len(query["game_id"]) != 1 || len(query["account_id"]) != 1 {
		s.enrollmentReply(w, http.StatusBadRequest, op, "INVALID_REQUEST", nil, nil)
		return
	}
	view := enrollmentapi.NewView(q)
	reference, e := s.opts.Enrollment.Bind(q)
	if e != nil {
		s.enrollmentReply(w, http.StatusForbidden, op, "ENROLLMENT_NOT_AUTHORIZED", &view, nil)
		return
	}
	actor, ok := s.opts.Engine.(enrollmentEngine)
	if !ok {
		s.enrollmentReply(w, http.StatusForbidden, op, "ENROLLMENT_DISABLED", &view, nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.opts.OpTimeout)
	defer cancel()
	view, e = actor.EnrollmentLookup(ctx, game.EnrollmentRequest{RequestID: q.RequestID, CallerReference: reference, GameID: q.GameID})
	if e != nil {
		view = enrollmentapi.NewView(q)
		s.enrollmentReply(w, http.StatusServiceUnavailable, op, "OUTCOME_UNKNOWN", &view, nil)
		return
	}
	s.enrollmentReply(w, http.StatusOK, op, "", &view, nil)
}
