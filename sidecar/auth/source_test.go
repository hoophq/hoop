package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
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

// tlsKeypairFiles writes a self-signed certificate and its key.
func tlsKeypairFiles(t *testing.T) (cert, key string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sidecar"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cert, key = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	for _, f := range []struct {
		path, kind string
		der        []byte
	}{{cert, "CERTIFICATE", der}, {key, "PRIVATE KEY", keyDER}} {
		if err := os.WriteFile(f.path, pem.EncodeToMemory(&pem.Block{Type: f.kind, Bytes: f.der}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return cert, key
}

func writeMode(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	// A rotation replaces the file, as a Secret volume does; a 0400 file
	// cannot be written in place.
	_ = os.Remove(path)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// secret_files reads the user and the password per login, under the
// directory alone, trimmed, and refuses a password anyone else can read.
func TestSecretFilesSource(t *testing.T) {
	dir := t.TempDir()
	writeMode(t, filepath.Join(dir, "rw", "user"), "app_rw\n", 0o644)
	writeMode(t, filepath.Join(dir, "rw", "password"), " pw-1 \n", 0o400)
	writeMode(t, filepath.Join(dir, "open", "password"), "pw", 0o644)
	src, err := buildSecretFilesSource(&daemon.CredentialSourcesConfig{SecretFiles: &daemon.SecretFilesSourceConfig{Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cred, err := src.Credential(ctx, daemon.CredentialEntry{Name: "rw", Source: daemon.SourceSecretFiles, UserFile: "rw/user", PasswordFile: "rw/password"}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if cred.User != "app_rw" || cred.Password != "pw-1" || cred.Name != "" {
		t.Fatalf("credential = %+v", cred)
	}
	// A literal user, and an absolute path inside the directory.
	cred, err = src.Credential(ctx, daemon.CredentialEntry{Name: "rw", User: "literal", PasswordFile: filepath.Join(dir, "rw", "password")}, session.Identity{})
	if err != nil || cred.User != "literal" {
		t.Fatalf("literal user: %+v %v", cred, err)
	}
	// Re-read per call: a rotation lands at the next login.
	writeMode(t, filepath.Join(dir, "rw", "password"), "pw-2", 0o400)
	if cred, _ = src.Credential(ctx, daemon.CredentialEntry{Name: "rw", User: "u", PasswordFile: "rw/password"}, session.Identity{}); cred.Password != "pw-2" {
		t.Fatalf("after a rotation: %q", cred.Password)
	}
	// An http lane may hold a bearer with no user.
	if cred, err = src.Credential(ctx, daemon.CredentialEntry{Name: "tok", PasswordFile: "rw/password"}, session.Identity{}); err != nil || cred.User != "" || cred.Password != "pw-2" {
		t.Fatalf("bearer: %+v %v", cred, err)
	}

	for name, tc := range map[string]struct {
		entry daemon.CredentialEntry
		want  string
	}{
		"world readable":   {daemon.CredentialEntry{Name: "o", User: "u", PasswordFile: "open/password"}, "readable by group or other (mode 0644)"},
		"outside the dir":  {daemon.CredentialEntry{Name: "o", User: "u", PasswordFile: "../etc/passwd"}, "is outside credentials.secret_files.dir"},
		"absolute outside": {daemon.CredentialEntry{Name: "o", User: "u", PasswordFile: "/etc/passwd"}, "is outside credentials.secret_files.dir"},
		"missing":          {daemon.CredentialEntry{Name: "o", User: "u", PasswordFile: "rw/none"}, "no such file"},
		"no password file": {daemon.CredentialEntry{Name: "o", User: "u"}, "password_file is required"},
		"two users":        {daemon.CredentialEntry{Name: "o", User: "u", UserFile: "rw/user", PasswordFile: "rw/password"}, "user and user_file both"},
		"a directory":      {daemon.CredentialEntry{Name: "o", User: "u", PasswordFile: "rw"}, "is a directory"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := src.Credential(ctx, tc.entry, session.Identity{})
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), `credential "o"`) {
				t.Fatalf("err = %v, want %q naming the entry", err, tc.want)
			}
			if strings.Contains(err.Error(), "pw") {
				t.Fatalf("the error carries file content: %v", err)
			}
		})
	}
	big := strings.Repeat("a", maxSecretFileBytes+1)
	writeMode(t, filepath.Join(dir, "big"), big, 0o400)
	if _, err := src.Credential(ctx, daemon.CredentialEntry{Name: "o", User: "u", PasswordFile: "big"}, session.Identity{}); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("an oversized file: %v", err)
	}
	if _, err := buildSecretFilesSource(&daemon.CredentialSourcesConfig{SecretFiles: &daemon.SecretFilesSourceConfig{}}); err == nil {
		t.Fatal("a block with no dir built")
	}
}

// A symlink inside the directory may point at a file inside it, the way a
// Kubernetes mount links through ..data; one that leaves it is refused.
func TestSecretFilesSourceFollowsSymlinksInsideTheDirOnly(t *testing.T) {
	outside := t.TempDir()
	writeMode(t, filepath.Join(outside, "secret"), "leaked", 0o400)
	dir := t.TempDir()
	// The kubelet layout: password -> ..data/password, ..data -> ..2026_01.
	writeMode(t, filepath.Join(dir, "..2026_01", "password"), "pw-k8s", 0o400)
	if err := os.Symlink("..2026_01", filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..data", "password"), filepath.Join(dir, "password")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	src, err := buildSecretFilesSource(&daemon.CredentialSourcesConfig{SecretFiles: &daemon.SecretFilesSourceConfig{Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cred, err := src.Credential(ctx, daemon.CredentialEntry{Name: "k", User: "u", PasswordFile: "password"}, session.Identity{})
	if err != nil || cred.Password != "pw-k8s" {
		t.Fatalf("through ..data: %+v %v", cred, err)
	}
	_, err = src.Credential(ctx, daemon.CredentialEntry{Name: "o", User: "u", PasswordFile: "escape"}, session.Identity{})
	if err == nil || !strings.Contains(err.Error(), "outside credentials.secret_files.dir") || strings.Contains(err.Error(), "leaked") {
		t.Fatalf("a link out of the dir: %v", err)
	}
}

// gcp_impersonation asks the metadata server for the pod's token, mints one
// for the entry's account through iamcredentials, and caches it per entry
// until five minutes before it expires.
func TestGCPImpersonationSource(t *testing.T) {
	var metaCalls, mintCalls atomic.Int64
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metaCalls.Add(1)
		if r.Header.Get("Metadata-Flavor") != "Google" || r.URL.Path != "/computeMetadata/v1/instance/service-accounts/default/token" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "own-token", "expires_in": 3600, "token_type": "Bearer"})
	}))
	defer meta.Close()
	expireIn := time.Hour
	iam := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mintCalls.Add(1)
		sa, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/v1/projects/-/serviceAccounts/"), ":generateAccessToken")
		if !ok || r.Header.Get("Authorization") != "Bearer own-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Request had invalid authentication credentials."}}`))
			return
		}
		var body struct {
			Scope    []string `json:"scope"`
			Lifetime string   `json:"lifetime"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if sa != "gw@p.iam.gserviceaccount.com" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"message":"Permission 'iam.serviceAccounts.getAccessToken' denied"}}`))
			return
		}
		if len(body.Scope) != 1 || body.Scope[0] != gcpDefaultScope || body.Lifetime != gcpImpersonationLifetime {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"accessToken": "sa-token-" + time.Now().Format("05.000000000"),
			"expireTime": time.Now().Add(expireIn).UTC().Format(time.RFC3339)})
	}))
	defer iam.Close()
	prev := iamCredentialsEndpoint
	iamCredentialsEndpoint = iam.URL
	t.Cleanup(func() { iamCredentialsEndpoint = prev })

	src := newGCPImpersonationSource(&daemon.GCPImpersonationSourceConfig{MetadataHost: strings.TrimPrefix(meta.URL, "http://")}, nil)
	src.iam = iam.Client()
	ctx := context.Background()
	entry := daemon.CredentialEntry{Name: "gw", Source: daemon.SourceGCPImpersonation, ServiceAccount: "gw@p.iam.gserviceaccount.com"}
	cred, err := src.Credential(ctx, entry, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if cred.User != entry.ServiceAccount || !strings.HasPrefix(cred.Password, "sa-token-") {
		t.Fatalf("credential = %+v", cred)
	}
	again, _ := src.Credential(ctx, entry, session.Identity{})
	if again.Password != cred.Password || mintCalls.Load() != 1 || metaCalls.Load() != 1 {
		t.Fatalf("the second login minted again: mints %d meta %d", mintCalls.Load(), metaCalls.Load())
	}
	// Near expiry the token is replaced.
	src.cache[gcpCacheKey(entry.ServiceAccount, gcpScopes(entry))] = gcpMinted{token: cred.Password, exp: time.Now().Add(gcpAccessTokenRefreshBefore - time.Second)}
	fresh, err := src.Credential(ctx, entry, session.Identity{})
	if err != nil || fresh.Password == cred.Password || mintCalls.Load() != 2 {
		t.Fatalf("near expiry: %+v %v mints %d", fresh, err, mintCalls.Load())
	}
	// A denied account names the binding to add.
	_, err = src.Credential(ctx, daemon.CredentialEntry{Name: "other", ServiceAccount: "other@p.iam.gserviceaccount.com"}, session.Identity{})
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "serviceAccountTokenCreator") {
		t.Fatalf("a denied mint: %v", err)
	}
	if _, err := src.Credential(ctx, daemon.CredentialEntry{Name: "x", ServiceAccount: "not-an-email"}, session.Identity{}); err == nil || !strings.Contains(err.Error(), "is not an email") {
		t.Fatalf("a bad account: %v", err)
	}
	// An unreachable metadata server names itself.
	down := newGCPImpersonationSource(&daemon.GCPImpersonationSourceConfig{MetadataHost: "127.0.0.1:1"}, nil)
	if _, err := down.Credential(ctx, entry, session.Identity{}); err == nil || !strings.Contains(err.Error(), "metadata server at 127.0.0.1:1") {
		t.Fatalf("metadata down: %v", err)
	}
}

// Two lanes naming different accounts "rw" are two mints: the cache is
// keyed by the account and scopes, never by the entry name.
func TestGCPImpersonationCacheIsKeyedByAccountAndScopes(t *testing.T) {
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "own-token", "expires_in": 3600, "token_type": "Bearer"})
	}))
	defer meta.Close()
	var mints atomic.Int64
	iam := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := mints.Add(1)
		sa, _ := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/v1/projects/-/serviceAccounts/"), ":generateAccessToken")
		var body struct {
			Scope []string `json:"scope"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"accessToken": fmt.Sprintf("%s|%s|%d", sa, strings.Join(body.Scope, ","), n),
			"expireTime":  time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		})
	}))
	defer iam.Close()
	prev := iamCredentialsEndpoint
	iamCredentialsEndpoint = iam.URL
	t.Cleanup(func() { iamCredentialsEndpoint = prev })
	src := newGCPImpersonationSource(&daemon.GCPImpersonationSourceConfig{MetadataHost: strings.TrimPrefix(meta.URL, "http://")}, nil)
	src.iam = iam.Client()
	ctx := context.Background()

	a := daemon.CredentialEntry{Name: "rw", ServiceAccount: "a@p.iam.gserviceaccount.com"}
	b := daemon.CredentialEntry{Name: "rw", ServiceAccount: "b@p.iam.gserviceaccount.com"}
	ca, err := src.Credential(ctx, a, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	cb, err := src.Credential(ctx, b, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if ca.Password == cb.Password || !strings.HasPrefix(ca.Password, "a@") || !strings.HasPrefix(cb.Password, "b@") {
		t.Fatalf("lane b got lane a's token: %q vs %q", ca.Password, cb.Password)
	}
	// Scopes are part of the key, in any order; the same account and
	// scopes hit the cache.
	s1 := daemon.CredentialEntry{Name: "rw", ServiceAccount: a.ServiceAccount, Scopes: []string{"s2", "s1"}}
	s2 := daemon.CredentialEntry{Name: "ro", ServiceAccount: a.ServiceAccount, Scopes: []string{"s1", "s2"}}
	c1, _ := src.Credential(ctx, s1, session.Identity{})
	c2, _ := src.Credential(ctx, s2, session.Identity{})
	if c1.Password == ca.Password || c1.Password != c2.Password || mints.Load() != 3 {
		t.Fatalf("scoped tokens %q %q, mints %d", c1.Password, c2.Password, mints.Load())
	}
}

// x509 loads the pair per login, refuses a readable key and defaults the
// user to the certificate's common name.
func TestX509Source(t *testing.T) {
	cert, key := tlsKeypairFiles(t)
	if err := os.Chmod(key, 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := buildX509Source(&daemon.CredentialSourcesConfig{X509: &daemon.X509SourceConfig{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cred, err := src.Credential(ctx, daemon.CredentialEntry{Name: "svc", CertFile: cert, KeyFile: key}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if cred.Cert == nil || cred.Cert.Leaf == nil || cred.User != cred.Cert.Leaf.Subject.CommonName || cred.Password != "" {
		t.Fatalf("credential = %+v", cred)
	}
	if cred, _ = src.Credential(ctx, daemon.CredentialEntry{Name: "svc", User: "named", CertFile: cert, KeyFile: key}, session.Identity{}); cred.User != "named" {
		t.Fatalf("user = %q", cred.User)
	}
	if err := os.Chmod(key, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Credential(ctx, daemon.CredentialEntry{Name: "svc", CertFile: cert, KeyFile: key}, session.Identity{}); err == nil || !strings.Contains(err.Error(), "readable by group or other") {
		t.Fatalf("an open key: %v", err)
	}
	if _, err := src.Credential(ctx, daemon.CredentialEntry{Name: "svc", CertFile: cert}, session.Identity{}); err == nil || !strings.Contains(err.Error(), "cert_file and key_file are required") {
		t.Fatalf("no key: %v", err)
	}
	if _, err := buildX509Source(&daemon.CredentialSourcesConfig{}); err == nil {
		t.Fatal("built with no block")
	}
}

// The registry builds a source once per configured block that a pool
// names, and refuses a pool naming an unconfigured one.
func TestBuildCredentialSources(t *testing.T) {
	cfg := &daemon.Config{
		Credentials: &daemon.CredentialSourcesConfig{SecretFiles: &daemon.SecretFilesSourceConfig{Dir: t.TempDir()}, X509: &daemon.X509SourceConfig{}},
		Listeners: []daemon.ListenerConfig{
			{Credentials: &daemon.CredentialsConfig{Pool: []daemon.CredentialEntry{{Name: "a", Source: daemon.SourceSecretFiles}}}},
			{Credentials: &daemon.CredentialsConfig{Pool: []daemon.CredentialEntry{{Name: "b", Source: daemon.SourceSecretFiles}, {Name: "c", Source: daemon.SourceX509}}}},
		},
	}
	sources, err := daemon.BuildCredentialSources(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || sources[daemon.SourceSecretFiles] == nil || sources[daemon.SourceX509] == nil {
		t.Fatalf("sources = %v", sources)
	}
	cfg.Listeners[0].Credentials.Pool[0].Source = daemon.SourceGCPImpersonation
	if _, err := daemon.BuildCredentialSources(cfg, nil); err == nil || !strings.Contains(err.Error(), "no credentials.gcp_impersonation block") {
		t.Fatalf("an unconfigured source: %v", err)
	}
}
