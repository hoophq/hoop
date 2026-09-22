package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/policy"
	codecssh "github.com/hoophq/libhoop/v2/codec/ssh"
)

// SSHConfig is the `ssh` block on a listener: everything ADR-0015 adds to the
// config file, and nothing else.
//
// The keys divide into three jobs. Two say who may connect at all (HostKey,
// TrustedCA). Two say what a connection may then do (CapabilitiesAllowed,
// DestinationsAllowed). One says who policy and audit think is on the other
// end (Identity).
//
// There is no key for the OS account. A session runs as the login name the
// certificate admitted, the way sshd does, and the account the PROCESS runs
// as is what bounds that: unprivileged, it can only ever become itself, and
// no config file can say otherwise.
type SSHConfig struct {
	// HostKey is the private host key this listener presents, the same file
	// a real sshd would hold. Required: an SSH server with no identity of
	// its own cannot complete a handshake.
	HostKey string `json:"host_key"`

	// TrustedCA is an authorized_keys-format file naming the CA public
	// key(s) a user certificate must be signed by. Required, and the only
	// standing trust decision a listener makes.
	//
	// There is no password method and no authorized-keys list to fall back
	// on, so an unreadable file here is a listener that refuses everyone.
	// It is loaded at validation for that reason.
	TrustedCA string `json:"trusted_ca"`

	// CapabilitiesAllowed is which session capabilities this listener
	// admits, and it is TRI-STATE. That is why it is a pointer:
	//
	//	absent      admits the five capabilities v1 delivers
	//	empty, []   admits none — how a jump host drops the shell
	//	populated   admits those, refuses the rest
	//
	// A []string could not tell the first two apart, and they are opposite
	// configurations: a listener that meant to admit nothing would get a
	// shell. resolveCapabilities collapses the three states to one set, at
	// load, and the set is what crosses the seam — so the tri-state exists
	// in exactly one place and libhoop is never asked to guess a default.
	CapabilitiesAllowed *Capabilities `json:"capabilities_allowed,omitempty"`

	// DestinationsAllowed is where a client-opened forward may be carried,
	// as network[:port] entries or the single word "any".
	//
	// Absent and empty BOTH deny every forward. This key stays default-deny
	// where CapabilitiesAllowed does not, because a forward leaves the
	// sidecar's reach the moment it is dialled: nothing downstream inspects
	// those bytes, so the destination list is the whole control.
	DestinationsAllowed []string `json:"destinations_allowed,omitempty"`

	// Identity maps certificate fields onto the session identity policy and
	// audit read. Absent takes the key id as the subject.
	Identity *SSHIdentityConfig `json:"identity,omitempty"`
}

// SSHIdentityConfig names which certificate field fills each identity slot.
//
// A certificate has no field called "email", so this is a mapping rather than
// a set of values. Each of the three scalars takes a source spelling —
// "key_id", "principals", or "extensions.<name>" — and Attributes names
// extensions to surface to policy verbatim.
type SSHIdentityConfig struct {
	Subject    string   `json:"subject,omitempty"`
	Email      string   `json:"email,omitempty"`
	Groups     string   `json:"groups,omitempty"`
	Attributes []string `json:"attributes,omitempty"`
}

// Identity source spellings. Anything else is a config error: a source this
// package cannot read would leave the field empty, and an empty subject is an
// audit trail that cannot say who ran a command.
const (
	identitySourceKeyID      = "key_id"
	identitySourcePrincipals = "principals"
	identitySourceExtPrefix  = "extensions."
)

// Capabilities is a capability list as written in the config file. It is a
// named type so the pointer on the field reads as what it is: absent, empty
// and populated are three configurations, not two.
type Capabilities []string

