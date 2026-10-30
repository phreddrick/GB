// Package logging configures the process-wide structured logger (log/slog,
// JSON to stdout) and the conventions for its fields. The legacy log package
// is redirected through the same handler so that gameplay files that still
// call log.Printf produce JSON lines without being modified in this phase.
package logging

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"strings"
)

// Field names shared by all components (migration plan 14.2).
const (
	KeyComponent = "component"
	KeyOp        = "op"
	KeyRequestID = "request_id"
	KeySessionID = "session_id"
	KeyPlayer    = "player"
	KeyGovernor  = "governor"
	KeyTurn      = "turn"
	KeySegment   = "segment"
	KeyDuration  = "duration_ms"
	KeyErrClass  = "err_class"
	KeyErr       = "err"
	KeyMode      = "mode"
)

// ParseLevel maps a configuration string to a slog level (default info).
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New returns a JSON logger writing to w at the given level and installs it
// as the slog and log default, so every package logs in one format.
func New(w io.Writer, level slog.Level, service string) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	logger := slog.New(h).With("service", service)
	slog.SetDefault(logger)
	// log.Printf lines become {"level":"INFO","msg":"<text>","service":...}.
	log.SetFlags(0)
	log.SetOutput(slog.NewLogLogger(h, slog.LevelInfo).Writer())
	return logger
}

// ErrClass returns a bounded classification of an error for logs and
// metrics: the dynamic type name without package path or pointer marker, or
// "nil".
func ErrClass(err error) string {
	if err == nil {
		return "nil"
	}
	t := strings.TrimPrefix(fmt.Sprintf("%T", err), "*")
	if i := strings.LastIndex(t, "."); i >= 0 {
		t = t[i+1:]
	}
	return t
}
