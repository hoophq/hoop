package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/session"
)

// The gcp_secret_manager source reads a pool entry's secret version from
// Secret Manager at login, with the pod's Workload Identity from the
// metadata server as the caller. The pod needs roles/secretmanager.secretAccessor
// on the secret and nothing else. The payload is the password when the
// entry names the user, else a JSON document {"user": ..., "password": ...}.
//
// One HTTPS call with the metadata token, so it stays in the root module.
const (
	secretManagerHost      = "https://secretmanager.googleapis.com"
	secretManagerTimeout   = 10 * time.Second
	maxSecretManagerAnswer = 1 << 20
)

// secretManagerEndpoint is a var so a test and the compose stack point it
// at a fake.
var secretManagerEndpoint = secretManagerHost

type gcpSecretManagerSource struct {
	metadataHost string
	client       *http.Client
}

func buildGCPSecretManagerSource(cfg *CredentialSourcesConfig) (CredentialSource, error) {
	if cfg == nil || cfg.GCPSecretManager == nil {
		return nil, errors.New("credentials.gcp_secret_manager is not configured")
	}
	return &gcpSecretManagerSource{
		metadataHost: cfg.GCPSecretManager.MetadataHost,
		client: &http.Client{
			Timeout: secretManagerTimeout,
			Transport: &http.Transport{
				Proxy:           http.ProxyFromEnvironment,
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
	}, nil
}

func (g *gcpSecretManagerSource) useRoots(roots *x509.CertPool) {
	g.client.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
}

func (g *gcpSecretManagerSource) validateEntry(e CredentialEntry) error {
	if e.Secret == "" {
		return errors.New("secret is required: projects/<project>/secrets/<name>/versions/<version>")
	}
	parts := strings.Split(e.Secret, "/")
	if len(parts) != 6 || parts[0] != "projects" || parts[2] != "secrets" || parts[4] != "versions" || parts[1] == "" || parts[3] == "" || parts[5] == "" {
		return fmt.Errorf("secret %q is not projects/<project>/secrets/<name>/versions/<version>", e.Secret)
	}
	return nil
}

func (g *gcpSecretManagerSource) Credential(ctx context.Context, e CredentialEntry, _ session.Identity) (Credential, error) {
	if err := g.validateEntry(e); err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	own, err := (&gcpImpersonationSource{metadataHost: g.metadataHost}).ownAccessToken(ctx)
	if err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	ctx, cancel := context.WithTimeout(ctx, secretManagerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secretManagerEndpoint+"/v1/"+e.Secret+":access", nil)
	if err != nil {
		return Credential{}, err
	}
	req.Header.Set("Authorization", "Bearer "+own)
	req.Header.Set("User-Agent", userAgent("credential/gcp_secret_manager"))
	resp, err := g.client.Do(req)
	if err != nil {
		return Credential{}, fmt.Errorf("credential %q: Secret Manager is unreachable: %w", e.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSecretManagerAnswer))
	if err != nil {
		return Credential{}, fmt.Errorf("credential %q: reading Secret Manager's answer: %w", e.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return Credential{}, fmt.Errorf("credential %q: Secret Manager answered %s for %s: %s", e.Name, resp.Status, e.Secret, controlPlaneMessage(raw))
	}
	var ans struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &ans); err != nil || ans.Payload.Data == "" {
		return Credential{}, fmt.Errorf("credential %q: Secret Manager answered with no payload for %s", e.Name, e.Secret)
	}
	payload, err := base64.StdEncoding.DecodeString(ans.Payload.Data)
	if err != nil {
		return Credential{}, fmt.Errorf("credential %q: the payload of %s is not base64", e.Name, e.Secret)
	}
	if e.User != "" {
		return Credential{User: e.User, Password: strings.TrimSpace(string(payload))}, nil
	}
	var doc struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil || doc.User == "" || doc.Password == "" {
		return Credential{}, fmt.Errorf("credential %q: the payload of %s is not a JSON document with user and password; set user on the entry to read the payload as the password", e.Name, e.Secret)
	}
	return Credential{User: doc.User, Password: doc.Password}, nil
}
