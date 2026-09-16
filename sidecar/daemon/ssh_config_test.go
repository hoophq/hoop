package daemon

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// sshKeys writes two readable files and returns their paths, so a test that
// is about one refusal does not also trip the host_key and trusted_ca ones.
func sshKeys(t *testing.T) (hostKey, trustedCA string) {
	t.Helper()
	dir := t.TempDir()
	hostKey = filepath.Join(dir, "host_key")
	trustedCA = filepath.Join(dir, "ca.pub")
	for _, p := range []string{hostKey, trustedCA} {
		if err := os.WriteFile(p, []byte("not a real key, and nothing here parses one\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return hostKey, trustedCA
}

// sshConfig builds a config file with one ssh listener whose ssh block is
// the JSON in block, with host_key and trusted_ca filled in.
func sshConfig(t *testing.T, block map[string]any, extra ...string) string {
	t.Helper()
	hostKey, trustedCA := sshKeys(t)
	if _, ok := block["host_key"]; !ok {
		block["host_key"] = hostKey
	}
	if _, ok := block["trusted_ca"]; !ok {
		block["trusted_ca"] = trustedCA
	}
	raw, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("marshal ssh block: %v", err)
	}
	listener := `{"name":"jump","protocol":"ssh","listen":":2222","ssh":` + string(raw)
	for _, e := range extra {
		listener += "," + e
	}
	listener += "}"
	return writeConfig(t, `{"listeners":[`+listener+`]}`)
}

// loadErr loads a config and returns the error text, failing when the config
// was accepted.
func loadErr(t *testing.T, path string) string {
	t.Helper()
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("an invalid config was accepted")
	}
	return err.Error()
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("error missing %q:\n%s", want, got)
		}
	}
}

// A minimal ssh lane loads. Every other test here asserts a refusal, so this
// one asserts that the refusals are not simply "ssh".
func TestSSHMinimalLaneLoads(t *testing.T) {
	p := sshConfig(t, map[string]any{})
	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("a minimal ssh lane was refused: %v", err)
	}
}

// An ssh lane has no fixed backend, so `upstream` is refused rather than
// ignored — and its absence must not read as the missing-upstream error
// every other lane gets.
func TestSSHRefusesUpstreamKeys(t *testing.T) {
	hostKey, trustedCA := sshKeys(t)
	p := writeConfig(t, `{"listeners":[{
      "name":"jump","protocol":"ssh","listen":":2222",
      "upstream":"db:5432",
      "upstream_tls":{"ca_file":"/dev/null"},
      "downstream_tls":{"cert_file":"/dev/null","key_file":"/dev/null"},
      "identity_header":"x-user",
      "ssh":{"host_key":"`+hostKey+`","trusted_ca":"`+trustedCA+`"}
    }]}`)
	got := loadErr(t, p)
	mustContain(t, got,
		"upstream is not valid on an ssh listener",
		"upstream_tls is not valid on an ssh listener",
		"downstream_tls is not valid on an ssh listener",
		"identity_header is not valid on an ssh listener")
	if strings.Contains(got, "jump: no upstream") {
		t.Errorf("an ssh lane was told it is missing an upstream:\n%s", got)
	}
}

// The block is required: without it the listener has no host key and no
// trusted CA, so it could not complete a handshake.
func TestSSHBlockIsRequired(t *testing.T) {
	p := writeConfig(t, `{"listeners":[{"name":"jump","protocol":"ssh","listen":":2222"}]}`)
	mustContain(t, loadErr(t, p), `there is no "ssh" block`)
}

// And it is refused anywhere else, where nothing would read it.
func TestSSHBlockOnlyOnSSHLane(t *testing.T) {
	hostKey, trustedCA := sshKeys(t)
	p := writeConfig(t, `{"listeners":[{
      "name":"pg","protocol":"postgres","listen":":5432","upstream":"db:5432",
      "ssh":{"host_key":"`+hostKey+`","trusted_ca":"`+trustedCA+`"}
    }]}`)
	mustContain(t, loadErr(t, p), `an "ssh" block is only valid on an ssh listener`)
}

func TestSSHKeysAreRequiredAndRead(t *testing.T) {
	p := writeConfig(t, `{"listeners":[{
      "name":"jump","protocol":"ssh","listen":":2222","ssh":{}
    }]}`)
	mustContain(t, loadErr(t, p), "ssh.host_key is required", "ssh.trusted_ca is required")

	// A path that does not exist is a listener that refuses everyone and
	// cannot say why. It must not wait for the first login.
	missing := filepath.Join(t.TempDir(), "nope")
	p = writeConfig(t, `{"listeners":[{
      "name":"jump","protocol":"ssh","listen":":2222",
      "ssh":{"host_key":"`+missing+`","trusted_ca":"`+missing+`"}
    }]}`)
	mustContain(t, loadErr(t, p), "ssh.host_key:", "ssh.trusted_ca:")
}