// UnmarshalJSON decodes the ssh block, refusing the one spelling the
// tri-state cannot answer.
//
// `capabilities_allowed:` written with NO VALUE transcodes to null, and null
// on a pointer field is indistinguishable from an absent key by the time the
// field is set — encoding/json clears the pointer without consulting the
// element type. So the raw bytes are checked here, before that happens.
// Absent means "admit the delivered set" and empty means "admit none": those
// are opposite readings, and an operator who wrote neither has to say which
// they meant.
//
// The decode also re-imposes DisallowUnknownFields, which the outer decoder
// applies at the top level but does not propagate into a type that
// unmarshals itself. Without it a typo inside the ssh block would be
// silently dropped — on the block whose keys decide what a listener admits.
func (s *SSHConfig) UnmarshalJSON(b []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return fmt.Errorf("ssh: %w", err)
	}
	if raw, ok := probe["capabilities_allowed"]; ok &&
		string(bytes.TrimSpace(raw)) == "null" {
		return errors.New(
			"ssh.capabilities_allowed is written with no value; omit the key to " +
				"admit the capabilities this build delivers, or write [] to admit none")
	}

	// The alias sheds the method, so this decode is the plain struct one.
	type plain SSHConfig
	var out plain
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return fmt.Errorf("ssh: %w", err)
	}
	*s = SSHConfig(out)
	return nil
}

// sshDeliveredCapabilities is what an omitted list admits: the five
// capabilities v1 has a handler for, in the order the ADR writes them.
//
// libhoop refuses anything it does not deliver at NewServer, so this list
// cannot admit something that would then not work. It is here as well
// because the DEFAULT belongs to the consumer — libhoop is deliberately
// unable to guess it — and a default has to be written somewhere.
var sshDeliveredCapabilities = []codecssh.Capability{
	codecssh.CapShell,
	codecssh.CapPTY,
	codecssh.CapExec,
	codecssh.CapEnv,
	codecssh.CapSFTP,
}

// sshUndeliveredCapabilities are names the design defines but v1 does not
// deliver. They are refused separately from an unknown name because the
// operator's mistake is different: a typo is a typo, but writing `x11` is
// asking for something the design names and this build cannot do.
var sshUndeliveredCapabilities = []codecssh.Capability{
	codecssh.CapSubsystem,
	codecssh.CapAgentFwd,
	codecssh.CapX11,
	codecssh.CapRemoteFwd,
}

// resolveCapabilities collapses the tri-state to the set the lane admits.
//
// Call it once, at load. The resolved set is what buildSSHServer hands
// libhoop; nothing downstream sees the pointer again.
func (s *SSHConfig) resolveCapabilities() []codecssh.Capability {
	if s == nil || s.CapabilitiesAllowed == nil {
		return sshDeliveredCapabilities
	}
	out := make([]codecssh.Capability, 0, len(*s.CapabilitiesAllowed))
	for _, name := range *s.CapabilitiesAllowed {
		out = append(out, codecssh.Capability(strings.TrimSpace(name)))
	}
	return out
}

// admitsNothing reports whether the lane resolves to no session capability,
// so it forwards and nothing else. It reads the written form rather than the
// resolved one, because only an explicitly empty list can mean this.
func (s *SSHConfig) admitsNothing() bool {
	return s != nil && s.CapabilitiesAllowed != nil && len(*s.CapabilitiesAllowed) == 0
}

// admits reports whether the resolved set carries one capability.
func (s *SSHConfig) admits(want codecssh.Capability) bool {
	for _, c := range s.resolveCapabilities() {
		if c == want {
			return true
		}
	}
	return false
}

// sshDestination is one parsed entry of destinations_allowed: a network, an
// optional port, or the wildcard.
//
// Parsed at LOAD, never at connection time. A forward is checked on the hot
// path of a client opening a channel, and re-parsing text there would put a
// config typo's consequences on a user rather than on the operator who wrote
// it.
type sshDestination struct {
	// any matches every address and every port.
	any bool

	// prefix is the network. Unset when any is true.
	prefix netip.Prefix

	// port restricts the entry to one port. Zero means any port on prefix.
	port uint16
}

// Allows reports whether one resolved address may be dialled.
//
// It takes an AddrPort rather than a host name on purpose: the endpoint
// resolves once and hands over what it WILL dial, so the address checked here
// and the address connected to are the same bytes. Checking a name and
// dialling it again is the window ADR-0015 closes.
func (d sshDestination) Allows(target netip.AddrPort) bool {
	if d.port != 0 && d.port != target.Port() {
		return false
	}
	if d.any {
		return true
	}
	// Unmap first: a v4-mapped v6 address ("::ffff:10.0.0.1") is the same
	// host as 10.0.0.1, and netip.Prefix.Contains is false across families.
	// A resolver that returns the mapped form would otherwise slip past a
	// 10.0.0.0/8 entry.
	return d.prefix.Contains(target.Addr().Unmap())
}

