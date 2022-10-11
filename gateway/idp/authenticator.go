package idp

import (
	"context"
	"errors"
	"fmt"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"os"
)

type Authenticator struct {
	*oidc.Provider
	oauth2.Config
	*oidc.IDTokenVerifier
}

func NewAuthenticator() (*Authenticator, error) {
	provider, err := oidc.NewProvider(
		context.Background(), "https://"+os.Getenv("IDP_DOMAIN")+"/",
	)
	if err != nil {
		return nil, err
	}

	conf := oauth2.Config{
		ClientID:     os.Getenv("IDP_CLIENT_ID"),
		ClientSecret: os.Getenv("IDP_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("API_URL") + "/api/callback",
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}

	oidcConfig := &oidc.Config{
		ClientID: conf.ClientID,
	}

	verifier := provider.Verifier(oidcConfig)

	return &Authenticator{
		Provider:        provider,
		Config:          conf,
		IDTokenVerifier: verifier,
	}, nil
}

func (a *Authenticator) VerifyIDToken(ctx context.Context, token *oauth2.Token) (*oidc.IDToken, error) {
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("no id_token field in oauth2 token")
	}

	return a.Verify(ctx, rawIDToken)
}

func (a *Authenticator) VerifyAccessToken(accessToken string) error {
	token, err := a.Verify(context.Background(), accessToken)
	if err != nil {
		return err
	}

	fmt.Printf("%v", token)
	return nil
	//issuerURL, err := url.Parse(os.Getenv("IDP_DOMAIN"))
	//if err != nil {
	//	log.Fatalf("Failed to parse the issuer url: %v", err)
	//}
	//
	//provider := jwks.NewCachingProvider(issuerURL, 5*time.Minute)
	//
	//jwtValidator, err := validator.New(
	//	provider.KeyFunc,
	//	validator.RS256,
	//	issuerURL.String(),
	//	[]string{os.Getenv("AUTH0_AUDIENCE")},
	//	validator.WithCustomClaims(
	//		func() validator.CustomClaims {
	//			return &CustomClaims{}
	//		},
	//	),
	//	validator.WithAllowedClockSkew(time.Minute),
	//)
	//if err != nil {
	//	log.Fatalf("Failed to set up the jwt validator")
	//}
	//
	//validatedToken, err := jwtValidator.ValidateToken(context.Background(), token)
	//if err != nil {
	//	return err
	//}
	//
	//
	//
	//fmt.Printf("%v", validatedToken)

	return nil
}