// The tri-state is the whole point of the pointer: absent and empty are
// opposite configurations, and nothing downstream may see them as one.
func TestSSHCapabilitiesTriState(t *testing.T) {
	var absent SSHConfig
	if got := len(absent.resolveCapabilities()); got != len(sshDeliveredCapabilities) {
		t.Errorf("an absent list resolved to %d capabilities, want the %d delivered",
			got, len(sshDeliveredCapabilities))
	}
	if absent.admitsNothing() {
		t.Error("an absent list reads as a bastion; it admits the delivered set")
	}

	empty := SSHConfig{CapabilitiesAllowed: &Capabilities{}}
	if got := len(empty.resolveCapabilities()); got != 0 {
		t.Errorf("an empty list resolved to %d capabilities, want none", got)
	}
	if !empty.admitsNothing() {
		t.Error("an empty list does not read as a bastion")
	}

	one := SSHConfig{CapabilitiesAllowed: &Capabilities{"exec"}}
	if !one.admits("exec") || one.admits("shell") {
		t.Error("a populated list admits the wrong set")
	}
}

// `capabilities_allowed:` with nothing after it is asking for one of two
// opposite readings, so it is refused rather than guessed.
func TestSSHCapabilitiesNullIsRefused(t *testing.T) {
	p := sshConfig(t, map[string]any{"capabilities_allowed": nil})
	mustContain(t, loadErr(t, p), "ssh.capabilities_allowed is written with no value")
}

// A name this version has no handler for must fail at load. Admitting it
// would leave the operator believing the capability is on.
func TestSSHUndeliveredCapabilityIsRefused(t *testing.T) {
	for _, name := range []string{"remote_forward", "agent_forward", "x11", "subsystem"} {
		p := sshConfig(t, map[string]any{"capabilities_allowed": []string{name}})
		mustContain(t, loadErr(t, p), name, "does not deliver")
	}
}

func TestSSHUnknownCapabilityIsRefused(t *testing.T) {
	p := sshConfig(t, map[string]any{"capabilities_allowed": []string{"shel"}})
	mustContain(t, loadErr(t, p), `"shel"`, "not a capability this design defines")
}

// Forwarding is governed by destinations_allowed, so naming it as a
// capability has to say where the real switch is.
func TestSSHForwardIsNotACapability(t *testing.T) {
	p := sshConfig(t, map[string]any{"capabilities_allowed": []string{"local_forward"}})
	mustContain(t, loadErr(t, p), "forwarding is not a", "destinations_allowed")
}

// run_as was a key and is not one any more: a session runs as the login name
// the certificate admitted, and the account the PROCESS runs as is what
// bounds that.
//
// A config carrying it must fail at load rather than start a listener that
// quietly ignores it. The ssh block refuses unknown keys for exactly this,
// and the key it names is what tells an operator which line to delete.
func TestSSHRunAsIsNoLongerAKey(t *testing.T) {
	p := sshConfig(t, map[string]any{"run_as": "hoop-session"})
	mustContain(t, loadErr(t, p), "run_as")
}

// The account this process already is resolves, and the process can become
// it. Any other account needs privilege, which the check reports at load.
func TestSSHRunAsResolvesThisAccount(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	// A cgo build reaches NSS, so the account can resolve and still not be
	// in the passwd file — every developer on macOS is in that position.
	// The session refuses there on purpose; this test is about the other
	// case, so it steps aside rather than asserting the refusal here.
	if _, err := lookupShell(me.Username); errors.Is(err, errNotInPasswd) {
		t.Skipf("this account is not in %s: %v", passwdFile, err)
	}
	runAs, err := resolveRunAs(me.Username)
	if err != nil {
		t.Fatalf("the running account does not resolve: %v", err)
	}
	if runAs.Shell == "" {
		t.Error("a resolved account has no shell, so the session would run one nobody chose")
	}
	if runAs.Name == "" {
		t.Error("a resolved account has no name, which refuses the session")
	}
	// nil means "nobody resolved them" across the seam and refuses. Empty
	// non-nil is a different, valid answer.
	if runAs.Groups == nil {
		t.Error("supplementary groups came back nil; the session would be refused")
	}
	if err := canBecome(runAs); err != nil {
		t.Errorf("this process cannot become the account it already runs as: %v", err)
	}
}

