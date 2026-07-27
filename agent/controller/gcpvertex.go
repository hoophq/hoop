package controller

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// claudeCodeVertexFlag gates the claude-code → Google Vertex AI federation.
// When off, the agent never mints GCP tokens for a claude-code connection and
// leaves the request headers untouched.
const claudeCodeVertexFlag = "experimental.claude_code_vertex"

// gcpVertexScope is the OAuth scope minted bearers carry. cloud-platform is the
// scope Vertex AI prediction calls require (roles/aiplatform.user).
const gcpVertexScope = "https://www.googleapis.com/auth/cloud-platform"

// gcpVertexBearer returns a valid GCP OAuth access token for the session,
// minted from the connection's service-account key (saJSON).
//
// The oauth2.TokenSource built from the service-account key is cached per
// session in a.gcpTokenSources and reused across every proxied request, so the
// token is minted lazily on first use and transparently refreshed by the oauth2
// library shortly before it expires. This is what lets a long-lived Claude Code
// session keep working past the ~1h token TTL without re-opening the session.
//
// google.CredentialsFromJSON only parses the key (no network I/O); the network
// round-trip to Google's token endpoint happens inside TokenSource.Token().
func (a *Agent) gcpVertexBearer(sessionID, saJSON string) (string, error) {
	ts, ok := a.gcpTokenSources.Load(sessionID)
	if !ok {
		creds, err := google.CredentialsFromJSON(context.Background(), []byte(saJSON), gcpVertexScope)
		if err != nil {
			return "", fmt.Errorf("invalid gcp service account credentials: %w", err)
		}
		// LoadOrStore guards against two concurrent requests for the same
		// session each building a source: the first stored wins and both
		// share its token cache.
		ts, _ = a.gcpTokenSources.LoadOrStore(sessionID, creds.TokenSource)
	}

	tokenSource, isTokenSource := ts.(oauth2.TokenSource)
	if !isTokenSource {
		return "", fmt.Errorf("unexpected cached token source type %T", ts)
	}
	token, err := tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("failed minting gcp access token: %w", err)
	}
	return token.AccessToken, nil
}

// removeHeader deletes a proxy header by name, case-insensitively. The header
// map is built from connection env vars (e.g. "HEADER_X_API_KEY"), so the exact
// casing is whatever was stored; this guards against a stale key surviving when
// the Vertex bearer supersedes a connection's static API-key header.
func removeHeader(headers map[string]string, name string) {
	for k := range headers {
		if strings.EqualFold(k, name) {
			delete(headers, k)
		}
	}
}
