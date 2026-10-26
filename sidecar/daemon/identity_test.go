package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// A pooled lane is a login the sidecar terminates. Each refusal below is a
// combination that would load and do nothing, put a key on the wire in the
// clear, or hand a credential to an identity nobody mapped.
func TestLaneAuthIsRefusedWhereItCannotWork(t *testing.T) {
	const pool = `"credentials":{"pool":[{"name":"ro","source":"secret_files","user":"app","password_file":"ro/password"}],"select":{"rules":[{"credential":"ro"}]}}`
	const sources = `"credentials":{"secret_files":{"dir":"/run/creds"}},`
	for _, tc := range []struct{ name, top, listener, want string }{
		{"identity without a pool", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{}`,
			"identity needs a credentials pool"},
		{"none on tcp", sources, `"protocol":"postgres","identity":{"type":"none"},` + pool,
			"identity type none needs network: unix"},
		{"pool without identity on tcp", sources, `"protocol":"postgres",` + pool,
			"credentials without identity needs network: unix"},
		{"unknown type", sources, `"protocol":"postgres","identity":{"type":"kubernets"},` + pool,
			`identity.type "kubernets" is not none, kubernetes, hoop_credential, jwt or spiffe`},
		{"jwt without an issuer", sources, `"protocol":"http","identity":{"type":"jwt"},` + pool,
			"identity type jwt needs identity.issuer"},
		{"jwt on plain tcp postgres", sources, `"protocol":"postgres","identity":{"type":"jwt","issuer":"https://login.acme.com"},` + pool,
			"identity type jwt sends the secret as the login password; a tcp postgres listener needs downstream_tls"},
		{"jwt settings on kubernetes", sources, `"protocol":"http","identity":{"type":"kubernetes","jwks_url":"https://k/keys"},` + pool,
			"identity.jwks_url and identity.claims are jwt settings, not kubernetes ones"},
		{"spiffe settings on jwt", sources, `"protocol":"http","identity":{"type":"jwt","issuer":"https://login.acme.com","prefix":"/x"},` + pool,
			"identity.trust_domain and identity.prefix are spiffe settings, not jwt ones"},
		{"beside identity_header", sources, `"protocol":"http","identity_header":"x-user","identity":{"type":"hoop_credential"},` + pool,
			"identity and identity_header both name the caller"},
		{"beside google_identity", sources, `"protocol":"http","google_identity":{},"identity":{"type":"hoop_credential"},` + pool,
			"identity and google_identity both name the caller"},
		{"beside caller_token", sources, `"protocol":"http","caller_token":{"file":"/t","name":"a"},"identity":{"type":"hoop_credential"},` + pool,
			"identity and caller_token both name the caller"},
		{"hoop_credential on plain tcp postgres", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},` + pool,
			"needs downstream_tls, or a unix socket"},
		{"hoop_credential on plain tcp clickhouse", sources, `"protocol":"clickhouse","identity":{"type":"hoop_credential"},` + pool,
			"needs downstream_tls, or a unix socket"},
		{"credentials on ssh", sources, `"protocol":"ssh",` + pool,
			"credentials is not valid on an ssh listener"},
		{"identity on ssh", sources, `"protocol":"ssh","identity":{"type":"none"}`,
			"identity is not valid on an ssh listener"},
		{"credentials on oracle", sources, `"protocol":"oracle","identity":{"type":"hoop_credential"},` + pool,
			"oracle: credential swap is not supported in this release; use passthrough_users"},
		{"identity on oracle", sources, `"protocol":"oracle","identity":{"type":"kubernetes"}`,
			"oracle: credential swap is not supported in this release"},
		{"empty pool", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[],"select":{"opa":true}}`,
			"credentials.pool is empty"},
		{"unknown source", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"vault"}],"select":{"rules":[{"credential":"a"}]}}`,
			`source "vault" is not secret_files, gcp_impersonation, x509, spiffe or kerberos`},
		{"source not configured", "", `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},` + pool,
			"names source secret_files, and the configuration has no credentials.secret_files block"},
		{"duplicate name", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"secret_files","user":"u","password_file":"p"},{"name":"a","source":"secret_files","user":"u","password_file":"p"}],"select":{"rules":[{"credential":"a"}]}}`,
			`name "a" is used twice`},
		{"rules and opa", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"secret_files","user":"u","password_file":"p"}],"select":{"rules":[{"credential":"a"}],"opa":true}}`,
			"credentials.select names rules and opa; keep one"},
		{"neither rules nor opa", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"secret_files","user":"u","password_file":"p"}]}`,
			"credentials.select needs rules, or opa: true"},
		{"opa without an endpoint", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"secret_files","user":"u","password_file":"p"}],"select":{"opa":true}}`,
			"credentials.select.opa needs an opa.url"},
		{"rule names a stranger", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"secret_files","user":"u","password_file":"p"}],"select":{"rules":[{"credential":"b"}]}}`,
			`names credential "b", which is not in the pool`},
		{"rule with a bad key", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"secret_files","user":"u","password_file":"p"}],"select":{"rules":[{"when":{"identity.role":"x"},"credential":"a"}]}}`,
			`when key "identity.role" is not`},
		{"secret_files without a user on postgres", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"secret_files","password_file":"p"}],"select":{"rules":[{"credential":"a"}]}}`,
			"user or user_file is required on a postgres listener"},
		{"secret_files outside the dir", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"secret_files","user":"u","password_file":"/etc/shadow"}],"select":{"rules":[{"credential":"a"}]}}`,
			"is outside credentials.secret_files.dir"},
		{"gcp without an account", `"credentials":{"gcp_impersonation":{}},`, `"protocol":"http","identity":{"type":"hoop_credential"},"credentials":{"pool":[{"name":"a","source":"gcp_impersonation"}],"select":{"rules":[{"credential":"a"}]}}`,
			"service_account is required"},
		{"x509 on http", `"credentials":{"x509":{}},`, `"protocol":"http","identity":{"type":"hoop_credential"},"credentials":{"pool":[{"name":"a","source":"x509","cert_file":"c","key_file":"k"}],"select":{"rules":[{"credential":"a"}]}}`,
			"a certificate is offered per connection"},
		{"passthrough on http", sources, `"protocol":"http","identity":{"type":"hoop_credential"},"passthrough_users":["admin"],` + pool,
			"passthrough_users is only supported on a database listener"},
		{"passthrough without identity", sources, `"protocol":"postgres","passthrough_users":["admin"]`,
			"passthrough_users needs an identity block"},
		{"propagate without a pool", sources, `"protocol":"postgres","propagate":{"application_name":"x"}`,
			"propagate needs a credentials pool"},
		{"application_name off postgres", sources, `"protocol":"mysql","identity":{"type":"hoop_credential"},"propagate":{"application_name":"x"},` + pool,
			"propagate.application_name is a postgres startup parameter"},
		{"impersonate off http", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"propagate":{"impersonate_user":true},` + pool,
			"propagate.impersonate_user adds Impersonate-User"},
		{"bad template", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"propagate":{"application_name":"{{.Principal"},` + pool,
			"propagate.application_name:"},
		{"reserved context key", sources, `"protocol":"postgres","context":{"principal":"x"}`,
			`context key "principal" is one the policy context reserves`},
		{"secret_files without a dir", `"credentials":{"secret_files":{}},`, `"protocol":"postgres"`,
			"credentials.secret_files.dir is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeConfig(t, `{`+tc.top+`"listeners": [{"listen":":1","upstream":"h:1",`+tc.listener+`}]}`)
			if got := loadErr(t, p); !strings.Contains(got, tc.want) {
				t.Errorf("error %q does not say %q", got, tc.want)
			}
		})
	}
}

