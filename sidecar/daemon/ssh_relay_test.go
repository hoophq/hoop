package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codecssh "github.com/hoophq/libhoop/v2/codec/ssh"
)

// writeRelayKey writes a usable private key, with the permissions ssh(1)
// insists on. Generated with the STANDARD LIBRARY: this module has exactly
// one dependency and must not grow a second one, even from a test file.
func writeRelayKey(t *testing.T, dir, name string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(
		&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeRelayPublicKey writes the PUBLIC half, which is the file an operator
// reaches for by mistake: it sits beside the private one with a name three
// characters longer, and it is the half that belongs in the target's
// authorized_keys.
func writeRelayPublicKey(t *testing.T, dir, name string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(part)))
		blob = append(blob, part...)
	}
	path := filepath.Join(dir, name)
	line := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " alice\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeKnownHosts writes a known_hosts file naming one host.
func writeKnownHosts(t *testing.T, dir string, hosts ...string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(part)))
		blob = append(blob, part...)
	}
	encoded := base64.StdEncoding.EncodeToString(blob)
	var b strings.Builder
	for _, h := range hosts {
		b.WriteString(h + " ssh-ed25519 " + encoded + "\n")
	}
	path := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// relayFixture is a minimal working relay block plus the paths behind it.
type relayFixture struct {
	dir        string
	key        string
	knownHosts string
}

func newRelayFixture(t *testing.T) relayFixture {
	t.Helper()
	dir := t.TempDir()
	return relayFixture{
		dir:        dir,
		key:        writeRelayKey(t, dir, "prod"),
		knownHosts: writeKnownHosts(t, dir, "db-01.prod:22"),
	}
}

func (f relayFixture) config(targets map[string]*SSHRelayTarget) *SSHRelayConfig {
	return &SSHRelayConfig{KnownHosts: f.knownHosts, Targets: targets}
}

// laneWith builds an ssh lane carrying a relay block, for validate.
func laneWith(f relayFixture, relay *SSHRelayConfig, caps *Capabilities) *SSHConfig {
	return &SSHConfig{
		HostKey:             f.key,
		TrustedCA:           f.key,
		CapabilitiesAllowed: caps,
		Relay:               relay,
	}
}

func capsOf(names ...string) *Capabilities {
	c := Capabilities(names)
	return &c
}

// problemsFor runs validation and joins the result, so a test can assert on
// the sentence an operator actually reads.
func problemsFor(sc *SSHConfig) string {
	return strings.Join(sc.Relay.validate("prod-bastion", sc.resolveCapabilities()), "\n")
}

func TestRelayConfigRequiresKnownHostsAndTargets(t *testing.T) {
	f := newRelayFixture(t)

	got := problemsFor(laneWith(f, &SSHRelayConfig{}, nil))
	for _, want := range []string{"known_hosts is required", "targets is empty"} {
		if !strings.Contains(got, want) {
			t.Fatalf("problems %q do not mention %q", got, want)
		}
	}
}

// TestRelayCredentialPairs is the pair the ADR checks together: two sources,
// never a preference and a fallback.
func TestRelayCredentialPairs(t *testing.T) {
	f := newRelayFixture(t)

	t.Run("both is an error", func(t *testing.T) {
		got := problemsFor(laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {PrivateKey: f.key, AgentIdentity: true},
		}), nil))
		if !strings.Contains(got, "two credential sources") {
			t.Fatalf("problems %q do not refuse the pair", got)
		}
	})

	t.Run("neither is an error", func(t *testing.T) {
		got := problemsFor(laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {},
		}), nil))
		if !strings.Contains(got, "has no credential") {
			t.Fatalf("problems %q admit a target with no credential", got)
		}
	})

	t.Run("identities alone is enough", func(t *testing.T) {
		rc := f.config(map[string]*SSHRelayTarget{"db-01.prod": {}})
		rc.Identities = t.TempDir()
		if got := problemsFor(laneWith(f, rc, nil)); got != "" {
			t.Fatalf("a target under identities was refused: %q", got)
		}
	})
}

// TestRelayPrivateKeyIsCheckedAtLoad covers the mistakes that would otherwise
// surface as an opaque authentication failure against every host the key was
// meant to reach.
func TestRelayPrivateKeyIsCheckedAtLoad(t *testing.T) {
	f := newRelayFixture(t)

	t.Run("a public key is named", func(t *testing.T) {
		pub := writeRelayPublicKey(t, f.dir, "prod.pub")
		got := problemsFor(laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {PrivateKey: pub},
		}), nil))
		if !strings.Contains(got, "PUBLIC key") {
			t.Fatalf("problems %q do not name the mistake: %s", got, pub)
		}
	})

	t.Run("readable by group or other", func(t *testing.T) {
		loose := writeRelayKey(t, f.dir, "loose")
		if err := os.Chmod(loose, 0o644); err != nil {
			t.Fatal(err)
		}
		got := problemsFor(laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {PrivateKey: loose},
		}), nil))
		if !strings.Contains(got, "readable by group or other") {
			t.Fatalf("problems %q admit a world-readable key", got)
		}
	})

	t.Run("missing", func(t *testing.T) {
		got := problemsFor(laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {PrivateKey: filepath.Join(f.dir, "absent")},
		}), nil))
		if !strings.Contains(got, "no such file") {
			t.Fatalf("problems %q admit a missing key", got)
		}
	})
}

