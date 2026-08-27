// Package logging builds the control plane's logger. It is a constructor and
// nothing else.
//
// There is no wrapper type, no package-level Info/Warn/Error, and no f
// variants. Call sites use log/slog directly. A wrapper would have to offer
// both attribute and printf forms, and the moment it offers both, half the
// codebase picks one and half picks the other; the printf form then collapses
// the fields a JSON handler exists to keep separate.
//
// Why not github.com/hoophq/hoop/common/log, which is the convention
// everywhere else in this repository: it is a zap wrapper, and importing it
// means requiring the whole common module, which reaches the Anthropic SDK,
// the OpenAI SDK, k8s.io/api and go-git. None of that links into the final
// binary, but all of it lands in go.mod and go.sum. A module created
// specifically to escape the gateway's dependency tree should not re-import
// it for a logger.
//
// hoopinspect, the process on the other end of this control plane's socket,
// already threads *slog.Logger through sidecar and proxy. Matching it means
// the two halves of hoop 2.0 log the same way.
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
