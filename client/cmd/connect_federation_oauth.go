package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/common/httpclient"
	"github.com/hoophq/hoop/common/log"
)

// federationOAuthNotConnectedCode is the stable, machine-readable marker the
// gateway embeds in the session-open precondition error when a per-user
// federation provider (gcp_oauth) has no stored credential for the user. See
// gateway/transport/client.go.
const federationOAuthNotConnectedCode = "code=oauth_not_connected"

// parseFederationOAuthNotConnected reports whether errMsg is the gateway's
// "user has not connected their account" precondition error and, when present,
// extracts the connection name from the `connection=<name>` tag. Matching on
// the stable code (not the human-readable text) keeps this resilient to message
// wording changes, and works whether errMsg is a raw rpc error string or an
// HTTP error body that embeds it.
func parseFederationOAuthNotConnected(errMsg string) (connectionName string, ok bool) {
	if !strings.Contains(errMsg, federationOAuthNotConnectedCode) {
		return "", false
	}
	const marker = "connection="
	idx := strings.Index(errMsg, marker)
	if idx == -1 {
		return "", true
	}
	rest := errMsg[idx+len(marker):]
	// The tag is rendered as "[code=oauth_not_connected connection=<name>]";
	// the name ends at the first space or closing bracket.
	if end := strings.IndexAny(rest, " ]"); end != -1 {
		return strings.TrimSpace(rest[:end]), true
	}
	return strings.TrimSpace(rest), true
}

// printFederationOAuthConsentAndExit renders an actionable "connect your Google
// account" prompt in place of the raw error, fetching the consent URL from the
// gateway with the user's stored access token and best-effort opening the
// browser. It always terminates the process. Shared by the interactive connect
// path and the reviewed-session exec path.
func printFederationOAuthConsentAndExit(apiURL, token, tlsCA string, jsonMode bool, connectionName string, rawErr error) {
	consentURL, err := fetchFederationConsentURL(apiURL, token, tlsCA, connectionName)
	if err != nil {
		// If we cannot obtain the consent URL (e.g. the access token expired),
		// never swallow the original cause: show it alongside a manual path.
		log.Debugf("failed fetching federation consent url for %q: %v", connectionName, err)
		fatalErr(jsonMode,
			"%s\n\nTo connect your Google account for this connection, run 'hoop login' to refresh your "+
				"session and try again.",
			rawErr.Error())
		return
	}

	if jsonMode {
		out, _ := json.Marshal(map[string]any{
			"error":       "oauth_not_connected",
			"connection":  connectionName,
			"consent_url": consentURL,
			"message":     "connect your Google account, then re-run the command",
		})
		fmt.Fprintln(os.Stdout, string(out))
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, styles.ClientErrorSimple(
		"This connection runs queries as you and needs your Google account connected."))
	fmt.Fprintf(os.Stderr,
		"\nOpen this link to authorize, then run your command again:\n\n  %s\n\n",
		consentURL)
	if connectionName != "" {
		fmt.Fprintf(os.Stderr, "Resource: %s\n\n", styles.Keyword(fmt.Sprintf(" %s ", connectionName)))
	}

	if err := openBrowser(consentURL); err != nil {
		log.Debugf("failed opening browser for consent url: %v", err)
	}
	os.Exit(1)
}

// fetchFederationConsentURL asks the gateway for the Google consent URL the
// user must visit to connect their account to connectionName. It uses the
// stored access token; the gateway scopes the request to connections the user
// can run and to gcp_oauth-federated connections.
func fetchFederationConsentURL(apiURL, token, tlsCA, connectionName string) (string, error) {
	if connectionName == "" {
		return "", fmt.Errorf("missing connection name")
	}
	endpoint := fmt.Sprintf("%s/api/connections/%s/federation/oauth/authorize",
		apiURL, url.PathEscape(connectionName))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpclient.NewHttpClient(tlsCA).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("authorize endpoint returned status=%d body=%s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("failed decoding authorize response: %w", err)
	}
	if out.URL == "" {
		return "", fmt.Errorf("authorize response did not include a consent url")
	}
	return out.URL, nil
}