// TestRelayForwardingNeedsItsBound is the other pair, and it is deliberately
// unlike destinations_allowed: there, absent means "no forwards"; here, it is
// a contradiction.
func TestRelayForwardingNeedsItsBound(t *testing.T) {
	f := newRelayFixture(t)
	caps := capsOf("exec", "shell", "pty", "env")

	t.Run("local_forward with no list", func(t *testing.T) {
		got := problemsFor(laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {
				PrivateKey:          f.key,
				CapabilitiesAllowed: capsOf("exec", "local_forward"),
			},
		}), caps))
		if !strings.Contains(got, "names no forwards_allowed") {
			t.Fatalf("problems %q admit an unbounded tunnel", got)
		}
	})

	t.Run("a list that bounds nothing", func(t *testing.T) {
		got := problemsFor(laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {
				PrivateKey:          f.key,
				CapabilitiesAllowed: capsOf("exec"),
				ForwardsAllowed:     []string{"127.0.0.1:5432"},
			},
		}), caps))
		if !strings.Contains(got, "does not admit local_forward") {
			t.Fatalf("problems %q accept a list that bounds nothing", got)
		}
	})

	t.Run("both, which is the working shape", func(t *testing.T) {
		got := problemsFor(laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {
				PrivateKey:          f.key,
				CapabilitiesAllowed: capsOf("exec", "local_forward"),
				ForwardsAllowed:     []string{"127.0.0.1:5432"},
			},
		}), caps))
		if got != "" {
			t.Fatalf("a correct target was refused: %q", got)
		}
	})
}

// TestRelayCapabilitiesNarrowAndNeverWiden pins the tri-state and the subset
// rule, including the one exemption.
func TestRelayCapabilitiesNarrowAndNeverWiden(t *testing.T) {
	f := newRelayFixture(t)
	listener := capsOf("exec", "env")

	cases := []struct {
		name   string
		target *Capabilities
		want   string
	}{
		{"sftp is not delivered", capsOf("sftp"), "opaque subsystem"},
		{"remote_forward is not delivered", capsOf("remote_forward"), "reverse channel"},
		{"agent_forward points at agent_identity", capsOf("agent_forward"), "agent_identity"},
		{"a typo is a typo", capsOf("exce"), "is not a capability"},
		{"widening is refused", capsOf("exec", "shell"), "listener does not admit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := problemsFor(laneWith(f, f.config(map[string]*SSHRelayTarget{
				"db-01.prod": {PrivateKey: f.key, CapabilitiesAllowed: tc.target},
			}), listener))
			if !strings.Contains(got, tc.want) {
				t.Fatalf("problems %q do not mention %q", got, tc.want)
			}
		})
	}

	t.Run("local_forward is exempt from the subset rule", func(t *testing.T) {
		got := problemsFor(laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {
				PrivateKey:          f.key,
				CapabilitiesAllowed: capsOf("exec", "local_forward"),
				ForwardsAllowed:     []string{"127.0.0.1:5432"},
			},
		}), listener))
		if got != "" {
			t.Fatalf("local_forward was checked against the listener's list: %q", got)
		}
	})
}

// TestRelayTargetPrecedenceIsTotal is the property that keeps a connection
// from being decided by map iteration.
func TestRelayTargetPrecedenceIsTotal(t *testing.T) {
	f := newRelayFixture(t)
	sc := laneWith(f, f.config(map[string]*SSHRelayTarget{
		"db-01.prod":  {PrivateKey: f.key},
		"*.prod":      {PrivateKey: f.key},
		"10.0.0.0/8":  {PrivateKey: f.key},
		"10.0.0.0/24": {PrivateKey: f.key},
		"10.0.0.5":    {PrivateKey: f.key},
	}), nil)
	if got := problemsFor(sc); got != "" {
		t.Fatalf("the fixture does not validate: %q", got)
	}
	relay, err := buildSSHRelay("prod-bastion", sc, discardLog())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	want := []string{"db-01.prod", "*.prod", "10.0.0.5", "10.0.0.0/24", "10.0.0.0/8"}
	var got []string
	for _, tr := range relay.targets {
		got = append(got, tr.key)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("precedence order = %v, want %v", got, want)
	}

	// And the order is what matching actually uses.
	cases := []struct {
		host    string
		dialing string
		want    string
	}{
		{"db-01.prod", "10.0.0.5:22", "db-01.prod"},
		{"web-01.prod", "10.0.0.5:22", "*.prod"},
		{"anything", "10.0.0.5:22", "10.0.0.5"},
		{"anything", "10.0.0.9:22", "10.0.0.0/24"},
		{"anything", "10.9.0.9:22", "10.0.0.0/8"},
		{"anything", "192.168.0.1:22", ""},
	}
	for _, tc := range cases {
		ap := netip.MustParseAddrPort(tc.dialing)
		hit := relay.match(tc.host, ap)
		switch {
		case tc.want == "" && hit != nil:
			t.Fatalf("%s/%s matched %q, want blind carriage", tc.host, tc.dialing, hit.key)
		case tc.want != "" && hit == nil:
			t.Fatalf("%s/%s matched nothing, want %q", tc.host, tc.dialing, tc.want)
		case tc.want != "" && hit.key != tc.want:
			t.Fatalf("%s/%s matched %q, want %q", tc.host, tc.dialing, hit.key, tc.want)
		}
	}
}

// TestRelayNameKeysAreMatchedBeforeResolution is the reason names outrank
// addresses: a name key must not be able to widen reachability.
func TestRelayNameKeysAreMatchedBeforeResolution(t *testing.T) {
	f := newRelayFixture(t)
	sc := laneWith(f, f.config(map[string]*SSHRelayTarget{
		"db-01.prod": {PrivateKey: f.key},
	}), nil)
	relay, err := buildSSHRelay("prod-bastion", sc, discardLog())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// The same address under a name nobody wrote down is carried blind, so
	// the map cannot be used to reach a host the destinations list covers
	// but the operator never named.
	if hit := relay.match("db-99.prod", netip.MustParseAddrPort("10.0.0.5:22")); hit != nil {
		t.Fatalf("a name key matched %q", hit.key)
	}
	if hit := relay.match("db-01.prod", netip.MustParseAddrPort("10.0.0.5:22")); hit == nil {
		t.Fatal("the name key did not match the name it was written for")
	}
}

