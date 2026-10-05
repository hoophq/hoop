package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/analytics"
)

// A sidecar proves who it is to the control plane with exactly one of three
// credentials:
//
//   - the token the plane issued when the sidecar was registered;
//   - a Kubernetes projected service account token, read from a file;
//   - a Google ID token from the GCE/GKE metadata server.
//
// The last two are identities the platform already issued. The plane maps
// them to a sidecar row through an allowlist entry, so a fleet of workspaces
// needs no token per sidecar and a new pod lands on the row its name maps to.
const (
	// SidecarIdentityHeader carries a platform-issued JWT, raw, with no
	// "Bearer " prefix. Exported because the gateway reads what this sets:
	// one constant in the module that sends it, the same reason
	// LicenseManagedHeader is one.
	SidecarIdentityHeader = "hoop-sidecar-identity"

	// SidecarIdentityTokenFileEnv names a file holding the JWT. It is a
	// path, unlike SidecarTokenEnv: the kubelet rotates a projected token in
	// place, so the file is re-read on every request and a value copied
	// into the environment would expire under the running process.
	SidecarIdentityTokenFileEnv = "HOOP_SIDECAR_IDENTITY_TOKEN_FILE"
	// SidecarIdentityGCPEnv, when true, fetches a Google ID token for the
	// node's or the pod's (Workload Identity) service account from the
	// metadata server.
	SidecarIdentityGCPEnv = "HOOP_SIDECAR_IDENTITY_GCP"
	// SidecarIdentityAudienceEnv is the Google ID token's audience,
	// default the control plane URL. A plane shared by several
	// organizations needs one audience per organization, because the plane
	// maps an (issuer, audience) pair to one organization. Only the GCP
	// source reads it: a projected token's audience is fixed where the
	// token is minted.
	SidecarIdentityAudienceEnv = "HOOP_SIDECAR_IDENTITY_AUDIENCE"

	// gcpMetadataHostEnv is the override the Google client libraries
	// honor. Reading the same name means an operator who already points
	// those at an emulator or a proxy points this process there too.
	gcpMetadataHostEnv = "GCE_METADATA_HOST"

	// maxIdentityToken bounds a JWT read from a file or the metadata
	// server. A service account token is around 1 KiB; anything near this
	// is the wrong file, and sending it would put an arbitrary file's bytes
	// in a request header.
	maxIdentityToken = 16 << 10

	// gcpTokenRefreshBefore is how long before its exp a cached Google ID
	// token is replaced. Wide enough that a token never expires between
	// the check and the plane's verification, clock skew included.
	gcpTokenRefreshBefore = 5 * time.Minute
	gcpMetadataTimeout    = 10 * time.Second
)

// gcpMetadataHost is where the metadata server answers on GCE and GKE. A
// var so a test can point it at a fake without setting the environment.
var gcpMetadataHost = "metadata.google.internal"

