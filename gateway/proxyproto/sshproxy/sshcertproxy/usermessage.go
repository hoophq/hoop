package sshcertproxy

import (
	"io"
	"strings"

	"golang.org/x/crypto/ssh"
)

const upstreamFailurePrefix = "hoop: "
const agentClosePayloadPrefix = "connection closed by server, payload="

// translateUpstreamError converts a raw libhoop / agent error string into a
// user-facing one-liner safe to display on an SSH terminal. Returns the empty
// string when the cause should produce no user-visible message (e.g. the user
// disconnected themselves).
func translateUpstreamError(cause string) string {
	if cause == "" {
		return ""
	}

	core := cause
	if idx := strings.Index(core, agentClosePayloadPrefix); idx >= 0 {
		core = core[idx+len(agentClosePayloadPrefix):]
	}

	lower := strings.ToLower(core)

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

	return "cannot connect to target server"
}

// notifyOpenChannels writes the same message line to the stderr stream of
// every currently-open client SSH channel and then closes each channel.
func notifyOpenChannels(rangeFn func(func(any, any) bool), message string) {
	if message == "" {
		return
	}
	line := upstreamFailurePrefix + message + "\r\n"

	rangeFn(func(key, value any) bool {
		ch, ok := value.(ssh.Channel)
		if !ok || ch == nil {
			return true
		}
		_, _ = io.WriteString(ch.Stderr(), line)
		_ = ch.Close()
		return true
	})
}
