package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/session"
)

// A jwt lane takes a signed token from any issuer the lane names: a
// company identity provider, a CI provider, a gateway's authorization
// service, the control plane. It is the kubernetes verifier with the issuer
// made a setting and the claims mapped by configuration; the key fetch, the
// registered-claim checks and the signature check are the same code.
//
// The issuer is the authentication boundary: whoever signs with its key is
// whoever the rules allow. The audience keeps a token minted for another
// service from opening a database.
const (
	defaultSubjectClaim = "sub"
	defaultEmailClaim   = "email"
	defaultGroupsClaim  = "groups"
)

type jwtIdentity struct {
	*kubernetesIdentity
	subjectClaim string
	emailClaim   string
	groupsClaim  string
}

func buildJWTIdentity(cfg IdentityConfig, deps IdentityDeps) (IdentitySource, error) {
	issuer := strings.TrimRight(cfg.Issuer, "/")
	if issuer == "" {
		return nil, fmt.Errorf("identity type jwt needs identity.issuer")
	}
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("identity.issuer %q is not an https URL", cfg.Issuer)
	}
	if cfg.JWKSURL != "" {
		ju, err := url.Parse(cfg.JWKSURL)
		if err != nil || ju.Scheme != "https" || ju.Host == "" {
			return nil, fmt.Errorf("identity.jwks_url %q is not an https URL", cfg.JWKSURL)
		}
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}
	v := &jwtIdentity{
		kubernetesIdentity: &kubernetesIdentity{
			kind:     IdentityJWT,
			issuer:   issuer,
			audience: cfg.audience(),
			roots:    deps.Roots,
			log:      log,
			now:      time.Now,
			jwksURI:  cfg.JWKSURL,
		},
		subjectClaim: defaultSubjectClaim,
		emailClaim:   defaultEmailClaim,
		groupsClaim:  defaultGroupsClaim,
	}
	if c := cfg.Claims; c != nil {
		if c.Subject != "" {
			v.subjectClaim = c.Subject
		}
		if c.Email != "" {
			v.emailClaim = c.Email
		}
		if c.Groups != "" {
			v.groupsClaim = c.Groups
		}
	}
	return v, nil
}

func (j *jwtIdentity) Verify(ctx context.Context, user string, secret string, _ PeerInfo) (session.Identity, error) {
	if secret == "" {
		return session.Identity{}, refused("no token presented")
	}
	var claims map[string]json.RawMessage
	if err := j.verifyToken(ctx, secret, &claims); err != nil {
		return session.Identity{}, err
	}
	subject, err := claimString(claims, j.subjectClaim)
	if err != nil {
		return session.Identity{}, refused("JWT claim %q: %v", j.subjectClaim, err)
	}
	if subject == "" {
		return session.Identity{}, refused("JWT has no %q claim", j.subjectClaim)
	}
	email, err := claimString(claims, j.emailClaim)
	if err != nil {
		return session.Identity{}, refused("JWT claim %q: %v", j.emailClaim, err)
	}
	groups, err := claimStrings(claims, j.groupsClaim)
	if err != nil {
		return session.Identity{}, refused("JWT claim %q: %v", j.groupsClaim, err)
	}
	// A protocol that carries a login user must agree with the token, so a
	// session is never recorded under one name and opened under another.
	if user != "" && user != subject && (email == "" || user != email) {
		return session.Identity{}, refused("the token names %q, and the login user is %q", subject, user)
	}
	return session.Identity{
		Subject:    subject,
		Email:      email,
		Groups:     groups,
		Attributes: map[string]string{identityTypeAttribute: IdentityJWT, "issuer": j.issuer},
	}, nil
}

// claimString reads a string claim; absent is "".
func claimString(claims map[string]json.RawMessage, name string) (string, error) {
	raw, ok := claims[name]
	if !ok {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("not a string")
	}
	return s, nil
}

// claimStrings reads a claim written as a list of strings or as one string;
// absent is nil.
func claimStrings(claims map[string]json.RawMessage, name string) ([]string, error) {
	raw, ok := claims[name]
	if !ok {
		return nil, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, fmt.Errorf("not a string or a list of strings")
	}
	return []string{one}, nil
}
