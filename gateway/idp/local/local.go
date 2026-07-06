package localprovider

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/keys"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
)

var ErrNotImplemented = fmt.Errorf("local: user info endpoint not implemented for local provider")

type Options struct {
	SharedSigningKey ed25519.PrivateKey
}

type Provider struct {
	Options
	tokenSigningKey ed25519.PrivateKey
}

func New(opts Options) (*Provider, error) {
	return &Provider{tokenSigningKey: opts.SharedSigningKey}, nil
}

func (p *Provider) VerifyAccessTokenWithUserInfo(accessToken string) (*idptypes.ProviderUserInfo, error) {
	return nil, ErrNotImplemented
}

func (p *Provider) NewAccessToken(subject, email string, tokenDuration time.Duration) (string, error) {
	return keys.NewJwtToken(p.tokenSigningKey, subject, email, tokenDuration)
}

func (p *Provider) VerifyAccessToken(accessToken string) (string, error) {
	if len(p.tokenSigningKey) == 0 {
		return "", fmt.Errorf("signing key is not set")
	}

	pubKey, ok := p.tokenSigningKey.Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("internal error, failed to cast private key to ed25519.PublicKey")
	}
	return keys.VerifyAccessToken(accessToken, pubKey)
}

// RenewAccessToken implements idp.SessionRenewer: it re-mints a still-valid
// token as a sliding session. The renewed token carries a fresh expiry but
// preserves the original auth_time claim, and renewal is refused once the
// original authentication is older than maxSessionAge — a stolen token can
// therefore only be kept alive up to the absolute cap, never indefinitely.
func (p *Provider) RenewAccessToken(currentToken string, tokenDuration, maxSessionAge time.Duration) (string, error) {
	if len(p.tokenSigningKey) == 0 {
		return "", fmt.Errorf("signing key is not set")
	}
	pubKey, ok := p.tokenSigningKey.Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("internal error, failed to cast private key to ed25519.PublicKey")
	}
	claims, err := keys.VerifySessionClaims(currentToken, pubKey)
	if err != nil {
		return "", fmt.Errorf("cannot renew: %w", err)
	}
	sessionAge := time.Since(claims.AuthTime)
	if sessionAge > maxSessionAge {
		return "", fmt.Errorf("session exceeded the maximum age (%s > %s), re-authentication required",
			sessionAge.Truncate(time.Minute), maxSessionAge)
	}
	return keys.NewJwtTokenWithAuthTime(p.tokenSigningKey, claims.Subject, claims.Email, tokenDuration, claims.AuthTime)
}
