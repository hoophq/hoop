// Package logging builds the control plane's logger. It is a constructor and
// nothing else. There is no wrapper type and no package-level Info/Warn/Error:
// call sites use log/slog directly.
//
// common/log is avoided on purpose. It is a separate module with a hundred
// dependencies, go-git and k8s.io/api among them, and importing it would drag
// all of them into this go.mod.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// Level and format come from the environment rather than from config.Config.
// The logger has to exist before config is loaded, or the errors config
// loading itself produces have nowhere to go.
const (
	EnvLevel  = "LOG_LEVEL"
	EnvFormat = "LOG_FORMAT"
)

// New builds a logger writing to w.
//
// level accepts trace, debug, info, warn, warning or error, case-insensitive,
// and anything else means info. trace maps to debug: the gateway accepts
// trace in the same variable, and an operator who copies a working value
// across should not get a startup failure for it.
//
// format accepts json or text; anything else means json. Unparseable logs in
// production cost more than readable logs in development do.
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
