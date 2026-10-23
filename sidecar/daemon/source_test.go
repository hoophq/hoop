package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/session"
)

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
	src, err := buildSecretFilesSource(&CredentialSourcesConfig{SecretFiles: &SecretFilesSourceConfig{Dir: dir}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cred, err := src.Credential(ctx, CredentialEntry{Name: "rw", Source: SourceSecretFiles, UserFile: "rw/user", PasswordFile: "rw/password"}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if cred.User != "app_rw" || cred.Password != "pw-1" || cred.Name != "" {
		t.Fatalf("credential = %+v", cred)
	}
	// A literal user, and an absolute path inside the directory.
	cred, err = src.Credential(ctx, CredentialEntry{Name: "rw", User: "literal", PasswordFile: filepath.Join(dir, "rw", "password")}, session.Identity{})
	if err != nil || cred.User != "literal" {
		t.Fatalf("literal user: %+v %v", cred, err)
	}
	// Re-read per call: a rotation lands at the next login.
	writeMode(t, filepath.Join(dir, "rw", "password"), "pw-2", 0o400)
	if cred, _ = src.Credential(ctx, CredentialEntry{Name: "rw", User: "u", PasswordFile: "rw/password"}, session.Identity{}); cred.Password != "pw-2" {
		t.Fatalf("after a rotation: %q", cred.Password)
	}
	// An http lane may hold a bearer with no user.
	if cred, err = src.Credential(ctx, CredentialEntry{Name: "tok", PasswordFile: "rw/password"}, session.Identity{}); err != nil || cred.User != "" || cred.Password != "pw-2" {
		t.Fatalf("bearer: %+v %v", cred, err)
	}

	for name, tc := range map[string]struct {
		entry CredentialEntry
		want  string
	}{
		"world readable":   {CredentialEntry{Name: "o", User: "u", PasswordFile: "open/password"}, "readable by group or other (mode 0644)"},
		"outside the dir":  {CredentialEntry{Name: "o", User: "u", PasswordFile: "../etc/passwd"}, "is outside credentials.secret_files.dir"},
		"absolute outside": {CredentialEntry{Name: "o", User: "u", PasswordFile: "/etc/passwd"}, "is outside credentials.secret_files.dir"},
		"missing":          {CredentialEntry{Name: "o", User: "u", PasswordFile: "rw/none"}, "no such file"},
		"no password file": {CredentialEntry{Name: "o", User: "u"}, "password_file is required"},
		"two users":        {CredentialEntry{Name: "o", User: "u", UserFile: "rw/user", PasswordFile: "rw/password"}, "user and user_file both"},
		"a directory":      {CredentialEntry{Name: "o", User: "u", PasswordFile: "rw"}, "is a directory"},
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
	if _, err := src.Credential(ctx, CredentialEntry{Name: "o", User: "u", PasswordFile: "big"}, session.Identity{}); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("an oversized file: %v", err)
	}
	if _, err := buildSecretFilesSource(&CredentialSourcesConfig{SecretFiles: &SecretFilesSourceConfig{}}); err == nil {
		t.Fatal("a block with no dir built")
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
	roots, _ := loadTrustRoots(nil)
	_ = roots

	src := newGCPImpersonationSource(&GCPImpersonationSourceConfig{MetadataHost: strings.TrimPrefix(meta.URL, "http://")}, nil)
	src.iam = iam.Client()
	ctx := context.Background()
	entry := CredentialEntry{Name: "gw", Source: SourceGCPImpersonation, ServiceAccount: "gw@p.iam.gserviceaccount.com"}
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
	src.cache["gw"] = gcpMinted{token: cred.Password, exp: time.Now().Add(gcpAccessTokenRefreshBefore - time.Second)}
	fresh, err := src.Credential(ctx, entry, session.Identity{})
	if err != nil || fresh.Password == cred.Password || mintCalls.Load() != 2 {
		t.Fatalf("near expiry: %+v %v mints %d", fresh, err, mintCalls.Load())
	}
	// A denied account names the binding to add.
	_, err = src.Credential(ctx, CredentialEntry{Name: "other", ServiceAccount: "other@p.iam.gserviceaccount.com"}, session.Identity{})
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "serviceAccountTokenCreator") {
		t.Fatalf("a denied mint: %v", err)
	}
	if _, err := src.Credential(ctx, CredentialEntry{Name: "x", ServiceAccount: "not-an-email"}, session.Identity{}); err == nil || !strings.Contains(err.Error(), "is not an email") {
		t.Fatalf("a bad account: %v", err)
	}
	// An unreachable metadata server names itself.
	down := newGCPImpersonationSource(&GCPImpersonationSourceConfig{MetadataHost: "127.0.0.1:1"}, nil)
	if _, err := down.Credential(ctx, entry, session.Identity{}); err == nil || !strings.Contains(err.Error(), "metadata server at 127.0.0.1:1") {
		t.Fatalf("metadata down: %v", err)
	}
}

// x509 loads the pair per login, refuses a readable key and defaults the
// user to the certificate's common name.
func TestX509Source(t *testing.T) {
	cert, key := tlsKeypairFiles(t)
	if err := os.Chmod(key, 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := buildX509Source(&CredentialSourcesConfig{X509: &X509SourceConfig{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cred, err := src.Credential(ctx, CredentialEntry{Name: "svc", CertFile: cert, KeyFile: key}, session.Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if cred.Cert == nil || cred.Cert.Leaf == nil || cred.User != cred.Cert.Leaf.Subject.CommonName || cred.Password != "" {
		t.Fatalf("credential = %+v", cred)
	}
	if cred, _ = src.Credential(ctx, CredentialEntry{Name: "svc", User: "named", CertFile: cert, KeyFile: key}, session.Identity{}); cred.User != "named" {
		t.Fatalf("user = %q", cred.User)
	}
	if err := os.Chmod(key, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Credential(ctx, CredentialEntry{Name: "svc", CertFile: cert, KeyFile: key}, session.Identity{}); err == nil || !strings.Contains(err.Error(), "readable by group or other") {
		t.Fatalf("an open key: %v", err)
	}
	if _, err := src.Credential(ctx, CredentialEntry{Name: "svc", CertFile: cert}, session.Identity{}); err == nil || !strings.Contains(err.Error(), "cert_file and key_file are required") {
		t.Fatalf("no key: %v", err)
	}
	if _, err := buildX509Source(&CredentialSourcesConfig{}); err == nil {
		t.Fatal("built with no block")
	}
}

// The registry builds a source once per configured block that a pool
// names, and refuses a pool naming an unconfigured one.
func TestBuildCredentialSources(t *testing.T) {
	cfg := &Config{
		Credentials: &CredentialSourcesConfig{SecretFiles: &SecretFilesSourceConfig{Dir: t.TempDir()}, X509: &X509SourceConfig{}},
		Listeners: []ListenerConfig{
			{Credentials: &CredentialsConfig{Pool: []CredentialEntry{{Name: "a", Source: SourceSecretFiles}}}},
			{Credentials: &CredentialsConfig{Pool: []CredentialEntry{{Name: "b", Source: SourceSecretFiles}, {Name: "c", Source: SourceX509}}}},
		},
	}
	sources, err := buildCredentialSources(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 || sources[SourceSecretFiles] == nil || sources[SourceX509] == nil {
		t.Fatalf("sources = %v", sources)
	}
	cfg.Listeners[0].Credentials.Pool[0].Source = SourceGCPImpersonation
	if _, err := buildCredentialSources(cfg, nil); err == nil || !strings.Contains(err.Error(), "no credentials.gcp_impersonation block") {
		t.Fatalf("an unconfigured source: %v", err)
	}
}
