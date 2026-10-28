package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// With the engine linked the built-ins are in the header, Validate and Run
// accept a pooled lane, and a nested verifier this binary did not link is
// still refused on the host while the decoder accepts it for the plane.
func TestTheEngineLinksTheBuiltInsAndRefusesAnUnlinkedVerifier(t *testing.T) {
	caps := daemon.SidecarCapabilities()
	for _, want := range []string{"identity:none", "identity:kubernetes", "identity:jwt",
		"credentials.secret_files", "credentials.gcp_impersonation", "credentials.x509", "credentials.vault", "credentials.gcp_secret_manager"} {
		if !slices.Contains(caps, want) {
			t.Errorf("the header lacks %q: %v", want, caps)
		}
	}
	sock := filepath.Join(t.TempDir(), "l.sock")
	cfg, err := daemon.LoadConfigBytes([]byte(`{"credentials":{"secret_files":{"dir":"/run/creds"}},"listeners":[{"protocol":"postgres","network":"unix","listen":"` + sock + `","upstream":"h:1",
	  "credentials":{"pool":[{"name":"ro","source":"secret_files","user":"app","password_file":"ro/password"}],"select":{"rules":[{"credential":"ro"}]}},"passthrough_users":["postgres"],"propagate":{"application_name":"x"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	lanes, err := daemon.Validate(cfg, nil)
	if err != nil || len(lanes) != 1 {
		t.Fatalf("Validate = %v, %v; want the pooled lane accepted", lanes, err)
	}

	cfg, err = daemon.LoadConfigBytes([]byte(`{"credentials":{"spiffe":{}},"listeners":[{"protocol":"postgres","network":"unix","listen":"` + sock + `","upstream":"h:1",
	  "identity":{"type":"spiffe"},"credentials":{"pool":[{"name":"svc","source":"spiffe","spiffe_id":"spiffe://example.org/sa/x"}],"select":{"rules":[{"credential":"svc"}]}}}]}`))
	if err != nil {
		t.Fatalf("the config decoder refused a spiffe lane: %v", err)
	}
	if _, ok := daemon.RegisteredIdentitySource(daemon.IdentitySPIFFE); ok {
		t.Skip("this test binary links the spiffe module")
	}
	_, err = daemon.Validate(cfg, nil)
	if err == nil || !strings.Contains(err.Error(), `identity type "spiffe" is not linked`) || !strings.Contains(err.Error(), "sidecar/identity/spiffe") {
		t.Fatalf("Validate = %v, want the unlinked verifier and its module named", err)
	}
}

// Rules are first match wins over the identity and the lane context; no
// match is a refusal, and an OPA lane reads the credential the policy named.
func TestCredentialSelection(t *testing.T) {
	a := &laneAuth{
		lane: "appdb",
		pool: []daemon.CredentialEntry{{Name: "rw"}, {Name: "ro"}, {Name: "svc"}},
		rules: []daemon.CredentialRule{
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
	b := &laneAuth{pool: []daemon.CredentialEntry{{Name: "pod"}}, rules: []daemon.CredentialRule{{Credential: "pod"}}}
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
	c := &laneAuth{lane: "appdb", proto: inspect.Postgres, pool: []daemon.CredentialEntry{{Name: "rw"}},
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
	f := startIssuer(t)
	token := f.token(t, "rsa-1", map[string]any{"sub": "alice", "email": "alice@example.com"})
	cfg := &daemon.Config{
		Credentials: &daemon.CredentialSourcesConfig{SecretFiles: &daemon.SecretFilesSourceConfig{Dir: dir}},
		Listeners: []daemon.ListenerConfig{{Name: "l", Protocol: "postgres", Listen: ":1", Upstream: "h:1",
			Identity: &daemon.IdentityConfig{Type: daemon.IdentityJWT, Issuer: f.srv.URL},
			Credentials: &daemon.CredentialsConfig{
				Pool:   []daemon.CredentialEntry{{Name: "rw", Source: daemon.SourceSecretFiles, User: "app", PasswordFile: "password"}, {Name: "broken", Source: daemon.SourceSecretFiles, User: "x", PasswordFile: "absent"}},
				Select: daemon.SelectConfig{Rules: []daemon.CredentialRule{{When: map[string]string{"identity.subject": "alice"}, Credential: "rw"}}},
			}}},
	}
	sources, err := daemon.BuildCredentialSources(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	built, err := buildLaneAuth(cfg.Listeners[0], "l", daemon.AuthDeps{Roots: f.roots()}, sources)
	if err != nil {
		t.Fatal(err)
	}
	a := built.(*laneAuth)
	id, cred, err := a.Authenticate(context.Background(), "alice", token)
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "alice" || id.Attributes[daemon.IdentityTypeAttribute] != daemon.IdentityJWT {
		t.Errorf("identity = %+v", id)
	}
	if cred.Name != "rw" || cred.User != "app" || cred.Password != "s3cret" {
		t.Errorf("credential = %+v", cred)
	}
	_, _, err = a.Authenticate(context.Background(), "alice", "nope")
	if !errors.Is(err, codectypes.ErrLoginRefused) {
		t.Fatalf("a wrong token: %v", err)
	}

	a.rules = []daemon.CredentialRule{{Credential: "broken"}}
	_, _, err = a.Authenticate(context.Background(), "alice", token)
	if !errors.Is(err, codectypes.ErrLoginRefused) || strings.Contains(err.Error(), "absent") {
		t.Fatalf("a source failure = %v; want a refusal that names no file", err)
	}

	// The libhoop form carries the same facts.
	a.rules = []daemon.CredentialRule{{Credential: "rw"}}
	cid, ucred, err := a.Authenticator()(context.Background(), "alice", token)
	if err != nil || cid.Subject != "alice" || ucred.User != "app" || ucred.Password != "s3cret" {
		t.Fatalf("authenticator: %+v %+v %v", cid, ucred, err)
	}
	// And the http form: a bearer in, basic out for a user and password.
	res, err := a.Swap(context.Background(), "Bearer "+token)
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
