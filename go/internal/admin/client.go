package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/phreddrick/GB/go/internal/db"
	"github.com/phreddrick/GB/go/internal/universe"
)

// Client talks to the admin listener (used by gbctl and tests).
type Client struct {
	BaseURL string // e.g. http://127.0.0.1:8081
	Token   string
	HTTP    *http.Client
}

// Error is a non-2xx reply.
type Error struct {
	Code int
	Op   string
	Msg  string
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("admin %s: HTTP %d", e.Op, e.Code)
	}
	return fmt.Sprintf("admin %s: HTTP %d: %s", e.Op, e.Code, e.Msg)
}

// ErrUnauthorized is returned for 401 replies.
var ErrUnauthorized = errors.New("admin token rejected")

// ErrBadResponse is returned when a reply cannot be trusted as a confirmed
// result: unreadable or truncated body, invalid JSON, a body that is not the
// documented envelope, or a 2xx reply that does not confirm success for the
// requested operation (GB1-06). An HTTP 2xx status alone is never proof.
var ErrBadResponse = errors.New("invalid reply from the admin listener")

// maxReply bounds the reply body the client reads.
const maxReply = 1 << 20

func (c *Client) do(ctx context.Context, method, path string, body any) (*Response, error) {
	return c.doOp(ctx, method, path, strings.TrimPrefix(path, "/admin/v1/"), body, nil)
}

// resultCheck validates the operation-specific "result" of a confirmed 2xx
// envelope (raw JSON, nil when absent) and returns its typed form, which
// replaces Response.Result. Operations without a check must carry no result.
type resultCheck func(raw json.RawMessage) (any, error)

// doOp performs one request and validates the reply. op is the operation
// name the server must echo in a successful envelope; check validates the
// operation's result (GB1-06R).
func (c *Client) doOp(ctx context.Context, method, path, op string, body any, check resultCheck) (*Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxReply+1))
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%w (HTTP 401)", ErrUnauthorized)
	}
	if resp.StatusCode/100 != 2 {
		// Error replies: the status code decides; the envelope (if any)
		// only supplies the message.
		var out Response
		msg := ""
		if readErr == nil && json.Unmarshal(data, &out) == nil {
			msg = out.Error
		}
		if msg == "" {
			msg = strings.TrimSpace(string(data))
			if len(msg) > 200 {
				msg = msg[:200] + "..."
			}
		}
		return &out, &Error{Code: resp.StatusCode, Op: op, Msg: msg}
	}
	// Success replies must be complete, well-formed and confirm the result.
	if readErr != nil {
		return nil, fmt.Errorf("%w: %s: reading the reply failed (HTTP %d): %v", ErrBadResponse, op, resp.StatusCode, readErr)
	}
	if len(data) > maxReply {
		return nil, fmt.Errorf("%w: %s: reply larger than %d bytes", ErrBadResponse, op, maxReply)
	}
	out, rawResult, err := decodeEnvelope(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s (HTTP %d): %v", ErrBadResponse, op, resp.StatusCode, err)
	}
	if err := out.confirm(op); err != nil {
		return out, fmt.Errorf("%w: %s (HTTP %d): %v", ErrBadResponse, op, resp.StatusCode, err)
	}
	// The result is validated per operation and replaced by its typed form;
	// a generic "non-null" test is not a confirmation (GB1-06R).
	if check == nil {
		if !resultAbsent(rawResult) {
			return out, fmt.Errorf("%w: %s (HTTP %d): reply carries an unexpected result", ErrBadResponse, op, resp.StatusCode)
		}
		out.Result = nil
		return out, nil
	}
	typed, err := check(rawResult)
	if err != nil {
		return out, fmt.Errorf("%w: %s (HTTP %d): %v", ErrBadResponse, op, resp.StatusCode, err)
	}
	out.Result = typed
	return out, nil
}

// decodeEnvelope parses exactly one JSON object with the envelope's fields
// and nothing else; the "ok" field must be present. It also returns the raw
// "result" member (nil when absent) for operation-specific validation.
func decodeEnvelope(data []byte) (*Response, json.RawMessage, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil, errors.New("empty body")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, nil, fmt.Errorf("not a JSON object: %v", err)
	}
	if _, ok := probe["ok"]; !ok {
		return nil, nil, errors.New(`missing required field "ok"`)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var out Response
	if err := dec.Decode(&out); err != nil {
		return nil, nil, fmt.Errorf("unexpected envelope: %v", err)
	}
	if dec.More() {
		return nil, nil, errors.New("trailing data after the JSON object")
	}
	return &out, probe["result"], nil
}

