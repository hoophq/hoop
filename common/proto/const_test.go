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
