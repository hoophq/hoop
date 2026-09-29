package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	codecssh "github.com/hoophq/libhoop/v2/codec/ssh"
)

// SSHRelayConfig is the `ssh.relay` block, and ITS PRESENCE IS THE WHOLE MODE
// SWITCH. Delete it and the listener is exactly ADR-0015's: an endpoint that
// terminates sessions on this host and carries every forward blind.
//
// With it, a forward whose destination matches a target is terminated — a
// second SSH server runs inside the channel and the session is relayed to an
// ordinary sshd — and the whole enforcement chain applies to a host with
// nothing installed on it. A destination no target covers is still carried
// blind.
//
// The block carries admission, credentials and topology, and NEVER rule
// blocks. guardrails, mask and the analyzer stay outside it and gain no
// per-target form, so they apply to every target without exception. Nothing
// in here can subtract from the policy; it can only narrow the surface the
// policy applies to.
type SSHRelayConfig struct {
	// KnownHosts verifies a target's host key, under the name the client
	// asked for rather than the address dialled. Required: the bastion is
	// the only component positioned to notice that the host behind it was
	// replaced, because the client verified only the bastion's own key.
	KnownHosts string `json:"known_hosts"`

	// HostKeyCheck is what an unknown or changed key does, spelled to mean
	// what StrictHostKeyChecking means in ssh(1). Defaults to strict.
	HostKeyCheck string `json:"host_key_check,omitempty"`

	// Identities is a directory whose FILENAMES are certificate subjects,
	// holding one private key each. Its presence adds a per-user credential
	// overlay.
	//
	// The directory EXISTS at load; each key is read per session, so a key
	// added or revoked on disk takes effect without a restart. The subject
	// is joined onto the path, so identityPathFor guards the traversal a
	// certificate could otherwise steer: a separator, a dot name or a
	// leading dot is refused, and the result must still sit in this
	// directory.
	Identities string `json:"identities,omitempty"`

	// Login is the lane's upstream account. Absent uses the login the
	// client asked for, which the certificate's principals vouched for.
	Login string `json:"login,omitempty"`

	// Targets is which destinations are terminated, keyed by name, glob or
	// network[:port]. Empty is a load error: a relay block that terminates
	// nothing is a listener the operator believes is inspecting and is not.
	Targets map[string]*SSHRelayTarget `json:"targets"`
}

// SSHRelayTarget is one terminated destination.
type SSHRelayTarget struct {
	// PrivateKey is the key authenticating to this target, and the fallback
	// for a subject with no identity key. Required unless `identities` is
	// set or this target sets AgentIdentity.
	//
	// Omitted under `identities`, the target has NO fallback: an unenrolled
	// subject is refused rather than admitted as somebody shared. A target
	// meant to be per-user should omit it once its people are enrolled;
	// carrying one under `identities` is a migration position, not a steady
	// state.
	PrivateKey string `json:"private_key,omitempty"`

	// AgentIdentity makes the client's forwarded agent sign the upstream
	// handshake, so the user's OWN certificate reaches the target and its
	// sshd logs the verified person.
	//
	// Exclusive with PrivateKey — naming both is a load error, because they
	// are two sources rather than a preference and a fallback. A client
	// offering no agent is refused, never downgraded.
	AgentIdentity bool `json:"agent_identity,omitempty"`

	// KnownHosts overrides the lane's for this target.
	KnownHosts string `json:"known_hosts,omitempty"`

	// HostKeyCheck overrides the lane's for this target — a lab subnet on
	// accept_new beside production on strict.
	HostKeyCheck string `json:"host_key_check,omitempty"`

	// Login overrides the lane's upstream account.
	Login string `json:"login,omitempty"`

	// CapabilitiesAllowed narrows the listener's list for this target, and
	// it is TRI-STATE, with the same three readings the listener's own list
	// has:
	//
	//	absent      inherits the listener's list
	//	empty, []   admits none — a target reached only by a forward
	//	populated   admits those, refuses the rest
	//
	// A []string could not tell the first two apart, and they are opposite
	// configurations. It can only ever NARROW: naming a capability the
	// listener does not admit is a load error.
	CapabilitiesAllowed *Capabilities `json:"capabilities_allowed,omitempty"`

	// ForwardsAllowed is where an `ssh -L` inside this target's session may
	// reach. REQUIRED when the target admits local_forward: admitting the
	// capability without the list fails validation rather than loading as
	// an allow-everything tunnel.
	//
	// The destinations are resolved and dialled BY THE TARGET, so 127.0.0.1
	// here is the target's loopback and not this sidecar's.
	ForwardsAllowed []string `json:"forwards_allowed,omitempty"`
}