// resultAbsent reports whether a raw result member is missing or JSON null.
func resultAbsent(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

// confirm checks that a decoded 2xx envelope reports a successful result of
// the requested operation.
func (r *Response) confirm(op string) error {
	switch {
	case !r.OK:
		if r.Error != "" {
			return fmt.Errorf("operation not confirmed (ok=false): %s", r.Error)
		}
		return errors.New("operation not confirmed (ok=false)")
	case r.Error != "":
		return fmt.Errorf("success reply carries an error: %s", r.Error)
	case r.Op != op:
		return fmt.Errorf("reply is for operation %q, expected %q", r.Op, op)
	case r.Status == nil || r.Status.Mode == "":
		return errors.New("reply lacks the engine status")
	}
	return nil
}

// universeRecordFromReply decodes and validates the result of a successful
// universe.init reply (GB1-06R). The contract is the server's: Result is the
// db.UniverseRecord that universe.Init wrote (seed, parameters, planet count,
// generator version, time and tool, all set), so the record must be a JSON
// object with exactly those members, every mandatory field present and in
// range, and the seed and any explicitly requested parameters must agree
// with the request. Anything else is not a confirmed initialisation.
func universeRecordFromReply(raw json.RawMessage, req InitRequest) (*db.UniverseRecord, error) {
	if resultAbsent(raw) {
		return nil, errors.New("universe init reply lacks the universe record")
	}
	t := bytes.TrimSpace(raw)
	if t[0] != '{' {
		return nil, fmt.Errorf("universe record is not a JSON object (got %s)", jsonKind(t))
	}
	dec := json.NewDecoder(bytes.NewReader(t))
	dec.DisallowUnknownFields()
	var rec db.UniverseRecord
	if err := dec.Decode(&rec); err != nil {
		return nil, fmt.Errorf("universe record: %v", err)
	}
	if err := validateUniverseRecord(&rec, req); err != nil {
		return nil, fmt.Errorf("universe record: %v", err)
	}
	return &rec, nil
}

// validateUniverseRecord checks a decoded record against the server's
// contract and the request it answers.
func validateUniverseRecord(rec *db.UniverseRecord, req InitRequest) error {
	switch {
	case rec.Seed == 0:
		return errors.New("seed is missing or zero")
	case rec.Seed != req.Seed:
		return fmt.Errorf("seed %d does not match the requested seed %d", rec.Seed, req.Seed)
	case rec.Stars == 0:
		return errors.New("stars is missing or zero")
	case rec.MinPlanets == 0:
		return errors.New("min_planets is missing or zero")
	case rec.MaxPlanets == 0:
		return errors.New("max_planets is missing or zero")
	}
	if err := (universe.Params{Stars: rec.Stars, MinPlanets: rec.MinPlanets, MaxPlanets: rec.MaxPlanets}).Validate(); err != nil {
		return err
	}
	switch {
	case req.Stars != 0 && rec.Stars != req.Stars:
		return fmt.Errorf("stars %d does not match the requested %d", rec.Stars, req.Stars)
	case req.MinPlanets != 0 && rec.MinPlanets != req.MinPlanets:
		return fmt.Errorf("min_planets %d does not match the requested %d", rec.MinPlanets, req.MinPlanets)
	case req.MaxPlanets != 0 && rec.MaxPlanets != req.MaxPlanets:
		return fmt.Errorf("max_planets %d does not match the requested %d", rec.MaxPlanets, req.MaxPlanets)
	case rec.Planets < rec.Stars*rec.MinPlanets || rec.Planets > rec.Stars*rec.MaxPlanets:
		return fmt.Errorf("planets %d is outside %d..%d for %d stars with %d..%d planets each",
			rec.Planets, rec.Stars*rec.MinPlanets, rec.Stars*rec.MaxPlanets, rec.Stars, rec.MinPlanets, rec.MaxPlanets)
	case strings.TrimSpace(rec.Generator) == "":
		return errors.New("generator is missing")
	case rec.InitializedAt.IsZero():
		return errors.New("initialized_at is missing")
	case strings.TrimSpace(rec.Tool) == "":
		return errors.New("tool is missing")
	}
	return nil
}

// jsonKind names the JSON type of a raw value for error messages (never the
// value itself beyond its first character class).
func jsonKind(t []byte) string {
	switch t[0] {
	case '"':
		return "a string"
	case '[':
		return "an array"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	}
	return "a number"
}

// Status fetches the engine status.
func (c *Client) Status(ctx context.Context) (*Response, error) {
	return c.do(ctx, http.MethodGet, "/admin/v1/status", nil)
}

// Update triggers a full update and waits for it.
func (c *Client) Update(ctx context.Context) (*Response, error) {
	return c.do(ctx, http.MethodPost, "/admin/v1/update", nil)
}

// Segment triggers a movement segment and waits for it.
func (c *Client) Segment(ctx context.Context) (*Response, error) {
	return c.do(ctx, http.MethodPost, "/admin/v1/segment", nil)
}

// Quiesce pauses the engine.
func (c *Client) Quiesce(ctx context.Context) (*Response, error) {
	return c.do(ctx, http.MethodPost, "/admin/v1/quiesce", nil)
}

// Resume resumes a quiesced engine.
func (c *Client) Resume(ctx context.Context) (*Response, error) {
	return c.do(ctx, http.MethodPost, "/admin/v1/resume", nil)
}

// UniverseInit initialises the universe.
// On success Response.Result is the validated *db.UniverseRecord.
func (c *Client) UniverseInit(ctx context.Context, req InitRequest) (*Response, error) {
	return c.doOp(ctx, http.MethodPost, "/admin/v1/universe/init", "universe.init", req, func(raw json.RawMessage) (any, error) {
		return universeRecordFromReply(raw, req)
	})
}
