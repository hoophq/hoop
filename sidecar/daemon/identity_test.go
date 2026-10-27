package daemon

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
		{"unknown source", sources, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"consul"}],"select":{"rules":[{"credential":"a"}]}}`,
			`source "consul" is not secret_files, gcp_impersonation, x509, vault, gcp_secret_manager, spiffe or kerberos`},
		{"vault without addr", `"credentials":{"vault":{"auth":{"type":"kubernetes","role":"r"}}},`, `"protocol":"postgres","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"vault","path":"secret/data/x"}],"select":{"rules":[{"credential":"a"}]}}`,
			"credentials.vault: addr is required"},
		{"vault dynamic path on http", `"credentials":{"vault":{"addr":"https://vault","auth":{"type":"kubernetes","role":"r"}}},`, `"protocol":"http","identity":{"type":"hoop_credential"},"credentials":{"pool":[{"name":"a","source":"vault","path":"database/creds/x"}],"select":{"rules":[{"credential":"a"}]}}`,
			"a database engine path mints a user per session"},
		{"vault dynamic path on clickhouse", `"credentials":{"vault":{"addr":"https://vault","auth":{"type":"kubernetes","role":"r"}}},`, `"protocol":"clickhouse","identity":{"type":"hoop_credential"},"downstream_tls":{},"credentials":{"pool":[{"name":"a","source":"vault","path":"database/creds/x"}],"select":{"rules":[{"credential":"a"}]}}`,
			"a clickhouse listener terminates the login in its codec, which cannot revoke the lease"},
		{"secret manager without a secret", `"credentials":{"gcp_secret_manager":{}},`, `"protocol":"http","identity":{"type":"hoop_credential"},"credentials":{"pool":[{"name":"a","source":"gcp_secret_manager"}],"select":{"rules":[{"credential":"a"}]}}`,
			"secret is required"},
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
// Config.Validate accepts a pooled lane; the host refuses it when the build
// did not link sidecar/auth, naming the module, and still accepts a
// document with none of the keys.
func TestLaneAuthIsRefusedWithoutTheAuthModule(t *testing.T) {
	if registeredAuthEngine() != nil {
		t.Skip("this test binary links the auth engine")
	}
	sock := filepath.Join(t.TempDir(), "l.sock")
	const pool = `"credentials":{"pool":[{"name":"ro","source":"secret_files","user":"app","password_file":"ro/password"}],"select":{"rules":[{"credential":"ro"}]}}`
	for _, tc := range []struct{ name, top, listener, key string }{
		{"identity and pool", `"credentials":{"secret_files":{"dir":"/run/creds"}},`,
			`"protocol":"postgres","network":"unix","listen":"` + sock + `","upstream":"h:1","identity":{"type":"none"},` + pool, "identity"},
		{"pool alone", `"credentials":{"secret_files":{"dir":"/run/creds"}},`,
			`"protocol":"postgres","network":"unix","listen":"` + sock + `","upstream":"h:1",` + pool, "credentials"},
		{"passthrough", ``,
			`"protocol":"oracle","network":"unix","listen":"` + sock + `","upstream":"h:1","identity":{"type":"none"},"passthrough_users":["system"]`, "identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadConfig(writeConfig(t, `{`+tc.top+`"listeners":[{`+tc.listener+`}]}`))
			if err != nil {
				t.Fatalf("the config decoder refused a pooled lane: %v", err)
			}
			_, err = Validate(cfg, nil)
			if err == nil || !strings.Contains(err.Error(), tc.key+" needs the credential-swap runtime") || !strings.Contains(err.Error(), authModule) {
				t.Fatalf("Validate = %v, want the auth module named", err)
			}
			if err := checkAuthEngine(cfg); err == nil {
				t.Fatal("checkAuthEngine accepted a pooled lane with no engine")
			}
		})
	}
	// Every key the check reads, on a listener the decoder otherwise refuses.
	for _, l := range []ListenerConfig{
		{PassthroughUsers: []string{"admin"}}, {Propagate: &PropagateConfig{ApplicationName: "x"}},
	} {
		if err := checkAuthEngine(&Config{Listeners: []ListenerConfig{l}}); err == nil {
			t.Errorf("checkAuthEngine accepted %+v", l)
		}
	}
	plain := Config{Listeners: []ListenerConfig{{Name: "l", Protocol: "postgres", Listen: ":1", Upstream: "h:1", Context: map[string]string{"env": "demo"}}}}
	if err := checkAuthEngine(&plain); err != nil {
		t.Fatalf("a lane with no pool was refused: %v", err)
	}
	if _, err := Validate(&plain, nil); err != nil {
		t.Fatalf("Validate refused a plain lane: %v", err)
	}
}