// sshDestinationAny is the wildcard spelling. One word, no port: a port
// restriction on "any" would read as a network rule that is not one, so an
// operator who wants "every network, one port" writes the two default
// routes instead.
const sshDestinationAny = "any"

// parseSSHDestination parses one destinations_allowed entry.
//
// The grammar is prefix[:port], or "any". The split is anchored on "/"
// rather than on the last colon so IPv6 needs no bracket spelling:
// everything up to "/" is the address, and the bits and the optional port
// follow it. "2001:db8::/32:22" therefore parses exactly like
// "10.0.0.0/8:22".
func parseSSHDestination(raw string) (sshDestination, error) {
	entry := strings.TrimSpace(raw)
	if entry == "" {
		return sshDestination{}, errors.New("empty destination")
	}
	if entry == sshDestinationAny {
		return sshDestination{any: true}, nil
	}

	slash := strings.IndexByte(entry, '/')
	if slash < 0 {
		return sshDestination{}, fmt.Errorf(
			"%q is not a network; write a prefix such as 10.0.0.0/8, a single host "+
				"as 10.0.0.5/32, or %q", entry, sshDestinationAny)
	}
	addr, rest := entry[:slash], entry[slash+1:]

	var port uint16
	if colon := strings.IndexByte(rest, ':'); colon >= 0 {
		p, err := strconv.ParseUint(rest[colon+1:], 10, 16)
		if err != nil || p == 0 {
			return sshDestination{}, fmt.Errorf(
				"%q: %q is not a port", entry, rest[colon+1:])
		}
		port = uint16(p)
		rest = rest[:colon]
	}

	prefix, err := netip.ParsePrefix(addr + "/" + rest)
	if err != nil {
		return sshDestination{}, fmt.Errorf("%q is not a network: %v", entry, err)
	}
	// Masked: 10.0.0.5/8 and 10.0.0.0/8 describe the same network, and
	// keeping the host bits would make Contains depend on how the operator
	// spelled it.
	return sshDestination{prefix: prefix.Masked(), port: port}, nil
}

