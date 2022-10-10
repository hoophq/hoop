package idp

import (
	"gopkg.in/auth0.v5/management"
	"os"
)

type (
	Auth0Provider struct {
		client *management.Management
	}
)

func NewAuth0Provider() *Auth0Provider {
	providerDomain := os.Getenv("AUTH0_DOMAIN")
	if providerDomain == "" {
		providerDomain = "hoophq.us.auth0.com"
	}

	clientID := os.Getenv("AUTH0_CLIENT_ID")
	if clientID == "" {
		return nil
	}

	clientSecret := os.Getenv("AUTH0_CLIENT_SECRET")
	if clientSecret == "" {
		return nil
	}

	m, err := management.New(providerDomain,
		management.WithClientCredentials(
			clientID,
			clientSecret))
	if err != nil {
		return nil
	}
	return &Auth0Provider{client: m}
}
