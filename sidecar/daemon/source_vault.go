package daemon

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/session"
)

// The vault source reads a pool entry's path from HashiCorp Vault at login.
// A KV path yields a user and a password the platform rotates in Vault; a
// database engine path yields a user Vault creates for this session, which
// the sidecar revokes when the session ends. The sidecar logs in to Vault
// with the pod's service account token or a token file, never with a token
// in the config: a Vault token in a committed file would be the secret the
// source exists to keep out of it.
//
// Plain HTTPS and JSON, so it stays in the root module.
const (
	VaultAuthKubernetes = "kubernetes"
	VaultAuthTokenFile  = "token_file"

	vaultTimeout      = 10 * time.Second
	maxVaultAnswer    = 1 << 20
	maxVaultTokenFile = 64 << 10

	// vaultTokenRefreshAt is the fraction of a kubernetes-login token's
	// lifetime after which the next login logs in again, so a token never
	// expires under a request.
	vaultTokenRefreshAt = 0.8
)

type vaultSource struct {
	addr      string
	auth      VaultAuthConfig
	namespace string
	client    *http.Client
	caFile    string
	now       func() time.Time

	mu       sync.Mutex
	token    string
	tokenExp time.Time // zero for a token file, re-read per call
}

func (v *VaultSourceConfig) validate() []string {
	var problems []string
	if v.Addr == "" {
		problems = append(problems, "addr is required")
	} else if u, err := url.Parse(v.Addr); err != nil || u.Scheme != "https" || u.Host == "" {
		problems = append(problems, fmt.Sprintf("addr %q is not an https URL", v.Addr))
	}
	switch v.Auth.Type {
	case VaultAuthKubernetes:
		if v.Auth.Role == "" {
			problems = append(problems, "auth.role is required with the kubernetes method")
		}
	case VaultAuthTokenFile:
		if v.Auth.TokenFile == "" {
			problems = append(problems, "auth.token_file is required with the token_file method")
		}
		if v.Auth.Role != "" || v.Auth.Mount != "" {
			problems = append(problems, "auth.role and auth.mount are kubernetes settings, not token_file ones")
		}
	case "":
		problems = append(problems, "auth.type is required: kubernetes or token_file")
	default:
		problems = append(problems, fmt.Sprintf("auth.type %q is not kubernetes or token_file", v.Auth.Type))
	}
	return problems
}

func buildVaultSource(cfg *CredentialSourcesConfig) (CredentialSource, error) {
	if cfg == nil || cfg.Vault == nil {
		return nil, errors.New("credentials.vault is not configured")
	}
	if p := cfg.Vault.validate(); len(p) > 0 {
		return nil, errors.New(strings.Join(p, "; "))
	}
	return &vaultSource{
		addr:      strings.TrimRight(cfg.Vault.Addr, "/"),
		auth:      cfg.Vault.Auth,
		namespace: cfg.Vault.Namespace,
		caFile:    cfg.Vault.CAFile,
		now:       time.Now,
		client: &http.Client{
			Timeout: vaultTimeout,
			Transport: &http.Transport{
				Proxy:           http.ProxyFromEnvironment,
				TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			},
		},
	}, nil
}

// useRoots installs the process trust pool, plus ca_file when set. Called
// by buildLanes once the bundle is loaded; the file is read then, not at
// Validate, which runs on the control plane too.
func (v *vaultSource) useRoots(roots *x509.CertPool) {
	pool := roots
	if v.caFile != "" {
		if pem, err := os.ReadFile(v.caFile); err == nil {
			if pool == nil {
				pool = x509.NewCertPool()
			} else {
				pool = pool.Clone()
			}
			pool.AppendCertsFromPEM(pem)
		}
	}
	v.client.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool
}

func (v *vaultSource) validateEntry(e CredentialEntry) error {
	if e.Path == "" {
		return errors.New("path is required: the Vault API path to read")
	}
	if strings.HasPrefix(e.Path, "/") || strings.HasPrefix(e.Path, "v1/") {
		return fmt.Errorf("path %q is relative to the API root; write secret/data/<name>, not /v1/...", e.Path)
	}
	if vaultIsDynamic(e.Path) && (e.User != "" || e.UserKey != "" || e.PasswordKey != "") {
		return errors.New("a database engine path names its own user and password; user, user_key and password_key are KV settings")
	}
	return nil
}

// vaultIsDynamic reports a database secrets engine path, whose answer is a
// leased user rather than a stored secret.
func vaultIsDynamic(path string) bool {
	return strings.Contains("/"+path, "/creds/")
}