func TestSSHDestinationParsing(t *testing.T) {
	tests := []struct {
		entry   string
		allowed []string
		denied  []string
	}{
		{"any", []string{"10.0.0.1:22", "203.0.113.9:443"}, nil},
		{"10.0.0.0/8", []string{"10.1.2.3:22", "10.1.2.3:5432"}, []string{"192.168.0.1:22"}},
		{"10.0.0.0/8:2222", []string{"10.1.2.3:2222"}, []string{"10.1.2.3:22", "192.168.0.1:2222"}},
		// The host bits are masked off, so two spellings of one network
		// behave identically.
		{"10.1.2.3/8", []string{"10.9.9.9:22"}, nil},
		{"10.0.0.5/32", []string{"10.0.0.5:22"}, []string{"10.0.0.6:22"}},
		// IPv6 needs no bracket spelling: the split is anchored on "/".
		{"2001:db8::/32:22", []string{"[2001:db8::1]:22"}, []string{"[2001:db8::1]:23", "[2001:db9::1]:22"}},
	}
	for _, tc := range tests {
		d, err := parseSSHDestination(tc.entry)
		if err != nil {
			t.Errorf("%s: %v", tc.entry, err)
			continue
		}
		for _, a := range tc.allowed {
			if !d.Allows(netip.MustParseAddrPort(a)) {
				t.Errorf("%s: denied %s", tc.entry, a)
			}
		}
		for _, x := range tc.denied {
			if d.Allows(netip.MustParseAddrPort(x)) {
				t.Errorf("%s: allowed %s", tc.entry, x)
			}
		}
	}
}

// A resolver may hand back the v4-mapped form of a v4 address. Without the
// unmap it would slip past every v4 network entry.
func TestSSHDestinationUnmapsV4InV6(t *testing.T) {
	d, err := parseSSHDestination("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allows(netip.MustParseAddrPort("[::ffff:10.1.2.3]:22")) {
		t.Error("a v4-mapped address did not match its v4 network")
	}
}

func TestSSHDestinationParseFailureIsAStartupRefusal(t *testing.T) {
	for _, bad := range []string{"10.0.0.1", "10.0.0.0/8:0", "10.0.0.0/8:notaport", "10.0.0.0/99", ""} {
		if _, err := parseSSHDestination(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	p := sshConfig(t, map[string]any{"destinations_allowed": []string{"10.0.0.1"}})
	mustContain(t, loadErr(t, p), "ssh.destinations_allowed:")
}

// Absent and empty both deny. This key stays default-deny where
// capabilities_allowed does not.
func TestSSHDestinationsDefaultDeny(t *testing.T) {
	for _, list := range [][]string{nil, {}} {
		got, err := parseSSHDestinations(list)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("%v resolved to %d destinations, want none", list, len(got))
		}
	}
}

func TestSSHIdentitySourcesAreChecked(t *testing.T) {
	p := sshConfig(t, map[string]any{
		"identity": map[string]any{"subject": "common_name"},
	})
	mustContain(t, loadErr(t, p), "ssh.identity.subject", "key_id", "principals", "extensions.")

	p = sshConfig(t, map[string]any{
		"identity": map[string]any{
			"subject":    "key_id",
			"email":      "extensions.email@hoop.dev",
			"groups":     "principals",
			"attributes": []string{"permit-pty"},
		},
	})
	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("a valid identity mapping was refused: %v", err)
	}
}

// A variable-length replacement desynchronizes a terminal's escape
// sequences, so `mask` is the only strategy this lane can carry — and the
// unwritten default is `redact`, which it cannot.
func TestSSHMaskStrategyMustPreserveLength(t *testing.T) {
	p := sshConfig(t, map[string]any{},
		`"mask":{"rules":[{"name":"emails","entities":["EMAIL_ADDRESS"]}]}`)
	mustContain(t, loadErr(t, p), `mask rule "emails"`, "redact (the default", "only strategy")

	p = sshConfig(t, map[string]any{},
		`"mask":{"rules":[{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"hash"}]}`)
	mustContain(t, loadErr(t, p), "strategy hash")

	// "mask" preserves the RUNE count; this lane needs the byte count.
	p = sshConfig(t, map[string]any{},
		`"mask":{"rules":[{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"mask","mask_char":9608}]}`)
	mustContain(t, loadErr(t, p), "more than one byte")

	p = sshConfig(t, map[string]any{},
		`"mask":{"rules":[{"name":"emails","entities":["EMAIL_ADDRESS"],"strategy":"mask"}]}`)
	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("a length-preserving mask rule was refused: %v", err)
	}
}