// gcpMetadataClient never uses HTTP_PROXY: the metadata server answers only
// on the node's link-local network, so a proxy in the environment would turn
// every token fetch into a failure. The Google client libraries bypass
// proxies for the same reason. It never follows a redirect either, because
// the server never sends one.
var gcpMetadataClient = &http.Client{
	Transport: &http.Transport{Proxy: nil},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// credential is what one control plane request presents. present is called
// per request, never once at startup, because two of the three sources
// rotate under a running process.
type credential interface {
	present(ctx context.Context) (header, value string, err error)
}

// tokenCredential is the token the plane issued at registration. It never
// changes for the life of the process.
type tokenCredential string

func (t tokenCredential) present(context.Context) (string, string, error) {
	return sidecarTokenHeader, string(t), nil
}

// tokenFileCredential reads a projected service account token. The kubelet
// replaces the file before the token expires, so reading it once would work
// for an hour and then fail every heartbeat until a restart.
type tokenFileCredential struct {
	path string
}

func (f tokenFileCredential) present(context.Context) (string, string, error) {
	fh, err := os.Open(f.path)
	if err != nil {
		return "", "", fmt.Errorf("reading the service account token (%s): %w", SidecarIdentityTokenFileEnv, err)
	}
	// Read-only file, fully read below; a close error carries nothing.
	defer func() { _ = fh.Close() }()
	raw, err := io.ReadAll(io.LimitReader(fh, maxIdentityToken+1))
	if err != nil {
		return "", "", fmt.Errorf("reading the service account token (%s): %w", SidecarIdentityTokenFileEnv, err)
	}
	if len(raw) > maxIdentityToken {
		return "", "", fmt.Errorf("%s names %s, which holds more than %d bytes; "+
			"a service account token is about 1 KiB, so this is not one",
			SidecarIdentityTokenFileEnv, f.path, maxIdentityToken)
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return "", "", fmt.Errorf("%s names %s, which is empty; "+
			"check the projected serviceAccountToken volume", SidecarIdentityTokenFileEnv, f.path)
	}
	return SidecarIdentityHeader, tok, nil
}

// gcpCredential fetches a Google ID token for audience, the control plane
// URL unless HOOP_SIDECAR_IDENTITY_AUDIENCE overrides it. The token is
// cached: the metadata server mints a new one per call, and one per review
// request would put it on the data path.
type gcpCredential struct {
	audience string

	mu    sync.Mutex
	token string
	exp   time.Time
}

func (g *gcpCredential) present(ctx context.Context) (string, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Now().UTC().Before(g.exp.Add(-gcpTokenRefreshBefore)) {
		return SidecarIdentityHeader, g.token, nil
	}
	tok, exp, err := fetchGCPIdentityToken(ctx, g.audience)
	if err != nil {
		return "", "", err
	}
	g.token, g.exp = tok, exp
	return SidecarIdentityHeader, tok, nil
}

// fetchGCPIdentityToken asks the metadata server for an ID token. format=full
// puts the instance and project in the claims, which the plane does not need
// but Workload Identity tokens carry anyway; it keeps the two token shapes
// one.
func fetchGCPIdentityToken(ctx context.Context, audience string) (string, time.Time, error) {
	host := os.Getenv(gcpMetadataHostEnv)
	if host == "" {
		host = gcpMetadataHost
	}
	u := url.URL{
		Scheme:   "http",
		Host:     host,
		Path:     "/computeMetadata/v1/instance/service-accounts/default/identity",
		RawQuery: url.Values{"audience": {audience}, "format": {"full"}}.Encode(),
	}
	ctx, cancel := context.WithTimeout(ctx, gcpMetadataTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("metadata server request: %w", err)
	}
	// Without it the server refuses the call: it is the server's guard
	// against a request forged through an SSRF.
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := gcpMetadataClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%s is set but the GCP metadata server at %s is unreachable "+
			"(this process must run on GCE or GKE): %w", SidecarIdentityGCPEnv, host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxIdentityToken+1))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reading the GCP metadata server's answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("the GCP metadata server at %s answered %s for an ID token: %s",
			host, resp.Status, controlPlaneMessage(raw))
	}
	if len(raw) > maxIdentityToken {
		return "", time.Time{}, fmt.Errorf("the GCP metadata server at %s answered more than %d bytes "+
			"for an ID token", host, maxIdentityToken)
	}
	tok := string(bytes.TrimSpace(raw))
	claims, err := jwtClaimsUnverified(tok)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("the GCP metadata server at %s answered with no usable ID token: %w", host, err)
	}
	// The cache keys on exp. A token without one cannot be cached safely,
	// and Google always sets it, so its absence means this is not Google.
	if claims.Exp <= 0 {
		return "", time.Time{}, fmt.Errorf("the GCP metadata server at %s answered with an ID token "+
			"that has no exp", host)
	}
	return tok, time.Unix(int64(claims.Exp), 0), nil
}

// jwtClaims are the payload fields this process reads. It never verifies
// them: the plane does, and these only name the identity in a message, key
// the analytics id and time the cache.
type jwtClaims struct {
	Iss   string  `json:"iss"`
	Sub   string  `json:"sub"`
	Email string  `json:"email"`
	Exp   float64 `json:"exp"`
}

// subject names the identity the way an allowlist entry matches it: email
// for Google, whose sub is an opaque number, sub for everything else.
func (c jwtClaims) subject() string {
	if c.Email != "" {
		return c.Email
	}
	return c.Sub
}

// jwtClaimsUnverified decodes a JWT's payload without checking the
// signature.
func jwtClaimsUnverified(tok string) (jwtClaims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return jwtClaims{}, errors.New("not a JWT (want three dot-separated parts)")
	}
	// RawURLEncoding is what RFC 7515 mandates; trimming padding accepts
	// an issuer that pads anyway.
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return jwtClaims{}, fmt.Errorf("the JWT payload is not base64url: %w", err)
	}
	var c jwtClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return jwtClaims{}, fmt.Errorf("the JWT payload is not JSON: %w", err)
	}
	return c, nil
}

