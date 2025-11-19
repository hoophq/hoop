package idp

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/hoophq/hoop/common/keys"
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

var UserTokens = sync.Map{}

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
	serverConfig            idptypes.ServerConfig
	currentServerAuthConfig *models.ServerAuthConfig
	cacheExpirationTime     time.Time
}

func (v userInfoTokenVerifier) hasServerConfigChanged(providerType idptypes.ProviderType, old, new *models.ServerAuthConfig) (hasChanged bool) {
	switch providerType {
	case idptypes.ProviderTypeLocal:
		var newc models.ServerAuthConfig
		if new != nil {
			newc = *new
		}

		newConfigStr := fmt.Sprintf("authmethod=%v,apikey=%v,grpcurl=%v,shared-signing-key=%v",
			toStr(newc.AuthMethod), toStr(newc.ApiKey), toStr(newc.GrpcServerURL), toStr(newc.SharedSigningKey))

		var oldc models.ServerAuthConfig
		if old != nil {
			oldc = *old
		}

		oldConfigStr := fmt.Sprintf("authmethod=%v,apikey=%v,grpcurl=%v,shared-signing-key=%v",
			toStr(oldc.AuthMethod), toStr(oldc.ApiKey), toStr(oldc.GrpcServerURL), toStr(oldc.SharedSigningKey))
		return newConfigStr != oldConfigStr
	case idptypes.ProviderTypeOIDC, idptypes.ProviderTypeIDP:
		var newc models.ServerAuthConfig
		if new != nil {
			newc = *new
		}
		var oid models.ServerAuthOidcConfig
		if newc.OidcConfig != nil {
			oid = *newc.OidcConfig
		}

		newConfigStr := fmt.Sprintf("authmethod=%v,apikey=%v,grpcurl=%v,issuer=%v,clientid=%v,clientsecret=%v,audience=%v,scopes=%v,groupsclaim=%v",
			toStr(newc.AuthMethod), toStr(newc.ApiKey), toStr(newc.GrpcServerURL), oid.IssuerURL, oid.ClientID, oid.ClientSecret,
			oid.Audience, oid.Scopes, oid.GroupsClaim)

		var oldc models.ServerAuthConfig
		if old != nil {
			oldc = *old
		}

		var oldOidc models.ServerAuthOidcConfig
		if oldc.OidcConfig != nil {
			oldOidc = *oldc.OidcConfig
		}

		oldConfigStr := fmt.Sprintf("authmethod=%v,apikey=%v,grpcurl=%v,issuer=%v,clientid=%v,clientsecret=%v,audience=%v,scopes=%v,groupsclaim=%v",
			toStr(oldc.AuthMethod), toStr(oldc.ApiKey), toStr(oldc.GrpcServerURL), oldOidc.IssuerURL, oldOidc.ClientID, oldOidc.ClientSecret,
			oldOidc.Audience, oldOidc.Scopes, oldOidc.GroupsClaim)

		return newConfigStr != oldConfigStr
	case idptypes.ProviderTypeSAML:
		var newc models.ServerAuthConfig
		if new != nil {
			newc = *new
		}
		var saml models.ServerAuthSamlConfig
		if newc.SamlConfig != nil {
			saml = *newc.SamlConfig
		}

		newConfigStr := fmt.Sprintf("authmethod=%v,apikey=%v,grpcurl=%v,idp-metadata-url=%v,groupsclaim=%v,shared-signing-key=%v",
			toStr(newc.AuthMethod), toStr(newc.ApiKey), toStr(newc.GrpcServerURL), saml.IdpMetadataURL, saml.GroupsClaim, toStr(newc.SharedSigningKey))

		var oldc models.ServerAuthConfig
		if old != nil {
			oldc = *old
		}

		var oldSaml models.ServerAuthSamlConfig
		if oldc.SamlConfig != nil {
			oldSaml = *oldc.SamlConfig
		}

		oldConfigStr := fmt.Sprintf("authmethod=%v,apikey=%v,grpcurl=%v,idp-metadata-url=%v,groupsclaim=%v,shared-signing-key=%v",
			toStr(oldc.AuthMethod), toStr(oldc.ApiKey), toStr(oldc.GrpcServerURL), oldSaml.IdpMetadataURL, oldSaml.GroupsClaim, toStr(oldc.SharedSigningKey))

		return newConfigStr != oldConfigStr
	}
	log.Warnf("unknown provider type %v, cannot determine if server auth config has changed", providerType)
	return true
}