// Four rule types have nothing on an SSH lane to read. A rule that loads,
// evaluates and never fires is worse than one that refuses.
func TestSSHRefusedRuleTypes(t *testing.T) {
	cases := []struct{ rule, want string }{
		{`{"name":"r","type":"table","tables":["users"]}`, "SSH has no relations"},
		{`{"name":"r","type":"http_resource","resources":["/x"]}`, "there is no request path"},
		{`{"name":"r","type":"http_status","statuses":["500"]}`, "there is no response status"},
		{`{"name":"r","type":"grpc_status","statuses":["7"]}`, "there is no RPC"},
	}
	for _, tc := range cases {
		p := sshConfig(t, map[string]any{}, `"guardrails":{"rules":[`+tc.rule+`]}`)
		mustContain(t, loadErr(t, p), tc.want)
	}
}

// The types that do work need no protocol knowledge, and refusing them would
// leave the lane with no way to fence anything.
func TestSSHKeepsTheRuleTypesThatWork(t *testing.T) {
	p := sshConfig(t, map[string]any{}, `"guardrails":{"rules":[
      {"name":"no-preload","type":"pattern_match","pattern_regex":"^LD_PRELOAD$","operations":["env_set"]},
      {"name":"reads-only","type":"operation","operations":["sftp_write"]}
    ]}`)
	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("a pattern_match or operation rule was refused on an ssh lane: %v", err)
	}
}

// passwdFixture points passwdFile at a file this test wrote, so the shell
// field can be exercised without an account on the host.
func passwdFixture(t *testing.T, lines ...string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "passwd")
	body := "# a comment, and a blank line below\n\n" + strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write passwd: %v", err)
	}
	old := passwdFile
	passwdFile = path
	t.Cleanup(func() { passwdFile = old })
}

// shellFile writes a file and returns its path. mode decides whether it
// passes for a shell.
func shellFile(t *testing.T, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// The shell comes from the account's own passwd entry, and an empty seventh
// field takes the default rather than refusing.
func TestSSHShellComesFromTheAccount(t *testing.T) {
	bash := shellFile(t, "bash", 0o755)
	passwdFixture(t,
		"alice:x:1001:1001::/home/alice:"+bash,
		"bob:x:1002:1002::/home/bob:",
	)

	got, err := lookupShell("alice")
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	if got != bash {
		t.Errorf("alice runs %q; the account named %q", got, bash)
	}

	// Empty field, not a missing account: sshd reads it as the default
	// shell, and so does this.
	got, err = lookupShell("bob")
	if err != nil {
		t.Fatalf("bob: %v", err)
	}
	if got != "" {
		t.Errorf("an empty shell field gave %q instead of the default", got)
	}
}

// An account the passwd file does not hold refuses, rather than running
// something the host never named.
func TestSSHShellRefusesAnAccountNotInPasswd(t *testing.T) {
	passwdFixture(t, "alice:x:1001:1001::/home/alice:/bin/sh")
	_, err := lookupShell("carol")
	if !errors.Is(err, errNotInPasswd) {
		t.Fatalf("carol resolved a shell from a file that does not hold her: %v", err)
	}
}

// A shell that does not exist, or that cannot be executed, is an account
// that can run nothing. sshd refuses the login for both; so does this, and
// the message names the file rather than surfacing an exec error later.
func TestSSHShellMustExistAndBeExecutable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-shell")
	notExec := shellFile(t, "sh", 0o644)
	dir := t.TempDir()

	for _, tc := range []struct{ shell, want string }{
		{missing, "does not exist"},
		{notExec, "is not executable"},
		{dir, "is not a regular file"},
	} {
		err := usableShell("alice", tc.shell)
		if err == nil {
			t.Errorf("%s: admitted", tc.shell)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.shell, err, tc.want)
		}
	}
}

// nologin is a shell like any other, and that is the point.
//
// It is an executable whose job is to refuse, so RUNNING it is the refusal —
// the same one every other login path on this host gets. A list of disabled
// shell paths here would be a second copy of that decision, drifting from
// the host's, and this test fails if anyone adds one.
func TestSSHNologinIsRunRatherThanListed(t *testing.T) {
	nologin := shellFile(t, "nologin", 0o755)
	passwdFixture(t, "alice:x:1001:1001::/home/alice:"+nologin)

	shell, err := lookupShell("alice")
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	if err := usableShell("alice", shell); err != nil {
		t.Fatalf("a nologin shell was refused by name rather than left to run: %v", err)
	}
	if shell != nologin {
		t.Errorf("the session would run %q instead of the account's %q", shell, nologin)
	}
}