// parseSSHDestinations parses the whole list. An absent or empty list gives
// an empty result, which denies every forward.
func parseSSHDestinations(list []string) ([]sshDestination, error) {
	out := make([]sshDestination, 0, len(list))
	for _, raw := range list {
		d, err := parseSSHDestination(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// defaultShell is what an account whose passwd entry names no shell runs. It
// is the same fallback sshd applies to the same empty field.
const defaultShell = "/bin/sh"

// passwdFile is where an account's login shell is read from.
//
// os/user gives the name, the ids and the home directory, and NOT the shell.
// That field is the one deciding whether the account may run anything at all
// — `usermod -s /sbin/nologin alice` is how a host disables an account
// without deleting it — so a session that never read it would hand alice a
// shell the host had already taken away. It is read from the same file this
// build's os/user reads, so the two answers cannot disagree.
var passwdFile = "/etc/passwd"

// errNotInPasswd marks an account that resolved through some source other
// than passwdFile, which is a cgo build reaching NSS. Its shell cannot be
// read, so the session cannot proceed; the sentinel exists so a caller can
// tell that apart from an entry this file does have.
var errNotInPasswd = errors.New("account is not in the passwd file")

// lookupShell returns the login shell an account's passwd entry names.
//
// An entry whose seventh field is empty gives "", which the caller reads as
// the default shell — the same reading sshd gives it.
func lookupShell(name string) (string, error) {
	data, err := os.ReadFile(passwdFile)
	if err != nil {
		return "", fmt.Errorf(
			"%s is unreadable, so no account's login shell resolves: %w", passwdFile, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		// name:password:uid:gid:gecos:home:shell. A line with fewer fields
		// is not an entry, which is what skips comments, blanks and the
		// +/- NIS compat lines.
		fields := strings.Split(line, ":")
		if len(fields) < 7 || fields[0] != name {
			continue
		}
		return strings.TrimSpace(fields[6]), nil
	}
	return "", fmt.Errorf(
		"account %q resolved but is not in %s, so its login shell cannot be read "+
			"and the field this host disables an account with would be ignored "+
			"(this binary reads %s directly and does not consult NSS): %w",
		name, passwdFile, passwdFile, errNotInPasswd)
}

// usableShell makes the check sshd makes before it admits a login: the shell
// must exist and be an executable regular file. A passwd entry naming an
// interpreter this host does not have is an account that can run nothing,
// and refusing here turns an obscure exec failure into a message naming the
// file.
//
// There is deliberately NO list of disabled shells. /sbin/nologin and
// /bin/false are executables whose whole job is to refuse, and RUNNING one
// is the refusal — the same refusal every other login path on this host
// gets, in the account holder's own words. A list of paths here would be a
// second copy of that decision, and it would drift from the host's.
func usableShell(name, shell string) error {
	st, err := os.Stat(shell)
	if err != nil {
		return fmt.Errorf("account %q: shell %q does not exist: %w", name, shell, err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("account %q: shell %q is not a regular file", name, shell)
	}
	// Any execute bit, which is the test sshd makes.
	if st.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("account %q: shell %q is not executable", name, shell)
	}
	return nil
}

// resolveRunAs looks up an OS account and fills every field libhoop needs to
// become it, supplementary groups and login shell included.
//
// The groups matter more than they look. A child process that sets a
// credential without naming them does not inherit the parent's: Go issues
// setgroups(0, NULL), which CLEARS them. A real sshd calls initgroups() here.
// So this does the equivalent lookup, and a RunAs whose Groups stayed nil
// refuses the session rather than running it with fewer memberships than
// `id` reports.
//
// A caveat worth knowing: the sidecar builds with CGO_ENABLED=0, so os/user
// reads /etc/passwd and /etc/group directly and does not consult NSS. An
// account that exists only in LDAP or SSSD will not resolve here. That fails
// closed — the session is refused — and it is why the error names the file
// rather than only the account.
func resolveRunAs(name string) (codecssh.RunAs, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return codecssh.RunAs{}, fmt.Errorf(
			"account %q does not resolve (this binary reads /etc/passwd directly "+
				"and does not consult NSS): %w", name, err)
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return codecssh.RunAs{}, fmt.Errorf("account %q has a non-numeric uid %q", name, u.Uid)
	}
	gid, err := strconv.ParseUint(u.Gid, 10, 32)
	if err != nil {
		return codecssh.RunAs{}, fmt.Errorf("account %q has a non-numeric gid %q", name, u.Gid)
	}

	ids, err := u.GroupIds()
	if err != nil {
		return codecssh.RunAs{}, fmt.Errorf(
			"account %q: supplementary groups do not resolve, and a session that "+
				"ran without them would silently have fewer memberships than `id %s` "+
				"reports: %w", name, name, err)
	}
	// Non-nil even when the account has none: nil means "nobody resolved
	// them" across the seam and refuses the session, which is not the same
	// answer as "resolved, and there are none".
	groups := make([]uint32, 0, len(ids))
	for _, raw := range ids {
		g, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return codecssh.RunAs{}, fmt.Errorf(
				"account %q has a non-numeric group id %q", name, raw)
		}
		groups = append(groups, uint32(g))
	}

	// The shell is the account's own, not this process's idea of one: a
	// session that ran /bin/sh for an account the host had pointed at
	// /sbin/nologin would put back the access an administrator removed.
	shell, err := lookupShell(u.Username)
	if err != nil {
		return codecssh.RunAs{}, err
	}
	if shell == "" {
		shell = defaultShell
	}
	if err := usableShell(u.Username, shell); err != nil {
		return codecssh.RunAs{}, err
	}

	return codecssh.RunAs{
		Name:   u.Username,
		UID:    uint32(uid),
		GID:    uint32(gid),
		Groups: groups,
		Home:   u.HomeDir,
		Shell:  shell,
	}, nil
}

// canBecome reports whether this process can run a child as an account.
//
// Dropping to another uid needs privilege; staying put needs none. A listener
// that would refuse every session for lacking it must say so at load, when
// the operator who chose the account is present, not at the first login.
func canBecome(runAs codecssh.RunAs) error {
	if uint32(os.Geteuid()) == runAs.UID {
		return nil
	}
	if os.Geteuid() == 0 {
		return nil
	}
	return fmt.Errorf(
		"this process runs as uid %d and cannot become %q (uid %d); run the "+
			"sidecar as that account, or as root",
		os.Geteuid(), runAs.Name, runAs.UID)
}

// validate checks one lane's ssh block. lane is the operator-facing listener
// name; every message begins with it because a config with two ssh lanes
// reports both in one run.
func (s *SSHConfig) validate(lane string) []string {
	var problems []string
	p := func(format string, args ...any) {
		problems = append(problems, lane+": "+fmt.Sprintf(format, args...))
	}

	if s == nil {
		p("protocol is ssh but there is no \"ssh\" block; the listener has no host " +
			"key and no trusted CA, so it could not complete a handshake")
		return problems
	}

	if strings.TrimSpace(s.HostKey) == "" {
		p("ssh.host_key is required: the listener presents this key as its own identity")
	}
	if strings.TrimSpace(s.TrustedCA) == "" {
		p("ssh.trusted_ca is required: it is the only trust decision this listener " +
			"makes, and without it no certificate can be verified")
	}
	// Readability now, parsing later. Discovering an unreadable host key on
	// the first connection means one failed login per restart and nothing in
	// the startup log; discovering an unreadable CA that way means a
	// listener that refuses everyone and cannot say why. What the bytes
	// CONTAIN is libhoop's to judge, at NewServer, so this does not open a
	// second copy of key parsing on this side of the seam.
	if path := strings.TrimSpace(s.HostKey); path != "" {
		if err := readable(path); err != nil {
			p("ssh.host_key: %v", err)
		}
	}
	if path := strings.TrimSpace(s.TrustedCA); path != "" {
		if err := readable(path); err != nil {
			p("ssh.trusted_ca: %v", err)
		}
	}

	problems = append(problems, s.validateCapabilities(lane)...)

	if _, err := parseSSHDestinations(s.DestinationsAllowed); err != nil {
		p("ssh.destinations_allowed: %v", err)
	}

	problems = append(problems, s.Identity.validate(lane)...)
	return problems
}

// validateCapabilities separates the two mistakes an operator can make in the
// list, because they need different answers.
func (s *SSHConfig) validateCapabilities(lane string) []string {
	if s.CapabilitiesAllowed == nil {
		return nil
	}
	delivered := map[codecssh.Capability]bool{}
	for _, c := range sshDeliveredCapabilities {
		delivered[c] = true
	}
	undelivered := map[codecssh.Capability]bool{}
	for _, c := range sshUndeliveredCapabilities {
		undelivered[c] = true
	}

	var problems []string
	seen := map[codecssh.Capability]bool{}
	for _, raw := range *s.CapabilitiesAllowed {
		name := codecssh.Capability(strings.TrimSpace(raw))
		switch {
		case delivered[name]:
			if seen[name] {
				problems = append(problems, fmt.Sprintf(
					"%s: ssh.capabilities_allowed names %q twice", lane, name))
			}
		case undelivered[name]:
			problems = append(problems, fmt.Sprintf(
				"%s: ssh.capabilities_allowed names %q, which this version does not "+
					"deliver: no handler exists, so admitting it would leave you "+
					"believing the capability is on", lane, name))
		case name == "local_forward" || name == "forward":
			problems = append(problems, fmt.Sprintf(
				"%s: ssh.capabilities_allowed names %q, and forwarding is not a "+
					"member of this list; ssh.destinations_allowed decides where a "+
					"forward may be carried, and an absent or empty one denies every "+
					"forward", lane, name))
		default:
			problems = append(problems, fmt.Sprintf(
				"%s: ssh.capabilities_allowed names %q, which is not a capability "+
					"this design defines (have %s)", lane, name, sshCapabilityNames()))
		}
		seen[name] = true
	}
	return problems
}

// validate checks the identity mapping. A nil block is valid: the subject
// falls back to the key id.
func (i *SSHIdentityConfig) validate(lane string) []string {
	if i == nil {
		return nil
	}
	var problems []string
	check := func(field, source string) {
		if source == "" {
			return
		}
		switch {
		case source == identitySourceKeyID, source == identitySourcePrincipals:
		case strings.HasPrefix(source, identitySourceExtPrefix) &&
			len(source) > len(identitySourceExtPrefix):
		default:
			problems = append(problems, fmt.Sprintf(
				"%s: ssh.identity.%s names %q, which is not a certificate field "+
					"this lane can read (want %s, %s, or %s<name>)",
				lane, field, source,
				identitySourceKeyID, identitySourcePrincipals, identitySourceExtPrefix))
		}
	}
	check("subject", i.Subject)
	check("email", i.Email)
	check("groups", i.Groups)

	for _, name := range i.Attributes {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, fmt.Sprintf(
				"%s: ssh.identity.attributes contains an empty extension name", lane))
		}
	}
	return problems
}