// TestRelayIdentitiesAreReadPerSession covers the overlay: an enrolled
// subject gets their own key, an unenrolled one gets the target's shared key
// or a refusal, and a file that is present but unusable refuses rather than
// falling through.
func TestRelayIdentitiesAreReadPerSession(t *testing.T) {
	f := newRelayFixture(t)
	ids := t.TempDir()
	writeRelayKey(t, ids, "alice@example.com")
	writeRelayPublicKey(t, ids, "carol@example.com")
	loose := writeRelayKey(t, ids, "dave@example.com")
	if err := os.Chmod(loose, 0o644); err != nil {
		t.Fatal(err)
	}

	rc := f.config(map[string]*SSHRelayTarget{
		"db-01.prod":  {},                  // enrolled subjects only
		"app-01.prod": {PrivateKey: f.key}, // with a shared fallback
	})
	rc.Identities = ids
	sc := laneWith(f, rc, nil)
	if got := problemsFor(sc); got != "" {
		t.Fatalf("the fixture does not validate: %q", got)
	}
	relay, err := buildSSHRelay("prod-bastion", sc, discardLog())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	strict := relay.byKey("db-01.prod")
	shared := relay.byKey("app-01.prod")

	t.Run("an enrolled subject uses their own key", func(t *testing.T) {
		key, source, fellBack, err := relay.credentialFor(strict, "alice@example.com")
		if err != nil {
			t.Fatalf("alice was refused: %v", err)
		}
		if fellBack {
			t.Fatal("an enrolled subject was reported as a fallback")
		}
		if !strings.Contains(source, "alice@example.com") || len(key) == 0 {
			t.Fatalf("source %q is not alice's key", source)
		}
	})

	t.Run("enrolment takes effect without a restart", func(t *testing.T) {
		// The property the per-session read buys, and the reason the old
		// map is gone: a key dropped in after the listener started is used
		// by the next connection.
		if _, _, _, err := relay.credentialFor(strict, "erin@example.com"); err == nil {
			t.Fatal("erin resolved before she was enrolled")
		}
		writeRelayKey(t, ids, "erin@example.com")
		_, source, fellBack, err := relay.credentialFor(strict, "erin@example.com")
		if err != nil {
			t.Fatalf("erin was refused after enrolment: %v", err)
		}
		if fellBack || !strings.Contains(source, "erin@example.com") {
			t.Fatalf("erin did not get her own key (source %q, fellBack %v)", source, fellBack)
		}
	})

	t.Run("a file that is present but unusable REFUSES", func(t *testing.T) {
		// carol's file is a PUBLIC key and dave's is world-readable. Both
		// were deliberately enrolled, so serving them on the shared key
		// would be exactly the silent downgrade this design refuses.
		for _, subject := range []string{"carol@example.com", "dave@example.com"} {
			if _, _, _, err := relay.credentialFor(shared, subject); err == nil {
				t.Fatalf("%s was served on the shared key despite an enrolled file", subject)
			}
		}
	})

	t.Run("no shared key means refuse, never substitute", func(t *testing.T) {
		_, _, _, err := relay.credentialFor(strict, "eve@example.com")
		if err == nil {
			t.Fatal("an unenrolled subject was admitted as somebody else")
		}
		if !strings.Contains(err.Error(), "not enrolled") {
			t.Fatalf("the refusal does not say why: %v", err)
		}
	})

	t.Run("a shared key is a fallback, and it is reported", func(t *testing.T) {
		_, source, fellBack, err := relay.credentialFor(shared, "eve@example.com")
		if err != nil {
			t.Fatalf("the shared key was not used: %v", err)
		}
		if !fellBack {
			t.Fatal("a fallback was not reported; silent degradation is the whole risk")
		}
		if source != f.key {
			t.Fatalf("source = %q, want the target's shared key", source)
		}
	})
}

// TestRelayIdentitySubjectCannotEscapeTheDirectory is the guard that replaces
// what enumerating the directory used to give for free.
//
// The key is now read per session, so a subject the CERTIFICATE carries
// becomes a filesystem path. A subject is a filename and nothing else:
// anything that could reach outside the directory an operator enrolled people
// in is refused before the path is built.
func TestRelayIdentitySubjectCannotEscapeTheDirectory(t *testing.T) {
	ids := t.TempDir()
	outside := filepath.Dir(ids)
	writeRelayKey(t, outside, "secret-key")

	for _, subject := range []string{
		"../secret-key",
		"../../etc/passwd",
		"/etc/hoop-inspect/keys/prod",
		"sub/../../secret-key",
		"a/b",
		`a\b`,
		".",
		"..",
		"",
		"   ",
		".hidden",
	} {
		t.Run(subject, func(t *testing.T) {
			if _, err := identityPathFor(ids, subject); err == nil {
				t.Fatalf("subject %q was accepted as a path inside %s", subject, ids)
			}
			// And the read path refuses it too, not only the helper.
			if _, _, err := readIdentity(ids, subject); err == nil {
				t.Fatalf("subject %q resolved to a readable identity", subject)
			}
		})
	}

	// A plain subject still works, so the guard is not simply refusing
	// everything.
	writeRelayKey(t, ids, "alice@example.com")
	got, err := identityPathFor(ids, "alice@example.com")
	if err != nil {
		t.Fatalf("an ordinary subject was refused: %v", err)
	}
	if got != filepath.Join(ids, "alice@example.com") {
		t.Fatalf("identityPathFor = %q", got)
	}
}

