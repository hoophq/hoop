package daemon

import (
	"context"
	"net/netip"

	codecssh "github.com/hoophq/libhoop/v2/codec/ssh"
)

// relayTarget answers, for HOP 1, whether one client-opened forward is
// terminated.
//
// A destination the target map covers is terminated and inspected; one it
// does not cover is carried blind, which is exactly what this listener did
// before the relay block existed. That is the only new branch, and it runs
// AFTER destinations_allowed has admitted the destination — reachability
// stays decided in one place, so a target the allowlist does not cover can
// never be reached by naming it in the map.
func (c *sshConnState) relayTarget(
	_ context.Context,
	dest codecssh.Destination,
	dialing netip.AddrPort,
) (*codecssh.RelayTarget, *codecssh.Refusal) {
	t := c.relay.match(dest.Host, dialing)
	if t == nil {
		return nil, nil
	}
	return &codecssh.RelayTarget{
		Name: t.key,
		// The address the destination RESOLVED to, which is the address
		// destinations_allowed just admitted. Re-resolving the name here
		// would open the window between the checked address and the
		// connected one that the single resolve exists to close.
		Addr: dialing.String(),
		// Verified under the NAME the client asked for, because that is
		// what an operator writes known_hosts entries against and what
		// ssh(1) checks. Destination.String brackets an IPv6 literal, which
		// known_hosts and net.SplitHostPort both require.
		HostKeyName:   dest.String(),
		Login:         t.login,
		AgentIdentity: t.agentIdentity,
		KnownHosts:    t.knownHosts,
		HostKeyCheck:  t.hostKeyCheck,
		Capabilities:  t.caps,
		Forward:       c.relayForward(t),
	}, nil
}

// relayForward bounds an `ssh -L` opened inside a terminated session.
//
// The destination is checked as the client NAMED it, because the target
// dials it: 127.0.0.1 in the allowlist is the target's loopback, resolved on
// the target's host and in the target's network. The refusal reaches the
// client on its own channel and the reason reaches the trail; libhoop emits
// the event, so nothing is recorded twice here.
func (c *sshConnState) relayForward(t *sshRelayTarget) func(context.Context, codecssh.Destination) *codecssh.Refusal {
	return func(_ context.Context, dest codecssh.Destination) *codecssh.Refusal {
		if t.allowsForward(dest.Host, dest.Port) {
			return nil
		}
		// The destination is the client's own words, so it travels back and
		// makes the refusal actionable. The config key that decided and the
		// target key that carries it do not: the trail records both against
		// this event, which is where an operator reads them.
		return codecssh.Refuse("%s is not a destination this host may reach", dest)
	}
}

// relayCredential answers, for HOP 2, what this person authenticates to the
// target with.
//
// It runs where the subject is known, and it is a MAP LOOKUP against a
// directory read at load — never a path built out of a field the certificate
// carries. That is why the traversal problem a per-session search would have
// is absent rather than guarded.
func (c *sshConnState) relayCredential(ctx context.Context) (*codecssh.RelayCredential, *codecssh.Refusal) {
	key, source, fellBack, err := c.relay.credentialFor(c.target, c.subject)
	if err != nil {
		// THE FULL REASON to the trail and the log; the client's line to the
		// client. The two are separated here rather than at each refusal so
		// a new one cannot leak by being written the obvious way.
		c.log.Warn("ssh upstream credential refused",
			"target", c.target.key, "subject", c.subject, "error", err)
		c.event(ctx, "upstream_credential_refused", map[string]string{
			"target":  c.target.key,
			"subject": c.subject,
			"reason":  err.Error(),
		})
		return nil, codecssh.Refuse("%s", clientMessage(err))
	}
	if fellBack {
		// EVERY TIME, because the failure mode of an overlay is silent
		// degradation: a subject format changed at the IdP, or a filename
		// that no longer matches, keeps working on the shared key and
		// quietly stops naming anyone. A target meant to be per-user should
		// omit private_key once its people are enrolled.
		c.log.Warn("ssh upstream credential fell back to the target's shared key",
			"target", c.target.key, "subject", c.subject,
			"identities", c.relay.identityDir)
		c.event(ctx, "upstream_credential_fallback", map[string]string{
			"target":  c.target.key,
			"subject": c.subject,
			"source":  source,
			"reason": "this subject has no key in the identities directory, so the " +
				"target's shared key was used and its sshd will not see who this was",
		})
	}
	return &codecssh.RelayCredential{PrivateKey: key, Source: source}, nil
}
