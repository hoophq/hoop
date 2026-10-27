package auth

import (
	"context"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
)

// noneIdentity is the verifier of a unix-socket lane: whoever can open the
// socket is the lane's one caller, and filesystem permissions decided that
// before any byte arrived. The login user and secret are ignored, so the
// trail names the principal the operator wrote in the lane's context, if
// any, and nothing a client typed.
type noneIdentity struct{}

func buildNoneIdentity(daemon.IdentityConfig, daemon.IdentityDeps) (daemon.IdentitySource, error) {
	return noneIdentity{}, nil
}

func (noneIdentity) Verify(context.Context, string, string, daemon.PeerInfo) (session.Identity, error) {
	return session.Identity{Attributes: map[string]string{daemon.IdentityTypeAttribute: daemon.IdentityNone}}, nil
}