// UnmarshalJSON re-imposes DisallowUnknownFields, which the outer decoder
// applies at the top level but does not propagate into a type that
// unmarshals itself. Without it a typo inside the relay block would be
// silently dropped — on the block that decides which hosts are inspected and
// what authenticates to them.
func (r *SSHRelayConfig) UnmarshalJSON(b []byte) error {
	type plain SSHRelayConfig
	var out plain
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return fmt.Errorf("ssh.relay: %w", err)
	}
	*r = SSHRelayConfig(out)
	return nil
}

// UnmarshalJSON refuses the one capability spelling the tri-state cannot
// answer, exactly as the listener's block does, and for the same reason: a
// key written with no value transcodes to null, and null on a pointer field
// is indistinguishable from an absent key by the time the field is set.
func (t *SSHRelayTarget) UnmarshalJSON(b []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return fmt.Errorf("ssh.relay.targets: %w", err)
	}
	if raw, ok := probe["capabilities_allowed"]; ok &&
		string(bytes.TrimSpace(raw)) == "null" {
		return errors.New(
			"ssh.relay.targets: capabilities_allowed is written with no value; omit the " +
				"key to inherit the listener's list, or write [] to admit no session " +
				"capability on this target")
	}
	type plain SSHRelayTarget
	var out plain
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return fmt.Errorf("ssh.relay.targets: %w", err)
	}
	*t = SSHRelayTarget(out)
	return nil
}

// sshRelayDeliveredCapabilities is what a TERMINATED session may admit, and
// it is not the listener's delivered set.
//
// sftp drops out and local_forward comes in. On an end-hop the sidecar IS
// the file-transfer server and sees decoded operations; against a remote
// sftp-server it is an opaque subsystem stream. Forwarding is the mirror
// image: on the listener it is not a capability at all — destinations_allowed
// decides it — while inside a terminated session it is a per-target decision
// bounded by that target's own allowlist.
var sshRelayDeliveredCapabilities = []codecssh.Capability{
	codecssh.CapShell,
	codecssh.CapPTY,
	codecssh.CapExec,
	codecssh.CapEnv,
	codecssh.CapLocalForward,
}

// sshRelayUndelivered names what a terminated session cannot carry, with the
// reason each one is out. They are refused separately from an unknown name
// because the operator's mistake is different: a typo is a typo, but writing
// `sftp` is asking for something the design names and this topology cannot
// do.
var sshRelayUndelivered = map[codecssh.Capability]string{
	codecssh.CapSFTP: "against a remote sftp-server this is an opaque subsystem " +
		"stream, and keeping path gating and download masking would mean decoding " +
		"the protocol both ways; relaying it un-decoded would leave file transfer " +
		"the one un-inspected path on a listener whose purpose is inspection",
	codecssh.CapRemoteFwd: "remote forwarding exposes your machine to the target " +
		"rather than the reverse, and needs a listener on the target plus reverse " +
		"channel plumbing that does not exist here",
	codecssh.CapAgentFwd: "the client's agent is consumed at this listener to " +
		"authenticate upstream and is never proxied to the target; set " +
		"agent_identity on the target if that is what you meant",
	codecssh.CapX11:       "x11 forwarding is not delivered",
	codecssh.CapSubsystem: "no subsystem has a wire format this build can decode",
}

