package idp

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc"
	pb "github.com/runopsio/hoop/common/proto"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v4"
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
		return p.UserInfoEndpoint(accessToken)
	}

	token, err := jwt.Parse(accessToken, p.JWKS.Keyfunc)
	if err != nil {
		return "", invalidAuthErr
	}

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		identifier, ok := claims["sub"].(string)
		if !ok || identifier == "" {
			return "", invalidAuthErr
		}
		return identifier, nil
	}

	return "", invalidAuthErr
}

func (p *Provider) UserInfoEndpoint(accessToken string) (string, error) {
	user, err := p.Provider.UserInfo(context.Background(), &UserInfoToken{token: &oauth2.Token{
		AccessToken: accessToken,
		TokenType:   "Bearer",
	}})
	if err != nil {
		return "", invalidAuthErr
	}
	if user == nil {
		return "", invalidAuthErr
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
			panic("API_URL environment variable is required")
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
		panic(errors.New("missing required ID provider variables"))
	}

	if provider.Issuer == "" {
		provider.Issuer = DefaultProviderIssuer
	}

	if provider.ClientID == "" {
		provider.ClientID = defaultProviderClientID
	}

	oidcProviderConfig, err := newProviderConfig(provider.Context, provider.Issuer)
	if err != nil {
		panic(err)
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
		panic(err)
	}
	return jwks
}

func (u *UserInfoToken) Token() (*oauth2.Token, error) {
	return u.token, nil
}