// TestRelayFallbackIsNotReportedWithoutAnOverlay keeps the warning meaningful:
// a lane with no identities directory is not degrading when it uses the only
// credential it has.
func TestRelayFallbackIsNotReportedWithoutAnOverlay(t *testing.T) {
	f := newRelayFixture(t)
	sc := laneWith(f, f.config(map[string]*SSHRelayTarget{
		"db-01.prod": {PrivateKey: f.key},
	}), nil)
	relay, err := buildSSHRelay("prod-bastion", sc, discardLog())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	_, _, fellBack, err := relay.credentialFor(relay.byKey("db-01.prod"), "alice@example.com")
	if err != nil {
		t.Fatalf("credential: %v", err)
	}
	if fellBack {
		t.Fatal("a lane with no overlay reported a fallback")
	}
}

// TestRelayForwardsAllowedMatchesWhatTheClientNamed pins the difference from
// destinations_allowed: the TARGET resolves these, so a name is matched as a
// name and a network can only cover a literal address.
func TestRelayForwardsAllowedMatchesWhatTheClientNamed(t *testing.T) {
	cases := []struct {
		entry string
		host  string
		port  int
		want  bool
	}{
		{"127.0.0.1:5432", "127.0.0.1", 5432, true},
		{"127.0.0.1:5432", "127.0.0.1", 6379, false},
		{"127.0.0.1:5432", "localhost", 5432, false},
		{"localhost:5432", "localhost", 5432, true},
		{"localhost:5432", "LOCALHOST", 5432, true},
		{"10.0.0.0/8:5432", "10.1.2.3", 5432, true},
		{"10.0.0.0/8:5432", "10.1.2.3", 6379, false},
		{"10.0.0.0/8", "10.1.2.3", 6379, true},
		// A NAME cannot be matched against a network here: it is resolved
		// on the target, by the target, so this side cannot say where it
		// lands — and guessing with a local resolver would check one answer
		// and connect to another.
		{"10.0.0.0/8:5432", "db.internal", 5432, false},
	}
	for _, tc := range cases {
		d, err := parseSSHForward(tc.entry)
		if err != nil {
			t.Fatalf("%q: %v", tc.entry, err)
		}
		if got := d.Allows(tc.host, tc.port); got != tc.want {
			t.Fatalf("%q allows %s:%d = %v, want %v", tc.entry, tc.host, tc.port, got, tc.want)
		}
	}
}

// TestRelayForwardsAllowedRefusesTheWildcard keeps the uninspected exception
// bounded by construction.
func TestRelayForwardsAllowedRefusesTheWildcard(t *testing.T) {
	if _, err := parseSSHForward("any"); err == nil {
		t.Fatal(`"any" was accepted as a forward destination`)
	}
	if _, err := parseSSHForward("127.0.0.1"); err == nil {
		t.Fatal("a host with no port was accepted")
	}
}

// TestRelayTargetKeyRefusesTheWildcard is the same argument one level up:
// terminating everything reachable would make every forward a session this
// listener decrypts.
func TestRelayTargetKeyRefusesTheWildcard(t *testing.T) {
	if _, err := parseSSHTargetKey("any"); err == nil {
		t.Fatal(`"any" was accepted as a target key`)
	}
}

// TestRelayMatchesTheRootLabel keeps a fully qualified name from being a way
// out of inspection.
//
// ssh(1) passes the name through to the direct-tcpip request exactly as it
// was typed, and "host.prod." is the same host to DNS, to ssh and to
// known_hosts. Matching the raw string made one trailing dot miss every name
// and glob target, so the forward was carried BLIND: the same host, no
// statements, no guardrails, no masking, and only the destination in the
// trail — one character away from the spelling the operator wrote their
// targets against.
func TestRelayMatchesTheRootLabel(t *testing.T) {
	dialing := netip.MustParseAddrPort("10.0.0.5:22")

	for _, key := range []string{"db-01.prod", "*.prod"} {
		target, err := parseSSHTargetKey(key)
		if err != nil {
			t.Fatalf("%q: %v", key, err)
		}
		for _, typed := range []string{"db-01.prod", "db-01.prod.", "DB-01.PROD."} {
			if !target.matches(typed, dialing) {
				t.Errorf("target %q does not cover %q, so it is carried blind", key, typed)
			}
		}
	}

	// A bare dot is not a spelling of anything, and must not become the
	// empty string and match a glob that way.
	target, err := parseSSHTargetKey("*.prod")
	if err != nil {
		t.Fatal(err)
	}
	for _, typed := range []string{"db-01.prod..", "other.dev", "."} {
		if target.matches(typed, dialing) {
			t.Errorf("target %q wrongly covers %q", "*.prod", typed)
		}
	}
}

// TestRelayWarnsWhenAddressesAreNotCovered states the gap between the two
// vocabularies a lane writes destinations in.
//
// destinations_allowed admits by ADDRESS; targets are usually written by
// NAME, because a name is what the client types. A client that types the
// address instead matches no name target, passes destinations_allowed on the
// resolved address, and is carried blind to the same host. It is a warning
// rather than a refusal: whether an uncovered destination should be carried
// at all is ADR-0021's model, and a config that works today must not stop
// starting on an upgrade.
func TestRelayWarnsWhenAddressesAreNotCovered(t *testing.T) {
	nameOnly := []*sshRelayTarget{mustTargetKey(t, "*.prod")}

	t.Run("a network no address target covers", func(t *testing.T) {
		got := uninspectedByAddress([]string{"172.31.78.0/24:22"}, nameOnly)
		if len(got) != 1 {
			t.Fatalf("expected one warning, got %v", got)
		}
		if !strings.Contains(got[0], "CARRIED BLIND") {
			t.Fatalf("the warning does not say what is lost: %q", got[0])
		}
	})

	t.Run("any is covered by nothing", func(t *testing.T) {
		if got := uninspectedByAddress([]string{"any"}, nameOnly); len(got) != 1 {
			t.Fatalf("expected one warning for %q, got %v", "any", got)
		}
	})

	t.Run("a network target covering it is silent", func(t *testing.T) {
		covered := []*sshRelayTarget{
			mustTargetKey(t, "*.prod"),
			mustTargetKey(t, "172.31.78.0/24:22"),
		}
		if got := uninspectedByAddress([]string{"172.31.78.0/24:22"}, covered); len(got) != 0 {
			t.Fatalf("a covered network still warned: %v", got)
		}
	})

	t.Run("a wider target covers a narrower destination", func(t *testing.T) {
		covered := []*sshRelayTarget{mustTargetKey(t, "10.0.0.0/8")}
		if got := uninspectedByAddress([]string{"10.1.2.0/24"}, covered); len(got) != 0 {
			t.Fatalf("a contained network still warned: %v", got)
		}
	})

	t.Run("a narrower target does NOT cover a wider destination", func(t *testing.T) {
		partial := []*sshRelayTarget{mustTargetKey(t, "10.1.2.0/24")}
		if got := uninspectedByAddress([]string{"10.0.0.0/8"}, partial); len(got) != 1 {
			t.Fatalf("a partially covered network was treated as covered: %v", got)
		}
	})

	t.Run("a relay with no targets says nothing", func(t *testing.T) {
		if got := uninspectedByAddress([]string{"any"}, nil); len(got) != 0 {
			t.Fatalf("a lane with no relay warned: %v", got)
		}
	})
}