// sshTargetKind is what a target key matches on, and the order of these
// constants IS the precedence order.
//
// Precedence has to be total and fixed, or map iteration would decide which
// rule applied — per connection, differently each time. Exact names beat
// globs because a name written out is the more specific statement; both beat
// addresses because a NAME KEY IS MATCHED BEFORE RESOLUTION and an address
// key after, which keeps a name key from widening reachability;
// destinations_allowed still decides that on the resolved address.
type sshTargetKind int

const (
	sshTargetExact sshTargetKind = iota
	sshTargetGlob
	sshTargetAddress
	sshTargetPrefix
)

func (k sshTargetKind) String() string {
	switch k {
	case sshTargetExact:
		return "name"
	case sshTargetGlob:
		return "glob"
	case sshTargetAddress:
		return "address"
	default:
		return "network"
	}
}

// sshRelay is the resolved relay block: every path read, every key checked,
// every target ordered. Built once at load; a connection reads it and never
// re-parses config text.
type sshRelay struct {
	lane    string
	targets []*sshRelayTarget

	// identityDir is the per-user overlay: a directory holding one private
	// key per certificate subject, read PER SESSION. Empty when the lane
	// configures no overlay, which is the only thing that distinguishes "not
	// enrolled" from "no overlay" in credentialFor.
	identityDir string
}

// sshRelayTarget is one resolved target.
type sshRelayTarget struct {
	key  string
	kind sshTargetKind

	// The matcher, one of these by kind.
	glob   string
	addr   netip.Addr
	prefix netip.Prefix
	// port restricts the key to one port. Zero matches any.
	port uint16

	privateKey    []byte
	keySource     string
	agentIdentity bool
	login         string
	caps          []codecssh.Capability
	forwards      []sshForwardDest
	knownHosts    string
	hostKeyCheck  codecssh.HostKeyCheck
}

// admits reports whether this target admits a capability.
func (t *sshRelayTarget) admits(want codecssh.Capability) bool {
	for _, c := range t.caps {
		if c == want {
			return true
		}
	}
	return false
}

// hostKeyCheckOrDefault resolves the tri-level setting: target, then lane,
// then strict.
func hostKeyCheckOrDefault(target, lane string) codecssh.HostKeyCheck {
	switch {
	case strings.TrimSpace(target) != "":
		return codecssh.HostKeyCheck(strings.TrimSpace(target))
	case strings.TrimSpace(lane) != "":
		return codecssh.HostKeyCheck(strings.TrimSpace(lane))
	default:
		return codecssh.HostKeyStrict
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// parseSSHTargetKey classifies one target key.
//
// The grammar is a name, a glob, an address, or a network — each with an
// optional :port. An IPv6 literal carrying a port needs the bracket spelling
// ssh(1) uses, because otherwise the colons are ambiguous; without a port it
// is written plain.
func parseSSHTargetKey(raw string) (*sshRelayTarget, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return nil, errors.New("empty target key")
	}
	if key == sshDestinationAny {
		return nil, errors.New(
			`"any" is a destinations_allowed spelling and is not a target key. ` +
				"That list decides where a forward may be CARRIED; this map decides " +
				"which destinations are TERMINATED, and terminating everything " +
				"reachable would turn every forward into a session this listener " +
				"decrypts. Name the hosts, or write a glob that covers them")
	}
	t := &sshRelayTarget{key: key}

	// A network key is anchored on "/", so IPv6 needs no bracket spelling:
	// everything up to "/" is the address, the bits and the optional port
	// follow it, exactly as destinations_allowed reads them.
	if strings.Contains(key, "/") {
		dest, err := parseSSHDestination(key)
		if err != nil {
			return nil, err
		}
		t.kind, t.prefix, t.port = sshTargetPrefix, dest.prefix, dest.port
		return t, nil
	}

	host, port := key, ""
	if addr, err := netip.ParseAddr(key); err == nil {
		// A bare address, no port.
		t.kind, t.addr = sshTargetAddress, addr.Unmap()
		return t, nil
	}
	if h, p, err := net.SplitHostPort(key); err == nil {
		host, port = h, p
	}
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("%q: %q is not a port", key, port)
		}
		t.port = uint16(n)
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		t.kind, t.addr = sshTargetAddress, addr.Unmap()
		return t, nil
	}
	if host == "" {
		return nil, fmt.Errorf("%q names no host", key)
	}
	if strings.ContainsAny(host, "*?[") {
		t.kind, t.glob = sshTargetGlob, host
		// Fail here rather than on the connection that first fails to
		// match: path.Match reports a bad pattern only when it is used.
		if _, err := path.Match(host, "probe"); err != nil {
			return nil, fmt.Errorf("%q is not a valid pattern: %v", key, err)
		}
		return t, nil
	}
	t.kind, t.glob = sshTargetExact, host
	return t, nil
}

