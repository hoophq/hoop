package idp

import (
	"context"
	"errors"
	"fmt"
	"github.com/MicahParks/keyfunc"
	pb "github.com/runopsio/hoop/common/proto"
	"log"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v4"
	"golang.org/x/oauth2"
)

const (
	defaultProviderDomain   = "https://hoophq.us.auth0.com"
	defaultProviderClientID = "DatIOCxntNv8AZrQLVnLb3tr1Y3oVwGW"
)

type Provider struct {
	ApiURL       string
	Domain       string
	Audience     string
	ClientID     string
	ClientSecret string
	Profile      string

	*oidc.Provider
	oauth2.Config
	*oidc.IDTokenVerifier
	context.Context
	*keyfunc.JWKS
}

func (a *Provider) VerifyIDToken(token *oauth2.Token) (*oidc.IDToken, error) {
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("no id_token field in oauth2 token")
	}

	return a.Verify(a.Context, rawIDToken)
}

func (a *Provider) VerifyAccessToken(accessToken string) (string, error) {
	token, err := jwt.Parse(accessToken, a.JWKS.Keyfunc)
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

func NewProvider(profile string) *Provider {
	ctx := context.Background()
	apiURL := os.Getenv("API_URL")

	if apiURL == "" {
		apiURL = "http://localhost:3333"
	}

	provider := &Provider{
		Context: ctx,
		ApiURL:  apiURL,
		Profile: profile,
	}

	if profile == pb.DevProfile {
		return provider
	}

	provider.Domain = os.Getenv("IDP_DOMAIN")
	provider.ClientID = os.Getenv("IDP_CLIENT_ID")
	provider.ClientSecret = os.Getenv("IDP_CLIENT_SECRET")
	provider.Audience = os.Getenv("IDP_AUDIENCE")
	jwksURL := os.Getenv("IDP_JWKS_URL")

	if provider.ClientSecret == "" {
		panic(errors.New("missing required ID provider variables"))
	}

	if provider.Domain == "" {
		provider.Domain = defaultProviderDomain
	}

	if provider.ClientID == "" {
		provider.ClientID = defaultProviderClientID
	}

	if jwksURL == "" {
		jwksURL = "https://" + provider.Domain + "/.well-known/jwks.json"
	}

	if strings.Contains(provider.Domain, pb.ProviderOkta) {
		ctx = oidc.InsecureIssuerURLContext(ctx, providerDomain(provider.Domain))
		provider.Context = ctx
	}

	oidcProvider, err := oidc.NewProvider(
		provider.Context, providerDomain(provider.Domain),
	)
	if err != nil {
		panic(err)
	}

	conf := oauth2.Config{
		ClientID:     provider.ClientID,
		ClientSecret: provider.ClientSecret,
		RedirectURL:  provider.ApiURL + "/api/callback",
		Endpoint:     oidcProvider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	oidcConfig := &oidc.Config{
		ClientID: provider.ClientID,
	}

	if strings.Contains(provider.Domain, pb.ProviderOkta) {
		oidcConfig.SkipIssuerCheck = true
		authorizeEndpoint := os.Getenv("IDP_AUTHORIZE_ENDPOINT")
		tokenEndpoint := os.Getenv("IDP_TOKEN_ENDPOINT")
		if authorizeEndpoint == "" || tokenEndpoint == "" {
			panic(errors.New("missing authorization and token endpoints urls"))
		}
		conf.Endpoint = oauth2.Endpoint{
			AuthURL:   authorizeEndpoint,
			TokenURL:  tokenEndpoint,
			AuthStyle: 0,
		}
	}

	provider.Config = conf
	provider.Provider = oidcProvider
	provider.IDTokenVerifier = provider.Verifier(oidcConfig)
	provider.JWKS = downloadJWKS(jwksURL)

	return provider
}

var invalidAuthErr = errors.New("invalid auth")

func downloadJWKS(jwksURL string) *keyfunc.JWKS {
	fmt.Println("Downloading provider public key")
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

func providerDomain(domain string) string {
	return "https://" + domain + "/"
}
