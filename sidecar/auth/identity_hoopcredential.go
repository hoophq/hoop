package auth

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// scramSecretPrefix marks a secret a driver passes after a SCRAM proof
// against the served material: "scram:<hex hash>" names the credential it
// proved, since the raw key never crossed the wire.
const scramSecretPrefix = "scram:"

// hoopCredentialIdentity verifies a login against the credentials the plane
// minted for this lane. The secret is the raw key, matched by digest, or the
// scram form above, matched by hash. The login user, when the protocol
// carries one, has to be the credential's subject or email: a key is one
// person's, and alice's key presented as bob is refused, not recorded as
// alice.
type hoopCredentialIdentity struct {
	lane string
	set  *credentialSet
}

func buildHoopCredentialIdentity(_ daemon.IdentityConfig, deps daemon.IdentityDeps) (daemon.IdentitySource, error) {
	set, ok := deps.CredentialSet.(*credentialSet)
	if !ok || set == nil {
		return nil, errors.New("hoop_credential needs the credential set the plane serves")
	}
	return &hoopCredentialIdentity{lane: deps.Lane, set: set}, nil
}

func (h *hoopCredentialIdentity) Verify(_ context.Context, user, secret string, _ daemon.PeerInfo) (session.Identity, error) {
	var entry daemon.CredentialSetEntry
	var ok bool
	if hash, proven := strings.CutPrefix(secret, scramSecretPrefix); proven {
		entry, ok = h.set.byHash(h.lane, hash)
	} else if secret != "" {
		entry, ok = h.set.match(h.lane, secret)
	}
	if !ok {
		return session.Identity{}, daemon.Refused("no hoop credential for this listener matches the one presented")
	}
	if expired(entry, time.Now()) {
		return session.Identity{}, daemon.Refused("the hoop credential of %s expired at %s", entry.Subject, entry.ExpiresAt.UTC().Format(time.RFC3339))
	}
	if user != "" && user != entry.Subject && user != entry.Email {
		return session.Identity{}, daemon.Refused("the hoop credential belongs to %s, not to the login user %q", entry.Subject, user)
	}
	return session.Identity{
		Subject:    entry.Subject,
		Email:      entry.Email,
		Groups:     slices.Clone(entry.Groups),
		Attributes: map[string]string{daemon.IdentityTypeAttribute: daemon.IdentityHoopCredential},
	}, nil
}

// scramVerifier is the relay's hook for a driver acting as the SCRAM server
// on the client leg: the material of the lane's credential for this login
// user, Hash included so the driver can name it back.
func (h *hoopCredentialIdentity) scramVerifier() func(user string) (codectypes.SCRAMMaterial, bool) {
	return func(user string) (codectypes.SCRAMMaterial, bool) { return h.set.scram(h.lane, user) }
}
