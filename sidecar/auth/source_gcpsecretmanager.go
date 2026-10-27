package auth

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

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
)

// The gcp_secret_manager source reads a pool entry's secret version from
// Secret Manager at login, with the pod's Workload Identity from the
// metadata server as the caller. The pod needs roles/secretmanager.secretAccessor
// on the secret and nothing else. The payload is the password when the
// entry names the user, else a JSON document {"user": ..., "password": ...}.
//
// One HTTPS call with the metadata token, the pod's own.
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

func buildGCPSecretManagerSource(cfg *daemon.CredentialSourcesConfig) (daemon.CredentialSource, error) {
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

// UseRoots installs the process trust pool; BuildCredentialSources calls it
// once the bundle is loaded.
func (g *gcpSecretManagerSource) UseRoots(roots *x509.CertPool) {
	g.client.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
}

func (g *gcpSecretManagerSource) Credential(ctx context.Context, e daemon.CredentialEntry, _ session.Identity) (daemon.Credential, error) {
	if err := daemon.ValidateGCPSecretManagerEntry(e); err != nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	own, err := (&gcpImpersonationSource{metadataHost: g.metadataHost}).ownAccessToken(ctx)
	if err != nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	ctx, cancel := context.WithTimeout(ctx, secretManagerTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secretManagerEndpoint+"/v1/"+e.Secret+":access", nil)
	if err != nil {
		return daemon.Credential{}, err
	}
	req.Header.Set("Authorization", "Bearer "+own)
	req.Header.Set("User-Agent", daemon.UserAgent("credential/gcp_secret_manager"))
	resp, err := g.client.Do(req)
	if err != nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: Secret Manager is unreachable: %w", e.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSecretManagerAnswer))
	if err != nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: reading Secret Manager's answer: %w", e.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		return daemon.Credential{}, fmt.Errorf("credential %q: Secret Manager answered %s for %s: %s", e.Name, resp.Status, e.Secret, daemon.ErrorMessage(raw))
	}
	var ans struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &ans); err != nil || ans.Payload.Data == "" {
		return daemon.Credential{}, fmt.Errorf("credential %q: Secret Manager answered with no payload for %s", e.Name, e.Secret)
	}
	payload, err := base64.StdEncoding.DecodeString(ans.Payload.Data)
	if err != nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: the payload of %s is not base64", e.Name, e.Secret)
	}
	if e.User != "" {
		return daemon.Credential{User: e.User, Password: strings.TrimSpace(string(payload))}, nil
	}
	var doc struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil || doc.User == "" || doc.Password == "" {
		return daemon.Credential{}, fmt.Errorf("credential %q: the payload of %s is not a JSON document with user and password; set user on the entry to read the payload as the password", e.Name, e.Secret)
	}
	return daemon.Credential{User: doc.User, Password: doc.Password}, nil
}
