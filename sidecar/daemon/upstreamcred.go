package daemon

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/upstreamcred"
)

// UpstreamCredentialConfig is the upstream_credential block on an http
// listener: the lane sets one header on every request it forwards, with a
// token read from a file on the sidecar host, in place of whatever the
// client sent in that header.
//
// It is for an agent that must reach an API without holding the API's
// token: the agent holds only the caller token, and the token the upstream
// reads stays on the sidecar host. upstreamcred holds the guarantees.
type UpstreamCredentialConfig struct {
	// Header is the header the upstream reads its credential from.
	Header string `json:"header,omitempty" label:"Header" placeholder:"Authorization" help:"The header the upstream reads its credential from. The sidecar removes the client's copy and sets its own." ui:"required"`

	// Scheme is written before the token, with one space: Bearer gives
	// Authorization: Bearer <token>. Empty sends the token alone, as an
	// X-API-Key header wants it.
	Scheme string `json:"scheme,omitempty" label:"Scheme" placeholder:"Bearer" help:"Written before the token. Empty sends the token alone."`

	// File is a path on the sidecar host holding the token alone, readable
	// by its owner only. It is read at startup and re-read when it changes,
	// so the token rotates without a restart. The control plane never reads
	// it.
	File string `json:"file,omitempty" label:"Token file" placeholder:"/etc/hoop-inspect/upstream-token" help:"A path on the sidecar host, mode 0600, holding the token. Re-read when it changes." ui:"required"`
}

func (c *UpstreamCredentialConfig) spec() upstreamcred.Spec {
	return upstreamcred.Spec{Header: c.Header, Scheme: c.Scheme, File: c.File}
}

// validate checks the block against its lane. Like CallerTokenConfig's, it
// never reads the file: the plane validates without it. openUpstreamCredentials
// reads it where the lane is built.
func (c *UpstreamCredentialConfig) validate(name string, l ListenerConfig) []string {
	var problems []string
	for _, p := range c.spec().Check() {
		problems = append(problems, fmt.Sprintf("%s: upstream_credential.%s", name, p))
	}
	// An empty header is one of Check's problems; nothing below applies.
	h := strings.ToLower(strings.TrimSpace(c.Header))
	switch {
	case h == "":
	case h == l.credentialHeader():
		// The codec lifts the caller's credential from the bytes the
		// filter forwards, so it would hand the lane's own token to the
		// identity resolver: under google_identity, to Google.
		problems = append(problems, fmt.Sprintf(
			"%s: upstream_credential.header %q is the header this listener reads the caller from; the two cannot share one",
			name, c.Header))
	case analyzer.IsClientModeHeader(h):
		problems = append(problems, fmt.Sprintf(
			"%s: upstream_credential.header %q is a header the sidecar reads from the client; pick another", name, c.Header))
	case l.HTTP.captures(h):
		// The codec would keep it in the captured headers, and those
		// reach the audit trail, OPA input and the analyzer's prompt.
		problems = append(problems, fmt.Sprintf(
			"%s: http.headers lists %q, the upstream_credential header, so the token would reach the audit trail, policy and the analyzer; remove it from http.headers",
			name, h))
	}
	if l.origin() == nil {
		problems = append(problems, fmt.Sprintf(
			"%s: upstream_credential needs upstream written as a URL, such as https://api.example.com: a host:port upstream forwards the Host the client wrote, so the credential would go wherever that Host routes",
			name))
	}
	return problems
}

// openUpstreamCredentials reads the token file of every upstream_credential
// lane and hands the credential to the lane. An error names the lane and the
// file, and stops startup (Run) or the -validate report (Validate): a lane
// that promises its upstream a credential must not forward without one.
func openUpstreamCredentials(lanes []lane, log *slog.Logger) ([]*upstreamcred.Credential, error) {
	var creds []*upstreamcred.Credential
	for i := range lanes {
		uc := lanes[i].cfg.UpstreamCredential
		if uc == nil {
			continue
		}
		var clog *slog.Logger
		if log != nil {
			clog = log.With("listener", lanes[i].name)
		}
		c, err := upstreamcred.Open(uc.spec(), clog)
		if err != nil {
			// Open names the field first: "file: open /x: ...".
			return nil, fmt.Errorf("%s: upstream_credential.%w", lanes[i].name, err)
		}
		lanes[i].upstreamCred = c
		creds = append(creds, c)
	}
	return creds, nil
}
