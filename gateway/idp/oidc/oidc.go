package oidcprovider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	keyfunc "github.com/MicahParks/keyfunc/v2"
	"github.com/aws/smithy-go/ptr"
	"github.com/coreos/go-oidc/v3/oidc"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"golang.org/x/oauth2"
)

const (
	googleIssuerURL   = "https://accounts.google.com"
	gsuiteGroupsScope = "https://www.googleapis.com/auth/cloud-identity.groups.readonly"
)

type provider struct {
	context context.Context

	issuer                   string
	audience                 string
	clientID                 string
	clientSecret             string
	customScopes             string
	groupsClaim              string
	mustValidateWithUserInfo bool
	mustFetchGsuiteGroups    bool
	serverAuthConfig         *models.ServerAuthConfig

	oidcProvider    *oidc.Provider
	idTokenVerifier *oidc.IDTokenVerifier
	jwks            *keyfunc.JWKS

	oauth2Config oauth2.Config
}

type UserInfoToken struct {
	token *oauth2.Token
}

func New(serverAuthConfig *models.ServerAuthConfig) (*provider, error) {
	ctx := context.Background()
	p := &provider{context: ctx, serverAuthConfig: serverAuthConfig}
	if serverAuthConfig == nil || serverAuthConfig.OidcConfig == nil {
		if err := setProviderConfFromEnvs(p); err != nil {
			return nil, fmt.Errorf("failed to set provider configuration from environment variables: %v", err)
		}
	} else {
		p.issuer = serverAuthConfig.OidcConfig.IssuerURL
		p.clientID = serverAuthConfig.OidcConfig.ClientID
		p.clientSecret = serverAuthConfig.OidcConfig.ClientSecret
		p.audience = serverAuthConfig.OidcConfig.Audience
		p.customScopes = strings.Join(serverAuthConfig.OidcConfig.Scopes, ",")
		p.groupsClaim = serverAuthConfig.OidcConfig.GroupsClaim
		p.mustValidateWithUserInfo = false
	}

	log.Infof("issuer-url=%s, audience=%v, custom-scopes=%v, idp-uri-set=%v, server-auth-config-set=%v",
		p.issuer, p.audience, p.customScopes, os.Getenv("IDP_URI") != "", serverAuthConfig != nil)
	oidcProviderConfig, err := newProviderConfig(p.context, p.issuer)
	if err != nil {
		return nil, err
	}
	oidcProvider := oidcProviderConfig.NewProvider(ctx)
	scopes := []string{oidc.ScopeOpenID, "profile", "email"}
	if p.customScopes != "" {
		scopes = addCustomScopes(scopes, p.customScopes)
	}

	jwksKeyFunc, err := downloadJWKS(oidcProviderConfig.JWKSURL)
	if err != nil {
		return nil, fmt.Errorf("failed to download JWKS from %s, reason=%v", oidcProviderConfig.JWKSURL, err)
	}
	apiURL := appconfig.Get().ApiURL()
	redirectURL := apiURL + "/api/callback"
	log.Infof("loaded oidc provider configuration, redirect-url=%v, with-user-info=%v, auth=%v, token=%v, userinfo=%v, jwks=%v, algorithms=%v, groupsclaim=%v, scopes=%v",
		redirectURL,
		p.mustValidateWithUserInfo,
		oidcProviderConfig.AuthURL,
		oidcProviderConfig.TokenURL,
		oidcProviderConfig.UserInfoURL,
		oidcProviderConfig.JWKSURL,
		oidcProviderConfig.Algorithms,
		p.groupsClaim,
		scopes)

	conf := oauth2.Config{
		ClientID:     p.clientID,
		ClientSecret: p.clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     oidcProvider.Endpoint(),
		Scopes:       scopes,
	}

	oidcConfig := &oidc.Config{
		ClientID:             p.clientID,
		SupportedSigningAlgs: oidcProviderConfig.Algorithms,
	}
	p.oauth2Config = conf
	p.oidcProvider = oidcProvider
	p.idTokenVerifier = oidcProvider.Verifier(oidcConfig)
	p.jwks = jwksKeyFunc
	p.mustFetchGsuiteGroups = p.issuer == googleIssuerURL && slices.Contains(scopes, gsuiteGroupsScope)
	return p, nil
}