// sshCapabilityNames lists what the design defines, sorted so an error
// message reads the same on every run.
func sshCapabilityNames() string {
	all := make([]string, 0, len(sshDeliveredCapabilities)+len(sshUndeliveredCapabilities))
	for _, c := range sshDeliveredCapabilities {
		all = append(all, string(c))
	}
	for _, c := range sshUndeliveredCapabilities {
		all = append(all, string(c))
	}
	sort.Strings(all)
	return strings.Join(all, ", ")
}

// readable reports whether this process can read a file, opening it rather
// than stat-ing it: a path can exist and still be unreadable, and the mode
// bits are not the whole answer once ACLs or a read-only mount are involved.
func readable(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return f.Close()
}

// validateSSHRules refuses the rule types an SSH statement cannot answer.
//
// The list lives in policy, next to the matchers that make it true; the
// refusal happens here, because startup refusals are the daemon's.
func validateSSHRules(rules []policy.Rule, lane string) []string {
	var problems []string
	for _, msg := range policy.ValidateForSSH(rules) {
		problems = append(problems, lane+": "+msg)
	}
	return problems
}

// validateSSHMasking refuses a mask rule that would change the length of what
// it rewrites.
//
// SSH masks a byte stream in place: there is no length header to correct and
// no frame to rebuild, so a replacement of a different size shifts every byte
// after it. On a terminal that desynchronizes the escape sequences and
// corrupts a full-screen program; on a download it corrupts the file.
//
// The runtime enforces this too — a rewrite that returns a different length
// fails the stream closed rather than forwarding it — so this check is not
// what makes the guarantee true. It is what makes the guarantee CHEAP: an
// operator learns at load, instead of a user learning when their session
// dies mid-command.
//
// It reads two fields of a plugin-owned rule shape, which the daemon
// otherwise keeps out of. Both decide byte length, and nothing else here
// can answer for them: MaskConfig.Rules is raw JSON precisely so the daemon
// links no detector. countMaskRules already decodes these bytes for the
// same reason.
func ValidateSSHMasking(mc MaskConfig, lane string) []string { return validateSSHMasking(mc, lane) }

