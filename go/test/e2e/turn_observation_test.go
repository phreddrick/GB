package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type turnLog struct {
	Time      time.Time `json:"time"`
	Component string    `json:"component"`
	Message   string    `json:"msg"`
	Op        string    `json:"op"`
	Trigger   string    `json:"trigger"`
	Turn      int       `json:"turn"`
	CommitSeq uint64    `json:"commit_seq"`
}

type turnLogSource interface {
	snapshot() (string, <-chan struct{})
}

func waitTurnLog(ctx context.Context, logs turnLogSource, message string, turn int) (turnLog, error) {
	for {
		if err := ctx.Err(); err != nil {
			return turnLog{}, err
		}
		text, changed := logs.snapshot()
		for _, line := range strings.Split(text, "\n") {
			var event turnLog
			if json.Unmarshal([]byte(line), &event) != nil || event.Component != "engine" || event.Op != "turn.update" || event.Trigger != "admin" || event.Message != message || (turn != 0 && event.Turn != turn) {
				continue
			}
			if event.Time.IsZero() || event.Turn < 1 || (message == "turn complete" && event.CommitSeq == 0) {
				return turnLog{}, fmt.Errorf("invalid %s record", message)
			}
			return event, nil
		}
		select {
		case <-ctx.Done():
			return turnLog{}, ctx.Err()
		case <-changed:
		}
	}
}

// The subprocess and parent use the same host wall clock (slog JSON loses
// monotonic time). Arrival at os/exec's log copier is not an engine event time.
func checkTurnObservation(started, finished turnLog, sent, received time.Time) error {
	if started.Time.IsZero() || finished.Time.IsZero() || started.Turn < 1 || started.Turn != finished.Turn || finished.CommitSeq == 0 || !started.Time.Before(finished.Time) {
		return fmt.Errorf("invalid turn completion evidence")
	}
	if !started.Time.Before(sent) || !sent.Before(finished.Time) {
		return fmt.Errorf("command was not sent during the admitted turn")
	}
	if !finished.Time.Before(received) {
		return fmt.Errorf("command output arrived before the turn finished")
	}
	return nil
}

func observationEvents() (turnLog, turnLog, time.Time, time.Time) {
	start := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	first := turnLog{Time: start, Component: "engine", Message: "turn starting", Op: "turn.update", Trigger: "admin", Turn: 1}
	last := first
	last.Message, last.Time, last.CommitSeq = "turn complete", start.Add(70*time.Second), 2
	return first, last, start.Add(time.Second), start.Add(71 * time.Second)
}

func TestTurnObservationOrdering(t *testing.T) {
	for _, which := range []string{"valid", "early-output", "equal-output", "before-admission", "after-completion", "wrong-turn", "no-commit", "no-time", "backward-time"} {
		t.Run(which, func(t *testing.T) {
			start, finish, sent, received := observationEvents()
			switch which {
			case "early-output":
				received = finish.Time.Add(-time.Millisecond)
			case "equal-output":
				received = finish.Time
			case "before-admission":
				sent = start.Time.Add(-time.Millisecond)
			case "after-completion":
				sent = finish.Time.Add(time.Millisecond)
			case "wrong-turn":
				finish.Turn++
			case "no-commit":
				finish.CommitSeq = 0
			case "no-time":
				finish.Time = time.Time{}
			case "backward-time":
				finish.Time = start.Time.Add(-time.Second)
			}
			err := checkTurnObservation(start, finish, sent, received)
			if (err == nil) != (which == "valid") {
				t.Fatalf("%s: %v", which, err)
			}
		})
	}
}

type observationWriter func([]byte) (int, error)

func (f observationWriter) Write(p []byte) (int, error) { return f(p) }

type inspectedLogs struct {
	logs      *logBuffer
	inspected chan struct{}
	once      sync.Once
}

func (l *inspectedLogs) snapshot() (string, <-chan struct{}) {
	text, changed := l.logs.snapshot()
	l.once.Do(func() { close(l.inspected) })
	return text, changed
}

