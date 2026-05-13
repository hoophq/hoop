package sshproxy

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// upstreamFailurePrefix is the line prefix used when writing a
// translated upstream error to the user's SSH channel stderr. Keeping
// it consistent ("hoop: ") makes the line easy to recognize in user
// terminals and easy to grep for in logs that mirror the message.
const upstreamFailurePrefix = "hoop: "

// agentClosePayloadPrefix is the prefix the gateway adds when
// converting an inbound ClientSessionClose into a cancellation cause.
// See sshproxy.go where the cause is built as
// `connection closed by server, payload=<libhoop error>`. We strip
// this here so the user message starts with the libhoop reason
// rather than the gateway-side framing.
const agentClosePayloadPrefix = "connection closed by server, payload="

// translateUpstreamError converts a raw libhoop / agent error string
// into a user-facing one-liner that's safe to display on an SSH
// terminal. The classification is best-effort and conservative: any
// pattern we don't recognize falls through to a generic message so
// we never leak internal addresses, stack traces, or library jargon
// to end users.
//
// Returns the empty string if the cause should not produce any
// user-visible message (e.g. the user disconnected themselves —
// they don't need a message about that).
func translateUpstreamError(cause string) string {
	if cause == "" {
		return ""
	}

	// Most upstream failures arrive wrapped in the gateway's
	// "connection closed by server, payload=..." framing. Strip it so
	// downstream classification sees the bare libhoop reason.
	core := cause
	if idx := strings.Index(core, agentClosePayloadPrefix); idx >= 0 {
		core = core[idx+len(agentClosePayloadPrefix):]
	}

	lower := strings.ToLower(core)

	// User-initiated disconnects: don't produce a message at all.
	// "ssh client disconnected" is the gateway-side cancelFn fired
	// when the user's local ssh process closes. There's nothing
	// useful to tell them.
	switch lower {
	case "ssh client disconnected":
		return ""
	}

	switch {
	case strings.Contains(lower, "connection refused"):
		return "cannot connect to target server: connection refused"
	case strings.Contains(lower, "i/o timeout"),
		strings.Contains(lower, "deadline exceeded"):
		return "cannot connect to target server: connection timed out"
	case strings.Contains(lower, "no route to host"):
		return "cannot connect to target server: no route to host"
	case strings.Contains(lower, "no such host"),
		strings.Contains(lower, "lookup"):
		return "cannot resolve target server hostname"
	case strings.Contains(lower, "unable to authenticate"),
		strings.Contains(lower, "auth failed"),
		strings.Contains(lower, "no supported methods remain"):
		return "authentication failed against target server"
	case strings.Contains(lower, "session timed out before it was ready"):
		return "session timed out waiting for the target server"
	case strings.Contains(lower, "missing protocol hoop library"):
		return "ssh is not enabled on this hoop agent build, contact your administrator"
	case strings.Contains(lower, "credential revoked"),
		strings.Contains(lower, "access expired"):
		return "your access to this target server has been revoked"
	}

	// Generic fallback: the cause exists but we don't have a
	// specific translation. Tell the user something failed without
	// leaking the raw text.
	return "cannot connect to target server"
}

// notifyOpenChannels writes the same message line to the stderr
// stream of every currently-open client SSH channel and then closes
// each channel. Called from handleConnection's shutdown path when
// the session ctx is cancelled with a non-empty cause.
//
// channels is the gateway-side map of channel-id-string → ssh.Channel
// (the value the gateway accepted via newCh.Accept()).
//
// Writes are best-effort — failures to write or close are logged at
// the caller's discretion but don't fail the shutdown.
func notifyOpenChannels(channels *sync.Map, message string) {
	if message == "" {
		return
	}
	line := upstreamFailurePrefix + message + "\r\n"

	channels.Range(func(key, value any) bool {
		ch, ok := value.(ssh.Channel)
		if !ok || ch == nil {
			return true
		}
		// Best-effort write; if stderr is already closed or the
		// transport has gone away, the user won't see this anyway
		// and we don't want to block the shutdown.
		if _, err := io.WriteString(ch.Stderr(), line); err != nil {
			// Log via the caller's logger if needed; here we
			// silently move on so this helper stays cheap and
			// log-free for callers to control verbosity.
			_ = err
		}
		return true
	})
}

// userMessageDecorator is a tiny helper that lets a caller compose a
// failure message + structured logging in one place. Kept as a
// standalone type rather than a closure so its lifecycle is obvious
// in the call site.
type userMessageDecorator struct {
	channels *sync.Map
}

func newUserMessageDecorator(channels *sync.Map) *userMessageDecorator {
	return &userMessageDecorator{channels: channels}
}

// Notify writes the translated message (if any) and returns the
// translated text so the caller can include it in its own log line.
// Returns the empty string if no message was written, which the
// caller can use to skip logging.
func (d *userMessageDecorator) Notify(cause error) string {
	if cause == nil {
		return ""
	}
	msg := translateUpstreamError(cause.Error())
	if msg == "" {
		return ""
	}
	notifyOpenChannels(d.channels, msg)
	return msg
}

// formatRaw is a tiny convenience for tests / debugging — returns
// the unmodified line the helper would write, including the prefix
// and trailing CR/LF.
func formatRaw(message string) string {
	return fmt.Sprintf("%s%s\r\n", upstreamFailurePrefix, message)
}
