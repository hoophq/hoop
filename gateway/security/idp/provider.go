package idp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type (
	contextKey   int
	providerJSON struct {
		Issuer      string   `json:"issuer"`
		AuthURL     string   `json:"authorization_endpoint"`
		TokenURL    string   `json:"token_endpoint"`
		JWKSURL     string   `json:"jwks_uri"`
		UserInfoURL string   `json:"userinfo_endpoint"`
		Algorithms  []string `json:"id_token_signing_alg_values_supported"`
	}
)

var issuerURLKey contextKey

func unmarshalResp(r *http.Response, body []byte, v interface{}) error {
	err := json.Unmarshal(body, &v)
	if err == nil {
		return nil
	}
	ct := r.Header.Get("Content-Type")
	mediaType, _, parseErr := mime.ParseMediaType(ct)
	if parseErr == nil && mediaType == "application/json" {
		return fmt.Errorf("got Content-Type = application/json, but could not unmarshal as JSON: %v", err)
	}
	return fmt.Errorf("expected Content-Type = application/json, got %q: %v", ct, err)
}

// newProvider fix non standard identity providers which uses
// custom open id suffixes. It has the same logic of oidc.NewProvider(...)
func newProviderConfig(ctx context.Context, issuer string) (*oidc.ProviderConfig, error) {
	openidConfigSuffix := "/.well-known/openid-configuration"
	switch {
	case strings.Contains(issuer, "okta.com"):
		openidConfigSuffix = "/.well-known/oauth-authorization-server"
	}
	wellKnown := strings.TrimSuffix(issuer, "/") + openidConfigSuffix
	req, err := http.NewRequest("GET", wellKnown, nil)
	if err != nil {
		return nil, err
	}
	client := http.DefaultClient
	if c, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok {
		client = c
	}
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("unable to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, body)
	}

	var p providerJSON
	err = unmarshalResp(resp, body, &p)
	if err != nil {
		return nil, fmt.Errorf("oidc: failed to decode provider discovery object: %v", err)
	}

	issuerURL, skipIssuerValidation := ctx.Value(issuerURLKey).(string)
	if !skipIssuerValidation {
		issuerURL = issuer
	}
	if p.Issuer != issuerURL && !skipIssuerValidation {
		return nil, fmt.Errorf("oidc: issuer did not match the issuer returned by provider, expected %q got %q", issuer, p.Issuer)
	}
	return &oidc.ProviderConfig{
		IssuerURL:   issuerURL,
		AuthURL:     p.AuthURL,
		TokenURL:    p.TokenURL,
		UserInfoURL: p.UserInfoURL,
		Algorithms:  p.Algorithms,
		JWKSURL:     p.JWKSURL,
	}, nil
}