// The header lists every verifier and source this build links, so the plane
// serves a lane only to a build that can run it, and CheckServable asks for
// the entry a lane needs. The built-in entries come and go with the auth
// engine; the field caps are in every build.
func TestTheHeaderListsVerifiersAndSources(t *testing.T) {
	caps := SidecarCapabilities()
	for _, want := range []string{"identity", "credentials", "credential_set"} {
		if !slices.Contains(caps, want) {
			t.Errorf("the header lacks %q: %v", want, caps)
		}
	}
	builtins := []string{"identity:none", "identity:kubernetes", "identity:hoop_credential", "identity:jwt",
		"credentials.secret_files", "credentials.gcp_impersonation", "credentials.x509", "credentials.vault", "credentials.gcp_secret_manager"}
	_, linked := RegisteredIdentitySource(IdentityNone)
	for _, entry := range builtins {
		if slices.Contains(caps, entry) != linked {
			t.Errorf("the header lists %q = %v, want %v (auth engine linked: %v)", entry, !linked, linked, linked)
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
	if _, linked := RegisteredIdentitySource(IdentitySPIFFE); !linked {
		if err == nil || !strings.Contains(err.Error(), `"identity:spiffe"`) {
			t.Fatalf("CheckServable = %v, want the spiffe verifier required", err)
		}
	}
	// A build with no engine has none of the built-ins, so the plane holds
	// a pooled lane back from it.
	cfg.Listeners[0].Identity = &IdentityConfig{Type: IdentityHoopCredential}
	err = CheckServable(cfg, Handshake{Capabilities: caps})
	if (err == nil) != linked {
		t.Fatalf("CheckServable = %v with the engine linked = %v", err, linked)
	}
}

// recordingSet is the engine's set as the reloader sees it: what was last
// swapped in.
type recordingSet struct{ entries []CredentialSetEntry }

func (s *recordingSet) Swap(entries []CredentialSetEntry) { s.entries = entries }
func (s *recordingSet) Size() int                         { return len(s.entries) }

func (s *recordingSet) has(lane, hash string) bool {
	for _, e := range s.entries {
		if e.Lane == lane && e.Hash == hash {
			return true
		}
	}
	return false
}

// The reloader swaps the set from every document the plane sends, whatever
// the document's fate, and the set never reaches the baseline: a mint is
// not a restart.
func TestAReloadSwapsTheCredentialSet(t *testing.T) {
	const base = `{"listeners":[{"name":"l","protocol":"postgres","listen":"127.0.0.1:0","upstream":"h:1"}]}`
	rl, buf := testReloader(t, base)
	set := &recordingSet{}
	rl.credentials = set
	entry, _ := json.Marshal([]CredentialSetEntry{{Hash: strings.Repeat("ab", 32), Subject: "a", Lane: "l", ExpiresAt: time.Now().Add(time.Hour)}})
	withKey := strings.TrimSuffix(base, "}") + `,"credential_set":` + string(entry) + `}`
	if out := applyWith(rl, buf, withKey); out == reloadRestart || out == reloadRefused {
		t.Fatalf("outcome = %v: %s", out, buf.String())
	}
	if !set.has("l", strings.Repeat("ab", 32)) {
		t.Fatal("the reload did not put the served key in force")
	}
	if !strings.Contains(buf.String(), "credential set swapped") {
		t.Fatalf("the swap was not logged: %s", buf.String())
	}
	// A document that needs a restart still carries the keys in force:
	// here one that revokes the key and moves the listen address.
	moved := strings.Replace(base, "127.0.0.1:0", "127.0.0.1:1", 1)
	if out := applyWith(rl, buf, moved); out != reloadRestart {
		t.Fatalf("outcome = %v, want a restart", out)
	}
	if set.Size() != 0 {
		t.Fatal("a revoked key survived the reload")
	}
	cfg := Config{Listeners: []ListenerConfig{{Name: "l", Protocol: "postgres", Listen: ":1", Upstream: "h:1"}}}
	before, _ := BaselineDoc(&cfg)
	cfg.CredentialSet = []CredentialSetEntry{{Hash: strings.Repeat("ab", 32), Lane: "l"}}
	after, _ := BaselineDoc(&cfg)
	if string(before) != string(after) {
		t.Fatalf("the credential set reached the baseline:\n%s\n%s", before, after)
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
		u, s := SplitAuthorization(in)
		if u != want[0] || s != want[1] {
			t.Errorf("%q = (%q, %q), want %v", in, u, s, want)
		}
	}
}