// matches reports whether this target covers one destination.
//
// A name or glob key is matched on what the CLIENT TYPED; an address or
// network key on what it resolved to. Both are handed in, and the resolve
// still happened exactly once.
func (t *sshRelayTarget) matches(host string, dialing netip.AddrPort) bool {
	if t.port != 0 && t.port != dialing.Port() {
		return false
	}
	switch t.kind {
	case sshTargetExact:
		return strings.EqualFold(t.glob, host)
	case sshTargetGlob:
		ok, err := path.Match(strings.ToLower(t.glob), strings.ToLower(host))
		return err == nil && ok
	case sshTargetAddress:
		return t.addr == dialing.Addr().Unmap()
	default:
		return t.prefix.Contains(dialing.Addr().Unmap())
	}
}

// match finds the target covering one destination, or nil to carry it blind.
//
// The list is already in precedence order, so the first match is the answer
// and nothing here has to compare two candidates.
func (r *sshRelay) match(host string, dialing netip.AddrPort) *sshRelayTarget {
	for _, t := range r.targets {
		if t.matches(host, dialing) {
			return t
		}
	}
	return nil
}

// sortTargets puts the list in the precedence order the ADR fixes: exact
// name, then glob, then literal address, then longest prefix.
//
// Within a kind the order is made total too, or two overlapping globs would
// be decided by map iteration — the thing the ordering exists to prevent. A
// longer pattern is the more specific one; ties fall to the key text, which
// is stable and visible in the config.
func sortTargets(targets []*sshRelayTarget) {
	sort.SliceStable(targets, func(i, j int) bool {
		a, b := targets[i], targets[j]
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		if a.kind == sshTargetPrefix && a.prefix.Bits() != b.prefix.Bits() {
			return a.prefix.Bits() > b.prefix.Bits()
		}
		if a.kind == sshTargetGlob && len(a.glob) != len(b.glob) {
			return len(a.glob) > len(b.glob)
		}
		// A key with a port is more specific than the same key without one.
		if (a.port == 0) != (b.port == 0) {
			return a.port != 0
		}
		return a.key < b.key
	})
}

// sshForwardDest is one parsed forwards_allowed entry.
//
// It is matched against the destination the client NAMED inside a terminated
// session, not against a resolved address, and that is the whole difference
// from sshDestination. The target dials this one, on its own host and in its
// own network, so resolving it here would answer a different question — and
// answer it about the bastion's loopback.
type sshForwardDest struct {
	// host is a literal host or address, lowercased. Empty when prefix is
	// set.
	host string

	prefix   netip.Prefix
	isPrefix bool

	// port restricts the entry. Zero means any port, which only a network
	// entry may leave unset.
	port uint16
}

