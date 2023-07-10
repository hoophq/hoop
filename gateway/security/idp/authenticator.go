package idp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v4"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"golang.org/x/oauth2"
)

const (
	DefaultProviderIssuer   = "https://hoophq.us.auth0.com/"
	defaultProviderClientID = "DatIOCxntNv8AZrQLVnLb3tr1Y3oVwGW"
	defaultProviderAudience = DefaultProviderIssuer + "api/v2/"
)

var invalidAuthErr = errors.New("invalid auth")

type (
	Provider struct {
		Issuer       string
		Audience     string
		ClientID     string
		ClientSecret string
		Profile      string
		CustomScopes string
		ApiURL       string

		*oidc.Provider
		oauth2.Config
		*oidc.IDTokenVerifier
		context.Context
		*keyfunc.JWKS
	}
	UserInfoToken struct {
		token *oauth2.Token
	}
)

func (p *Provider) VerifyIDToken(token *oauth2.Token) (*oidc.IDToken, error) {
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("no id_token field in oauth2 token")
	}

	return p.Verify(p.Context, rawIDToken)
}

func (p *Provider) VerifyAccessToken(accessToken string) (string, error) {
	if len(strings.Split(accessToken, ".")) != 3 {
		return p.userInfoEndpoint(accessToken)
	}

	token, err := jwt.Parse(accessToken, p.JWKS.Keyfunc)
	if err != nil {
		return "", err
	}

	if !token.Valid {
		return "", fmt.Errorf("parse error, token invalid")
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok {
		identifier, ok := claims["sub"].(string)
		if !ok || identifier == "" {
			return "", fmt.Errorf("'sub' not found or has an empty value")
		}
		// https://openid.net/specs/openid-connect-core-1_0.html
		// If an azp (authorized party) Claim is present, the Client SHOULD verify that its client_id is the Claim Value.
		if authorizedParty, ok := claims["azp"].(string); ok {
			if authorizedParty != p.ClientID {
				return "", fmt.Errorf("it's not an authorized party")
			}
		}
		return identifier, nil
	}
	return "", fmt.Errorf("failed type casting token.Claims (%T) to jwt.MapClaims", token.Claims)
}

func (p *Provider) userInfoEndpoint(accessToken string) (string, error) {
	log.Debugf("starting user info endpoint token check")
	user, err := p.Provider.UserInfo(context.Background(), &UserInfoToken{token: &oauth2.Token{
		AccessToken: accessToken,
		TokenType:   "Bearer",
	}})
	if err != nil {
		return "", fmt.Errorf("failed validating token at userinfo endpoint, err=%v", err)
	}
	return user.Subject, nil
}

func NewProvider(profile string) *Provider {
	ctx := context.Background()

	apiURL := os.Getenv("API_URL")
	if apiURL == "" {
		if profile == pb.DevProfile {
			apiURL = "http://localhost:8009"
		} else {
			log.Fatal("API_URL environment variable is required")
		}
	}
	apiURL = strings.TrimSuffix(apiURL, "/")

	provider := &Provider{
		Context: ctx,
		Profile: profile,
		ApiURL:  apiURL,
	}

	if profile == pb.DevProfile {
		return provider
	}

	provider.Issuer = os.Getenv("IDP_ISSUER")
	provider.ClientID = os.Getenv("IDP_CLIENT_ID")
	provider.ClientSecret = os.Getenv("IDP_CLIENT_SECRET")
	provider.Audience = os.Getenv("IDP_AUDIENCE")
	provider.CustomScopes = os.Getenv("IDP_CUSTOM_SCOPES")

	if provider.ClientSecret == "" {
		log.Fatal(errors.New("missing required ID provider variables"))
	}

	if provider.Issuer == "" {
		provider.Issuer = DefaultProviderIssuer
	}

	if provider.ClientID == "" {
		provider.ClientID = defaultProviderClientID
	}

	oidcProviderConfig, err := newProviderConfig(provider.Context, provider.Issuer)
	if err != nil {
		log.Fatal(err)
	}
	oidcProvider := oidcProviderConfig.NewProvider(ctx)
	scopes := []string{oidc.ScopeOpenID, "profile", "email"}
	if provider.CustomScopes != "" {
		scopes = addCustomScopes(scopes, provider.CustomScopes)
	}

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

func addCustomScopes(scopes []string, customScope string) []string {
	custom := strings.Split(customScope, ",")
	for _, c := range custom {
		scopes = append(scopes, strings.Trim(c, " "))
	}
	return scopes
}

func downloadJWKS(jwksURL string) *keyfunc.JWKS {
	log.Println("downloading provider public key")
	options := keyfunc.Options{
		Ctx: context.Background(),
		RefreshErrorHandler: func(err error) {
			log.Printf("There was an error with the jwt.Keyfunc\nError: %s", err.Error())
		},
		RefreshInterval:   time.Hour,
		RefreshRateLimit:  time.Minute * 5,
		RefreshTimeout:    time.Second * 10,
		RefreshUnknownKID: true,
	}

	var err error
	jwks, err := keyfunc.Get(jwksURL, options)
	if err != nil {
		log.Fatal(err)
	}
	return jwks
}

func (u *UserInfoToken) Token() (*oauth2.Token, error) {
	return u.token, nil
}