// mustTargetKey parses a target key or fails the test.
func mustTargetKey(t *testing.T, key string) *sshRelayTarget {
	t.Helper()
	target, err := parseSSHTargetKey(key)
	if err != nil {
		t.Fatalf("%q: %v", key, err)
	}
	return target
}

// TestRelayTargetKeyRefusesAMalformedHostPort keeps a typo from turning
// inspection off.
//
// A key that is neither a host, a host:port, an address nor a network used to
// fall through to the EXACT-NAME branch carrying whatever it was. A
// destination host never contains a colon, so "db-01.prod:22:1" became a name
// nothing could equal: it loaded without complaint, matched no destination,
// and every session to that host went down the blind-carry path — no
// statements, no guardrails, no masking, and nothing in the trail saying the
// target was never used. Carrying a forward blind is a choice this schema
// makes visible; it must not also be what a typo does quietly.
func TestRelayTargetKeyRefusesAMalformedHostPort(t *testing.T) {
	for _, key := range []string{
		"db-01.prod:22:1",     // one field too many
		"db-01.prod:notaport", // a port that is not a number
		"db-01.prod:0",        // port zero
		"db-01.prod:22:",      // a trailing colon after a port
	} {
		if got, err := parseSSHTargetKey(key); err == nil {
			t.Errorf("%q was accepted as kind=%v name=%q; nothing can match it",
				key, got.kind, got.glob)
		}
	}

	// And the spellings that ARE meant still work, including the bracketed
	// form the refusal points at.
	for _, key := range []string{
		"db-01.prod",
		"db-01.prod:22",
		"*.prod",
		"10.0.0.5",
		"10.0.0.0/24",
		"[2001:db8::1]:22",
		"2001:db8::1",
	} {
		if _, err := parseSSHTargetKey(key); err != nil {
			t.Errorf("%q is a legitimate key and was refused: %v", key, err)
		}
	}
}

// TestRelayBuilderValidatesWithoutConfigValidate pins the load checks to the
// BUILDER, not to the path that happens to reach it.
//
// Config.Validate runs inside LoadConfigBytes, and a caller that supplies its
// own Loader to Setup builds a Config that never passes through it — the same
// caller setupAnalyzer keeps its own call for. What buildSSHRelay checked on
// its own was strictly less than validate, so that caller could load a
// credential readable by the world, a known_hosts path that is a directory,
// or a relay that terminates nothing.
func TestRelayBuilderValidatesWithoutConfigValidate(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("a private key readable by group or other", func(t *testing.T) {
		f := newRelayFixture(t)
		loose := filepath.Join(t.TempDir(), "id")
		raw, err := os.ReadFile(f.key)
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		if err := os.WriteFile(loose, raw, 0o644); err != nil {
			t.Fatalf("write key: %v", err)
		}
		sc := laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {PrivateKey: loose},
		}), nil)
		if _, err := buildSSHRelay("lane", sc, log); err == nil {
			t.Fatal("a mode 0644 private key was loaded")
		}
	})

	t.Run("a known_hosts path that is a directory", func(t *testing.T) {
		f := newRelayFixture(t)
		rc := f.config(map[string]*SSHRelayTarget{"db-01.prod": {PrivateKey: f.key}})
		rc.KnownHosts = f.dir // a directory, not a file
		if _, err := buildSSHRelay("lane", laneWith(f, rc, nil), log); err == nil {
			t.Fatal("a known_hosts path that is a directory was loaded")
		}
	})

	t.Run("a relay that terminates nothing", func(t *testing.T) {
		f := newRelayFixture(t)
		rc := f.config(nil)
		r, err := buildSSHRelay("lane", laneWith(f, rc, nil), log)
		if err == nil {
			t.Fatalf("a relay with no targets was built: %d targets", len(r.targets))
		}
	})

	t.Run("and a good config still builds", func(t *testing.T) {
		f := newRelayFixture(t)
		sc := laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {PrivateKey: f.key},
		}), nil)
		r, err := buildSSHRelay("lane", sc, log)
		if err != nil {
			t.Fatalf("a valid relay was refused: %v", err)
		}
		if len(r.targets) != 1 {
			t.Fatalf("expected one target, got %d", len(r.targets))
		}
	})
}

