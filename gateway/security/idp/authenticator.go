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

func (p *Provider) VerifyAccessTokenWithUserInfo(accessToken string) (*ProviderUserInfo, error) {
	return p.userInfoEndpoint(accessToken)
}

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
		// https://openid.net/specs/openid-connect-core-1_0.html
		// If an azp (authorized party) Claim is present, the Client SHOULD verify that its client_id is the Claim Value.
		if authorizedParty, ok := claims["azp"].(string); ok {
			if authorizedParty != p.ClientID {
				return "", fmt.Errorf("it's not an authorized party")
			}
		}
		return subject, nil
	}
	return "", fmt.Errorf("failed type casting token.Claims (%T) to jwt.MapClaims", token.Claims)
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
	ctx := context.Background()
	apiURL = strings.TrimSuffix(apiURL, "/")

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
	log.Infof("loaded oidc provider configuration, with-user-info=%v, auth=%v, token=%v, userinfo=%v, jwks=%v, algorithms=%v, groupsclaim=%v, scopes=%v",
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
		RedirectURL:  apiURL + "/api/callback",
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
			u.Hostname(),
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
		return fmt.Errorf("missing IDP credentials: IDP_CLIENT_ID, IDP_CLIENT_SECRET")
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
		issuerURL.Hostname(),
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
