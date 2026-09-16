package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/session"
	codecssh "github.com/hoophq/libhoop/v2/codec/ssh"
)

// sshTestSink collects what a lane wrote, so a test can assert both what
// reached the trail and what did not.
type sshTestSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (s *sshTestSink) Write(_ context.Context, ev audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

func (s *sshTestSink) Close() error { return nil }

func (s *sshTestSink) snapshot() []audit.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.Event(nil), s.events...)
}

// sshTestConn builds one connection's decision state: a real gate over real
// rules, with the callbacks the endpoint would call.
//
// It tests DECISIONS, which is this side of the seam. The handshake, the
// channel dispatch and the spawn are libhoop's mechanics and are tested
// there; reaching them from here would need an SSH client, and that is
// golang.org/x/crypto — a second direct dependency for a module whose whole
// shape is one.
func sshTestConn(t *testing.T, rules []policy.Rule, destinations []string) (*sshConnState, *sshTestSink) {
	t.Helper()
	var evaluator policy.Evaluator
	if len(rules) > 0 {
		r, err := policy.NewRules(rules)
		if err != nil {
			t.Fatalf("rules: %v", err)
		}
		evaluator = r
	}
	dests, err := parseSSHDestinations(destinations)
	if err != nil {
		t.Fatalf("destinations: %v", err)
	}

	sess := session.New(inspect.SSH, session.Identity{Subject: "alice@example.com"})
	sess.Connection = "prod-endpoint"
	sink := &sshTestSink{}
	g, err := gate.NewStatementGate(sess, gate.Config{
		Protocol: inspect.SSH,
		Policy:   evaluator,
		Audit:    sink,
	})
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	return &sshConnState{
		gate:         g,
		stmts:        sshStatements{},
		destinations: dests,
		log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, sink
}

func TestSSHExecIsAllowedAndRecorded(t *testing.T) {
	c, sink := sshTestConn(t, nil, nil)
	if r := c.exec(context.Background(), "uptime"); r != nil {
		t.Fatalf("an unruled lane refused a command: %v", r)
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("wrote %d events, want one statement", len(events))
	}
	ev := events[0]
	if ev.Kind != audit.KindStatement || !ev.Allowed {
		t.Errorf("kind = %q allowed = %v, want an allowed statement", ev.Kind, ev.Allowed)
	}
	if ev.Operation != inspect.OpExecLine || ev.Statement != "uptime" {
		t.Errorf("recorded %q as %q, want the command in full as exec_line",
			ev.Statement, ev.Operation)
	}
	if ev.Protocol != inspect.SSH {
		t.Errorf("protocol = %q", ev.Protocol)
	}
}

// The rule's own message is what the user sees. A generic "denied" would
// leave someone guessing which rule fired and why.
func TestSSHExecDenialCarriesTheRuleMessage(t *testing.T) {
	c, sink := sshTestConn(t, []policy.Rule{{
		Name:       "no-credential-reads",
		Type:       policy.MatchPattern,
		Pattern:    `(cat|less)\s+[^|;&]*/etc/shadow`,
		Operations: []inspect.Operation{inspect.OpExecLine},
		Message:    "reading credential material is not permitted",
	}}, nil)

	r := c.exec(context.Background(), "cat /etc/shadow")
	if r == nil {
		t.Fatal("a guardrail did not deny the command it matches")
	}
	if !strings.Contains(r.String(), "reading credential material is not permitted") {
		t.Errorf("refusal = %q, want the rule's message", r.String())
	}

	var violations int
	for _, ev := range sink.snapshot() {
		if ev.Kind == audit.KindViolation {
			violations++
			if ev.Rule != "no-credential-reads" {
				t.Errorf("violation names rule %q", ev.Rule)
			}
		}
	}
	if violations != 1 {
		t.Errorf("recorded %d violations, want one", violations)
	}
}

// A rule scoped to one operation must not fire on another: the text of an
// env_set is a variable name, and of an sftp_* a path.
func TestSSHRulesAreScopedByOperation(t *testing.T) {
	c, _ := sshTestConn(t, []policy.Rule{{
		Name:       "no-preload-injection",
		Type:       policy.MatchPattern,
		Pattern:    `^(LD_PRELOAD|LD_LIBRARY_PATH)$`,
		Operations: []inspect.Operation{inspect.OpEnvSet},
		Message:    "this environment variable is not permitted",
	}}, nil)

	if r := c.envSet(context.Background(), "LD_PRELOAD", "/tmp/evil.so"); r == nil {
		t.Error("the env rule did not fire on the variable it names")
	}
	if r := c.envSet(context.Background(), "TERM", "xterm"); r != nil {
		t.Errorf("the env rule fired on an unrelated variable: %v", r)
	}
	// The same text as a command is a different operation and must not match.
	if r := c.exec(context.Background(), "LD_PRELOAD"); r != nil {
		t.Errorf("an env_set rule fired on an exec_line: %v", r)
	}
}

// The value travels beside the name so the trail records what was set, and
// the NAME is what a rule matches.
func TestSSHEnvValueIsRecordedBesideTheName(t *testing.T) {
	c, sink := sshTestConn(t, nil, nil)
	if r := c.envSet(context.Background(), "TERM", "xterm-256color"); r != nil {
		t.Fatal(r)
	}
	ev := sink.snapshot()[0]
	if ev.Statement != "TERM" {
		t.Errorf("statement = %q, want the variable name", ev.Statement)
	}
	if ev.Metadata[MetadataSSHEnvValue] != "xterm-256color" {
		t.Errorf("metadata = %v, want the value beside the name", ev.Metadata)
	}
}

// A rename has two ends and both are questions. Evaluating only the source
// would let a move INTO a fenced directory past a rule written to fence it.
func TestSSHSFTPEvaluatesBothEndsOfARename(t *testing.T) {
	c, sink := sshTestConn(t, []policy.Rule{{
		Name:       "no-writes-to-etc",
		Type:       policy.MatchPattern,
		Pattern:    `^/etc/`,
		Operations: []inspect.Operation{inspect.OpSFTPRename},
		Message:    "/etc is not writable through this connection",
	}}, nil)

	r := c.sftpOp(context.Background(), inspect.OpSFTPRename, "/tmp/x", "/etc/passwd")
	if r == nil {
		t.Fatal("a rename INTO a fenced directory was allowed; only the source was checked")
	}

	var ends []string
	for _, ev := range sink.snapshot() {
		if ev.Metadata[MetadataSSHPathEnd] != "" {
			ends = append(ends, ev.Metadata[MetadataSSHPathEnd])
		}
	}
	if len(ends) != 2 || ends[0] != sshPathEndSource || ends[1] != sshPathEndTarget {
		t.Errorf("recorded ends %v, want source then target so the rows can be told apart", ends)
	}
}

// A single-path operation carries no target and no end marker, so the common
// case does not pay for the two-path shape.
func TestSSHSFTPSinglePathHasNoTargetMetadata(t *testing.T) {
	c, sink := sshTestConn(t, nil, nil)
	if r := c.sftpOp(context.Background(), inspect.OpSFTPRead, "/srv/data.csv", ""); r != nil {
		t.Fatal(r)
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("wrote %d events for one operation", len(events))
	}
	ev := events[0]
	if ev.Metadata[MetadataSSHTarget] != "" || ev.Metadata[MetadataSSHPathEnd] != "" {
		t.Errorf("a one-path operation carried two-path metadata: %v", ev.Metadata)
	}
	if ev.Direction != inspect.FromServer {
		t.Errorf("direction = %q, want a read to travel from the server", ev.Direction)
	}
	if ev.Metadata[MetadataSSHPath] != "/srv/data.csv" {
		t.Errorf("path metadata = %q", ev.Metadata[MetadataSSHPath])
	}
}

// The forward check reads the address that WILL be dialled, and the list is
// default-deny.
func TestSSHForwardChecksTheResolvedAddress(t *testing.T) {
	c, sink := sshTestConn(t, nil, []string{"10.0.0.0/8:2222"})
	dest := codecssh.Destination{Host: "endpoint.internal", Port: 2222}

	if r := c.forward(context.Background(), dest, netip.MustParseAddrPort("10.1.2.3:2222")); r != nil {
		t.Fatalf("an allowed destination was refused: %v", r)
	}
	// The NAME is the same; only the resolved address differs. Checking the
	// name would have allowed this.
	r := c.forward(context.Background(), dest, netip.MustParseAddrPort("203.0.113.9:2222"))
	if r == nil {
		t.Fatal("a destination outside the list was carried")
	}
	if !strings.Contains(r.String(), "endpoint.internal:2222") {
		t.Errorf("refusal = %q, want it to name the destination", r.String())
	}

	var denials int
	for _, ev := range sink.snapshot() {
		if ev.Kind == audit.KindActivity && ev.Metadata[audit.MetadataActivity] == "forward_denied" {
			denials++
			if ev.Metadata["dialing"] != "203.0.113.9:2222" {
				t.Errorf("the record does not carry the dialled address: %v", ev.Metadata)
			}
		}
	}
	if denials != 1 {
		t.Errorf("recorded %d forward denials, want one", denials)
	}
}

// Absent and empty both deny, so a lane that forgot the key carries nothing.
func TestSSHForwardWithNoDestinationsDenies(t *testing.T) {
	c, _ := sshTestConn(t, nil, nil)
	r := c.forward(context.Background(),
		codecssh.Destination{Host: "anything", Port: 22},
		netip.MustParseAddrPort("10.0.0.1:22"))
	if r == nil {
		t.Error("a lane with no destinations_allowed carried a forward")
	}
}

// A forward gets no statement. Its bytes are relayed blind, so a statement
// built from a destination would look like content policy and be none.
func TestSSHForwardWritesNoStatement(t *testing.T) {
	c, sink := sshTestConn(t, nil, []string{"any"})
	if r := c.forward(context.Background(),
		codecssh.Destination{Host: "db", Port: 5432},
		netip.MustParseAddrPort("10.0.0.1:5432")); r != nil {
		t.Fatal(r)
	}
	for _, ev := range sink.snapshot() {
		if ev.Kind == audit.KindStatement || ev.Kind == audit.KindViolation {
			t.Errorf("a forward produced a %s event", ev.Kind)
		}
	}
}

// There is no default account and no fallback to the sidecar's own user:
// either would hand a session an account nobody chose for it.
func TestSSHSessionAccountRefusesWhenNothingResolves(t *testing.T) {
	if _, r := resolveSessionAccount(""); r == nil {
		t.Error("a connection that named no login was admitted")
	}
	if _, r := resolveSessionAccount("no-such-account-here-4711"); r == nil {
		t.Error("a login that is not an account was admitted")
	}
}

func TestSSHCertificateFieldsMapToIdentity(t *testing.T) {
	const keyID = "alice@example.com"
	principals := []string{"sre", "oncall"}
	extensions := map[string]string{"permit-pty": "", "hoop-team": "platform,payments"}

	if got := certScalar(identitySourceKeyID, keyID, principals, extensions); got != keyID {
		t.Errorf("key_id = %q", got)
	}
	// A scalar slot holds one name; joining the list would produce a
	// subject no audit query matches.
	if got := certScalar(identitySourcePrincipals, keyID, principals, extensions); got != "sre" {
		t.Errorf("principals as a scalar = %q, want the first", got)
	}
	if got := certList(identitySourcePrincipals, keyID, principals, extensions); len(got) != 2 {
		t.Errorf("principals as a list = %v", got)
	}
	if got := certList("extensions.hoop-team", keyID, principals, extensions); len(got) != 2 || got[0] != "platform" {
		t.Errorf("extension list = %v", got)
	}
	// An OpenSSH flag extension carries an EMPTY value. Rendered as absence
	// it would read as "not permitted" to a policy.
	if v, ok := certExtension(extensions, "permit-pty"); !ok || v != "true" {
		t.Errorf("permit-pty = %q ok=%v, want true", v, ok)
	}
	if _, ok := certExtension(extensions, "permit-agent-forwarding"); ok {
		t.Error("an absent extension reported as present")
	}
}

// A certificate that names nobody is REFUSED, not admitted as "anonymous".
//
// The field a lane reads can be empty on a perfectly valid certificate:
// ssh-keygen -I "" leaves the key id blank, and an extension a CA has not
// rolled out yet is absent. What follows is not only a thin audit record.
// PolicyContext omits subject, email and groups when they are empty, so a
// Rego rule reading input.context.subject sees an undefined key, the rule
// does not fire, and a command a named certificate is denied runs for this
// one.
func TestSSHCertificateWithNoIdentityIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *SSHIdentityConfig
		says string
	}{
		{"the default lane reads the key id", nil, identitySourceKeyID},
		{"a lane that maps an extension names it",
			&SSHIdentityConfig{Subject: "extensions.login@hoop.dev"},
			"extensions.login@hoop.dev"},
		{"a lane that maps an email names both fields",
			&SSHIdentityConfig{Subject: "key_id", Email: "extensions.mail@hoop.dev"},
			"key_id or extensions.mail@hoop.dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := sshIdentityRefusal(tc.cfg, session.Identity{PeerAddr: "10.0.0.1:2222"})
			if r == nil {
				t.Fatal("a certificate carrying no identity was admitted")
			}
			// The message has to say which field to ask the CA to fill.
			// "refused" with no field names is a support ticket.
			if !strings.Contains(r.String(), tc.says) {
				t.Errorf("refusal = %q, want it to name %q", r.String(), tc.says)
			}
		})
	}
}