func (v *vaultSource) Credential(ctx context.Context, e CredentialEntry, _ session.Identity) (Credential, error) {
	if err := v.validateEntry(e); err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	token, err := v.vaultToken(ctx)
	if err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	var ans struct {
		LeaseID       string          `json:"lease_id"`
		LeaseDuration int             `json:"lease_duration"`
		Data          json.RawMessage `json:"data"`
	}
	if err := v.call(ctx, token, http.MethodGet, "v1/"+e.Path, nil, &ans); err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	if vaultIsDynamic(e.Path) {
		var d struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.Unmarshal(ans.Data, &d); err != nil || d.Username == "" || d.Password == "" {
			return Credential{}, fmt.Errorf("credential %q: %s answered with no username and password", e.Name, e.Path)
		}
		lease := ans.LeaseID
		return Credential{User: d.Username, Password: d.Password, Release: func(ctx context.Context) error {
			if lease == "" {
				return nil
			}
			return v.revoke(ctx, lease)
		}}, nil
	}
	data, err := vaultKVData(ans.Data)
	if err != nil {
		return Credential{}, fmt.Errorf("credential %q: %s: %w", e.Name, e.Path, err)
	}
	userKey, passwordKey := e.UserKey, e.PasswordKey
	if userKey == "" {
		userKey = "user"
	}
	if passwordKey == "" {
		passwordKey = "password"
	}
	password, ok := data[passwordKey]
	if !ok || password == "" {
		return Credential{}, fmt.Errorf("credential %q: %s has no %q key", e.Name, e.Path, passwordKey)
	}
	user := e.User
	if user == "" {
		user = data[userKey]
	}
	return Credential{User: user, Password: password}, nil
}

// vaultKVData unwraps a KV answer: v2 nests the secret under data.data
// beside metadata, v1 is the secret itself. Values are strings; anything
// else is refused rather than stringified.
func vaultKVData(raw json.RawMessage) (map[string]string, error) {
	var v2 struct {
		Data     map[string]json.RawMessage `json:"data"`
		Metadata json.RawMessage            `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &v2); err == nil && v2.Data != nil && v2.Metadata != nil {
		return stringValues(v2.Data)
	}
	var v1 map[string]json.RawMessage
	if err := json.Unmarshal(raw, &v1); err != nil || v1 == nil {
		return nil, errors.New("the answer holds no data")
	}
	return stringValues(v1)
}

func stringValues(m map[string]json.RawMessage) (map[string]string, error) {
	out := make(map[string]string, len(m))
	for k, raw := range m {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("key %q is not a string", k)
		}
		out[k] = s
	}
	return out, nil
}

// vaultToken is the token the source calls Vault with. token_file is
// re-read per call, so a platform that rotates the file is followed. The
// kubernetes method logs in once and again at 80% of the token's lifetime.
func (v *vaultSource) vaultToken(ctx context.Context) (string, error) {
	if v.auth.Type == VaultAuthTokenFile {
		raw, err := readSmallFile(v.auth.TokenFile, maxVaultTokenFile)
		if err != nil {
			return "", fmt.Errorf("vault token file: %w", err)
		}
		return raw, nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.token != "" && v.now().Before(v.tokenExp) {
		return v.token, nil
	}
	jwtFile := v.auth.TokenFile
	if jwtFile == "" {
		jwtFile = kubernetesTokenFile
	}
	jwt, err := readSmallFile(jwtFile, maxVaultTokenFile)
	if err != nil {
		return "", fmt.Errorf("service account token for the vault login: %w", err)
	}
	mount := v.auth.Mount
	if mount == "" {
		mount = "kubernetes"
	}
	body, _ := json.Marshal(map[string]string{"role": v.auth.Role, "jwt": jwt})
	var ans struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := v.call(ctx, "", http.MethodPost, "v1/auth/"+strings.Trim(mount, "/")+"/login", body, &ans); err != nil {
		return "", fmt.Errorf("vault login as role %q: %w", v.auth.Role, err)
	}
	if ans.Auth.ClientToken == "" {
		return "", fmt.Errorf("vault login as role %q answered with no token", v.auth.Role)
	}
	ttl := time.Duration(ans.Auth.LeaseDuration) * time.Second
	if ttl <= 0 {
		ttl = time.Hour
	}
	v.token = ans.Auth.ClientToken
	v.tokenExp = v.now().Add(time.Duration(float64(ttl) * vaultTokenRefreshAt))
	return v.token, nil
}

func (v *vaultSource) revoke(ctx context.Context, lease string) error {
	token, err := v.vaultToken(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"lease_id": lease})
	return v.call(ctx, token, http.MethodPut, "v1/sys/leases/revoke", body, nil)
}

// call is one Vault API request. A non-2xx answer names the status and
// Vault's own error list; the body never reaches a log beyond that.
func (v *vaultSource) call(ctx context.Context, token, method, path string, body []byte, into any) error {
	ctx, cancel := context.WithTimeout(ctx, vaultTimeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, v.addr+"/"+path, rd)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}
	if v.namespace != "" {
		req.Header.Set("X-Vault-Namespace", v.namespace)
	}
	req.Header.Set("User-Agent", userAgent("credential/vault"))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("vault at %s is unreachable: %w", v.addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxVaultAnswer))
	if err != nil {
		return fmt.Errorf("reading vault's answer: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		var e struct {
			Errors []string `json:"errors"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("vault answered %s for %s: %s", resp.Status, path, strings.Join(e.Errors, "; "))
	}
	if into == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("vault's answer for %s is not JSON: %w", path, err)
	}
	return nil
}

// readSmallFile reads a token file, trimmed, refusing an empty or oversized
// one so a mount that is not what the operator thinks fails with a reason.
func readSmallFile(path string, limit int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return "", err
	}
	if int64(len(raw)) > limit {
		return "", fmt.Errorf("%s is over %d bytes", path, limit)
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return s, nil
}