// TestRelayHostKeyCheckSpelling pins the three settings and the default.
func TestRelayHostKeyCheckSpelling(t *testing.T) {
	f := newRelayFixture(t)

	t.Run("a bad spelling is refused", func(t *testing.T) {
		rc := f.config(map[string]*SSHRelayTarget{"db-01.prod": {PrivateKey: f.key}})
		rc.HostKeyCheck = "yes"
		if got := problemsFor(laneWith(f, rc, nil)); !strings.Contains(got, "host_key_check") {
			t.Fatalf("problems %q accept a bad spelling", got)
		}
	})

	t.Run("the default is strict", func(t *testing.T) {
		sc := laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {PrivateKey: f.key},
		}), nil)
		relay, err := buildSSHRelay("prod-bastion", sc, discardLog())
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if got := relay.byKey("db-01.prod").hostKeyCheck; got != codecssh.HostKeyStrict {
			t.Fatalf("host_key_check = %q, want %q", got, codecssh.HostKeyStrict)
		}
	})

	t.Run("a target overrides the lane", func(t *testing.T) {
		rc := f.config(map[string]*SSHRelayTarget{
			"db-01.prod":  {PrivateKey: f.key},
			"lab-01.test": {PrivateKey: f.key, HostKeyCheck: "off"},
		})
		sc := laneWith(f, rc, nil)
		if got := problemsFor(sc); got != "" {
			t.Fatalf("the fixture does not validate: %q", got)
		}
		relay, err := buildSSHRelay("prod-bastion", sc, discardLog())
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if got := relay.byKey("lab-01.test").hostKeyCheck; got != codecssh.HostKeyOff {
			t.Fatalf("the target's override was lost: %q", got)
		}
		if got := relay.byKey("db-01.prod").hostKeyCheck; got != codecssh.HostKeyStrict {
			t.Fatalf("the override leaked to another target: %q", got)
		}
	})
}

// TestRelayListenerServesNoSessionOfItsOwn is the consequence that keeps the
// listener's capability list readable as a ceiling.
//
// Without it, the ADR's own example config — a listener admitting shell, pty,
// exec and env so its targets can narrow that — would hand out a shell ON THE
// BASTION: the host holding every target's credential and the plaintext of
// every session behind it.
func TestRelayListenerServesNoSessionOfItsOwn(t *testing.T) {
	f := newRelayFixture(t)
	rc := f.config(map[string]*SSHRelayTarget{"db-01.prod": {PrivateKey: f.key}})
	sc := &SSHConfig{
		CapabilitiesAllowed: capsOf("exec", "shell", "pty", "env"),
		Relay:               rc,
	}
	srv, err := buildSSHTestServer(t, sc)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer srv.Close()

	notes := strings.Join(srv.Notes(), "\n")
	if !strings.Contains(notes, "admits no session capability") {
		t.Fatalf("the bastion admits a session of its own:\n%s", notes)
	}
	if !strings.Contains(notes, "TERMINATES") {
		t.Fatalf("the notes do not say this listener terminates:\n%s", notes)
	}
	// And the ceiling reached the target rather than being dropped.
	if !strings.Contains(notes, "db-01.prod") {
		t.Fatalf("the target is not reported:\n%s", notes)
	}
}