// The combinations the validator admits: the unix lane that implies none,
// the TLS-terminated tcp lane, a pooled http lane, and OPA selection beside
// an endpoint.
func TestLaneAuthLoads(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "l.sock")
	cert, key := tlsKeypairFiles(t)
	for _, tc := range []struct{ name, top, listener string }{
		{"unix implies none", `"credentials":{"secret_files":{"dir":"/run/creds"}},`,
			`"protocol":"postgres","network":"unix","listen":"` + sock + `","upstream":"h:1","credentials":{"pool":[{"name":"ro","source":"secret_files","user":"app","password_file":"ro/password"}],"select":{"rules":[{"credential":"ro"}]}},"passthrough_users":["postgres"],"propagate":{"application_name":"{{.Principal}} via {{.Context.env}}"},"context":{"env":"demo"}`},
		{"hoop_credential under downstream tls", `"credentials":{"secret_files":{"dir":"/run/creds"}},`,
			`"protocol":"postgres","listen":":1","upstream":"h:1","identity":{"type":"hoop_credential"},"downstream_tls":{"cert_file":"` + cert + `","key_file":"` + key + `"},"credentials":{"pool":[{"name":"ro","source":"secret_files","user_file":"ro/user","password_file":"ro/password"}],"select":{"rules":[{"when":{"identity.groups":"dba"},"credential":"ro"}]}}`},
		{"http with impersonation", `"credentials":{"gcp_impersonation":{"metadata_host":"meta:80"}},`,
			`"protocol":"http","listen":":1","upstream":"h:1","identity":{"type":"hoop_credential"},"credentials":{"pool":[{"name":"gw","source":"gcp_impersonation","service_account":"sa@p.iam.gserviceaccount.com"}],"select":{"rules":[{"credential":"gw"}]}},"propagate":{"impersonate_user":true}`},
		{"kubernetes picks by opa", `"credentials":{"x509":{}},"opa":{"url":"http://opa:8181/v1/data/hoop"},`,
			`"protocol":"mysql","listen":":1","upstream":"h:1","identity":{"type":"kubernetes","issuer":"https://oidc.example.com/id/abc"},"credentials":{"pool":[{"name":"svc","source":"x509","cert_file":"c.pem","key_file":"k.pem"}],"select":{"opa":true}}`},
		{"oracle passthrough with none", ``,
			`"protocol":"oracle","network":"unix","listen":"` + sock + `","upstream":"h:1","identity":{"type":"none"},"passthrough_users":["system"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := writeConfig(t, `{`+tc.top+`"listeners": [{`+tc.listener+`}]}`)
			if _, err := LoadConfig(p); err != nil {
				t.Fatalf("refused: %v", err)
			}
		})
	}
}

// The plane validates with the same decoder and links no nested module, so
// Config.Validate accepts spiffe; the host refuses it when the build did not
// link sidecar/identity/spiffe.
func TestAnUnlinkedVerifierOrSourceIsRefusedOnTheHostOnly(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "l.sock")
	p := writeConfig(t, `{"credentials":{"spiffe":{}},"listeners":[{"protocol":"postgres","network":"unix","listen":"`+sock+`","upstream":"h:1",
	  "identity":{"type":"spiffe"},"credentials":{"pool":[{"name":"svc","source":"spiffe","spiffe_id":"spiffe://example.org/sa/x"}],"select":{"rules":[{"credential":"svc"}]}}}]}`)
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("the config decoder refused a spiffe lane: %v", err)
	}
	if _, ok := registeredIdentitySource(IdentitySPIFFE); ok {
		t.Skip("this test binary links the spiffe module")
	}
	_, err = Validate(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), `identity type "spiffe" is not linked`) {
		t.Fatalf("Validate = %v, want the unlinked verifier named", err)
	}
	// With the verifier linked the source would be next.
	if err := checkCredentialSources(cfg); err == nil || !strings.Contains(err.Error(), `credential source "spiffe" is not linked`) {
		t.Fatalf("checkCredentialSources = %v", err)
	}
}

// The header lists every verifier and source this build links, so the plane
// serves a lane only to a build that can run it, and CheckServable asks for
// the entry a lane needs.
func TestTheHeaderListsVerifiersAndSources(t *testing.T) {
	caps := SidecarCapabilities()
	for _, want := range []string{"identity:none", "identity:kubernetes", "identity:hoop_credential",
		"credentials.secret_files", "credentials.gcp_impersonation", "credentials.x509", "identity", "credentials", "credential_set"} {
		if !slices.Contains(caps, want) {
			t.Errorf("the header lacks %q: %v", want, caps)
		}
	}
	if _, ok := registeredCredentialSource(SourceKerberos); !ok && slices.Contains(caps, "credentials.kerberos") {
		t.Error("the header claims credentials.kerberos with no module linked")
	}

	cfg := Config{Listeners: []ListenerConfig{{
		Name: "l", Protocol: "postgres", Listen: "/s", Network: "unix", Upstream: "h:1",
		Credentials: &CredentialsConfig{Pool: []CredentialEntry{{Name: "a", Source: "kerberos"}}},
	}}}
	err := CheckServable(cfg, Handshake{Capabilities: caps})
	if _, linked := registeredCredentialSource(SourceKerberos); !linked {
		if err == nil || !strings.Contains(err.Error(), `"credentials.kerberos"`) {
			t.Fatalf("CheckServable = %v, want the kerberos entry required", err)
		}
	}
	cfg.Listeners[0].Identity = &IdentityConfig{Type: IdentitySPIFFE}
	cfg.Listeners[0].Credentials.Pool[0].Source = SourceSecretFiles
	err = CheckServable(cfg, Handshake{Capabilities: caps})
	if _, linked := registeredIdentitySource(IdentitySPIFFE); !linked {
		if err == nil || !strings.Contains(err.Error(), `"identity:spiffe"`) {
			t.Fatalf("CheckServable = %v, want the spiffe verifier required", err)
		}
	}
}

func hashOf(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// The set is matched by digest, per lane, with expiry and the login user
// checked; and the scram form names an entry by hash without the key.
func TestHoopCredentialVerifier(t *testing.T) {
	future := time.Now().Add(time.Hour)
	set := newCredentialSet([]CredentialSetEntry{
		{Hash: hashOf("hlc_alice"), Subject: "alice", Email: "alice@example.com", Groups: []string{"dba"}, Lane: "appdb", ExpiresAt: future},
		{Hash: hashOf("hlc_bob"), Subject: "bob", Lane: "appdb", ExpiresAt: time.Now().Add(-time.Minute)},
		{Hash: hashOf("hlc_carol"), Subject: "carol", Lane: "other", ExpiresAt: future},
		{Hash: "not-hex", Subject: "mallory", Lane: "appdb", ExpiresAt: future},
	})
	v, err := buildHoopCredentialIdentity(IdentityConfig{}, IdentityDeps{lane: "appdb", credentials: set})
	if err != nil {
		t.Fatal(err)
	}
	verify := func(user, secret string) (session.Identity, error) {
		return v.Verify(context.Background(), user, secret, PeerInfo{})
	}

	id, err := verify("alice", "hlc_alice")
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	if id.Subject != "alice" || id.Email != "alice@example.com" || !slices.Equal(id.Groups, []string{"dba"}) ||
		id.Attributes[identityTypeAttribute] != IdentityHoopCredential {
		t.Fatalf("identity = %+v", id)
	}
	if _, err := verify("alice@example.com", "hlc_alice"); err != nil {
		t.Fatalf("the email as login user: %v", err)
	}
	if _, err := verify("", "hlc_alice"); err != nil {
		t.Fatalf("no login user (http): %v", err)
	}
	if _, err := verify("alice", scramSecretPrefix+hashOf("hlc_alice")); err != nil {
		t.Fatalf("the scram form: %v", err)
	}

	for name, tc := range map[string]struct{ user, secret, want string }{
		"wrong key":       {"alice", "hlc_wrong", "no hoop credential"},
		"empty":           {"alice", "", "no hoop credential"},
		"other lane":      {"carol", "hlc_carol", "no hoop credential"},
		"expired":         {"bob", "hlc_bob", "expired at"},
		"another's key":   {"bob", "hlc_alice", "belongs to alice, not to the login user \"bob\""},
		"raw hash as key": {"alice", hashOf("hlc_alice"), "no hoop credential"},
		"unknown scram":   {"alice", scramSecretPrefix + hashOf("x"), "no hoop credential"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := verify(tc.user, tc.secret)
			if !errors.Is(err, codectypes.ErrLoginRefused) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want a refusal saying %q", err, tc.want)
			}
		})
	}
}

// The SCRAM lookup returns the material of the live entry for a login user,
// decoded, with the hash the driver passes back.
func TestSCRAMVerifierFindsTheLiveEntryForAUser(t *testing.T) {
	material := &SCRAMMaterial{
		Salt: base64.StdEncoding.EncodeToString([]byte("salt")), Iterations: 4096,
		StoredKey: base64.StdEncoding.EncodeToString([]byte("stored")), ServerKey: base64.StdEncoding.EncodeToString([]byte("server")),
	}
	old := time.Now().Add(time.Hour)
	set := newCredentialSet([]CredentialSetEntry{
		{Hash: hashOf("k1"), Subject: "alice", Email: "alice@example.com", Lane: "appdb", ExpiresAt: old, SCRAM: material},
		{Hash: hashOf("k2"), Subject: "alice", Lane: "appdb", ExpiresAt: old.Add(time.Hour), SCRAM: material},
		{Hash: hashOf("k3"), Subject: "bob", Lane: "appdb", ExpiresAt: time.Now().Add(-time.Hour), SCRAM: material},
		{Hash: hashOf("k4"), Subject: "dan", Lane: "appdb", ExpiresAt: old},
		{Hash: hashOf("k5"), Subject: "eve", Lane: "appdb", ExpiresAt: old, SCRAM: &SCRAMMaterial{Salt: "%%", Iterations: 1, StoredKey: "a", ServerKey: "b"}},
	})
	m, ok := set.scram("appdb", "alice")
	if !ok || string(m.Salt) != "salt" || m.Iterations != 4096 || string(m.StoredKey) != "stored" || string(m.ServerKey) != "server" {
		t.Fatalf("material = %+v, %v", m, ok)
	}
	if m.Hash != hashOf("k2") {
		t.Errorf("hash = %s, want the newest key's", m.Hash)
	}
	if m2, ok := set.scram("appdb", "alice@example.com"); !ok || m2.Hash != hashOf("k1") {
		t.Errorf("by email: %+v %v", m2, ok)
	}
	for _, user := range []string{"bob", "dan", "eve", "nobody"} {
		if _, ok := set.scram("appdb", user); ok {
			t.Errorf("%s has SCRAM material; want none (expired, no material, or undecodable)", user)
		}
	}
	// The verifier accepts what the driver proves.
	v, _ := buildHoopCredentialIdentity(IdentityConfig{}, IdentityDeps{lane: "appdb", credentials: set})
	if _, err := v.Verify(context.Background(), "alice", scramSecretPrefix+m.Hash, PeerInfo{}); err != nil {
		t.Fatal(err)
	}
}

// swap replaces the whole set: a revoked key is refused at the next login
// and a minted one accepted, with no restart.
func TestCredentialSetSwaps(t *testing.T) {
	set := newCredentialSet(nil)
	if set.size() != 0 {
		t.Fatal("an empty set has entries")
	}
	if _, ok := set.match("l", "k"); ok {
		t.Fatal("an empty set matched")
	}
	set.swap([]CredentialSetEntry{{Hash: hashOf("k"), Subject: "a", Lane: "l"}})
	if e, ok := set.match("l", "k"); !ok || e.Subject != "a" {
		t.Fatalf("after the swap: %+v %v", e, ok)
	}
	set.swap([]CredentialSetEntry{{Hash: hashOf("k2"), Subject: "b", Lane: "l"}})
	if _, ok := set.match("l", "k"); ok {
		t.Fatal("a revoked key still matches")
	}
	if e, ok := set.byHash("l", hashOf("k2")); !ok || e.Subject != "b" {
		t.Fatalf("byHash: %+v %v", e, ok)
	}
	if _, ok := set.byHash("l", "zz"); ok {
		t.Fatal("a malformed hash matched")
	}
}

// The reloader swaps the set from every document the plane sends, whatever
// the document's fate, and the set never reaches the baseline: a mint is
// not a restart.
func TestAReloadSwapsTheCredentialSet(t *testing.T) {
	const base = `{"listeners":[{"name":"l","protocol":"postgres","listen":"127.0.0.1:0","upstream":"h:1"}]}`
	rl, buf := testReloader(t, base)
	if rl.credentials == nil {
		t.Fatal("the reloader holds no credential set")
	}
	entry, _ := json.Marshal([]CredentialSetEntry{{Hash: hashOf("k"), Subject: "a", Lane: "l", ExpiresAt: time.Now().Add(time.Hour)}})
	withKey := strings.TrimSuffix(base, "}") + `,"credential_set":` + string(entry) + `}`
	if out := applyWith(rl, buf, withKey); out == reloadRestart || out == reloadRefused {
		t.Fatalf("outcome = %v: %s", out, buf.String())
	}
	if _, ok := rl.credentials.match("l", "k"); !ok {
		t.Fatal("the reload did not put the served key in force")
	}
	// A document that needs a restart still carries the keys in force:
	// here one that revokes the key and moves the listen address.
	moved := strings.Replace(base, "127.0.0.1:0", "127.0.0.1:1", 1)
	if out := applyWith(rl, buf, moved); out != reloadRestart {
		t.Fatalf("outcome = %v, want a restart", out)
	}
	if _, ok := rl.credentials.match("l", "k"); ok {
		t.Fatal("a revoked key survived the reload")
	}
	cfg := Config{Listeners: []ListenerConfig{{Name: "l", Protocol: "postgres", Listen: ":1", Upstream: "h:1"}}}
	before, _ := BaselineDoc(&cfg)
	cfg.CredentialSet = []CredentialSetEntry{{Hash: hashOf("k"), Lane: "l"}}
	after, _ := BaselineDoc(&cfg)
	if string(before) != string(after) {
		t.Fatalf("the credential set reached the baseline:\n%s\n%s", before, after)
	}
}

// Rules are first match wins over the identity and the lane context; no
// match is a refusal, and an OPA lane reads the credential the policy named.
func TestCredentialSelection(t *testing.T) {
	a := &laneAuth{
		lane: "appdb",
		pool: []CredentialEntry{{Name: "rw"}, {Name: "ro"}, {Name: "svc"}},
		rules: []CredentialRule{
			{When: map[string]string{"identity.groups": "dba", "context.env": "prod"}, Credential: "rw"},
			{When: map[string]string{"identity.attributes.namespace": "payments"}, Credential: "svc"},
			{When: map[string]string{"identity.email": "carol@example.com"}, Credential: "ro"},
			{When: map[string]string{"identity.subject": "dan"}, Credential: "missing"},
		},
		context: map[string]string{"env": "prod"},
	}
	ctx := context.Background()
	for name, tc := range map[string]struct {
		id   session.Identity
		want string
		err  string
	}{
		"dba in prod":        {session.Identity{Subject: "alice", Groups: []string{"dev", "dba"}}, "rw", ""},
		"namespace":          {session.Identity{Subject: "sa", Attributes: map[string]string{"namespace": "payments"}}, "svc", ""},
		"email":              {session.Identity{Subject: "carol", Email: "carol@example.com"}, "ro", ""},
		"nobody":             {session.Identity{Subject: "zed"}, "", "no credential rule matches zed"},
		"rule names a ghost": {session.Identity{Subject: "dan"}, "", `credential "missing" is not in the pool`},
	} {
		t.Run(name, func(t *testing.T) {
			e, err := a.selectEntry(ctx, tc.id)
			if tc.err != "" {
				if !errors.Is(err, codectypes.ErrLoginRefused) || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want refusal %q", err, tc.err)
				}
				return
			}
			if err != nil || e.Name != tc.want {
				t.Fatalf("picked %q, %v; want %q", e.Name, err, tc.want)
			}
		})
	}

	// Context-only rules serve a none lane, where the identity is empty.
	b := &laneAuth{pool: []CredentialEntry{{Name: "pod"}}, rules: []CredentialRule{{Credential: "pod"}}}
	if e, err := b.selectEntry(ctx, session.Identity{}); err != nil || e.Name != "pod" {
		t.Fatalf("an unconditional rule: %q %v", e.Name, err)
	}

	// OPA: the login document carries the identity, and the answer names
	// the credential.
	var seen map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Input map[string]any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		seen = in.Input
		ctxMap, _ := in.Input["context"].(map[string]any)
		switch ctxMap["principal"] {
		case "alice":
			_, _ = w.Write([]byte(`{"result":{"allow":true,"credential":"rw"}}`))
		case "bob":
			_, _ = w.Write([]byte(`{"result":{"allow":true}}`))
		default:
			_, _ = w.Write([]byte(`{"result":{"allow":false,"message":"not today"}}`))
		}
	}))
	defer srv.Close()
	c := &laneAuth{lane: "appdb", proto: inspect.Postgres, pool: []CredentialEntry{{Name: "rw"}},
		opa: &policy.OPAClient{URL: srv.URL}, context: map[string]string{"env": "prod"}}
	e, err := c.selectEntry(ctx, session.Identity{Subject: "alice", Groups: []string{"dba"}})
	if err != nil || e.Name != "rw" {
		t.Fatalf("opa picked %q, %v", e.Name, err)
	}
	if seen["statement"] != "login" || seen["protocol"] != "postgres" {
		t.Errorf("login document = %v", seen)
	}
	if ctxMap, _ := seen["context"].(map[string]any); ctxMap["groups"] != "dba" || ctxMap["env"] != "prod" || ctxMap["connection"] != "appdb" {
		t.Errorf("login context = %v", seen["context"])
	}
	if md, _ := seen["metadata"].(map[string]any); md["pool"] != "rw" {
		t.Errorf("login metadata = %v", seen["metadata"])
	}
	if _, err := c.selectEntry(ctx, session.Identity{Subject: "bob"}); err == nil || !strings.Contains(err.Error(), "named no credential") {
		t.Fatalf("an allow naming nothing: %v", err)
	}
	if _, err := c.selectEntry(ctx, session.Identity{Subject: "eve"}); !errors.Is(err, codectypes.ErrLoginRefused) || !strings.Contains(err.Error(), "not today") {
		t.Fatalf("a denial: %v", err)
	}
}

// authenticate composes verify, select and the source, names the credential
// after the pool entry and stamps the identity type; a source failure
// refuses without leaking its cause to the client.
func TestAuthenticateComposesTheThreeSteps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "password"), []byte("s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	set := newCredentialSet([]CredentialSetEntry{{Hash: hashOf("hlc_a"), Subject: "alice", Lane: "l", ExpiresAt: time.Now().Add(time.Hour)}})
	cfg := &Config{
		Credentials: &CredentialSourcesConfig{SecretFiles: &SecretFilesSourceConfig{Dir: dir}},
		Listeners: []ListenerConfig{{Name: "l", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
			Identity: &IdentityConfig{Type: IdentityHoopCredential},
			Credentials: &CredentialsConfig{
				Pool:   []CredentialEntry{{Name: "rw", Source: SourceSecretFiles, User: "app", PasswordFile: "password"}, {Name: "broken", Source: SourceSecretFiles, User: "x", PasswordFile: "absent"}},
				Select: SelectConfig{Rules: []CredentialRule{{When: map[string]string{"identity.subject": "alice"}, Credential: "rw"}}},
			}}},
	}
	sources, err := buildCredentialSources(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := buildLaneAuth(cfg, cfg.Listeners[0], "l", nil, set, sources)
	if err != nil {
		t.Fatal(err)
	}
	if a.scram == nil {
		t.Fatal("a hoop_credential lane offers no SCRAM lookup")
	}
	id, cred, err := a.authenticate(context.Background(), "alice", "hlc_a")
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "alice" || id.Attributes[identityTypeAttribute] != IdentityHoopCredential {
		t.Errorf("identity = %+v", id)
	}
	if cred.Name != "rw" || cred.User != "app" || cred.Password != "s3cret" {
		t.Errorf("credential = %+v", cred)
	}
	_, _, err = a.authenticate(context.Background(), "alice", "nope")
	if !errors.Is(err, codectypes.ErrLoginRefused) {
		t.Fatalf("a wrong key: %v", err)
	}

	a.rules = []CredentialRule{{Credential: "broken"}}
	_, _, err = a.authenticate(context.Background(), "alice", "hlc_a")
	if !errors.Is(err, codectypes.ErrLoginRefused) || strings.Contains(err.Error(), "absent") {
		t.Fatalf("a source failure = %v; want a refusal that names no file", err)
	}

	// The libhoop form carries the same facts.
	a.rules = []CredentialRule{{Credential: "rw"}}
	cid, ucred, err := a.authenticator()(context.Background(), "alice", "hlc_a")
	if err != nil || cid.Subject != "alice" || ucred.User != "app" || ucred.Password != "s3cret" {
		t.Fatalf("authenticator: %+v %+v %v", cid, ucred, err)
	}
	// And the http form: a bearer in, basic out for a user and password.
	res, err := a.swap(context.Background(), "Bearer hlc_a")
	if err != nil || res.User != "app" || res.Password != "s3cret" || res.Bearer || res.Identity.Subject != "alice" {
		t.Fatalf("swap: %+v %v", res, err)
	}
}

// The boundary rule refuses the statements that change the session's
// identity under a swapped credential, and nothing else.
func TestCredentialBoundaryRule(t *testing.T) {
	rules, err := policy.NewRules([]policy.Rule{credentialBoundaryRule})
	if err != nil {
		t.Fatal(err)
	}
	denied := []string{"SET ROLE admin", "  set role admin;", "SET LOCAL ROLE x", "SET SESSION ROLE x", "RESET ROLE", "SET SESSION AUTHORIZATION bob",
		"EXECUTE AS USER = 'dbo'", "EXECUTE AS LOGIN = 'sa'", "REVERT", "/* hi */ SET ROLE x", "-- c\nSET ROLE x"}
	allowed := []string{"SELECT revert FROM t", "SET search_path TO x", "SET statement_timeout = 1", "EXECUTE as_proc", "SELECT * FROM roles WHERE name = 'SET ROLE'", "RESET search_path"}
	for _, text := range denied {
		if v := rules.Evaluate(inspect.Statement{Text: text, Protocol: inspect.Postgres}); !v.Denied {
			t.Errorf("%q was allowed", text)
		}
	}
	for _, text := range allowed {
		if v := rules.Evaluate(inspect.Statement{Text: text, Protocol: inspect.Postgres}); v.Denied {
			t.Errorf("%q was denied: %s", text, v.Message)
		}
	}
	// Prepended outside observe, so a dry-run lane still enforces it.
	obs := policy.Observe{Evaluator: policy.Chain{}}
	ev := credentialBoundary(obs, inspect.Postgres)
	if v := ev.Evaluate(inspect.Statement{Text: "SET ROLE x", Protocol: inspect.Postgres}); !v.Denied {
		t.Error("the boundary did not enforce on an observing lane")
	}
	if ev := credentialBoundary(nil, inspect.HTTP); ev != nil {
		t.Error("an http lane got a boundary rule")
	}
}

// Authorization is read whichever way the client sent the key.
func TestSplitAuthorization(t *testing.T) {
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:hlc_a"))
	for in, want := range map[string][2]string{
		"Bearer hlc_a":  {"", "hlc_a"},
		"bearer  hlc_a": {"", "hlc_a"},
		basic:           {"alice", "hlc_a"},
		"hlc_a":         {"", "hlc_a"},
		"":              {"", ""},
		"Basic !!":      {"", ""},
	} {
		u, s := splitAuthorization(in)
		if u != want[0] || s != want[1] {
			t.Errorf("%q = (%q, %q), want %v", in, u, s, want)
		}
	}
}
