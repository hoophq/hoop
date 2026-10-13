// Package sidecartui renders `hoop start sidecar` for a person at a terminal.
//
// The sidecar daemon writes two JSON streams and knows nothing about who reads
// them: operational logs (slog) on stderr, the audit trail as JSON lines on
// stdout or in a file. Production pipes both into a log platform and must keep
// getting exactly those bytes. This package never changes what the daemon
// writes. In TUI mode it captures the two streams and draws them; in text mode
// it rewrites the log stream for a human; in JSON mode it is not involved.
package sidecartui

import (
	"fmt"
	"strings"
)

// Format is how the sidecar's output reaches the terminal.
type Format string

const (
	// FormatAuto picks one of the others from the environment. See Resolve.
	FormatAuto Format = "auto"
	// FormatTUI draws the interactive dashboard.
	FormatTUI Format = "tui"
	// FormatText writes the operational logs as logfmt with no ANSI codes.
	// The audit trail stays JSON lines: it is a record someone queries, not
	// a log someone reads.
	FormatText Format = "text"
	// FormatJSON writes what the daemon writes, untouched.
	FormatJSON Format = "json"
)

// Formats lists the values --log-format accepts, for its help text.
var Formats = []Format{FormatAuto, FormatTUI, FormatText, FormatJSON}

// ParseFormat reads a --log-format value.
func ParseFormat(s string) (Format, error) {
	f := Format(strings.ToLower(strings.TrimSpace(s)))
	if f == "" {
		return FormatAuto, nil
	}
	for _, known := range Formats {
		if f == known {
			return f, nil
		}
	}
	return "", fmt.Errorf("unknown --log-format %q, use one of: auto, tui, text, json", s)
}

// Resolve turns a requested format into the one to run.
//
// An explicit format always wins. Auto keeps today's JSON whenever no person
// can be watching (stdout is a pipe or a file, CI is set, or the process runs
// in a container), because that is what every deployed sidecar already
// ships to its log platform. A person who can read but not type (stdin is not
// a terminal), a terminal that cannot or must not draw (TERM=dumb, NO_COLOR),
// or an AI coding agent gets text: a dashboard nobody can key into cannot be
// quit or answer an approval. Everyone else gets the TUI.
//
// exists reports whether a path is present; it finds the files a container
// runtime leaves, which plain Docker sets no variable for.
func Resolve(requested Format, stdoutTTY, stdinTTY bool, getenv func(string) string, exists func(string) bool) Format {
	if requested != FormatAuto && requested != "" {
		return requested
	}
	if !stdoutTTY || getenv("CI") != "" || deployed(getenv, exists) {
		return FormatJSON
	}
	if !stdinTTY || getenv("NO_COLOR") != "" || strings.EqualFold(getenv("TERM"), "dumb") || codingAgent(getenv) {
		return FormatText
	}
	return FormatTUI
}

// agentEnv are the variables AI coding agents set in the shells they run
// commands in (the list DuckDB's agent mode reads). Several run commands in a
// pseudo-terminal, so a tty alone would hand them a full-screen dashboard
// they cannot drive.
var agentEnv = []string{
	"CLAUDECODE", "CURSOR_AGENT", "GEMINI_CLI", "CODEX_SANDBOX", "CODEX_THREAD_ID", "CODEX_CI",
	"COPILOT_AGENT", "COPILOT_CLI", "AI_AGENT", "AGENT",
}

// codingAgent is whether an AI coding agent runs this process.
func codingAgent(getenv func(string) string) bool {
	for _, k := range agentEnv {
		if getenv(k) != "" {
			return true
		}
	}
	return false
}

// deployedEnv are set by a scheduler in what it runs: a Kubernetes pod, an
// ECS task, a Nomad allocation; and container by Podman, systemd-nspawn and
// LXC.
var deployedEnv = []string{
	"KUBERNETES_SERVICE_HOST", "ECS_CONTAINER_METADATA_URI_V4", "ECS_CONTAINER_METADATA_URI",
	"NOMAD_ALLOC_ID", "container",
}

// deployedFiles are left by a container runtime: Docker, then Podman.
var deployedFiles = []string{"/.dockerenv", "/run/.containerenv"}

// deployed is whether this process runs in a container or under a
// scheduler. `docker run -it`, compose with tty and stdin_open, or a pod with
// tty: true and stdin: true have terminals nobody reads; the output is what
// the log collector ships.
func deployed(getenv func(string) string, exists func(string) bool) bool {
	for _, k := range deployedEnv {
		if getenv(k) != "" {
			return true
		}
	}
	for _, f := range deployedFiles {
		if exists(f) {
			return true
		}
	}
	return false
}

// Interactive is whether a person can answer at this terminal: both stdin and
// stdout are terminals. Only then may the TUI take held statements for
// approval. A TUI forced onto a pipe (--log-format tui) still draws, but a
// hold there would wait its whole timeout for a key nobody can press.
func Interactive(stdoutTTY, stdinTTY bool) bool { return stdoutTTY && stdinTTY }