func setProviderConfFromEnvs(p *provider) error {
	if idpURI := os.Getenv("IDP_URI"); idpURI != "" {
		u, err := url.Parse(idpURI)
		if err != nil {
			return fmt.Errorf("failed parsing IDP_URI env, reason=%v. Valid format is: <scheme>://<client-id>:<client-secret>@<issuer-host>?<options>=", err)
		}
		if u.User != nil {
			p.clientID = u.User.Username()
			p.clientSecret, _ = u.User.Password()
		}
		if p.clientID == "" || p.clientSecret == "" {
			return fmt.Errorf("missing credentials for IDP_URI env. Valid format is: <scheme>://<client-id>:<client-secret>@<issuer-host>?<options>=")
		}
		p.audience = os.Getenv("IDP_AUDIENCE")
		p.groupsClaim = u.Query().Get("groupsclaim")
		if p.groupsClaim == "" {
			// keep default value
			p.groupsClaim = proto.CustomClaimGroups
		}
		p.customScopes = u.Query().Get("scopes")
		p.mustValidateWithUserInfo = u.Query().Get("_userinfo") == "1"
		qs := u.Query()
		qs.Del("scopes")
		qs.Del("_userinfo")
		qs.Del("groupsclaim")
		encQueryStr := qs.Encode()
		if encQueryStr != "" {
			encQueryStr = "?" + encQueryStr
		}
		// scheme://host:port/path?query#fragment
		p.issuer = fmt.Sprintf("%s://%s%s%s",
			u.Scheme,
			u.Host,
			u.Path,
			encQueryStr,
		)
		return nil
	}
	p.issuer = os.Getenv("IDP_ISSUER")
	p.clientID = os.Getenv("IDP_CLIENT_ID")
	p.clientSecret = os.Getenv("IDP_CLIENT_SECRET")
	p.audience = os.Getenv("IDP_AUDIENCE")
	p.customScopes = os.Getenv("IDP_CUSTOM_SCOPES")
	p.groupsClaim = os.Getenv("IDP_GROUPS_CLAIM")
	if p.groupsClaim == "" {
		p.groupsClaim = proto.CustomClaimGroups
	}

	issuerURL, err := url.Parse(p.issuer)
	if err != nil {
		return fmt.Errorf("failed parsing IDP_ISSUER env, reason=%v", err)
	}
	p.mustValidateWithUserInfo = issuerURL.Query().Get("_userinfo") == "1"
	if p.clientSecret == "" || p.clientID == "" {
		return nil
	}
	qs := issuerURL.Query()
	qs.Del("_userinfo")
	encQueryStr := qs.Encode()
	if encQueryStr != "" {
		encQueryStr = "?" + encQueryStr
	}
	// scheme://host:port/path?query#fragment
	p.issuer = fmt.Sprintf("%s://%s%s%s",
		issuerURL.Scheme,
		issuerURL.Host,
		issuerURL.Path,
		encQueryStr,
	)
	return nil
}

func addCustomScopes(scopes []string, customScope string) []string {
	custom := strings.Split(customScope, ",")
	for _, c := range custom {
		scopes = append(scopes, strings.Trim(c, " "))
	}
	return scopes
}

func downloadJWKS(jwksURL string) (*keyfunc.JWKS, error) {
	log.Infof("downloading provider public key from=%v", jwksURL)
	options := keyfunc.Options{
		Ctx:                 context.Background(),
		RefreshErrorHandler: func(err error) { log.Errorf("error while refreshing public key, reason=%v", err) },
		RefreshInterval:     time.Hour,
		RefreshRateLimit:    time.Minute * 5,
		RefreshTimeout:      time.Second * 10,
		RefreshUnknownKID:   true,
	}
	return keyfunc.Get(jwksURL, options)
}

func (p *provider) GetAudience() string { return p.audience }
func (p *provider) GetAuthCodeURL(state string, opts ...oauth2.AuthCodeOption) string {
	return p.oauth2Config.AuthCodeURL(state, opts...)
}