func NewUserInfoTokenVerifierProvider() (UserInfoTokenVerifier, idptypes.ServerConfig, error) {
	serverAuthConfig, providerType, err := LoadServerAuthConfig()
	if err != nil {
		return nil, idptypes.ServerConfig{}, fmt.Errorf("failed to get server auth config: %v", err)
	}

	if obj := singletonStore.Get(singletonStoreKey); obj != nil {
		tokenVerifier, ok := obj.(*userInfoTokenVerifier)
		if !ok {
			singletonStore.Del(singletonStoreKey)
			return nil, idptypes.ServerConfig{},
				fmt.Errorf("internal error, failed to cast singleton store data to UserInfoTokenVerifier")
		}

		hasCacheExpired := time.Now().UTC().After(tokenVerifier.cacheExpirationTime)
		hasConfigChanged := tokenVerifier.hasServerConfigChanged(
			providerType,
			tokenVerifier.currentServerAuthConfig,
			serverAuthConfig,
		)

		if !hasConfigChanged && !hasCacheExpired {
			return tokenVerifier, tokenVerifier.serverConfig, nil
		}

		singletonStore.Del(singletonStoreKey)
		cacheTimeRemaining := tokenVerifier.cacheExpirationTime.Sub(time.Now().UTC()).
			Truncate(time.Minute).
			String()
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
		wrapper.UserInfoTokenVerifier, err = newOidcProvider(serverAuthConfig)
	case idptypes.ProviderTypeSAML:
		wrapper.UserInfoTokenVerifier, err = newSamlProvider(serverAuthConfig)
	case idptypes.ProviderTypeLocal:
		wrapper.UserInfoTokenVerifier, err = newLocalProvider(serverAuthConfig)
	default:
		return nil, idptypes.ServerConfig{}, ErrUnknownIdpProvider
	}

	if err != nil {
		return nil, idptypes.ServerConfig{}, fmt.Errorf("failed to initialize provider: %v", err)
	}

	// set server configuration falling back to appconfig
	appc := appconfig.Get()

	// legacy api key organization id
	orgID := strings.Split(appc.ApiKey(), "|")[0]
	// fallback loading from the server auth config in case it's set
	if serverAuthConfig != nil && serverAuthConfig.OrgID != "" {
		orgID = serverAuthConfig.OrgID
	}
	serverConfig := idptypes.ServerConfig{
		OrgID:      orgID,
		AuthMethod: providerType,
		ApiKey:     appc.ApiKey(),
		GrpcURL:    appc.GrpcURL(),
	}

	if serverAuthConfig != nil {
		serverConfig.OrgID = serverAuthConfig.OrgID
		if serverAuthConfig.ApiKey != nil {
			serverConfig.ApiKey = *serverAuthConfig.ApiKey
		}
		if serverAuthConfig.GrpcServerURL != nil {
			serverConfig.GrpcURL = *serverAuthConfig.GrpcServerURL
		}
	}
	wrapper.serverConfig = serverConfig
	wrapper.currentServerAuthConfig = serverAuthConfig

	singletonStore.Set(singletonStoreKey, &wrapper)
	return wrapper, wrapper.serverConfig, nil
}

func NewTokenVerifierProvider() (TokenVerifier, idptypes.ServerConfig, error) {
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
	return newLocalProvider(serverAuthConfig)
}

func NewOidcVerifierProvider() (OidcVerifier, error) {
	serverAuthConfig, providerType, err := LoadServerAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get server auth config: %v", err)
	}
	if providerType != idptypes.ProviderTypeOIDC && providerType != idptypes.ProviderTypeIDP {
		return nil, ErrUnknownIdpProvider
	}
	return newOidcProvider(serverAuthConfig)
}