// resolveCredential picks the one credential source that is set. It returns
// a nil credential when none is, and refuses two: precedence between a token
// and an identity would decide which sidecar row this process becomes, and
// no operator setting both meant the one that loses.
//
// The token flag outranking HOOP_SIDECAR_TOKEN is not two sources: it is
// the one token, set from two places, as it always was.
//
// planeURL is the Google ID token's audience unless
// HOOP_SIDECAR_IDENTITY_AUDIENCE is set. That env var with any other source,
// or with none, is an error: it would do nothing, and the operator who set
// it expects the plane to see that audience.
func resolveCredential(tokenFlag, planeURL string) (credential, string, error) {
	var set []string
	token, tokenSource := resolveSidecarToken(tokenFlag)
	if token != "" {
		set = append(set, tokenSource)
	}
	file := os.Getenv(SidecarIdentityTokenFileEnv)
	if file != "" {
		set = append(set, SidecarIdentityTokenFileEnv)
	}
	gcp := false
	if v := os.Getenv(SidecarIdentityGCPEnv); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, "", fmt.Errorf("%s holds %q, which is not a boolean (true or false)", SidecarIdentityGCPEnv, v)
		}
		if b {
			gcp = true
			set = append(set, SidecarIdentityGCPEnv)
		}
	}
	audience := os.Getenv(SidecarIdentityAudienceEnv)
	switch {
	case len(set) > 1:
		return nil, "", fmt.Errorf("more than one control plane credential is set (%s); set exactly one",
			strings.Join(set, ", "))
	case audience != "" && !gcp:
		other := "no credential"
		if len(set) == 1 {
			other = set[0]
		}
		return nil, "", fmt.Errorf("%s is set with %s; it is the Google ID token's audience and only %s=true uses it. "+
			"A projected service account token's audience is set where the token is minted "+
			"(the serviceAccountToken volume's audience)", SidecarIdentityAudienceEnv, other, SidecarIdentityGCPEnv)
	case token != "":
		return tokenCredential(token), tokenSource, nil
	case file != "":
		return tokenFileCredential{path: file}, SidecarIdentityTokenFileEnv, nil
	case gcp:
		if audience == "" {
			audience = planeURL
		}
		return &gcpCredential{audience: audience}, SidecarIdentityGCPEnv, nil
	}
	return nil, "", nil
}

// identityRejected words a 401 that answered a service account identity.
// The token's own subject goes in the message, decoded unverified, because
// the fix is usually an allowlist entry and the operator needs the string it
// has to match.
//
// A plane that knows identities says why it refused (no entry for the issuer,
// a subject no pattern allows, a deleted sidecar), and that reason is the
// whole message: an allowlist hint beside "this sidecar was deleted" sends
// the operator to the wrong screen. Only a vague answer gets the hint, and
// "access denied" is what a plane predating identities says to a request
// without a token.
func identityRejected(planeURL, jwt string, raw []byte) error {
	who := "the presented identity"
	if c, err := jwtClaimsUnverified(jwt); err == nil {
		who = fmt.Sprintf("issuer %q, subject %q", c.Iss, c.subject())
	}
	reason := controlPlaneMessage(raw)
	if reason == "" || reason == "access denied" {
		reason = fmt.Sprintf("%q; check the sidecar service account allowlist entry's issuer, audience "+
			"and subject pattern. A control plane without service account support also answers this way",
			reason)
	}
	return fmt.Errorf("the control plane at %s rejected the service account identity (%s): %s",
		planeURL, who, reason)
}

// analyticsID is the install's sidecar-id under this plane, or "" when the
// credential yields none and a host-derived id has to stand in.
//
// The token path hashes the token, as it always did, so an upgrade keeps
// every install's Segment profile. An identity token is never hashed: it
// rotates hourly, and a fresh id per rotation is a new billed profile. The
// plane URL, issuer and subject are what select the sidecar row, so they
// are what stays fixed for one install across restarts and rollouts.
func (cp *controlPlane) analyticsID() string {
	header, value, err := cp.cred.present(context.Background())
	if err != nil {
		return ""
	}
	if header == sidecarTokenHeader {
		return analytics.IDFromToken(value)
	}
	c, err := jwtClaimsUnverified(value)
	if err != nil || c.Iss == "" || c.subject() == "" {
		return ""
	}
	return analytics.IDFromToken(cp.url + "\x00" + c.Iss + "\x00" + c.subject())
}
