package sshproxy

import (
	"strings"
	"sync"
	"testing"
)

func TestTranslateUpstreamError(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty input returns empty",
			in:   "",
			want: "",
		},
		{
			name: "user-initiated disconnect is suppressed",
			in:   "ssh client disconnected",
			want: "",
		},
		{
			name: "connection refused with gateway wrapper",
			in:   "connection closed by server, payload=failed initializing connection, " +
				"reason=failed establishing tcp connection with 192.168.55.111:22: " +
				"dial tcp 192.168.55.111:22: connect: connection refused",
			want: "cannot connect to target server: connection refused",
		},
		{
			name: "i/o timeout with gateway wrapper",
			in:   "connection closed by server, payload=failed initializing connection, " +
				"reason=failed establishing tcp connection with 192.0.2.1:22: " +
				"dial tcp 192.0.2.1:22: i/o timeout",
			want: "cannot connect to target server: connection timed out",
		},
		{
			name: "deadline exceeded normalized to timeout",
			in:   "context deadline exceeded",
			want: "cannot connect to target server: connection timed out",
		},
		{
			name: "no route to host",
			in:   "dial tcp 10.0.0.1:22: connect: no route to host",
			want: "cannot connect to target server: no route to host",
		},
		{
			name: "dns failure",
			in:   "dial tcp: lookup not-a-real-host: no such host",
			want: "cannot resolve target server hostname",
		},
		{
			name: "ssh auth failure",
			in:   "connection closed by server, payload=ssh: handshake failed: " +
				"ssh: unable to authenticate, attempted methods [none password], " +
				"no supported methods remain",
			want: "authentication failed against target server",
		},
		{
			name: "session ready timeout",
			in:   "session timed out before it was ready",
			want: "session timed out waiting for the target server",
		},
		{
			name: "oss libhoop stub message",
			in:   "connection closed by server, payload=missing protocol hoop library for ssh, " +
				"contact your administrator",
			want: "ssh is not enabled on this hoop agent build, contact your administrator",
		},
		{
			name: "credential revoked",
			in:   "credential revoked",
			want: "your access to this target server has been revoked",
		},
		{
			name: "unknown error falls back to generic message",
			in:   "something we never anticipated happened deep in the stack: 0xdeadbeef",
			want: "cannot connect to target server",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := translateUpstreamError(tc.in)
			if got != tc.want {
				t.Errorf("translateUpstreamError(%q)\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatRaw(t *testing.T) {
	got := formatRaw("hello world")
	want := "hoop: hello world\r\n"
	if got != want {
		t.Errorf("formatRaw mismatch:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestNotifyOpenChannels_EmptyMessage(t *testing.T) {
	// notifyOpenChannels with an empty message must be a no-op even
	// when there are channels registered — this is the path the
	// caller takes for user-initiated disconnects (translate returns
	// "" → no Range, no Stderr writes).
	channels := &sync.Map{}
	stub := &stderrStubChannel{}
	channels.Store("1", stub)

	notifyOpenChannels(channels.Range, "")

	if stub.Buffer.Len() != 0 {
		t.Errorf("expected no writes for empty message, got %q", stub.Buffer.String())
	}
}

func TestNotifyOpenChannels_WritesToEveryChannel(t *testing.T) {
	channels := &sync.Map{}
	a := &stderrStubChannel{}
	b := &stderrStubChannel{}
	channels.Store("1", a)
	channels.Store("2", b)

	notifyOpenChannels(channels.Range, "cannot connect to target server")

	for label, ch := range map[string]*stderrStubChannel{"a": a, "b": b} {
		got := ch.Buffer.String()
		if !strings.HasPrefix(got, upstreamFailurePrefix) {
			t.Errorf("channel %s: expected prefix %q, got %q", label, upstreamFailurePrefix, got)
		}
		if !strings.Contains(got, "cannot connect to target server") {
			t.Errorf("channel %s: expected message body, got %q", label, got)
		}
		if !strings.HasSuffix(got, "\r\n") {
			t.Errorf("channel %s: expected trailing CRLF, got %q", label, got)
		}
		// notify must close each channel after the write so the
		// gateway-side read goroutine parked in clientCh.Read
		// unblocks. Without this, the gateway's flush wait hits
		// its 5-second timeout and the user stares at the
		// message before the connection actually drops.
		if ch.CloseCount != 1 {
			t.Errorf("channel %s: expected Close to be called once, got %d", label, ch.CloseCount)
		}
	}
}

func TestNotifyOpenChannels_StderrErrorIgnored(t *testing.T) {
	// A failing Stderr write must not panic or block the caller.
	// The whole point of the function is best-effort during shutdown
	// where writes can race with the transport closing.
	channels := &sync.Map{}
	channels.Store("1", &stderrStubChannel{failWrite: true})

	notifyOpenChannels(channels.Range, "cannot connect to target server")
}