// parseSSHForward parses one entry: host:port, or network[:port].
func parseSSHForward(raw string) (sshForwardDest, error) {
	entry := strings.TrimSpace(raw)
	if entry == "" {
		return sshForwardDest{}, errors.New("empty forward destination")
	}
	if entry == sshDestinationAny {
		return sshForwardDest{}, errors.New(
			`"any" is not a forward destination; the allowlist is what keeps ` +
				"forwarded traffic — which is carried without inspection — a deliberate " +
				"exception rather than a hole, so it has to name where")
	}
	if strings.Contains(entry, "/") {
		dest, err := parseSSHDestination(entry)
		if err != nil {
			return sshForwardDest{}, err
		}
		return sshForwardDest{prefix: dest.prefix, isPrefix: true, port: dest.port}, nil
	}
	host, port, err := net.SplitHostPort(entry)
	if err != nil {
		return sshForwardDest{}, fmt.Errorf(
			"%q needs a port: write host:port, such as 127.0.0.1:5432, or a network "+
				"such as 10.0.0.0/8:5432", entry)
	}
	n, perr := strconv.ParseUint(port, 10, 16)
	if perr != nil || n == 0 {
		return sshForwardDest{}, fmt.Errorf("%q: %q is not a port", entry, port)
	}
	if strings.TrimSpace(host) == "" {
		return sshForwardDest{}, fmt.Errorf("%q names no host", entry)
	}
	return sshForwardDest{host: strings.ToLower(host), port: uint16(n)}, nil
}

// Allows reports whether one client-named destination is in the list.
func (d sshForwardDest) Allows(host string, port int) bool {
	if port < 0 || port > 65535 {
		return false
	}
	if d.port != 0 && d.port != uint16(port) {
		return false
	}
	if !d.isPrefix {
		return strings.EqualFold(d.host, host)
	}
	// A network entry can only cover a destination the client named as a
	// literal address. A NAME is resolved on the target, by the target, so
	// this side cannot say which network it lands in — and guessing with a
	// local resolver would be an allowlist checked against one answer and a
	// connection made against another.
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return d.prefix.Contains(addr.Unmap())
}

// allowsForward reports whether a target carries a destination.
func (t *sshRelayTarget) allowsForward(host string, port int) bool {
	for _, d := range t.forwards {
		if d.Allows(host, port) {
			return true
		}
	}
	return false
}

// identityPathFor turns a certificate subject into the file that holds that
// person's upstream key, and REFUSES a subject that is not a plain filename.
//
// The guard is the whole of this function's reason to exist. The key is read
// per session, so a subject the certificate carries does become a filesystem
// path — and "../../etc/hoop-inspect/keys/prod" or an absolute path would
// otherwise reach a file outside the directory an operator enrolled people
// in. Enumerating the directory instead would make that impossible by
// construction rather than by check; reading per session is simpler and
// picks up an enrolment without a restart, and this is the cost of that
// trade, paid here in one place.
//
// A subject is a filename and nothing else: no separator, no parent, no
// leading dot. filepath.Base is then an identity on anything that passes,
// which is what makes the final comparison a proof rather than a second
// guess.
func identityPathFor(dir, subject string) (string, error) {
	name := strings.TrimSpace(subject)
	switch {
	case name == "":
		return "", errors.New("this session's certificate names no subject, so no " +
			"per-user key can be looked up for it")
	case strings.ContainsAny(name, "/\\\x00"):
		return "", fmt.Errorf("subject %q contains a path separator; a subject names "+
			"a FILE in the identities directory and cannot reach outside it", subject)
	case name == "." || name == "..":
		return "", fmt.Errorf("subject %q is not a filename", subject)
	case strings.HasPrefix(name, "."):
		// Dotfiles are skipped by enrolment tooling and are where an editor
		// leaves a half-written upload, so a subject that looks like one is
		// refused rather than read.
		return "", fmt.Errorf("subject %q begins with a dot", subject)
	}
	full := filepath.Join(dir, name)
	// Belt and braces: after the checks above this cannot fail, which is
	// exactly why it is here — if it ever does, the checks stopped holding.
	if filepath.Dir(full) != filepath.Clean(dir) {
		return "", fmt.Errorf("subject %q does not resolve inside %s", subject, dir)
	}
	return full, nil
}

