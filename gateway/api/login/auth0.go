package loginapi

import (
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
)

var auth0AuthorizerParams = []string{"screen_hint", "prompt"}

// parseAuth0QueryParams parses query string for the /authorize endpoint for the auth0 provider.
// It creates a list of parameters to append when generating the login url to propagate to auth0.
//
// # These parameters must be used only when the the gateway is configured with Auth0
//
// See: https://auth0.com/docs/authenticate/login/auth0-universal-login/universal-login-vs-classic-login/universal-experience#signup
func (h *handler) parseAuth0QueryParams(c *gin.Context) []oauth2.AuthCodeOption {
	var params []oauth2.AuthCodeOption
	// auth0 special query string for controlling which
	for _, key := range auth0AuthorizerParams {
		if val := c.Query(key); val != "" {
			params = append(params, oauth2.SetAuthURLParam(key, val))
		}
	}
	return params
}
