package idp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	keyfunc "github.com/MicahParks/keyfunc/v2"
	"github.com/coreos/go-oidc/v3/oidc"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"golang.org/x/oauth2"
)

type (
	Provider struct {
		Issuer           string
		Audience         string
		ClientID         string
		ClientSecret     string
		CustomScopes     string
		GroupsClaim      string
		ApiURL           string
		authWithUserInfo bool

		*oidc.Provider
		oauth2.Config
		*oidc.IDTokenVerifier
		context.Context
		*keyfunc.JWKS
	}
	UserInfoToken struct {
		token *oauth2.Token
	}
	ProviderUserInfo struct {
		Subject       string
		Email         string
		EmailVerified *bool
		Groups        []string
		Profile       string
		Picture       string

		MustSyncGroups bool
	}
)

func (p *Provider) VerifyIDToken(token *oauth2.Token) (*oidc.IDToken, error) {
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("no id_token field in oauth2 token")
	}

	return p.Verify(p.Context, rawIDToken)
}

// VerifyAccessTokenWithUserInfo verify the access token by querying the OIDC user info endpoint
func (p *Provider) VerifyAccessTokenWithUserInfo(accessToken string) (*ProviderUserInfo, error) {
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
func (p *Provider) VerifyAccessToken(accessToken string) (string, error) {
	if len(strings.Split(accessToken, ".")) != 3 || p.authWithUserInfo {
		uinfo, err := p.userInfoEndpoint(accessToken)
		if err != nil {
			return "", err
		}
		return uinfo.Subject, nil
	}

	token, err := jwt.Parse(accessToken, p.JWKS.Keyfunc)
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

func (p *Provider) validateAuthorizedParty(claims jwt.MapClaims) error {
	// auth0 specific claim, not part of the spec
	// allow client credentials clients as authorized party
	gty := fmt.Sprintf("%v", claims["gty"])
	if gty == "client-credentials" {
		return nil
	}

	authorizedParty, hasField := claims["azp"].(string)
	if !hasField {
		authorizedParty, hasField = claims["client_id"].(string)
	}

	if hasField && authorizedParty != p.ClientID {
		return fmt.Errorf("it's not an authorized party: %v", authorizedParty)
	}
	return nil
}

func (p *Provider) userInfoEndpoint(accessToken string) (*ProviderUserInfo, error) {
	user, err := p.Provider.UserInfo(context.Background(), &UserInfoToken{token: &oauth2.Token{
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
	uinfo := ParseIDTokenClaims(claims, p.GroupsClaim)
	uinfo.Email = user.Email
	uinfo.Subject = user.Subject
	uinfo.EmailVerified = &user.EmailVerified
	if len(user.Profile) > 0 {
		uinfo.Profile = user.Profile
	}
	return &uinfo, nil
}

func NewProvider(apiURL string) *Provider {
	if appconfig.Get().AuthMethod() == "local" {
		return &Provider{Context: context.Background(), ApiURL: apiURL}
	}
	ctx := context.Background()
	provider := &Provider{
		Context: ctx,
		ApiURL:  apiURL,
	}

	if err := setProviderConfFromEnvs(provider); err != nil {
		log.Fatal(err)
	}

	log.Infof("issuer-url=%s, audience=%v, custom-scopes=%v, idp-uri-set=%v",
		provider.Issuer, provider.Audience, provider.CustomScopes, os.Getenv("IDP_URI") != "")
	oidcProviderConfig, err := newProviderConfig(provider.Context, provider.Issuer)
	if err != nil {
		log.Fatal(err)
	}
	oidcProvider := oidcProviderConfig.NewProvider(ctx)
	scopes := []string{oidc.ScopeOpenID, "profile", "email"}
	if provider.CustomScopes != "" {
		scopes = addCustomScopes(scopes, provider.CustomScopes)
	}
	redirectURL := apiURL + "/api/callback"
	log.Infof("loaded oidc provider configuration, redirect-url=%v, with-user-info=%v, auth=%v, token=%v, userinfo=%v, jwks=%v, algorithms=%v, groupsclaim=%v, scopes=%v",
		redirectURL,
		provider.authWithUserInfo,
		oidcProviderConfig.AuthURL,
		oidcProviderConfig.TokenURL,
		oidcProviderConfig.UserInfoURL,
		oidcProviderConfig.JWKSURL,
		oidcProviderConfig.Algorithms,
		provider.GroupsClaim,
		scopes)

	conf := oauth2.Config{
		ClientID:     provider.ClientID,
		ClientSecret: provider.ClientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     oidcProvider.Endpoint(),
		Scopes:       scopes,
	}

	oidcConfig := &oidc.Config{
		ClientID:             provider.ClientID,
		SupportedSigningAlgs: oidcProviderConfig.Algorithms,
	}
	provider.Config = conf
	provider.Provider = oidcProvider
	provider.IDTokenVerifier = provider.Verifier(oidcConfig)
	provider.JWKS = downloadJWKS(oidcProviderConfig.JWKSURL)
	return provider
}

func setProviderConfFromEnvs(p *Provider) error {
	if idpURI := os.Getenv("IDP_URI"); idpURI != "" {
		u, err := url.Parse(idpURI)
		if err != nil {
			return fmt.Errorf("failed parsing IDP_URI env, reason=%v. Valid format is: <scheme>://<client-id>:<client-secret>@<issuer-host>?<options>=", err)
		}
		if u.User != nil {
			p.ClientID = u.User.Username()
			p.ClientSecret, _ = u.User.Password()
		}
		if p.ClientID == "" || p.ClientSecret == "" {
			return fmt.Errorf("missing credentials for IDP_URI env. Valid format is: <scheme>://<client-id>:<client-secret>@<issuer-host>?<options>=")
		}
		p.Audience = os.Getenv("IDP_AUDIENCE")
		p.GroupsClaim = u.Query().Get("groupsclaim")
		if p.GroupsClaim == "" {
			// keep default value
			p.GroupsClaim = proto.CustomClaimGroups
		}
		p.CustomScopes = u.Query().Get("scopes")
		p.authWithUserInfo = u.Query().Get("_userinfo") == "1"
		qs := u.Query()
		qs.Del("scopes")
		qs.Del("_userinfo")
		qs.Del("groupsclaim")
		encQueryStr := qs.Encode()
		if encQueryStr != "" {
			encQueryStr = "?" + encQueryStr
		}
		// scheme://host:port/path?query#fragment
		p.Issuer = fmt.Sprintf("%s://%s%s%s",
			u.Scheme,
			u.Host,
			u.Path,
			encQueryStr,
		)
		return nil
	}
	p.Issuer = os.Getenv("IDP_ISSUER")
	p.ClientID = os.Getenv("IDP_CLIENT_ID")
	p.ClientSecret = os.Getenv("IDP_CLIENT_SECRET")
	p.Audience = os.Getenv("IDP_AUDIENCE")
	p.CustomScopes = os.Getenv("IDP_CUSTOM_SCOPES")
	p.GroupsClaim = proto.CustomClaimGroups

	issuerURL, err := url.Parse(p.Issuer)
	if err != nil {
		return fmt.Errorf("failed parsing IDP_ISSUER env, reason=%v", err)
	}
	p.authWithUserInfo = issuerURL.Query().Get("_userinfo") == "1"
	if p.ClientSecret == "" || p.ClientID == "" {
		return nil
	}
	qs := issuerURL.Query()
	qs.Del("_userinfo")
	encQueryStr := qs.Encode()
	if encQueryStr != "" {
		encQueryStr = "?" + encQueryStr
	}
	// scheme://host:port/path?query#fragment
	p.Issuer = fmt.Sprintf("%s://%s%s%s",
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

func downloadJWKS(jwksURL string) *keyfunc.JWKS {
	log.Infof("downloading provider public key from=%v", jwksURL)
	options := keyfunc.Options{
		Ctx:                 context.Background(),
		RefreshErrorHandler: func(err error) { log.Errorf("error while refreshing public key, reason=%v", err) },
		RefreshInterval:     time.Hour,
		RefreshRateLimit:    time.Minute * 5,
		RefreshTimeout:      time.Second * 10,
		RefreshUnknownKID:   true,
	}

	var err error
	jwks, err := keyfunc.Get(jwksURL, options)
	if err != nil {
		log.Fatalf("failed loading jwks url, reason=%v", err)
	}
	return jwks
}

func ParseIDTokenClaims(idTokenClaims map[string]any, groupsClaimName string) (u ProviderUserInfo) {
	email, _ := idTokenClaims["email"].(string)
	if profile, ok := idTokenClaims["name"].(string); ok {
		u.Profile = profile
	}
	profilePicture, _ := idTokenClaims["picture"].(string)
	if emailVerified, ok := idTokenClaims["email_verified"].(bool); ok {
		u.EmailVerified = &emailVerified
	}
	u.Picture = profilePicture
	u.Email = email
	switch groupsClaim := idTokenClaims[groupsClaimName].(type) {
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
