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

func TestHasAgentCapability(t *testing.T) {
	cases := []struct {
		advertised string
		token      string
		want       bool
	}{
		{"", AgentCapabilityMSSQLGuardRails, false},
		{AgentCapabilityMSSQLGuardRails, AgentCapabilityMSSQLGuardRails, true},
		{"other,mssql_native_guardrails,more", AgentCapabilityMSSQLGuardRails, true},
		{" mssql_native_guardrails , x", AgentCapabilityMSSQLGuardRails, true},
		{"mssql_native_guardrail", AgentCapabilityMSSQLGuardRails, false},
		{"other", AgentCapabilityMSSQLGuardRails, false},
	}
	for _, tc := range cases {
		if got := HasAgentCapability(tc.advertised, tc.token); got != tc.want {
			t.Errorf("HasAgentCapability(%q, %q) = %v, want %v", tc.advertised, tc.token, got, tc.want)
		}
	}
}

func TestAgentAdvertisedCapabilities(t *testing.T) {
	if !HasAgentCapability(AgentAdvertisedCapabilities(), AgentCapabilityMSSQLGuardRails) {
		t.Errorf("advertised capabilities %q must include %q", AgentAdvertisedCapabilities(), AgentCapabilityMSSQLGuardRails)
	}
}