func NewSamlVerifierProvider() (SamlVerifier, error) {
	serverAuthConfig, providerType, err := LoadServerAuthConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get server auth config: %v", err)
	}
	if providerType != idptypes.ProviderTypeSAML {
		return nil, ErrUnknownIdpProvider
	}
	return newSamlProvider(serverAuthConfig)
}

// NewOidcProviderOptions creates a new OIDC provider options based on  the server auth config
// falling back to environment variables if the config is not set in the database.
func NewOidcProviderOptions(serverAuthConfig *models.ServerAuthConfig) (opts oidcprovider.Options, err error) {
	hasAuthConfig := serverAuthConfig != nil && serverAuthConfig.OidcConfig != nil
	isIdpEnvsSet := os.Getenv("IDP_ISSUER") != "" || os.Getenv("IDP_URI") != ""

	// load from environment variables if no server auth config is set in the database
	if isIdpEnvsSet && !hasAuthConfig {
		opts, err = oidcprovider.ParseOptionsFromEnv()
		if err != nil {
			return opts, fmt.Errorf("failed to parse OIDC provider options from env: %v", err)
		}
	}

	if hasAuthConfig {
		opts.IssuerURL = serverAuthConfig.OidcConfig.IssuerURL
		opts.ClientID = serverAuthConfig.OidcConfig.ClientID
		opts.ClientSecret = serverAuthConfig.OidcConfig.ClientSecret
		opts.Audience = serverAuthConfig.OidcConfig.Audience
		opts.GroupsClaim = serverAuthConfig.OidcConfig.GroupsClaim
		if len(serverAuthConfig.OidcConfig.Scopes) > 0 {
			opts.CustomScopes = strings.Join(serverAuthConfig.OidcConfig.Scopes, ",")
		}
	}

	return opts, nil
}

func newOidcProvider(serverAuthConfig *models.ServerAuthConfig) (*oidcprovider.Provider, error) {
	opts, err := NewOidcProviderOptions(serverAuthConfig)
	if err != nil {
		return nil, err
	}
	return oidcprovider.New(opts)
}

func newLocalProvider(serverAuthConfig *models.ServerAuthConfig) (*localprovider.Provider, error) {
	var sharedSigningKey string
	if serverAuthConfig != nil && serverAuthConfig.SharedSigningKey != nil {
		sharedSigningKey = *serverAuthConfig.SharedSigningKey
	}
	if sharedSigningKey == "" {
		_, tokenSigningKey, err := keys.GenerateEd25519KeyPair()
		if err != nil {
			return nil, fmt.Errorf("failed to generate ed25519 key pair: %v", err)
		}
		log.Infof("saving shared signing key")
		err = models.CreateServerSharedSigningKey(base64.StdEncoding.EncodeToString(tokenSigningKey))
		if err != nil {
			return nil, fmt.Errorf("failed to create server shared signing key: %v", err)
		}
		return localprovider.New(localprovider.Options{SharedSigningKey: tokenSigningKey})
	}

	tokenSigningKey, err := keys.Base64DecodeEd25519PrivateKey(sharedSigningKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode shared signing key: %v", err)
	}

	return localprovider.New(localprovider.Options{
		SharedSigningKey: tokenSigningKey,
	})
}

func newSamlProvider(serverAuthConfig *models.ServerAuthConfig) (*samlprovider.Provider, error) {
	if serverAuthConfig == nil || serverAuthConfig.SamlConfig == nil {
		return nil, fmt.Errorf("SAML configuration is not set in the database")
	}
	return samlprovider.New(samlprovider.Options{
		IdpMetadataURL: serverAuthConfig.SamlConfig.IdpMetadataURL,
		GroupsClaim:    serverAuthConfig.SamlConfig.GroupsClaim,
	})
}

func toStr(s *string) string { return ptr.ToString(s) }
