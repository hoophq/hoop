package spiffe

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DefaultAudience is the JWT-SVID audience a lane expects when its
// identity block names none. A client fetches its SVID for this audience
// and sends it as the password.
const DefaultAudience = "hoop-sidecar"

// Attribute keys on the identity Verify returns, for select rules and OPA.
const (
	AttrTrustDomain = "trust_domain"
	AttrPath        = "path"
)

// verifier is the `spiffe` identity type for one lane.
type verifier struct {
	audience    string
	trustDomain spiffeid.TrustDomain
	prefix      string
	conn        *conn
	log         *slog.Logger
}

func buildVerifier(cfg daemon.IdentityConfig, deps daemon.IdentityDeps) (daemon.IdentitySource, error) {
	v := &verifier{
		audience: cfg.Audience,
		prefix:   cfg.Prefix,
		conn:     connFor(socketAddr("")),
		log:      deps.Log,
	}
	if v.audience == "" {
		v.audience = DefaultAudience
	}
	if v.log == nil {
		v.log = slog.Default()
	}
	if cfg.TrustDomain != "" {
		td, err := spiffeid.TrustDomainFromString(cfg.TrustDomain)
		if err != nil {
			return nil, fmt.Errorf("identity.trust_domain: %w", err)
		}
		v.trustDomain = td
	}
	if v.prefix != "" {
		if err := spiffeid.ValidatePath(v.prefix); err != nil {
			return nil, fmt.Errorf("identity.prefix: %w", err)
		}
	}
	return v, nil
}

// Verify asks the Workload API to validate the JWT-SVID in secret for the
// lane's audience, then applies the trust domain and prefix the lane
// pinned. The login user is not part of a SPIFFE identity and is ignored.
func (v *verifier) Verify(ctx context.Context, _ string, secret string, _ daemon.PeerInfo) (session.Identity, error) {
	if secret == "" {
		return session.Identity{}, fmt.Errorf("%w: no jwt-svid presented", codectypes.ErrLoginRefused)
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	client, err := v.conn.workloadClient(ctx)
	if err != nil {
		return session.Identity{}, err
	}
	svid, err := client.ValidateJWTSVID(ctx, secret, v.audience)
	if err != nil {
		if refused(err) {
			return session.Identity{}, fmt.Errorf("%w: jwt-svid: %v", codectypes.ErrLoginRefused, err)
		}
		return session.Identity{}, fmt.Errorf("spiffe: validate jwt-svid: %w", err)
	}
	id := svid.ID
	if !v.trustDomain.IsZero() && id.TrustDomain() != v.trustDomain {
		return session.Identity{}, fmt.Errorf("%w: %s is outside trust domain %s", codectypes.ErrLoginRefused, id, v.trustDomain)
	}
	if v.prefix != "" && !underPrefix(id.Path(), v.prefix) {
		return session.Identity{}, fmt.Errorf("%w: %s is not under %s", codectypes.ErrLoginRefused, id, v.prefix)
	}
	return session.Identity{
		Subject: id.String(),
		Attributes: map[string]string{
			AttrTrustDomain: id.TrustDomain().Name(),
			AttrPath:        id.Path(),
		},
	}, nil
}

// underPrefix reports whether path is prefix or a path below it. A plain
// string prefix would let /teams/payments admit /teams/payments-sandbox.
func underPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/")
}

// refused tells a token the agent rejected from an agent that could not
// answer. Only the first is a login refusal; the second must surface as an
// error the operator sees, not as a bad password.
func refused(err error) bool {
	switch status.Code(err) {
	case codes.InvalidArgument, codes.PermissionDenied, codes.Unauthenticated:
		return true
	}
	return false
}
