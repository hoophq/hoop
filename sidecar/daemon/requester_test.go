package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// filingRequester decodes the requester object of one filing, and reports
// whether the body carried the key at all.
func filingRequester(t *testing.T, raw []byte) (map[string]string, bool) {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("the filing is not a JSON object: %v\n%s", err, raw)
	}
	r, ok := body["requester"]
	if !ok {
		return nil, false
	}
	var out map[string]string
	if err := json.Unmarshal(r, &out); err != nil {
		t.Fatalf("requester is not an object of strings: %v\n%s", err, r)
	}
	return out, true
}

// fileAs files one statement with id on the context, the way the gate puts
// the caller there, and returns what the plane received.
func fileAs(t *testing.T, id *session.Identity) reviewCall {
	t.Helper()
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`)
	ctx := context.Background()
	if id != nil {
		ctx = session.ContextWithIdentity(ctx, *id)
	}
	if _, err := cp.reviewer("payments", "payments-approvers").File(ctx, "DELETE FROM users"); err != nil {
		t.Fatalf("File: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("the plane saw %d requests, want 1", len(*calls))
	}
	return (*calls)[0]
}

// The requester names the caller in four strings, and nothing else of the
// identity leaves: groups and attributes are policy inputs, and copying them
// to the plane would put more of the caller in its database and in Slack
// than a reviewer needs.
func TestAFiledReviewCarriesTheRequester(t *testing.T) {
	got := fileAs(t, &session.Identity{
		Subject:    "alice",
		Email:      "alice@example.com",
		PeerAddr:   "10.0.0.7:51234",
		Method:     session.MethodGoogleIdentity,
		Groups:     []string{"sre-secret-group"},
		Attributes: map[string]string{"google.sub": "1234567890"},
	})

	r, ok := filingRequester(t, got.raw)
	if !ok {
		t.Fatalf("the filing carried no requester:\n%s", got.raw)
	}
	want := map[string]string{
		"subject":   "alice",
		"email":     "alice@example.com",
		"peer_addr": "10.0.0.7:51234",
		"method":    "google_identity",
	}
	if len(r) != len(want) {
		t.Errorf("requester = %v, want exactly %v", r, want)
	}
	for k, v := range want {
		if r[k] != v {
			t.Errorf("requester[%q] = %q, want %q", k, r[k], v)
		}
	}
	for _, leak := range []string{"groups", "attributes", "sre-secret-group", "google.sub", "1234567890"} {
		if strings.Contains(string(got.raw), leak) {
			t.Errorf("the filing carries %q:\n%s", leak, got.raw)
		}
	}
	// The fields the plane authorizes against are untouched by the new key.
	if got.listen != "payments" || got.rule != "payments-approvers" {
		t.Errorf("the filing named listener %q rule %q", got.listen, got.rule)
	}
}

// A plane older than this sidecar must receive the body it always did. A
// filing with no caller, or one naming nothing at all, is byte for byte what
// the map[string]string encoding produced, HTML escaping included.
func TestAFilingWithNoCallerIsTheOldBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   *session.Identity
	}{
		{"no caller on the context", nil},
		{"a caller naming nothing", &session.Identity{Method: session.MethodIdentityHeader}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := fileAs(t, tc.id)
			old, err := json.Marshal(map[string]string{
				"listener_name": "payments",
				"approval_rule": "payments-approvers",
				"payload":       base64.StdEncoding.EncodeToString([]byte("DELETE FROM users")),
			})
			if err != nil {
				t.Fatalf("encoding the old body: %v", err)
			}
			if string(got.raw) != string(old) {
				t.Errorf("the filing changed shape:\n got %s\nwant %s", got.raw, old)
			}
			want := `{"approval_rule":"payments-approvers","listener_name":"payments","payload":"` +
				base64.StdEncoding.EncodeToString([]byte("DELETE FROM users")) + `"}`
			if string(got.raw) != want {
				t.Errorf("the filing is\n %s\nwant\n %s", got.raw, want)
			}
		})
	}

	// Names that json escapes: the struct must escape them as the map did.
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"PENDING"}}`)
	if _, err := cp.fileReview(context.Background(), "a<b>&c", "r\u2028\"x", "SELECT '<&>'"); err != nil {
		t.Fatalf("fileReview: %v", err)
	}
	old, _ := json.Marshal(map[string]string{
		"listener_name": "a<b>&c",
		"approval_rule": "r\u2028\"x",
		"payload":       base64.StdEncoding.EncodeToString([]byte("SELECT '<&>'")),
	})
	if string((*calls)[0].raw) != string(old) {
		t.Errorf("escaping changed:\n got %s\nwant %s", (*calls)[0].raw, old)
	}
}

// The method is how a reviewer weighs the name, so it is never left for the
// plane to guess. Nobody named is an address and nothing more, whatever the
// identity's method said; a name with no method is unspecified, never the
// strongest source by default.
func TestARequesterWithNoMethodIsNamedByWhatItHas(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   session.Identity
		want map[string]string
	}{
		{"anonymous", session.Identity{PeerAddr: "10.0.0.7:51234"},
			map[string]string{"peer_addr": "10.0.0.7:51234", "method": "peer_address"}},
		{"anonymous with a method", session.Identity{PeerAddr: "10.0.0.7:51234", Method: session.MethodSSHCertificate},
			map[string]string{"peer_addr": "10.0.0.7:51234", "method": "peer_address"}},
		{"named with no method", session.Identity{Subject: "alice", PeerAddr: "10.0.0.7:51234"},
			map[string]string{"subject": "alice", "peer_addr": "10.0.0.7:51234", "method": "unspecified"}},
		{"email only", session.Identity{Email: "alice@example.com", Method: session.MethodIdentityHeader},
			map[string]string{"email": "alice@example.com", "method": "identity_header"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, ok := filingRequester(t, fileAs(t, &tc.id).raw)
			if !ok {
				t.Fatal("the filing carried no requester")
			}
			if len(r) != len(tc.want) {
				t.Errorf("requester = %v, want %v", r, tc.want)
			}
			for k, v := range tc.want {
				if r[k] != v {
					t.Errorf("requester[%q] = %q, want %q", k, r[k], v)
				}
			}
		})
	}
}

// A header or a StartupMessage can carry a name of any length and any bytes.
// Each value leaves bounded and as valid UTF-8, cut between runes, so one
// pathological client cannot inflate every filing, and the plane never gets
// half a character to render.
func TestRequesterValuesAreBounded(t *testing.T) {
	for _, tc := range []struct {
		name, subject string
	}{
		{"1 MiB of three-byte runes", strings.Repeat("\u20ac", (1<<20)/3)},
		{"invalid UTF-8", strings.Repeat("\xff\x80a", 400)},
		{"a rune across the bound", strings.Repeat("a", maxRequesterField-1) + "\u20ac"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, ok := filingRequester(t, fileAs(t, &session.Identity{
				Subject: tc.subject, Email: tc.subject, Method: session.MethodDatabaseUser,
			}).raw)
			if !ok {
				t.Fatal("the filing carried no requester")
			}
			for _, k := range []string{"subject", "email"} {
				v := r[k]
				if v == "" || len(v) > maxRequesterField {
					t.Errorf("%s is %d bytes, want 1..%d", k, len(v), maxRequesterField)
				}
				if !utf8.ValidString(v) {
					t.Errorf("%s is not valid UTF-8", k)
				}
				if !strings.HasPrefix(tc.subject, v) && utf8.ValidString(tc.subject) {
					t.Errorf("%s is not a prefix of the value sent", k)
				}
			}
		})
	}
	if got := boundRequesterField("alice"); got != "alice" {
		t.Errorf("a short value was changed to %q", got)
	}
}

// End to end through the real build path: the lane's analyzer inside the
// policy buildPolicy assembles, behind the gate a lane opens per connection.
// The caller the gate judged the statement under is the one the plane is
// told filed it.
func TestAHoldingLaneFilesTheRequester(t *testing.T) {
	// REJECTED so the hold ends on the filing.
	cp, calls := reviewPlane(t, http.StatusCreated,
		`{"forward":false,"review":{"id":"9f97","status":"REJECTED"}}`)
	deps := &analyzerDeps{
		cfg:      &AnalyzerConfig{Provider: "stub", Model: "m"},
		provider: highRiskProvider{},
		cp:       cp,
	}
	la := laneBlock()
	la.HighRisk = "require_review"
	la.ApprovalRule = "payments-approvers"
	pol, err := buildPolicy("payments", GuardrailsConfig{}, la, nil, nil, deps)
	if err != nil {
		t.Fatalf("buildPolicy: %v", err)
	}

	sess := session.New(inspect.Postgres, session.Identity{
		Subject:  "alice@example.com",
		PeerAddr: "10.0.0.7:51234",
		Method:   session.MethodIdentityHeader,
		Groups:   []string{"payments-admins"},
	})
	sess.Connection = "payments"
	g, err := gate.NewStatementGate(sess, gate.Config{Policy: pol})
	if err != nil {
		t.Fatalf("NewStatementGate: %v", err)
	}
	d := g.EvaluateStatement(context.Background(), inspect.Statement{
		Protocol:  inspect.Postgres,
		Direction: inspect.FromClient,
		Text:      "DELETE FROM users",
		Operation: inspect.OpDelete,
		Tables:    []string{"users"},
	})
	if d.Allowed {
		t.Fatal("a held statement was forwarded")
	}
	if len(*calls) != 1 {
		t.Fatalf("the plane saw %d requests, want 1", len(*calls))
	}
	r, ok := filingRequester(t, (*calls)[0].raw)
	if !ok {
		t.Fatalf("the filing carried no requester:\n%s", (*calls)[0].raw)
	}
	if r["subject"] != "alice@example.com" || r["method"] != "identity_header" || r["peer_addr"] != "10.0.0.7:51234" {
		t.Errorf("requester = %v, want alice@example.com via identity_header from 10.0.0.7:51234", r)
	}
}

// A grpc RPC names its caller from the identity header, else from a client
// certificate, else nobody, and says which. The header value goes in as
// sent, untrimmed, as it always has: the subject an existing trail and Rego
// policy key on must not move.
func TestGRPCCallerIdentityNamesItsMethod(t *testing.T) {
	spiffe, _ := url.Parse("spiffe://example.org/ns/prod/sa/billing")
	withCert := func(c *x509.Certificate) *http.Request {
		return &http.Request{
			RemoteAddr: "10.0.0.7:51234",
			TLS:        &tls.ConnectionState{PeerCertificates: []*x509.Certificate{c}},
		}
	}
	for _, tc := range []struct {
		name    string
		header  string
		req     *http.Request
		subject string
		method  session.IdentityMethod
	}{
		{"header", "alice@example.com", &http.Request{RemoteAddr: "10.0.0.7:51234"},
			"alice@example.com", session.MethodIdentityHeader},
		{"header untrimmed", " alice ", &http.Request{RemoteAddr: "10.0.0.7:51234"},
			" alice ", session.MethodIdentityHeader},
		{"header wins over a certificate", "alice", withCert(&x509.Certificate{Subject: pkix.Name{CommonName: "svc"}}),
			"alice", session.MethodIdentityHeader},
		{"certificate common name", "", withCert(&x509.Certificate{Subject: pkix.Name{CommonName: "svc"}}),
			"svc", session.MethodTLSClientCertificate},
		{"certificate URI SAN", "", withCert(&x509.Certificate{URIs: []*url.URL{spiffe}}),
			spiffe.String(), session.MethodTLSClientCertificate},
		{"neither", "", &http.Request{RemoteAddr: "10.0.0.7:51234"}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := grpcCallerIdentity(tc.header, tc.req)
			if id.Subject != tc.subject || id.Method != tc.method {
				t.Errorf("identity = %q/%q, want %q/%q", id.Subject, id.Method, tc.subject, tc.method)
			}
			if id.PeerAddr != "10.0.0.7:51234" {
				t.Errorf("peer = %q", id.PeerAddr)
			}
		})
	}
}

// A certificate the listener's CA signed says so, whichever field names the
// user: libhoop checked it before the connection opened.
func TestCertIdentityNamesTheSSHCertificate(t *testing.T) {
	principals := []string{"sre", "oncall"}
	extensions := map[string]string{"email": "alice@example.com", "permit-pty": ""}

	id := certIdentity(nil, "10.0.0.1:2222", "alice", principals, extensions)
	if id.Subject != "alice" || id.Method != session.MethodSSHCertificate || id.PeerAddr != "10.0.0.1:2222" {
		t.Errorf("default mapping = %+v, want alice via ssh_certificate from 10.0.0.1:2222", id)
	}

	id = certIdentity(&SSHIdentityConfig{
		Subject: identitySourcePrincipals, Email: "extensions.email", Attributes: []string{"permit-pty"},
	}, "10.0.0.1:2222", "key-4711", principals, extensions)
	if id.Subject != "sre" || id.Email != "alice@example.com" || id.Method != session.MethodSSHCertificate {
		t.Errorf("configured mapping = %+v, want sre / alice@example.com via ssh_certificate", id)
	}
	// The mapping is sshIdentity's, unchanged: a flag extension reads true.
	if v := id.Attributes["permit-pty"]; v != "true" {
		t.Errorf("attributes = %v, want the flag extension as true", id.Attributes)
	}

	// The refusal path is unchanged: a certificate naming nobody is still
	// refused, whatever the method says.
	if r := sshIdentityRefusal(nil, certIdentity(nil, "10.0.0.1:2222", "", nil, nil)); r == nil {
		t.Error("a certificate naming nobody was admitted")
	}
}