// readIdentity reads one subject's upstream key from disk.
//
// Per session, from the filesystem, so enrolling somebody takes effect on
// their next connection rather than at the next restart. The permission check
// is the one ssh(1) makes for the same reason, and it follows a SYMLINK
// deliberately: a Kubernetes Secret projects its keys as links into ../data,
// and a link's own mode is 0777.
//
// A missing file is not an error here — it is "this subject is not enrolled",
// which the caller answers with the target's shared key or with a refusal.
func readIdentity(dir, subject string) (key []byte, source string, err error) {
	full, err := identityPathFor(dir, subject)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(full)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, "", nil
	case err != nil:
		return nil, "", fmt.Errorf("identity %s: %w", full, err)
	case info.IsDir():
		return nil, "", fmt.Errorf("identity %s is a directory", full)
	case info.Mode().Perm()&0o077 != 0:
		return nil, "", fmt.Errorf("identity %s is readable by group or other "+
			"(mode %04o); ssh(1) refuses a key with these permissions and so does this",
			full, info.Mode().Perm())
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, "", fmt.Errorf("identity %s: %w", full, err)
	}
	// The parse lives in libhoop, which owns the crypto. This module has
	// exactly one dependency and must not grow a second one to answer a
	// question about a file.
	if verr := codecssh.ValidatePrivateKey(raw); verr != nil {
		return nil, "", fmt.Errorf("identity %s: %w", full, verr)
	}
	return raw, full, nil
}

// credentialProblem is a refusal with TWO AUDIENCES, and the split is the
// whole point of the type.
//
// The operator needs the identities directory, the subject, the target key
// and which config key decided — that is what a diagnosis is made of, and the
// client can see none of it. The client needs to know they were refused and
// whether it is theirs to fix.
//
// What must never cross: the identities directory (a server path, and one
// that says where every enrolled key lives), the names of config keys that
// decided, and whether a particular subject IS enrolled — which would turn a
// refusal into an enrolment oracle for anyone holding any valid certificate.
type credentialProblem struct {
	// client is what reaches the far side.
	client string
	// detail is the whole story, for the log and the audit trail.
	detail string
}

func (p *credentialProblem) Error() string { return p.detail }

// clientMessage is what a failure may show the client, falling back to
// something safe for an error that was not written with two audiences.
func clientMessage(err error) string {
	var p *credentialProblem
	if errors.As(err, &p) && p.client != "" {
		return p.client
	}
	return "no credential is available for this host; an operator can see why " +
		"in the audit trail"
}

// credentialFor resolves the upstream credential for one subject on one
// target, and reports whether it fell back.
//
// ONE SOURCE, NEVER A LADDER between the agent and a stored key: a target
// that uses the agent is answered before this is reached. Between the two
// STORED forms there is a fallback, and it is the one the ADR permits —
// which is why the second return value exists. A fallback is logged every
// time, because the failure mode of an overlay is silent degradation: a
// subject format changed at the IdP, or a filename that no longer matches,
// keeps working on the shared key and quietly stops naming anyone.
//
// The identity is read from the FILESYSTEM here, not from a map built at
// startup, so enrolling or removing somebody takes effect on their next
// connection. The property given up is that the subject now becomes a path;
// identityPathFor is what stands in for the enumeration that used to make
// that impossible.
//
// A file that is present but unusable — wrong permissions, a public key —
// REFUSES rather than falling through to the shared key. Someone deliberately
// enrolled that person, and quietly serving them as the account that names
// nobody is the silent downgrade this whole design refuses.
func (r *sshRelay) credentialFor(t *sshRelayTarget, subject string) (key []byte, source string, fellBack bool, err error) {
	if r.identityDir != "" {
		raw, from, rerr := readIdentity(r.identityDir, subject)
		switch {
		case rerr != nil:
			// A file that is present and unusable. The client is told it is
			// not theirs to fix and nothing else; which file, and what is
			// wrong with it, is the operator's.
			return nil, "", false, &credentialProblem{
				client: "your credential for this host is unusable; an operator " +
					"can see why in the audit trail",
				detail: rerr.Error(),
			}
		case raw != nil:
			return raw, from, false, nil
		}
	}
	if len(t.privateKey) > 0 {
		// Only a fallback when an overlay existed and did not cover this
		// subject. Without an overlay the shared key is simply the target's
		// credential, and reporting that as a degradation every session
		// would bury the case that is one.
		return t.privateKey, t.keySource, r.identityDir != "", nil
	}
	if r.identityDir != "" {
		return nil, "", false, &credentialProblem{
			// Not "you are not enrolled in /etc/hoop-inspect/identities".
			// The path is the server's layout, and confirming that a
			// particular subject is or is not enrolled turns every refusal
			// into a directory listing for anyone holding a certificate.
			client: "you do not have access to this host",
			detail: fmt.Sprintf(
				"target %q admits enrolled subjects only and %q is not enrolled in %s; "+
					"the target carries no private_key, so there is nothing shared to "+
					"fall back to and this session is refused rather than admitted as "+
					"somebody else", t.key, subject, r.identityDir),
		}
	}
	return nil, "", false, &credentialProblem{
		client: "no credential is available for this host",
		detail: fmt.Sprintf("target %q has no credential configured", t.key),
	}
}

