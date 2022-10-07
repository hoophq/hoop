package idp

import (
	"fmt"
	"github.com/runopsio/hoop/gateway/user"
	"gopkg.in/auth0.v5/management"
)

type (
	Auth0Provider struct {
		client *management.Management
	}
)

func NewAuth0Provider() *Auth0Provider {

	m, err := management.New("hoophq.us.auth0.com",
		management.WithClientCredentials(
			"oqtgd5V9oaibJ2MYybaNT3fcOSx3Bm0I",
			"tLDlJNw00poqZ5oe8RYx3mlhIZXdDGA6Jm0eqq_wq0jggPA8vAguQ2-xasv3gudm"))
	if err != nil {
		return nil
	}
	return &Auth0Provider{client: m}
}

func (a *Auth0Provider) ListOrgs(context *user.Context) error {
	result, err := a.client.Organization.List()
	if err != nil {
		return err
	}
	fmt.Printf("result: %v", result)
	return nil
}