// Either field names a principal, and a lane may map only the one its CA
// fills in. A peer address is not a principal: it names a machine, and the
// question the trail answers is which person ran the command.
func TestSSHIdentityAdmittedWhenEitherFieldIsSet(t *testing.T) {
	if r := sshIdentityRefusal(nil, session.Identity{Subject: "alice@example.com"}); r != nil {
		t.Errorf("a named certificate was refused: %v", r)
	}
	if r := sshIdentityRefusal(nil, session.Identity{Email: "alice@example.com"}); r != nil {
		t.Errorf("a certificate carrying only an email was refused: %v", r)
	}
	if r := sshIdentityRefusal(nil, session.Identity{
		Groups: []string{"sre"}, PeerAddr: "10.0.0.1:2222"}); r == nil {
		t.Error("groups and a peer address were accepted as an identity")
	}
}

// A bastion admits no session capability, so nothing is ever spawned. That
// leaves the login name as the only account it could resolve, on a host
// where that name has no reason to exist — so requiring one would refuse
// every jump through a correctly configured bastion.
//
// It is only reachable through a topology test, which is why it is pinned
// here: the unit path admits a session, and the bug is in the branch that
// never does.
func TestSSHBastionAdmitsAConnectionWithNoLocalAccount(t *testing.T) {
	srv, err := buildSSHTestServer(t, &SSHConfig{
		CapabilitiesAllowed: &Capabilities{},
		DestinationsAllowed: []string{"10.0.0.0/8:2222"},
	})
	if err != nil {
		t.Fatalf("a bastion lane did not build: %v", err)
	}
	defer srv.Close()

	// The zero RunAs is what crosses the seam, and it refuses a session on
	// the libhoop side. That is correct here and not a gap: a bastion has no
	// session, and the forward path never reads the account.
	if err := codecssh.SFTPServableBy(codecssh.RunAs{}); err == nil {
		t.Error("an unresolved account is servable; a bastion would serve files")
	}
}

// The end-hop keeps the opposite rule: it spawns, so a login that resolves
// to no account is refused rather than run as somebody.
func TestSSHEndHopStillRequiresAnAccount(t *testing.T) {
	if _, r := resolveSessionAccount("no-such-account-here-4711"); r == nil {
		t.Error("an end-hop admitted a login that is not an account")
	}
}
