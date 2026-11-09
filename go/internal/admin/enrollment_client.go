package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/phreddrick/GB/go/internal/engine"
	"github.com/phreddrick/GB/go/internal/enrollmentapi"
	"github.com/phreddrick/GB/go/internal/racegen"
	"github.com/phreddrick/GB/go/internal/wirejson"
)

// Enrollment never treats HTTP200 alone, partial JSON or a visible uncertain
// result as confirmed placement. Error bodies are sanitized, not echoed.
func (c *Client) Enrollment(ctx context.Context, req enrollmentapi.OperatorRequest) (*Response, error) {
	return c.enrollmentOp(ctx, "enroll", req.Query(), &req)
}
func (c *Client) EnrollmentStatus(ctx context.Context, q enrollmentapi.Query) (*Response, error) {
	return c.enrollmentOp(ctx, "enroll.status", q, nil)
}
func (c *Client) enrollmentOp(ctx context.Context, op string, q enrollmentapi.Query, body *enrollmentapi.OperatorRequest) (*Response, error) {
	if !q.Valid() {
		return nil, errors.New("invalid enrollment binding")
	}
	base, e := url.Parse(c.BaseURL)
	if e != nil || base.User != nil || base.RawQuery != "" || base.Fragment != "" || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, errors.New("invalid admin URL")
	}
	method, path := http.MethodPost, "/admin/v1/enroll"
	if body == nil {
		method = http.MethodGet
		path += "/" + q.RequestID + "?" + url.Values{"game_id": {q.GameID}, "account_id": {q.AccountID}}.Encode()
	}
	var reader io.Reader
	if body != nil {
		data, e := json.Marshal(body)
		if e != nil {
			return nil, errors.New("invalid enrollment request")
		}
		reader = bytes.NewReader(data)
	}
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, reader)
	if e != nil {
		return nil, errors.New("invalid admin request")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	hc := http.Client{Timeout: 15 * time.Minute}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, e := hc.Do(req)
	if e != nil {
		return nil, errors.New("enrollment outcome unknown: transport ended; use the same request/account/game status lookup")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	data, e := io.ReadAll(io.LimitReader(res.Body, maxReply+1))
	if e != nil || len(data) > maxReply {
		return nil, ErrBadResponse
	}
	var envelope struct {
		OK         bool                `json:"ok"`
		Op         string              `json:"op"`
		Error      string              `json:"error,omitempty"`
		Code       string              `json:"code,omitempty"`
		Validation racegen.Errors      `json:"validation,omitempty"`
		Status     *engine.Status      `json:"status"`
		Result     *enrollmentapi.View `json:"result,omitempty"`
	}
	if wirejson.Decode(data, &envelope) != nil || envelope.Op != op || envelope.Status == nil || !enrollmentMode(envelope.Status.Mode) {
		return nil, ErrBadResponse
	}
	if envelope.Result != nil && (envelope.Result.Validate(q) != nil || body != nil && envelope.Result.IntentDigest != enrollmentapi.IntentDigest(*body)) {
		return nil, ErrBadResponse
	}
	out := &Response{OK: envelope.OK, Op: op, Status: envelope.Status, Result: envelope.Result, Code: envelope.Code, Validation: envelope.Validation}
	if res.StatusCode/100 == 2 {
		if !envelope.OK || len(envelope.Validation) != 0 || envelope.Code != "" || envelope.Error != "" || envelope.Result == nil || op == "enroll" && envelope.Result.State != enrollmentapi.Confirmed {
			return nil, ErrBadResponse
		}
		return out, nil
	}
	if len(envelope.Validation) > 0 && envelope.Code != "INVALID_DESIGN" {
		return nil, ErrBadResponse
	}
	if envelope.OK || envelope.Code == "" || !safeEnrollmentCode(envelope.Code) || envelope.Error != envelope.Code {
		return nil, ErrBadResponse
	}
	out.Error = envelope.Code
	return out, &Error{Code: res.StatusCode, Op: op, Msg: envelope.Code}
}
func safeEnrollmentCode(s string) bool {
	switch s {
	case "INVALID_REQUEST", "ENROLLMENT_NOT_AUTHORIZED", "ENROLLMENT_DISABLED", "OUTCOME_UNKNOWN", "INVALID_IDENTITY", "UNAUTHORIZED_ENROLLMENT", "INVALID_NAME", "INVALID_DESIGN", "REQUEST_CONFLICT", "ACCOUNT_ALREADY_PLACED", "SLOTS_FULL", "GOD_BOOTSTRAP_REQUIRED", "NAME_TAKEN", "CREDENTIAL_PREPARATION_FAILED", "SHIP_ALLOCATION_CONFLICT", "ENROLLMENT_PENDING_TURN", "ENROLLMENT_RECONCILIATION_REQUIRED", "ENROLLMENT_COMMITTED_ERROR", "ENROLLMENT_IO", "INVALID_UNIVERSE", "INVALID_SEARCH_ORDER", "ENROLLMENT_RANDOMNESS_FAILED", "NO_HOME_PLANET", "NO_CAPITAL_SECTOR", "INVALID_ORBIT_CHAIN", "NOT_EXECUTED", "NOT_SERVING":
		return true
	}
	return false
}

func enrollmentMode(mode string) bool {
	switch mode {
	case "STARTING", "UNINITIALIZED", "SERVING", "TURN_IN_PROGRESS", "QUIESCED", "HALTED", "STOPPED":
		return true
	}
	return false
}
