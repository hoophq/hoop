package idp

import (
	"fmt"
	"time"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/gateway/appconfig"
	localprovider "github.com/hoophq/hoop/gateway/idp/local"
	oidcprovider "github.com/hoophq/hoop/gateway/idp/oidc"
	samlprovider "github.com/hoophq/hoop/gateway/idp/saml"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"golang.org/x/oauth2"
)

var ErrUnknownIdpProvider = fmt.Errorf("unknown idp provider")

type TokenVerifier interface {
	VerifyAccessToken(accessToken string) (subject string, err error)
	ServerConfig() idptypes.ServerConfig
}

type OidcVerifier interface {
	TokenVerifier
	GetAudience() string
	GetAuthCodeURL(state string, opts ...oauth2.AuthCodeOption) string
	VerifyIDTokenForCode(code string) (*oauth2.Token, idptypes.ProviderUserInfo, error)
	VerifyAccessTokenWithUserInfo(accessToken string) (*idptypes.ProviderUserInfo, error)
}

type LocalVerifier interface {
	TokenVerifier
	NewAccessToken(subject, email string, tokenDuration time.Duration) (string, error)
}

type SamlVerifier interface {
	TokenVerifier
	NewAccessToken(subject, email string, tokenDuration time.Duration) (string, error)
	ServiceProvider() *samlprovider.ServiceProvider
}

type UserInfoTokenVerifier interface {
	TokenVerifier
	VerifyAccessTokenWithUserInfo(accessToken string) (*idptypes.ProviderUserInfo, error)
	HasServerConfigChanged(newConfig *models.ServerAuthConfig) (hasChanged bool)
}

var (
	singletonStore         = memory.New()
	singletonStoreKey      = "1"
	singletonCacheDuration = time.Minute * 30
)

// LoadServerAuthConfig loads the server authentication configuration and returns it along with the provider type.
// It retrieves the configuration from the database and determines the provider type based on the auth method.
// The provider type fallbacks to environment variables in case there's no configuration in the database.
func LoadServerAuthConfig() (*models.ServerAuthConfig, idptypes.ProviderType, error) {
	serverAuthConfig, err := models.GetServerAuthConfig()
	switch err {
	case models.ErrNotFound, nil:
		providerType := idptypes.ProviderType(appconfig.Get().AuthMethod())
		if serverAuthConfig != nil && serverAuthConfig.AuthMethod != nil {
			providerType = idptypes.ProviderType(*serverAuthConfig.AuthMethod)
		}
		return serverAuthConfig, providerType, nil
	default:
		return nil, "", err
	}
}

type userInfoTokenVerifier struct {
	UserInfoTokenVerifier
	cacheExpirationTime time.Time
}

func NewUserInfoTokenVerifierProvider() (UserInfoTokenVerifier, error) {
	serverAuthConfig, providerType, err := LoadServerAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get server auth config: %v", err)
	}

	if obj := singletonStore.Get(singletonStoreKey); obj != nil {
		tokenVerifier, ok := obj.(*userInfoTokenVerifier)
		if !ok {
			singletonStore.Del(singletonStoreKey)
			return nil, fmt.Errorf("internal error, failed to cast singleton store data to UserInfoTokenVerifier")
		}

		hasConfigChanged := tokenVerifier.HasServerConfigChanged(serverAuthConfig)
		hasCacheExpired := time.Now().UTC().After(tokenVerifier.cacheExpirationTime)

		if !hasConfigChanged && !hasCacheExpired {
			return tokenVerifier, nil
		}

		singletonStore.Del(singletonStoreKey)
		cacheTimeRemaining := tokenVerifier.cacheExpirationTime.Sub(time.Now().UTC()).String()
		log.Warnf("clearing singleton store for authentication verifier, "+
			"provider-type=%v, configuration-changed=%v, cache-expired=%v, expires-in=%v",
			providerType, hasConfigChanged, hasCacheExpired, cacheTimeRemaining)
	}

	wrapper := userInfoTokenVerifier{
		UserInfoTokenVerifier: nil,
		cacheExpirationTime:   time.Now().UTC().Add(singletonCacheDuration),
	}
	switch providerType {
	case idptypes.ProviderTypeOIDC, idptypes.ProviderTypeIDP:
		wrapper.UserInfoTokenVerifier, err = oidcprovider.New(serverAuthConfig)
	case idptypes.ProviderTypeSAML:
		wrapper.UserInfoTokenVerifier, err = samlprovider.New(serverAuthConfig)
	case idptypes.ProviderTypeLocal:
		wrapper.UserInfoTokenVerifier, err = localprovider.New(serverAuthConfig)
	default:
		return nil, ErrUnknownIdpProvider
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize provider: %v", err)
	}

	singletonStore.Set(singletonStoreKey, &wrapper)
	return wrapper, nil
}

func NewTokenVerifierProvider() (TokenVerifier, error) {
	return NewUserInfoTokenVerifierProvider()
}

func NewLocalVerifierProvider() (LocalVerifier, error) {
	serverAuthConfig, providerType, err := LoadServerAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get server auth config: %v", err)
	}
	if providerType != idptypes.ProviderTypeLocal {
		return nil, ErrUnknownIdpProvider
	}
	return localprovider.New(serverAuthConfig)
}

func NewOidcVerifierProvider() (OidcVerifier, error) {
	serverAuthConfig, providerType, err := LoadServerAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get server auth config: %v", err)
	}
	if providerType != idptypes.ProviderTypeOIDC && providerType != idptypes.ProviderTypeIDP {
		return nil, ErrUnknownIdpProvider
	}
	return oidcprovider.New(serverAuthConfig)
}

func NewSamlVerifierProvider() (SamlVerifier, error) {
	serverAuthConfig, providerType, err := LoadServerAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get server auth config: %v", err)
	}
	if providerType != idptypes.ProviderTypeSAML {
		return nil, ErrUnknownIdpProvider
	}
	return samlprovider.New(serverAuthConfig)
}