// Deterministically hold the log copier after it has read the child's bytes.
// The valid command observation then precedes collection of its completion log,
// reproducing the old assertion's false failure without sleeps or clock changes.
func TestTurnObservationDelayedLogCopy(t *testing.T) {
	start, finish, sent, received := observationEvents()
	raw, err := json.Marshal(finish)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	arrived, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() { unblock(); _ = reader.Close(); _ = writer.Close() })
	logs := &logBuffer{}
	copied := make(chan error, 1)
	go func() {
		_, err := io.Copy(observationWriter(func(p []byte) (int, error) {
			close(arrived)
			<-release
			return logs.Write(p)
		}), reader)
		copied <- err
	}()
	emitted := make(chan error, 1)
	go func() { _, err := writer.Write(raw); _ = writer.Close(); emitted <- err }()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	select {
	case <-arrived:
	case <-ctx.Done():
		t.Fatal("log copier did not read the emitted record")
	}
	select {
	case err := <-emitted:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("record was not emitted")
	}
	old := &server{logs: logs}
	if old.hasLog("turn complete") {
		t.Fatal("controlled copy was not held; reproduction invalid")
	}
	// The original test would fail here, despite valid event ordering. Make
	// the new observer inspect that same empty snapshot before releasing it.
	source := &inspectedLogs{logs: logs, inspected: make(chan struct{})}
	observed := make(chan error, 1)
	go func() {
		got, err := waitTurnLog(ctx, source, "turn complete", start.Turn)
		if err == nil {
			err = checkTurnObservation(start, got, sent, received)
		}
		observed <- err
	}()
	select {
	case <-source.inspected:
	case <-ctx.Done():
		t.Fatal("observer did not inspect the held log copy")
	}
	unblock()
	select {
	case err := <-observed:
		if err != nil {
			t.Fatalf("delayed collection rejected valid ordering: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("observer did not see the released log copy")
	}
	select {
	case err := <-copied:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("log copier did not finish")
	}
}

func TestTurnLogWaitAndValidation(t *testing.T) {
	_, finish, _, _ := observationEvents()
	for _, which := range []string{"partial", "wrong-scope", "wrong-turn", "cancel", "missing-time", "missing-commit"} {
		t.Run(which, func(t *testing.T) {
			logs := &logBuffer{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			event := finish
			if which == "missing-time" {
				event.Time = time.Time{}
			}
			if which == "missing-commit" {
				event.CommitSeq = 0
			}
			raw, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			switch which {
			case "cancel":
				cancel()
			case "wrong-turn":
				// Both completions are valid and already collected. Only the
				// requested turn filter may select turn 1 over the first record.
				wrong := finish
				wrong.Turn = 2
				b, err := json.Marshal(wrong)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = logs.Write(append(b, '\n'))
				_, _ = logs.Write(raw)
			case "partial", "wrong-scope":
				_, changed := logs.snapshot()
				if which == "wrong-scope" {
					wrong := finish
					wrong.Trigger = "schedule"
					b, err := json.Marshal(wrong)
					if err != nil {
						t.Fatal(err)
					}
					_, _ = logs.Write(append(b, '\n'))
				} else {
					_, _ = logs.Write(raw[:len(raw)/2])
				}
				select {
				case <-changed:
				default:
					t.Fatal("append notification was lost")
				}
				if which == "partial" {
					_, _ = logs.Write(raw[len(raw)/2:])
				} else {
					_, _ = logs.Write(raw)
				}
			default:
				_, _ = logs.Write(raw)
			}
			got, err := waitTurnLog(ctx, logs, "turn complete", 1)
			if which == "wrong-turn" && (err != nil || got.Turn != 1) {
				// Assert selection itself, without a downstream ordering check.
				t.Fatalf("wrong-turn completion returned: got turn %d, requested turn 1: %v", got.Turn, err)
			}
			if (err == nil) != (which == "partial" || which == "wrong-scope" || which == "wrong-turn") || (err == nil && got != finish) {
				t.Fatalf("%s: %+v %v", which, got, err)
			}
		})
	}
}