func validateSSHMasking(mc MaskConfig, lane string) []string {
	if !mc.hasRules() {
		return nil
	}
	var rules []struct {
		Name     string `json:"name"`
		Strategy string `json:"strategy"`
		MaskChar int32  `json:"mask_char"`
	}
	if err := json.Unmarshal(mc.Rules, &rules); err != nil {
		// Shape errors belong to the plugin, which reports them with its own
		// vocabulary when it builds the masker. Saying nothing here avoids
		// two different messages for one typo.
		return nil
	}

	var problems []string
	for i, r := range rules {
		where := fmt.Sprintf("mask rule %d", i+1)
		if r.Name != "" {
			where = fmt.Sprintf("mask rule %q", r.Name)
		}
		// An unwritten strategy is "redact", which replaces a value with
		// "[REDACTED:<entity>]" and is therefore the default that this lane
		// cannot use. Silence is not agreement.
		if r.Strategy != sshOnlyMaskStrategy {
			got := r.Strategy
			if got == "" {
				got = "redact (the default when the key is omitted)"
			}
			problems = append(problems, fmt.Sprintf(
				"%s: %s uses strategy %s; an ssh lane masks bytes in place, so %q is "+
					"the only strategy it can carry", lane, where, got, sshOnlyMaskStrategy))
			continue
		}
		// "mask" preserves the RUNE count, and the stream contract is about
		// BYTES. A multi-byte replacement is length-preserving by the
		// plugin's measure and not by this lane's.
		if r.MaskChar > 127 {
			problems = append(problems, fmt.Sprintf(
				"%s: %s sets mask_char to U+%04X, which is more than one byte; "+
					"strategy %q preserves the rune count, and an ssh lane needs the "+
					"byte count preserved. Use an ASCII character",
				lane, where, r.MaskChar, sshOnlyMaskStrategy))
		}
	}
	return problems
}

// sshOnlyMaskStrategy is the one strategy an ssh lane can carry: it replaces
// each character with a single mask character and changes no length.
const sshOnlyMaskStrategy = "mask"
