package idp

import (
	"fmt"

	"github.com/hoophq/hoop/gateway/appconfig"
	localprovider "github.com/hoophq/hoop/gateway/idp/local"
	oidcprovider "github.com/hoophq/hoop/gateway/idp/oidc"
	samlprovider "github.com/hoophq/hoop/gateway/idp/saml"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"golang.org/x/oauth2"
)

var ErrUnknownIdpProvider = fmt.Errorf("unknown idp provider")

type TokenVerifier interface {
	VerifyAccessToken(accessToken string) (subject string, err error)
}

type OidcVerifier interface {
	TokenVerifier
	GetAudience() string
	GetAuthCodeURL(state string, opts ...oauth2.AuthCodeOption) string
	VerifyIDTokenForCode(code string) (*oauth2.Token, idptypes.ProviderUserInfo, error)
	VerifyAccessTokenWithUserInfo(accessToken string) (*idptypes.ProviderUserInfo, error)
}

type SamlVerifier interface {
	TokenVerifier
	ServiceProvider() *samlprovider.ServiceProvider
}

type UserInfoTokenVerifier interface {
	VerifyAccessTokenWithUserInfo(accessToken string) (*idptypes.ProviderUserInfo, error)
	TokenVerifier
}

func NewUserInfoTokenVerifierProvider() (UserInfoTokenVerifier, error) {
	providerType := idptypes.ProviderType(appconfig.Get().AuthMethod())
	switch providerType {
	case idptypes.ProviderTypeOIDC, idptypes.ProviderTypeIDP:
		return oidcprovider.GetInstance()
	case idptypes.ProviderTypeSAML:
		return samlprovider.GetInstance()
	case idptypes.ProviderTypeLocal:
		return localprovider.GetInstance()
	default:
		return nil, ErrUnknownIdpProvider
	}
}

func NewTokenVerifierProvider() (TokenVerifier, error) {
	return NewUserInfoTokenVerifierProvider()
}

func NewOidcVerifierProvider() (OidcVerifier, error) {
	providerType := idptypes.ProviderType(appconfig.Get().AuthMethod())
	if providerType != idptypes.ProviderTypeOIDC && providerType != idptypes.ProviderTypeIDP {
		return nil, ErrUnknownIdpProvider
	}
	return oidcprovider.GetInstance()
}

func NewSamlVerifierProvider() (SamlVerifier, error) {
	providerType := idptypes.ProviderType(appconfig.Get().AuthMethod())
	if providerType != idptypes.ProviderTypeSAML {
		return nil, ErrUnknownIdpProvider
	}
	return samlprovider.GetInstance()
}
