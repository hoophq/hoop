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
		ApiURL:  apiURL}

	if profile == pb.DevProfile {
		return provider
	}

	domain := os.Getenv("IDP_DOMAIN")
	clientID := os.Getenv("IDP_CLIENT_ID")
	clientSecret := os.Getenv("IDP_CLIENT_SECRET")
	audience := os.Getenv("IDP_AUDIENCE")
	jwksURL := os.Getenv("IDP_JWKS_URL")

	if domain == "" {
		domain = defaultProviderDomain
	}

	if clientID == "" {
		clientID = defaultProviderClientID
	}

	if jwksURL == "" {
		jwksURL = domain + "/.well-known/jwks.json"
	}

	if clientSecret == "" {
		panic(errors.New("missing required ID provider variables"))
	}

	if strings.Contains(domain, "okta") {
		ctx = oidc.InsecureIssuerURLContext(ctx, "https://"+domain+"/")
		provider.Context = ctx
	}

	provider.Domain = domain
	provider.ClientID = clientID
	provider.ClientSecret = clientSecret
	provider.Audience = audience

	oidcProvider, err := oidc.NewProvider(
		provider.Context, "https://"+provider.Domain+"/",
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

	if strings.Contains(domain, "okta") {
		oidcConfig.SkipIssuerCheck = true
		conf.Endpoint = oauth2.Endpoint{
			AuthURL:   os.Getenv("IDP_AUTHORIZE_ENDPOINT"),
			TokenURL:  os.Getenv("IDP_TOKEN_ENDPOINT"),
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