// notes reports what this relay block does, for -validate. Configuration
// only: there is no session content here and none to expose.
func (r *sshRelay) notes() []string {
	notes := []string{fmt.Sprintf(
		"ssh: this listener TERMINATES %d target(s); a session to one of them is "+
			"decrypted here, so this host holds the plaintext of every session behind "+
			"it and a credential that reaches them", len(r.targets)),
	}
	for _, t := range r.targets {
		credential := "a shared private key (" + t.keySource + ")"
		switch {
		case t.agentIdentity:
			credential = "the client's own forwarded agent"
		case len(t.privateKey) == 0:
			credential = "enrolled subjects only, with no shared fallback"
		}
		line := fmt.Sprintf("ssh: target %q (%s) admits %s, authenticates with %s",
			t.key, t.kind, joinCapabilityNames(t.caps), credential)
		if t.login != "" {
			line += ", logs in as " + t.login
		}
		notes = append(notes, line)
		if t.hostKeyCheck != codecssh.HostKeyStrict {
			notes = append(notes, fmt.Sprintf(
				"ssh: target %q sets host_key_check=%s against %s",
				t.key, t.hostKeyCheck, t.knownHosts))
		}
		if t.admits(codecssh.CapLocalForward) {
			notes = append(notes, fmt.Sprintf(
				"ssh: target %q carries forwards to %s, dialled BY THE TARGET and "+
					"relayed without inspection", t.key, joinComma(rawForwards(t))))
		}
	}
	if r.identityDir != "" {
		notes = append(notes, fmt.Sprintf(
			"ssh: per-user upstream keys are read per session from %s, named by "+
				"certificate subject; enrolling or removing somebody takes effect on "+
				"their next connection", r.identityDir))
	}
	return notes
}

func rawForwards(t *sshRelayTarget) []string {
	out := make([]string, 0, len(t.forwards))
	for _, f := range t.forwards {
		port := "any port"
		if f.port != 0 {
			port = strconv.Itoa(int(f.port))
		}
		if f.isPrefix {
			out = append(out, f.prefix.String()+":"+port)
			continue
		}
		out = append(out, f.host+":"+port)
	}
	return out
}

func joinCapabilityNames(caps []codecssh.Capability) string {
	if len(caps) == 0 {
		return "no session capability"
	}
	names := make([]string, 0, len(caps))
	for _, c := range caps {
		names = append(names, string(c))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func joinComma(items []string) string { return strings.Join(items, ", ") }

// byKey finds a resolved target by its config key.
func (r *sshRelay) byKey(key string) *sshRelayTarget {
	for _, t := range r.targets {
		if t.key == key {
			return t
		}
	}
	return nil
}
