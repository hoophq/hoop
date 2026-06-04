package loginflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// LocalAuth performs a synchronous email+password authentication
// against the gateway's /api/localauth/login endpoint.
//
// The gateway returns the JWT in the `Token` response header (NOT the
// body), per the contract documented in hoophq/hsh:src/auth/local.ts.
// This function discards the body and returns the token string.
//
// Errors:
//   - context cancellation → wrapped error
//   - 401 / 404            → ErrInvalidLocalCredentials so callers can
//                            phrase the message uniformly without
//                            leaking "user not found" vs "wrong
//                            password" distinctions.
//   - other non-2xx        → fmt.Errorf with the gateway message
//                            (decoded from the JSON body if available)
//   - empty Token header   → fmt.Errorf("gateway returned no token")
//
// Implementation note: this function is exposed alongside the OAuth
// state machine because it shares the daemon's gateway-talk surface,
// but it does not need a callback server, a state token, or polling.
// The IPC handler can call it synchronously and respond 204 on success.
type localAuthRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// ErrInvalidLocalCredentials is returned for 401/404 responses from
// /api/localauth/login. UI code uses errors.Is to render "Invalid
// email or password" rather than reproducing the gateway's distinction.
var ErrInvalidLocalCredentials = errors.New("loginflow: invalid email or password")

// LocalAuth posts the credentials to /api/localauth/login on the
// given hoop gateway and returns the bearer token from the `Token`
// response header.
//
// The caller is responsible for persisting the token (same OnSuccess
// callback the OAuth path uses); we keep this function free of
// filesystem coupling so it stays testable with httptest.
func LocalAuth(ctx context.Context, httpClient *http.Client, apiURL, email, password string) (string, error) {
	if apiURL == "" {
		return "", errors.New("loginflow.LocalAuth: apiURL is required")
	}
	if email == "" || password == "" {
		return "", errors.New("loginflow.LocalAuth: email and password are required")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	body, err := json.Marshal(localAuthRequest{Email: email, Password: password})
	if err != nil {
		return "", fmt.Errorf("loginflow.LocalAuth: marshal body: %w", err)
	}
	endpoint := strings.TrimRight(apiURL, "/") + "/api/localauth/login"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("loginflow.LocalAuth: POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound {
		// Drain so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		return "", ErrInvalidLocalCredentials
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		// Try to decode the documented {"message": "..."} shape; fall
		// back to the raw bytes so the operator still sees what
		// happened.
		var doc struct {
			Message string `json:"message"`
		}
		if jsonErr := json.Unmarshal(raw, &doc); jsonErr == nil && doc.Message != "" {
			return "", fmt.Errorf("gateway: %s (HTTP %d)", doc.Message, resp.StatusCode)
		}
		return "", fmt.Errorf("gateway returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	token := strings.TrimSpace(resp.Header.Get("Token"))
	if token == "" {
		return "", errors.New("loginflow.LocalAuth: gateway 2xx but Token header was empty")
	}
	// Drain the body for connection reuse; the body itself is
	// `{"status":"ok"}` and carries no information we don't already
	// have from the header.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	return token, nil
}
