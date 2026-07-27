package mcpserver

import (
	"github.com/hoophq/hoop/common/log"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/clientexec"
	"github.com/hoophq/hoop/gateway/federation"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// oauthRequiredEnvelope builds the actionable MCP response returned when a
// gcp_oauth-federated connection requires the user to connect their Google
// account before a session can run. It mints the consent URL in-process (no
// self-HTTP); when URL minting fails the envelope still tells the user how to
// connect via the web app so the underlying cause is never silently swallowed.
//
// An MCP server cannot open the user's browser, so the consent URL is returned
// in the tool result for the agent to relay to the human.
func oauthRequiredEnvelope(sc *storagev2.Context, connectionName string) map[string]any {
	env := map[string]any{
		"status":          "oauth_required",
		"connection_name": connectionName,
		"message": "This connection runs queries as your own Google identity and needs your Google " +
			"account connected. Open consent_url in a browser to authorize, then run the command again.",
		"next_step": "open consent_url, approve access, then retry the command",
	}
	consentURL, err := apiconnections.BuildFederationConsentURL(sc, connectionName, "")
	if err != nil {
		log.Warnf("mcp: failed building federation consent url for connection %q: %v", connectionName, err)
		env["error"] = "could not build the Google consent link automatically; open the Hoop web app and " +
			"connect your Google account for this connection, then retry"
	} else {
		env["consent_url"] = consentURL
	}
	return env
}

// federationConsentEnvelopeFromResponse returns an oauth_required envelope when
// a clientexec response carries the gateway's stable "user not connected"
// federation marker, and false otherwise. It is the response-stage safety net
// for the session-open federation gate — e.g. the user disconnected their
// account between the preflight check and the run, or a reviewed session hits
// the gate — so callers fall through to their normal envelope when not matched.
func federationConsentEnvelopeFromResponse(sc *storagev2.Context, resp *clientexec.Response) (map[string]any, bool) {
	if resp == nil || resp.OutputStatus != "failed" {
		return nil, false
	}
	name, ok := federation.ParseOAuthNotConnected(resp.Output)
	if !ok {
		return nil, false
	}
	return oauthRequiredEnvelope(sc, name), true
}