// TestRelayNotesNameTheCost puts the trade in front of an operator running
// -validate, because it is the thing this mode asks them to accept.
func TestRelayNotesNameTheCost(t *testing.T) {
	f := newRelayFixture(t)
	ids := t.TempDir()
	writeRelayKey(t, ids, "alice@example.com")
	rc := f.config(map[string]*SSHRelayTarget{
		"db-01.prod": {
			PrivateKey:          f.key,
			CapabilitiesAllowed: capsOf("exec", "local_forward"),
			ForwardsAllowed:     []string{"127.0.0.1:5432"},
		},
	})
	rc.Identities = ids
	srv, err := buildSSHTestServer(t, &SSHConfig{
		CapabilitiesAllowed: capsOf("exec"),
		Relay:               rc,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer srv.Close()

	notes := strings.Join(srv.Notes(), "\n")
	for _, want := range []string{
		"holds the plaintext of every session",
		"dialled BY THE TARGET",
		"read per session",
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("notes do not mention %q:\n%s", want, notes)
		}
	}
}

// TestRelayUnknownKeyIsRefused keeps a typo inside the relay block from being
// silently dropped, on the block that decides which hosts are inspected.
func TestRelayUnknownKeyIsRefused(t *testing.T) {
	var rc SSHRelayConfig
	err := rc.UnmarshalJSON([]byte(`{"known_hosts":"/tmp/kh","targts":{}}`))
	if err == nil {
		t.Fatal("a misspelled key inside ssh.relay was accepted")
	}

	var target SSHRelayTarget
	if err := target.UnmarshalJSON([]byte(`{"privatekey":"/tmp/k"}`)); err == nil {
		t.Fatal("a misspelled key inside a target was accepted")
	}
	if err := target.UnmarshalJSON([]byte(`{"capabilities_allowed":null}`)); err == nil {
		t.Fatal("capabilities_allowed written with no value was accepted")
	}
}

// TestRelayBlockIsRestartOnly pins the relay block into the baseline
// document, which is what makes a change to it a RESTART under ADR-0014's
// rule-only hot-reload boundary.
//
// It matters because the failure would be silent and in the dangerous
// direction. nonRuleDoc decides what can hot-swap; if the relay block ever
// fell out of it, editing a target's credential or its capability ceiling
// would be reported as applied while the running listener kept enforcing the
// old one — an operator who revoked a target's access and was told it took
// effect. A comment saying "topology is restart-only" would not catch that.
func TestRelayBlockIsRestartOnly(t *testing.T) {
	f := newRelayFixture(t)
	base := func(target *SSHRelayTarget) []byte {
		t.Helper()
		doc, err := BaselineDoc(&Config{
			Listeners: []ListenerConfig{{
				Name:     "prod-bastion",
				Protocol: "ssh",
				Listen:   "0.0.0.0:2222",
				SSH:      laneWith(f, f.config(map[string]*SSHRelayTarget{"db-01.prod": target}), nil),
			}},
		})
		if err != nil {
			t.Fatalf("BaselineDoc: %v", err)
		}
		return doc
	}

	before := base(&SSHRelayTarget{PrivateKey: f.key})
	if !strings.Contains(string(before), "db-01.prod") {
		t.Fatalf("the relay block is not in the baseline, so editing it would hot-swap:\n%s", before)
	}

	// Changing the credential must move the baseline.
	other := writeRelayKey(t, f.dir, "other")
	if after := base(&SSHRelayTarget{PrivateKey: other}); string(after) == string(before) {
		t.Fatal("changing a target's credential did not move the baseline")
	}
	// So must narrowing what it admits.
	narrowed := base(&SSHRelayTarget{PrivateKey: f.key, CapabilitiesAllowed: capsOf("exec")})
	if string(narrowed) == string(before) {
		t.Fatal("changing a target's capability ceiling did not move the baseline")
	}
}

// discardLog is a logger for the build path, which warns about rejected
// identities. A test asserts on the relay it builds, not on the warning.
func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRelayIdentityBehindASymlinkIsLoaded covers the deployment that needs
// the overlay most.
//
// A Kubernetes Secret projects its keys as SYMLINKS into ../data, and a
// symlink's own mode is 0777. Reading the link's mode instead of the file's
// rejected every identity there — so enrolled people silently fell back to a
// shared key or were refused.
func TestRelayIdentityBehindASymlinkIsLoaded(t *testing.T) {
	data := t.TempDir()
	real := writeRelayKey(t, data, "alice@example.com")

	ids := t.TempDir()
	if err := os.Symlink(real, filepath.Join(ids, "alice@example.com")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	key, source, err := readIdentity(ids, "alice@example.com")
	if err != nil {
		t.Fatalf("a symlinked identity was refused: %v", err)
	}
	if len(key) == 0 || source == "" {
		t.Fatalf("the symlinked identity did not load (source %q)", source)
	}
}

// TestRelayEffectiveKnownHostsIsValidated covers the combination the check
// used to miss: a target that relaxes host_key_check while INHERITING the
// lane's file. accept_new writes that file, and the lane was checked under
// the lane's mode, which needs no write at all.
func TestRelayEffectiveKnownHostsIsValidated(t *testing.T) {
	f := newRelayFixture(t)

	// A directory nothing can write into, so the accept_new probe must fail.
	locked := t.TempDir()
	kh := filepath.Join(locked, "known_hosts")
	if err := os.WriteFile(kh, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Skipf("cannot make a read-only directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("running as root, which ignores directory permissions")
	}

	rc := &SSHRelayConfig{
		KnownHosts: kh, // lane default: strict, so no write is needed
		Targets: map[string]*SSHRelayTarget{
			// Relaxes the MODE and inherits the PATH.
			"db-01.prod": {PrivateKey: f.key, HostKeyCheck: "accept_new"},
		},
	}
	got := problemsFor(laneWith(f, rc, nil))
	if !strings.Contains(got, "REPLACEABLE") {
		t.Fatalf("problems %q do not catch accept_new on an unwritable inherited file", got)
	}
}

// TestRelayTargetCapabilityTriState pins the three readings, which are the
// same three the listener's own list has.
func TestRelayTargetCapabilityTriState(t *testing.T) {
	f := newRelayFixture(t)
	listener := capsOf("exec", "env", "shell")

	build := func(t *testing.T, target *SSHRelayTarget) *sshRelayTarget {
		t.Helper()
		sc := laneWith(f, f.config(map[string]*SSHRelayTarget{"db-01.prod": target}), listener)
		if got := problemsFor(sc); got != "" {
			t.Fatalf("the fixture does not validate: %q", got)
		}
		relay, err := buildSSHRelay("prod-bastion", sc, discardLog())
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return relay.byKey("db-01.prod")
	}

	t.Run("absent inherits the listener's list", func(t *testing.T) {
		got := build(t, &SSHRelayTarget{PrivateKey: f.key})
		if joinCapabilityNames(got.caps) != "env, exec, shell" {
			t.Fatalf("inherited %q", joinCapabilityNames(got.caps))
		}
	})

	t.Run("empty admits nothing, and is not an error", func(t *testing.T) {
		got := build(t, &SSHRelayTarget{PrivateKey: f.key, CapabilitiesAllowed: capsOf()})
		if len(got.caps) != 0 {
			t.Fatalf("an empty list admitted %q", joinCapabilityNames(got.caps))
		}
	})

	t.Run("populated admits those and refuses the rest", func(t *testing.T) {
		got := build(t, &SSHRelayTarget{PrivateKey: f.key, CapabilitiesAllowed: capsOf("exec")})
		if joinCapabilityNames(got.caps) != "exec" {
			t.Fatalf("populated %q", joinCapabilityNames(got.caps))
		}
		if got.admits(codecssh.CapShell) {
			t.Fatal("a capability outside the list was admitted")
		}
	})

	t.Run("a listener that admits nothing relayable leaves the target with nothing", func(t *testing.T) {
		// sftp cannot travel over a terminated session, so inheriting a
		// listener that admits only sftp leaves this target admitting
		// nothing. That is the same answer as writing [], reached another
		// way, and it is a configuration rather than an error.
		sc := laneWith(f, f.config(map[string]*SSHRelayTarget{
			"db-01.prod": {PrivateKey: f.key},
		}), capsOf("sftp"))
		if got := problemsFor(sc); got != "" {
			t.Fatalf("inheriting an unrelayable list was refused: %q", got)
		}
	})
}

// TestRelayIPv6TargetIsVerifiableUnderItsName pins the bracket spelling.
// "fd00::5:22" is what "%s:%d" produces and what net.SplitHostPort rejects,
// so an unbracketed name would fail the host-key check on every IPv6 target.
func TestRelayIPv6TargetIsVerifiableUnderItsName(t *testing.T) {
	dest := codecssh.Destination{Host: "fd00::5", Port: 22}
	if got := dest.String(); got != "[fd00::5]:22" {
		t.Fatalf("Destination.String() = %q, want a bracketed IPv6 literal", got)
	}
	if _, _, err := net.SplitHostPort(dest.String()); err != nil {
		t.Fatalf("the name a host key is verified under does not parse: %v", err)
	}
}

// TestRelayHostKeyOffWarnsAtLoad pins the half of "off is loud" that an
// operator sees without running -validate.
//
// The -validate notes report it too, but a pass nobody runs is not a warning.
// This is the one setting whose cost is not local to the lane that sets it:
// HostKeyAlias already moved host identity off the client, so `off` decides
// whether the check exists anywhere rather than which party performs it.
func TestRelayHostKeyOffWarnsAtLoad(t *testing.T) {
	f := newRelayFixture(t)
	rc := f.config(map[string]*SSHRelayTarget{
		"db-01.prod":  {PrivateKey: f.key},
		"lab-01.test": {PrivateKey: f.key, HostKeyCheck: "off"},
	})

	var logged strings.Builder
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	if _, err := buildSSHRelay("prod-bastion", laneWith(f, rc, nil), log); err != nil {
		t.Fatalf("build: %v", err)
	}

	out := logged.String()
	if !strings.Contains(out, "host_key_check=off") {
		t.Fatalf("no warning at load for host_key_check=off:\n%s", out)
	}
	if !strings.Contains(out, "lab-01.test") {
		t.Fatalf("the warning does not name the target:\n%s", out)
	}
	if !strings.Contains(out, "prod-bastion") {
		t.Fatalf("the warning does not name the listener:\n%s", out)
	}
	if strings.Contains(out, "db-01.prod") {
		t.Fatalf("a target on the default strict check was warned about:\n%s", out)
	}
}

// TestRelayCredentialRefusalsDoNotLeakTheDeployment pins the split between
// what the client is told and what the trail records.
//
// A refusal is the one message an unauthorised party can make this listener
// produce on demand. Two things must not ride on it: the identities
// directory, which is where every enrolled key lives, and whether a
// particular subject IS enrolled — that second one turns each refusal into an
// enrolment oracle for anyone holding any valid certificate.
//
// The operator loses nothing, and that half is asserted here too: a refusal
// the client cannot diagnose must be one the trail can.
func TestRelayCredentialRefusalsDoNotLeakTheDeployment(t *testing.T) {
	f := newRelayFixture(t)
	ids := t.TempDir()
	writeRelayPublicKey(t, ids, "carol@example.com") // enrolled, but unusable

	rc := f.config(map[string]*SSHRelayTarget{
		"db-01.prod":  {},                  // enrolled subjects only
		"app-01.prod": {PrivateKey: f.key}, // with a shared fallback
	})
	rc.Identities = ids
	sc := laneWith(f, rc, nil)
	relay, err := buildSSHRelay("prod-bastion", sc, discardLog())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	cases := []struct {
		name    string
		target  string
		subject string
		// clientWants is what the user should be able to act on.
		clientWants string
		// trailWants is what an operator must still be able to read.
		trailWants []string
	}{
		{
			name:        "an unenrolled subject on a target with no shared key",
			target:      "db-01.prod",
			subject:     "eve@example.com",
			clientWants: "do not have access",
			trailWants:  []string{"eve@example.com", ids, "private_key"},
		},
		{
			name:        "an enrolled file that is unusable",
			target:      "app-01.prod",
			subject:     "carol@example.com",
			clientWants: "unusable",
			trailWants:  []string{"carol@example.com", ids},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := relay.credentialFor(relay.byKey(tc.target), tc.subject)
			if err == nil {
				t.Fatal("the credential resolved")
			}

			client := clientMessage(err)
			if !strings.Contains(client, tc.clientWants) {
				t.Fatalf("the client message %q does not say %q", client, tc.clientWants)
			}
			// The deployment's facts must not be in it.
			for _, banned := range []string{
				ids,            // the identities directory
				f.key,          // a server-side key path
				f.dir,          // any server path at all
				"private_key",  // the config key that decided
				"identities",   // ditto
				"not enrolled", // whether this subject is enrolled
				tc.target,      // the target's config key
			} {
				if strings.Contains(client, banned) {
					t.Fatalf("the client was told %q:\n  %s", banned, client)
				}
			}

			// And the operator still gets everything.
			for _, want := range tc.trailWants {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the trail lost %q:\n  %s", want, err.Error())
				}
			}
		})
	}
}

// TestRelayForwardRefusalNamesOnlyWhatTheClientSaid keeps the config key and
// the target key off a message the client provokes by asking.
func TestRelayForwardRefusalNamesOnlyWhatTheClientSaid(t *testing.T) {
	f := newRelayFixture(t)
	sc := laneWith(f, f.config(map[string]*SSHRelayTarget{
		"*.prod": {
			PrivateKey:          f.key,
			CapabilitiesAllowed: capsOf("exec", "local_forward"),
			ForwardsAllowed:     []string{"127.0.0.1:5432"},
		},
	}), capsOf("exec"))
	relay, err := buildSSHRelay("prod-bastion", sc, discardLog())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	state := &sshConnState{relay: relay, target: relay.byKey("*.prod")}
	refusal := state.relayForward(state.target)(
		context.Background(), codecssh.Destination{Host: "127.0.0.1", Port: 6379})
	if refusal == nil {
		t.Fatal("a destination outside the allowlist was admitted")
	}

	msg := refusal.String()
	// The destination is the client's own words and makes the refusal
	// actionable, so it stays.
	if !strings.Contains(msg, "127.0.0.1:6379") {
		t.Fatalf("the refusal does not name what was asked for: %q", msg)
	}
	for _, banned := range []string{"forwards_allowed", "*.prod"} {
		if strings.Contains(msg, banned) {
			t.Fatalf("the client was told %q:\n  %s", banned, msg)
		}
	}
}