func (p *provider) ServerConfig() (config idptypes.ServerConfig) {
	// TODO(san): these defaults are kept for backward compatibility
	// in future releases we should deprecated and remove them
	appConfig := appconfig.Get()
	config = idptypes.ServerConfig{
		ApiKey:     appConfig.ApiKey(),
		GrpcURL:    appConfig.GrpcURL(),
		AuthMethod: idptypes.ProviderTypeOIDC,
	}
	if p.serverAuthConfig != nil {
		config.OrgID = p.serverAuthConfig.OrgID
		if p.serverAuthConfig.ApiKey != nil {
			config.ApiKey = *p.serverAuthConfig.ApiKey
		}
		if p.serverAuthConfig.GrpcServerURL != nil {
			config.GrpcURL = *p.serverAuthConfig.GrpcServerURL
		}
	}
	return
}

func (p *provider) HasServerConfigChanged(newConfig *models.ServerAuthConfig) (hasChanged bool) {
	return hasServerConfigChanged(p.serverAuthConfig, newConfig)
}

func hasServerConfigChanged(old, new *models.ServerAuthConfig) bool {
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
}

func (p *provider) VerifyIDTokenForCode(code string) (token *oauth2.Token, uinfo idptypes.ProviderUserInfo, err error) {
	token, err = p.oauth2Config.Exchange(context.Background(), code)
	if err != nil {
		return nil, uinfo, fmt.Errorf("failed exchange authorization code, reason=%v", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, uinfo, errors.New("no id_token field in oauth2 token")
	}
	var idToken *oidc.IDToken
	idToken, err = p.idTokenVerifier.Verify(p.context, rawIDToken)
	if err != nil {
		return nil, uinfo, fmt.Errorf("failed verifying id_token, reason=%v", err)
	}

	idTokenClaims := map[string]any{}
	if err := idToken.Claims(&idTokenClaims); err != nil {
		return nil, uinfo, fmt.Errorf("failed extracting id token claims, reason=%v", err)
	}

	debugClaims(idToken.Subject, idTokenClaims, token)

	uinfo = p.parseUserInfo(idTokenClaims)

	// It's a best effort to sync groups, in case it fails just print the error
	groups, syncGsuiteGroups, err := p.fetchGsuiteGroups(token.AccessToken, uinfo.Email)
	if err != nil {
		log.Errorf("unable to synchronize groups from Google: %v", err)
	}

	// overwrite the groups and indicate it should sync groups
	if syncGsuiteGroups {
		uinfo.Groups = groups
		uinfo.MustSyncGroups = true
		uinfo.MustSyncGsuiteGroups = true
	}

	// uinfo = p.ParseUserInfo(idTokenClaims, token.AccessToken)
	log.With("issuer", idToken.Issuer, "subject", uinfo.Subject, "email", uinfo.Email, "email-verified", uinfo.EmailVerified).
		Infof("token exchanged (oauth2) and id_token verified")

	// overwrite the groups and indicate it should sync groups
	if syncGsuiteGroups {
		uinfo.Groups = groups
		uinfo.MustSyncGroups = true
		uinfo.MustSyncGsuiteGroups = true
	}
	return token, uinfo, err
}

// VerifyAccessTokenWithUserInfo verify the access token by querying the OIDC user info endpoint
func (p *provider) VerifyAccessTokenWithUserInfo(accessToken string) (*idptypes.ProviderUserInfo, error) {
	return p.userInfoEndpoint(accessToken)
}

// VerifyAccessToken validate the access token against the user info endpoint (OIDC) if it's an opaque token.
// Otherwise validate the JWT token following RFC9068 standard.
//
// When a JWT access token are present, this method also validates the validity of the authorized party by checking
// the "azp" and "client_id" claim against the client_id. It prevents access tokens from distinct applications from acessing the system.
// It's usually the case of Auth0 provider but it may be true to other providers as well.
//
// When the "gty" claim is present and set to "client-credentials" it will accept the token as valid.
// Such claim is not part of any specification and it's present when using Auth0.
// In cases of access tokens obtained through grants where no resource owner is involved, such as the client credentials grant,
func (p *provider) VerifyAccessToken(accessToken string) (string, error) {
	isBearerToken := len(strings.Split(accessToken, ".")) != 3
	if isBearerToken || p.mustValidateWithUserInfo {
		uinfo, err := p.userInfoEndpoint(accessToken)
		if err != nil {
			return "", err
		}
		return uinfo.Subject, nil
	}

	token, err := jwt.Parse(accessToken, p.jwks.Keyfunc)
	if err != nil {
		return "", err
	}

	if !token.Valid {
		return "", fmt.Errorf("parse error, token invalid")
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		subject, ok := claims["sub"].(string)
		if !ok || subject == "" {
			return "", fmt.Errorf("'sub' not found or has an empty value")
		}
		if err := p.validateAuthorizedParty(claims); err != nil {
			return "", err
		}
		return subject, nil
	}
	return "", fmt.Errorf("failed type casting token.Claims (%T) to jwt.MapClaims", token.Claims)
}

func (p *provider) validateAuthorizedParty(claims jwt.MapClaims) error {
	// Auth0 specific claim, not part of the spec
	// do not check the authorized party in this case
	gty := fmt.Sprintf("%v", claims["gty"])
	if gty == "client-credentials" {
		return nil
	}

	authorizedParty, hasField := claims["azp"].(string)
	if !hasField {
		authorizedParty, hasField = claims["client_id"].(string)
	}

	if hasField && authorizedParty != p.clientID {
		return fmt.Errorf("it's not an authorized party: %v", authorizedParty)
	}
	return nil
}

func (p *provider) userInfoEndpoint(accessToken string) (*idptypes.ProviderUserInfo, error) {
	user, err := p.oidcProvider.UserInfo(context.Background(), &UserInfoToken{token: &oauth2.Token{
		AccessToken: accessToken,
		TokenType:   "Bearer",
	}})
	if err != nil {
		return nil, fmt.Errorf("failed validating token at userinfo endpoint, err=%v", err)
	}
	claims := map[string]any{}
	if err = user.Claims(&claims); err != nil {
		return nil, fmt.Errorf("failed verifying user info claims, err=%v", err)
	}
	uinfo := p.parseUserInfo(claims)
	uinfo.Email = strings.ToLower(user.Email)
	uinfo.Subject = user.Subject
	uinfo.EmailVerified = &user.EmailVerified
	if len(user.Profile) > 0 {
		uinfo.Profile = user.Profile
	}
	return &uinfo, nil
}

// FetchGroups parses user information from the provided token claims.
func (p *provider) parseUserInfo(idTokenClaims map[string]any) (u idptypes.ProviderUserInfo) {
	email, _ := idTokenClaims["email"].(string)
	email = strings.ToLower(email)
	if profile, ok := idTokenClaims["name"].(string); ok {
		u.Profile = profile
	}
	profilePicture, _ := idTokenClaims["picture"].(string)
	if emailVerified, ok := idTokenClaims["email_verified"].(bool); ok {
		u.EmailVerified = &emailVerified
	}
	u.Picture = profilePicture
	u.Email = email
	switch groupsClaim := idTokenClaims[p.groupsClaim].(type) {
	case string:
		u.MustSyncGroups = true
		if groupsClaim != "" {
			u.Groups = []string{groupsClaim}
		}
	case []any:
		u.MustSyncGroups = true
		for _, g := range groupsClaim {
			groupName, _ := g.(string)
			if groupName == "" {
				continue
			}
			u.Groups = append(u.Groups, groupName)
		}
	case nil: // noop
	default:
		log.Errorf("failed syncing group claims, reason=unknown type:%T", groupsClaim)
	}
	return
}

func (u *UserInfoToken) Token() (*oauth2.Token, error) { return u.token, nil }

func debugClaims(subject string, claims map[string]any, accessToken *oauth2.Token) {
	logClaims := []any{}
	for claimKey, claimVal := range claims {
		val := fmt.Sprintf("%v", claimVal)
		if len(val) > 200 {
			logClaims = append(logClaims, claimKey, val[:200]+fmt.Sprintf(" (... %v)", len(val)-200))
			continue
		}
		logClaims = append(logClaims, claimKey, val)
	}
	var isJWT bool
	var jwtHeader []byte
	if parts := strings.Split(accessToken.AccessToken, "."); len(parts) == 3 {
		isJWT = true
		jwtHeader, _ = base64.RawStdEncoding.DecodeString(parts[0])
	}
	log.With(logClaims...).Infof("jwt-access-token=%v, jwt-header=%v, id_token claims=%v, subject=%s, admingroup=%q",
		isJWT, string(jwtHeader),
		len(claims), subject, types.GroupAdmin)
}

func toStr(s *string) string { return ptr.ToString(s) }
