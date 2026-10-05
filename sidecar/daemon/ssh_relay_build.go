package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	codecssh "github.com/hoophq/libhoop/v2/codec/ssh"
)

// validate checks one lane's relay block. Every message begins with the
// listener name, because a config with two ssh lanes reports both in one run.
//
// Everything here is knowable at LOAD, and that is the point: a credential
// that does not exist, a key readable by the world, a target that admits
// forwarding and bounds it nowhere — each reaches the operator who wrote it,
// at the moment they wrote it, rather than a user months later on a listener
// the operator still believes is configured.
func (r *SSHRelayConfig) validate(lane string, listenerCaps []codecssh.Capability) []string {
	if r == nil {
		return nil
	}
	var problems []string
	p := func(format string, args ...any) {
		problems = append(problems, lane+": "+fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(r.KnownHosts) == "" {
		p("ssh.relay.known_hosts is required: this listener is the only component " +
			"positioned to notice that a target was replaced, because the client " +
			"verified only this listener's host key")
	}
	if r.HostKeyCheck != "" && !validHostKeyCheck(r.HostKeyCheck) {
		p("ssh.relay.host_key_check is %q; write %s, %s or %s",
			r.HostKeyCheck, codecssh.HostKeyStrict,
			codecssh.HostKeyAcceptNew, codecssh.HostKeyOff)
	}
	if len(r.Targets) == 0 {
		p("ssh.relay.targets is empty: a relay block that terminates nothing is a " +
			"listener you believe is inspecting and is not. Remove the relay block, " +
			"or name the hosts it should terminate")
	}

	// The lane's own known_hosts is checked once, under the lane's mode.
	laneMode := hostKeyCheckOrDefault("", r.HostKeyCheck)
	if path := strings.TrimSpace(r.KnownHosts); path != "" && validHostKeyCheck(string(laneMode)) {
		if err := codecssh.ValidateKnownHosts(path, laneMode); err != nil {
			p("ssh.relay.known_hosts: %v", err)
		}
	}

	if dir := strings.TrimSpace(r.Identities); dir != "" {
		switch info, err := os.Stat(dir); {
		case err != nil:
			p("ssh.relay.identities: %v", err)
		case !info.IsDir():
			p("ssh.relay.identities: %s is not a directory; it holds one private key "+
				"per certificate subject, named by the subject", dir)
		}
	}

	// Sorted so two runs over the same config report the same problems in
	// the same order, which is what makes the output diffable.
	keys := make([]string, 0, len(r.Targets))
	for k := range r.Targets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		problems = append(problems, r.validateTarget(lane, key, r.Targets[key], listenerCaps)...)
	}
	return problems
}

func validHostKeyCheck(v string) bool {
	switch codecssh.HostKeyCheck(strings.TrimSpace(v)) {
	case codecssh.HostKeyStrict, codecssh.HostKeyAcceptNew, codecssh.HostKeyOff:
		return true
	}
	return false
}

// validateTarget checks one entry of the targets map.
func (r *SSHRelayConfig) validateTarget(
	lane, key string,
	t *SSHRelayTarget,
	listenerCaps []codecssh.Capability,
) []string {
	var problems []string
	p := func(format string, args ...any) {
		problems = append(problems,
			fmt.Sprintf("%s: ssh.relay.targets[%q]: %s", lane, key, fmt.Sprintf(format, args...)))
	}

	if t == nil {
		p("has no settings; a target needs at least a credential, or agent_identity")
		return problems
	}
	if _, err := parseSSHTargetKey(key); err != nil {
		p("%v", err)
	}

	// THE TWO CREDENTIAL PAIRS, checked together. Two sources, never a
	// preference and a fallback.
	hasKey := strings.TrimSpace(t.PrivateKey) != ""
	hasIdentities := strings.TrimSpace(r.Identities) != ""
	switch {
	case t.AgentIdentity && hasKey:
		p("names both agent_identity and private_key. They are two credential " +
			"sources, not a preference and a fallback: the client's own agent signs " +
			"as the user, a stored key signs as an account. Pick one")
	case !t.AgentIdentity && !hasKey && !hasIdentities:
		p("has no credential. Give it private_key, or set agent_identity so the " +
			"client's forwarded agent signs, or set ssh.relay.identities so enrolled " +
			"subjects use their own key")
	}

	if hasKey {
		problems = append(problems, checkPrivateKey(lane, key, t.PrivateKey)...)
	}

	// The EFFECTIVE pair, not the written one. A target that sets only
	// host_key_check and inherits the lane's known_hosts still changes what
	// that file must support: accept_new WRITES it, and the lane was checked
	// under the lane's mode, which may be strict and need no write at all.
	// Checking the target's own path alone left exactly that combination
	// unvalidated, and the failure it hides — a rename onto a bind-mounted
	// file — surfaces at the first unknown host rather than at load.
	mode := hostKeyCheckOrDefault(t.HostKeyCheck, r.HostKeyCheck)
	if t.HostKeyCheck != "" && !validHostKeyCheck(t.HostKeyCheck) {
		p("host_key_check is %q; write %s, %s or %s", t.HostKeyCheck,
			codecssh.HostKeyStrict, codecssh.HostKeyAcceptNew, codecssh.HostKeyOff)
	} else if path := firstNonEmpty(t.KnownHosts, r.KnownHosts); path != "" {
		if err := codecssh.ValidateKnownHosts(path, mode); err != nil {
			p("known_hosts %s: %v", path, err)
		}
	}

	caps, capProblems := resolveTargetCapabilities(lane, key, t, listenerCaps)
	problems = append(problems, capProblems...)

	// FORWARDING AND ITS BOUND ARE CHECKED TOGETHER, and this is
	// deliberately unlike destinations_allowed. There, absent is a sensible
	// "no forwards". Here, a target that admits the capability and names no
	// allowlist is a contradiction: both facts are known at load, and what
	// the pairing prevents is a target that admits forwarding and bounds it
	// nowhere.
	admitsForward := false
	for _, c := range caps {
		if c == codecssh.CapLocalForward {
			admitsForward = true
		}
	}
	switch {
	case admitsForward && len(t.ForwardsAllowed) == 0:
		p("admits local_forward and names no forwards_allowed. Forwarded bytes are " +
			"carried without inspection — there is no statement and no terminal " +
			"output in an arbitrary TCP stream — so the allowlist is the only control " +
			"that bounds them, and a target without one would be an unrestricted " +
			"tunnel into this host's network")
	case !admitsForward && len(t.ForwardsAllowed) > 0:
		p("names forwards_allowed but does not admit local_forward, so the list " +
			"bounds nothing. Add local_forward to capabilities_allowed, or remove the " +
			"list")
	}
	for _, raw := range t.ForwardsAllowed {
		if _, err := parseSSHForward(raw); err != nil {
			p("forwards_allowed: %v", err)
		}
	}
	return problems
}

// checkPrivateKey reads a stored credential at load, exactly as host_key and
// trusted_ca are read.
//
// Discovering an unreadable key on the first session means one failed login
// per restart and nothing in the startup log. Discovering a PUBLIC key that
// way means a target nobody can reach, with an authentication failure as the
// only clue — and the public half is the file an operator actually reaches
// for, since it is the one that goes in the target's authorized_keys.
func checkPrivateKey(lane, key, path string) []string {
	var problems []string
	p := func(format string, args ...any) {
		problems = append(problems,
			fmt.Sprintf("%s: ssh.relay.targets[%q]: private_key %s: %s",
				lane, key, path, fmt.Sprintf(format, args...)))
	}
	info, err := os.Stat(path)
	if err != nil {
		p("%v", err)
		return problems
	}
	if info.IsDir() {
		p("is a directory")
		return problems
	}
	if info.Mode().Perm()&0o077 != 0 {
		p("is readable by group or other (mode %04o); ssh(1) refuses a key with "+
			"these permissions and so does this", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		p("%v", err)
		return problems
	}
	if err := codecssh.ValidatePrivateKey(raw); err != nil {
		p("%v", err)
	}
	return problems
}

// resolveTargetCapabilities collapses a target's tri-state against the
// listener's resolved set and reports what is wrong with it.
//
// Absent inherits the listener's list; empty is an error; populated must
// narrow it. local_forward is the one exemption from the subset rule,
// because it is never a member of a listener's list at all — forwarding on
// the listener is destinations_allowed's business.
func resolveTargetCapabilities(
	lane, key string,
	t *SSHRelayTarget,
	listenerCaps []codecssh.Capability,
) ([]codecssh.Capability, []string) {
	var problems []string
	p := func(format string, args ...any) {
		problems = append(problems,
			fmt.Sprintf("%s: ssh.relay.targets[%q]: %s", lane, key, fmt.Sprintf(format, args...)))
	}

	if t.CapabilitiesAllowed == nil {
		// ABSENT INHERITS. The listener's list may name sftp, which a
		// terminated session cannot carry, so it is dropped here rather than
		// reported: the operator wrote it for the lane and it is correct
		// there.
		var out []codecssh.Capability
		for _, c := range listenerCaps {
			if isRelayDelivered(c) {
				out = append(out, c)
			}
		}
		return out, nil
	}
	if len(*t.CapabilitiesAllowed) == 0 {
		// EMPTY ADMITS NOTHING, the same reading the listener's own list
		// gives it, and it is a usable configuration rather than an error: a
		// target reached only by a forward wants exactly this. The tri-state
		// therefore means the same thing at both levels, which is the point
		// of spelling it the same way.
		return nil, nil
	}

	onListener := map[codecssh.Capability]bool{}
	for _, c := range listenerCaps {
		onListener[c] = true
	}
	seen := map[codecssh.Capability]bool{}
	var out []codecssh.Capability
	for _, raw := range *t.CapabilitiesAllowed {
		name := codecssh.Capability(strings.TrimSpace(raw))
		switch {
		case seen[name]:
			p("capabilities_allowed names %q twice", name)
			continue
		case isRelayDelivered(name):
			// local_forward is never on a listener's list, so the subset
			// rule cannot apply to it.
			if name != codecssh.CapLocalForward && !onListener[name] {
				p("capabilities_allowed names %q, which the listener does not admit. "+
					"A target NARROWS the listener's list and cannot widen it; add %q "+
					"to ssh.capabilities_allowed first", name, name)
				continue
			}
			out = append(out, name)
		default:
			if why, named := sshRelayUndelivered[name]; named {
				p("capabilities_allowed names %q, which a terminated session does not "+
					"deliver: %s", name, why)
				continue
			}
			p("capabilities_allowed names %q, which is not a capability a terminated "+
				"session defines (have %s)", name, relayCapabilityNames())
		}
		seen[name] = true
	}
	return out, problems
}

func isRelayDelivered(c codecssh.Capability) bool {
	for _, d := range sshRelayDeliveredCapabilities {
		if d == c {
			return true
		}
	}
	return false
}

func relayCapabilityNames() string {
	names := make([]string, 0, len(sshRelayDeliveredCapabilities))
	for _, c := range sshRelayDeliveredCapabilities {
		names = append(names, string(c))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// buildSSHRelay resolves the relay block once, at load.
//
// It reads every key, enumerates every identity and orders every target, so
// a connection reads resolved values and never re-parses config text. A typo
// cannot become a user's problem on the hot path.
//
// It assumes validate already ran and reports the same failures as errors,
// because a Config assembled in Go rather than loaded from a file skips
// validate entirely and must not get a half-built relay.
func buildSSHRelay(lane string, sc *SSHConfig, log *slog.Logger) (*sshRelay, error) {
	rc := sc.Relay
	if rc == nil {
		return nil, nil
	}

	// validate runs HERE as well, not only from Config.Validate.
	//
	// Validate is reached through LoadConfigBytes, and a caller that supplies
	// its own Loader to Setup builds a Config without ever passing through
	// it — the same caller setupAnalyzer keeps its own call for. What this
	// function checked on its own was strictly less: a private key was read
	// and parsed but its PERMISSIONS were not looked at, so a mode 0644
	// credential loaded; known_hosts had to be non-empty but was never
	// opened, so a path that is a directory loaded; and an empty target map
	// built a relay that terminates nothing, which is a listener the
	// operator believes is inspecting and is not.
	//
	// Calling validate rather than repeating those three is what stops the
	// two paths drifting apart again. The cost is one extra pass over the
	// files at startup.
	if problems := rc.validate(lane, sc.resolveCapabilities()); len(problems) > 0 {
		return nil, errors.New(problems[0])
	}

	r := &sshRelay{lane: lane, identityDir: strings.TrimSpace(rc.Identities)}

	// The overlay is a DIRECTORY and nothing more is read here: keys are
	// looked up per session, so enrolling somebody takes effect on their
	// next connection rather than at the next restart. What load owes is
	// only that the path is a directory this process can read.
	if r.identityDir != "" {
		info, err := os.Stat(r.identityDir)
		switch {
		case err != nil:
			return nil, fmt.Errorf("%s: ssh.relay.identities: %w", lane, err)
		case !info.IsDir():
			return nil, fmt.Errorf("%s: ssh.relay.identities: %s is not a directory",
				lane, r.identityDir)
		}
	}

	listenerCaps := sc.resolveCapabilities()
	for key, tc := range rc.Targets {
		t, err := parseSSHTargetKey(key)
		if err != nil {
			return nil, fmt.Errorf("%s: ssh.relay.targets[%q]: %w", lane, key, err)
		}
		if tc == nil {
			return nil, fmt.Errorf("%s: ssh.relay.targets[%q]: no settings", lane, key)
		}

		caps, problems := resolveTargetCapabilities(lane, key, tc, listenerCaps)
		if len(problems) > 0 {
			return nil, fmt.Errorf("%s", problems[0])
		}
		t.caps = caps
		t.agentIdentity = tc.AgentIdentity
		t.login = firstNonEmpty(tc.Login, rc.Login)
		t.knownHosts = firstNonEmpty(tc.KnownHosts, rc.KnownHosts)
		t.hostKeyCheck = hostKeyCheckOrDefault(tc.HostKeyCheck, rc.HostKeyCheck)

		if path := strings.TrimSpace(tc.PrivateKey); path != "" {
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("%s: ssh.relay.targets[%q]: private_key: %w", lane, key, err)
			}
			if err := codecssh.ValidatePrivateKey(raw); err != nil {
				return nil, fmt.Errorf("%s: ssh.relay.targets[%q]: private_key %s: %w",
					lane, key, path, err)
			}
			t.privateKey, t.keySource = raw, path
		}
		if t.agentIdentity && len(t.privateKey) > 0 {
			return nil, fmt.Errorf("%s: ssh.relay.targets[%q]: names both agent_identity "+
				"and private_key", lane, key)
		}
		if !t.agentIdentity && len(t.privateKey) == 0 && r.identityDir == "" {
			return nil, fmt.Errorf("%s: ssh.relay.targets[%q]: has no credential", lane, key)
		}
		if t.knownHosts == "" {
			return nil, fmt.Errorf("%s: ssh.relay.targets[%q]: no known_hosts file", lane, key)
		}

		for _, raw := range tc.ForwardsAllowed {
			f, err := parseSSHForward(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: ssh.relay.targets[%q]: forwards_allowed: %w",
					lane, key, err)
			}
			t.forwards = append(t.forwards, f)
		}
		if t.admits(codecssh.CapLocalForward) && len(t.forwards) == 0 {
			return nil, fmt.Errorf("%s: ssh.relay.targets[%q]: admits local_forward with "+
				"no forwards_allowed", lane, key)
		}
		// host_key_check: off is LOUD at load, and it has to be said here
		// rather than only in the -validate notes: an operator who never
		// runs that pass would otherwise get no warning at all, on the one
		// setting whose cost is not local to the lane that sets it.
		//
		// HostKeyAlias already moved host identity off the client, so this
		// decides whether the check exists ANYWHERE rather than which party
		// performs it. Every session under it also carries an audit event,
		// which libhoop emits; this is the half an operator sees at startup.
		if t.hostKeyCheck == codecssh.HostKeyOff {
			log.Warn("ssh target runs with host_key_check=off; NOTHING verifies that "+
				"this is the host it was meant to reach, and the client cannot notice "+
				"either because the bastion answers for the target's host key",
				"listener", lane, "target", key, "upstream_known_hosts", t.knownHosts)
		}
		r.targets = append(r.targets, t)
	}
	sortTargets(r.targets)

	for _, line := range uninspectedByAddress(sc.DestinationsAllowed, r.targets) {
		log.Warn(line, "listener", lane)
	}
	return r, nil
}

// uninspectedByAddress finds the gap between the two vocabularies a lane
// writes destinations in.
//
// destinations_allowed says where a forward may be CARRIED and is written in
// addresses. relay.targets says which destinations are TERMINATED and is
// usually written in names, because a name is what the client types, what
// known_hosts records and what HostKeyAlias stands in for. Nothing
// reconciles the two, and the fallthrough for an uncovered destination is to
// carry it blind.
//
// So a lane admitting 10.0.0.0/24 and terminating "*.prod" has an
// uninspected door onto every one of those hosts: a client that types the
// ADDRESS matches no name target, passes destinations_allowed on the
// resolved address, and reaches the same host with no statements, no
// guardrails, no masking and nothing in the trail but the destination. On an
// agent_identity target the client's own certificate opens it, because that
// target trusts the CA rather than a credential this host holds.
//
// A warning and not a refusal. Whether an uncovered destination should be
// carried blind at all is ADR-0027's model, not something to change from
// here — and a config that works today must not stop starting on an upgrade.
// What load owes the operator is that the gap is stated once, at the moment
// they can still choose.
func uninspectedByAddress(destinations []string, targets []*sshRelayTarget) []string {
	if len(targets) == 0 {
		return nil
	}
	parsed, err := parseSSHDestinations(destinations)
	if err != nil {
		// Malformed entries are validate's to report, with the spelling.
		return nil
	}

	var warnings []string
	for i, d := range parsed {
		if coveredByAddressTarget(d, targets) {
			continue
		}
		where := destinations[i]
		if d.any {
			where = sshDestinationAny
		}
		warnings = append(warnings, fmt.Sprintf(
			"ssh: destinations_allowed admits %s but no relay target names an address "+
				"or network covering it, so a client that types an ADDRESS in that range "+
				"reaches the host with the forward CARRIED BLIND: no statements, no "+
				"guardrails, no masking, and only the destination in the trail. Add a "+
				"network target covering it, or narrow destinations_allowed to what the "+
				"name targets resolve to", where))
	}
	return warnings
}

// coveredByAddressTarget reports whether some address or network target
// covers every address this destination admits.
//
// Name and glob targets cannot count. They are matched on the string the
// client typed, and the client that types an address never presents one.
func coveredByAddressTarget(d sshDestination, targets []*sshRelayTarget) bool {
	if d.any {
		// No prefix contains every address, so nothing can cover it.
		return false
	}
	for _, t := range targets {
		// A target bound to one port covers a destination only if the
		// destination is bound to the same one. An unported destination
		// admits every port, and a ported target leaves the rest open.
		if t.port != 0 && t.port != d.port {
			continue
		}
		switch t.kind {
		case sshTargetAddress:
			if d.prefix.Addr() == t.addr && d.prefix.Bits() == t.addr.BitLen() {
				return true
			}
		case sshTargetPrefix:
			if t.prefix.Bits() <= d.prefix.Bits() && t.prefix.Contains(d.prefix.Addr()) {
				return true
			}
		}
	}
	return false
}
