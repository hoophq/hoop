package idp

import (
	"context"
	"errors"
	"fmt"
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
	defaultProviderIssuer   = "https://hoophq.us.auth0.com/"
	defaultProviderClientID = "DatIOCxntNv8AZrQLVnLb3tr1Y3oVwGW"
	defaultProviderAudience = defaultProviderIssuer + "api/v2/"
)

var invalidAuthErr = errors.New("invalid auth")

type Provider struct {
	Issuer       string
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
	// https://www.oauth.com/oauth2-servers/redirect-uris/
	apiURL := os.Getenv("API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:3333/callback"
	}
	apiURL = strings.TrimSuffix(apiURL, "/")

	provider := &Provider{
		Context: ctx,
		Profile: profile,
	}

	if profile == pb.DevProfile {
		return provider
	}

	provider.Issuer = os.Getenv("IDP_ISSUER")
	provider.ClientID = os.Getenv("IDP_CLIENT_ID")
	provider.ClientSecret = os.Getenv("IDP_CLIENT_SECRET")
	provider.Audience = os.Getenv("IDP_AUDIENCE")

	if provider.ClientSecret == "" {
		panic(errors.New("missing required ID provider variables"))
	}

	if provider.Issuer == "" {
		provider.Issuer = defaultProviderIssuer
	}

	if provider.ClientID == "" {
		provider.ClientID = defaultProviderClientID
	}

	if provider.Audience == "" {
		provider.Audience = defaultProviderAudience
	}
	oidcProviderConfig, err := newProviderConfig(provider.Context, provider.Issuer)
	if err != nil {
		panic(err)
	}
	oidcProvider := oidcProviderConfig.NewProvider(ctx)
	conf := oauth2.Config{
		ClientID:     provider.ClientID,
		ClientSecret: provider.ClientSecret,
		RedirectURL:  apiURL + "/api/callback",
		Endpoint:     oidcProvider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
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
