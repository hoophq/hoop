package proto

import "testing"

func TestSessionOriginFromClientOrigin(t *testing.T) {
	cases := map[string]string{
		ConnectionOriginClient:             SessionOriginCLI,
		ConnectionOriginClientProxyManager: SessionOriginProxyManager,
		ConnectionOriginClientAPI:          SessionOriginAPI,
		ConnectionOriginClientAPIRunbooks:  SessionOriginRunbooks,
		ConnectionOriginAgent:              SessionOriginAgent,
		"something-else":                   SessionOriginUnknown,
		"":                                 SessionOriginUnknown,
	}
	for clientOrigin, want := range cases {
		if got := SessionOriginFromClientOrigin(clientOrigin); got != want {
			t.Errorf("SessionOriginFromClientOrigin(%q) = %q, want %q", clientOrigin, got, want)
		}
	}
}

func TestSessionOriginFromUserAgent(t *testing.T) {
	cases := map[string]string{
		"webapp.core": SessionOriginWebApp,
		"hoopcli":     SessionOriginCLI,
		"curl":        SessionOriginAPI,
		"":            SessionOriginAPI,
	}
	for userAgent, want := range cases {
		if got := SessionOriginFromUserAgent(userAgent); got != want {
			t.Errorf("SessionOriginFromUserAgent(%q) = %q, want %q", userAgent, got, want)
		}
	}
}

func TestSessionRecordingFormat(t *testing.T) {
	for _, tt := range []struct {
		connType, subtype, verb string
		want                    string
	}{
		{"custom", "", ClientVerbConnect, RecordingFormatPTY},
		{"command-line", "", ClientVerbConnect, RecordingFormatPTY},
		{"application", "python", ClientVerbConnect, RecordingFormatPTY},
		{"custom", "", ClientVerbExec, RecordingFormatExec},
		{"database", "postgres", ClientVerbExec, RecordingFormatExec},
		{"custom", "kubernetes", ClientVerbConnect, RecordingFormatRaw},
		{"custom", "kubernetes-eks", ClientVerbConnect, RecordingFormatRaw},
		{"custom", "kubernetes-token", ClientVerbConnect, RecordingFormatRaw},
		{"custom", "httpproxy", ClientVerbConnect, RecordingFormatRaw},
		{"custom", "aws-ssm", ClientVerbConnect, RecordingFormatRaw},
		{"application", "ssh", ClientVerbConnect, RecordingFormatRaw},
		{"application", "tcp", ClientVerbConnect, RecordingFormatRaw},
		{"database", "postgres", ClientVerbConnect, RecordingFormatRaw},
		{"custom", "rdp", ClientVerbConnect, RecordingFormatRDP},
	} {
		if got := SessionRecordingFormat(tt.connType, tt.subtype, tt.verb); got != tt.want {
			t.Errorf("SessionRecordingFormat(%s/%s, %s) = %q, want %q", tt.connType, tt.subtype, tt.verb, got, tt.want)
		}
	}
}
