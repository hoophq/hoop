package auth

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
)

// fakeVault answers the three calls the source makes: the kubernetes login,
// a read, and a lease revoke. It records what it saw.
type fakeVault struct {
	srv     *httptest.Server
	logins  atomic.Int64
	revoked atomic.Value // last lease id
	ttl     int
}

func startVault(t *testing.T) *fakeVault {
	t.Helper()
	f := &fakeVault{ttl: 3600}
	f.revoked.Store("")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/auth/kubernetes/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Role, JWT string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Role != "hoop-sidecar" || body.JWT != "sa-token" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string][]string{"errors": {"permission denied"}})
			return
		}
		f.logins.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"auth": map[string]any{"client_token": "hvs.test", "lease_duration": f.ttl}})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Vault-Token") != "hvs.test" && r.Header.Get("X-Vault-Token") != "hvs.file" {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string][]string{"errors": {"permission denied"}})
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /v1/secret/data/appdb", authed(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"data":     map[string]string{"user": "app_rw", "password": "pw-v2"},
			"metadata": map[string]any{"version": 3},
		}})
	}))
	mux.HandleFunc("GET /v1/kv1/appdb", authed(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"username": "app_ro", "pw": "pw-v1"}})
	}))
	mux.HandleFunc("GET /v1/database/creds/appdb-rw", authed(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"lease_id": "database/creds/appdb-rw/abc", "lease_duration": 300,
			"data": map[string]string{"username": "v-k8s-appdb-rw-xyz", "password": "dyn-pw"}})
	}))
	mux.HandleFunc("PUT /v1/sys/leases/revoke", authed(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			LeaseID string `json:"lease_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.revoked.Store(body.LeaseID)
		w.WriteHeader(http.StatusNoContent)
	}))
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func vaultSourceFor(t *testing.T, f *fakeVault, auth daemon.VaultAuthConfig) *vaultSource {
	t.Helper()
	src, err := buildVaultSource(&daemon.CredentialSourcesConfig{Vault: &daemon.VaultSourceConfig{Addr: f.srv.URL, Auth: auth}})
	if err != nil {
		t.Fatal(err)
	}
	v := src.(*vaultSource)
	pool := x509.NewCertPool()
	pool.AddCert(f.srv.Certificate())
	v.UseRoots(pool)
	return v
}

func writeToken(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The kubernetes method logs in once with the service account token and
// reuses the Vault token until 80% of its lifetime, then logs in again.
func TestVaultSourceKubernetesLoginIsCachedThenRenewed(t *testing.T) {
	f := startVault(t)
	v := vaultSourceFor(t, f, daemon.VaultAuthConfig{Type: daemon.VaultAuthKubernetes, Role: "hoop-sidecar", TokenFile: writeToken(t, "sa-token")})
	now := time.Now()
	v.now = func() time.Time { return now }
	entry := daemon.CredentialEntry{Name: "rw", Source: daemon.SourceVault, Path: "secret/data/appdb"}
	for i := 0; i < 3; i++ {
		c, err := v.Credential(context.Background(), entry, session.Identity{})
		if err != nil {
			t.Fatal(err)
		}
		if c.User != "app_rw" || c.Password != "pw-v2" || c.Release != nil {
			t.Errorf("credential %+v", c)
		}
	}
	if n := f.logins.Load(); n != 1 {
		t.Errorf("logged in %d times, want 1", n)
	}
	now = now.Add(time.Duration(float64(3600*time.Second) * 0.81))
	if _, err := v.Credential(context.Background(), entry, session.Identity{}); err != nil {
		t.Fatal(err)
	}
	if n := f.logins.Load(); n != 2 {
		t.Errorf("logged in %d times after the token aged, want 2", n)
	}
}

// A KV v1 path, custom keys, and a literal user that overrides the key.
func TestVaultSourceReadsKVv1WithCustomKeys(t *testing.T) {
	f := startVault(t)
	v := vaultSourceFor(t, f, daemon.VaultAuthConfig{Type: daemon.VaultAuthTokenFile, TokenFile: writeToken(t, "hvs.file")})
	c, err := v.Credential(context.Background(), daemon.CredentialEntry{Name: "ro", Source: daemon.SourceVault, Path: "kv1/appdb", UserKey: "username", PasswordKey: "pw"}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "app_ro" || c.Password != "pw-v1" {
		t.Errorf("credential %+v", c)
	}
	c, err = v.Credential(context.Background(), daemon.CredentialEntry{Name: "ro", Source: daemon.SourceVault, Path: "kv1/appdb", User: "literal", PasswordKey: "pw"}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "literal" {
		t.Errorf("user %q, want the literal", c.User)
	}
	_, err = v.Credential(context.Background(), daemon.CredentialEntry{Name: "ro", Source: daemon.SourceVault, Path: "kv1/appdb"}, session.Identity{})
	if err == nil || !strings.Contains(err.Error(), `no "password" key`) {
		t.Errorf("default password key missing, got %v", err)
	}
}

// A database engine path yields a leased user, and Release revokes the lease.
func TestVaultSourceDynamicCredentialIsRevokedOnRelease(t *testing.T) {
	f := startVault(t)
	v := vaultSourceFor(t, f, daemon.VaultAuthConfig{Type: daemon.VaultAuthTokenFile, TokenFile: writeToken(t, "hvs.file")})
	c, err := v.Credential(context.Background(), daemon.CredentialEntry{Name: "tmp", Source: daemon.SourceVault, Path: "database/creds/appdb-rw"}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "v-k8s-appdb-rw-xyz" || c.Password != "dyn-pw" || c.Release == nil {
		t.Fatalf("credential %+v", c)
	}
	if err := c.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.revoked.Load().(string); got != "database/creds/appdb-rw/abc" {
		t.Errorf("revoked %q", got)
	}
}

// Vault's refusals and config mistakes name the problem, never the token.
func TestVaultSourceErrors(t *testing.T) {
	f := startVault(t)
	v := vaultSourceFor(t, f, daemon.VaultAuthConfig{Type: daemon.VaultAuthKubernetes, Role: "wrong", TokenFile: writeToken(t, "sa-token")})
	_, err := v.Credential(context.Background(), daemon.CredentialEntry{Name: "rw", Source: daemon.SourceVault, Path: "secret/data/appdb"}, session.Identity{})
	if err == nil || !strings.Contains(err.Error(), "permission denied") || strings.Contains(err.Error(), "sa-token") {
		t.Errorf("got %v", err)
	}
	v = vaultSourceFor(t, f, daemon.VaultAuthConfig{Type: daemon.VaultAuthTokenFile, TokenFile: writeToken(t, "hvs.file")})
	_, err = v.Credential(context.Background(), daemon.CredentialEntry{Name: "rw", Source: daemon.SourceVault, Path: "secret/data/missing"}, session.Identity{})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing path: %v", err)
	}
	// A bad entry is refused before any call, with the plane's wording.
	if _, err := v.Credential(context.Background(), daemon.CredentialEntry{Name: "x", Source: daemon.SourceVault, Path: "/v1/secret/data/appdb"}, session.Identity{}); err == nil || !strings.Contains(err.Error(), `credential "x"`) {
		t.Errorf("an absolute path: %v", err)
	}
	if _, err := buildVaultSource(&daemon.CredentialSourcesConfig{Vault: &daemon.VaultSourceConfig{Addr: "http://vault"}}); err == nil {
		t.Error("a plain-http block built")
	}
}

// The Secret Manager source reads the payload as the password with a
// literal user, or as a user/password document without one.
func TestGCPSecretManagerSource(t *testing.T) {
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Metadata-Flavor") != "Google" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.own", "expires_in": 3600})
	}))
	t.Cleanup(meta.Close)
	sm := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ya29.own" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload string
		switch r.URL.Path {
		case "/v1/projects/acme/secrets/appdb-ro/versions/latest:access":
			payload = "ro-pw\n"
		case "/v1/projects/acme/secrets/appdb-rw/versions/7:access":
			payload = `{"user":"app_rw","password":"rw-pw"}`
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"payload": map[string]string{"data": base64.StdEncoding.EncodeToString([]byte(payload))}})
	}))
	t.Cleanup(sm.Close)
	old := secretManagerEndpoint
	secretManagerEndpoint = sm.URL
	t.Cleanup(func() { secretManagerEndpoint = old })

	src, err := buildGCPSecretManagerSource(&daemon.CredentialSourcesConfig{GCPSecretManager: &daemon.GCPSecretManagerSourceConfig{MetadataHost: strings.TrimPrefix(meta.URL, "http://")}})
	if err != nil {
		t.Fatal(err)
	}
	g := src.(*gcpSecretManagerSource)
	pool := x509.NewCertPool()
	pool.AddCert(sm.Certificate())
	g.UseRoots(pool)

	c, err := g.Credential(context.Background(), daemon.CredentialEntry{Name: "ro", Source: daemon.SourceGCPSecretManager, Secret: "projects/acme/secrets/appdb-ro/versions/latest", User: "app_ro"}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "app_ro" || c.Password != "ro-pw" {
		t.Errorf("credential %+v", c)
	}
	c, err = g.Credential(context.Background(), daemon.CredentialEntry{Name: "rw", Source: daemon.SourceGCPSecretManager, Secret: "projects/acme/secrets/appdb-rw/versions/7"}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if c.User != "app_rw" || c.Password != "rw-pw" {
		t.Errorf("credential %+v", c)
	}
	_, err = g.Credential(context.Background(), daemon.CredentialEntry{Name: "ro", Source: daemon.SourceGCPSecretManager, Secret: "projects/acme/secrets/appdb-ro/versions/latest"}, session.Identity{})
	if err == nil || !strings.Contains(err.Error(), "set user on the entry") {
		t.Errorf("plain payload without a user: %v", err)
	}
	for _, bad := range []string{"", "appdb-ro", "projects/acme/secrets/appdb-ro", "projects//secrets/x/versions/1"} {
		if _, err := g.Credential(context.Background(), daemon.CredentialEntry{Name: "x", Secret: bad}, session.Identity{}); err == nil || !strings.Contains(err.Error(), `credential "x"`) {
			t.Errorf("secret %q was accepted", bad)
		}
	}
}
