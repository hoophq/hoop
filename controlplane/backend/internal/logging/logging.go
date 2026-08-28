// Package logging builds the control plane's logger; a constructor and
// nothing else. Call sites use log/slog directly — no wrapper, so there is
// one logging style, and slog matches what the sidecar already threads
// through. common/log is avoided because importing it drags the whole
// common module (SDKs, k8s.io/api, go-git) into go.mod.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Read from the environment, not config.Config: the logger must exist
// before config loads so config errors have somewhere to go.
const (
	EnvLevel  = "LOG_LEVEL"
	EnvFormat = "LOG_FORMAT"
)

// New builds a logger writing to w.
//
// level accepts trace/debug/info/warn/warning/error, case-insensitive;
// unknown means info and trace maps to debug (gateway compatibility).
// format accepts json or text; anything else means json.
func New(w io.Writer, level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: ParseLevel(level)}
	if strings.EqualFold(strings.TrimSpace(format), "text") {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// FromEnv builds a logger from LOG_LEVEL and LOG_FORMAT.
func FromEnv(w io.Writer) *slog.Logger {
	return New(w, os.Getenv(EnvLevel), os.Getenv(EnvFormat))
}

// ParseLevel maps a level name onto slog.Level, defaulting to info.
func ParseLevel(v string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "TRACE", "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
