package oidcprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const (
	directoryMaxPages     = 100
	directoryPageSize     = 100 // Auth0 `take` max
	entraIssuerHost       = "login.microsoftonline.com"
	entraLegacyIssuerHost = "sts.windows.net"
	graphScope            = "https://graph.microsoft.com/.default"
	graphGroupsURL        = "https://graph.microsoft.com/v1.0/groups?$select=id,displayName&$top=999"
)

var directoryHTTPClient = &http.Client{Timeout: 15 * time.Second}

type directoryVendor int

const (
	vendorUnknown directoryVendor = iota
	vendorAuth0
	vendorEntra
	vendorGoogle
	vendorOkta
)

// directoryVendor infers the vendor from the issuer host. There is no
// standard OIDC directory endpoint, so only known hosts are supported.
func (p *Provider) directoryVendor() directoryVendor {
	if p.IssuerURL == googleIssuerURL {
		return vendorGoogle
	}
	u, err := url.Parse(p.IssuerURL)
	if err != nil {
		return vendorUnknown
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == entraIssuerHost || host == entraLegacyIssuerHost:
		return vendorEntra
	case strings.HasSuffix(host, ".auth0.com"):
		return vendorAuth0
	case strings.HasSuffix(host, ".okta.com"),
		strings.HasSuffix(host, ".oktapreview.com"),
		strings.HasSuffix(host, ".okta-emea.com"):
		return vendorOkta
	}
	return vendorUnknown
}

func (p *Provider) clientCredentialsToken(ctx context.Context, scopes []string, audience string) (*oauth2.Token, error) {
	cfg := clientcredentials.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		TokenURL:     p.oauth2Config.Endpoint.TokenURL,
		Scopes:       scopes,
	}
	if audience != "" {
		cfg.EndpointParams = url.Values{"audience": {audience}}
	}
	tok, err := cfg.Token(context.WithValue(ctx, oauth2.HTTPClient, directoryHTTPClient))
	if err != nil {
		return nil, fmt.Errorf("failed obtaining client credentials token, reason=%v", err)
	}
	return tok, nil
}

// ListDirectoryGroups lists every group of the identity provider directory
// using the OIDC client as the service account (client-credentials grant).
func (p *Provider) ListDirectoryGroups(ctx context.Context) ([]idptypes.DirectoryGroup, error) {
	switch p.directoryVendor() {
	case vendorAuth0:
		issuer := strings.TrimSuffix(p.IssuerURL, "/")
		audience := p.Audience
		if audience == "" {
			audience = issuer + "/api/v2/"
		}
		tok, err := p.clientCredentialsToken(ctx, nil, audience)
		if err != nil {
			return nil, err
		}
		return listAuth0Groups(ctx, tok.AccessToken, issuer+"/api/v2/groups")
	case vendorEntra:
		tok, err := p.clientCredentialsToken(ctx, []string{graphScope}, "")
		if err != nil {
			return nil, err
		}
		return listEntraGroups(ctx, tok.AccessToken, graphGroupsURL)
	case vendorGoogle:
		return nil, fmt.Errorf("%w: Google Workspace requires a service account with the Admin SDK Directory scope; the OIDC client cannot list groups", idptypes.ErrDirectoryGroupsUnsupported)
	case vendorOkta:
		return nil, fmt.Errorf("%w: Okta requires a private_key_jwt service app for the client credentials grant; the OIDC client secret is rejected", idptypes.ErrDirectoryGroupsUnsupported)
	default:
		return nil, fmt.Errorf("%w: issuer %s", idptypes.ErrDirectoryGroupsUnsupported, p.IssuerURL)
	}
}

func directoryGet(ctx context.Context, accessToken, rawURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed creating directory request, reason=%v", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := directoryHTTPClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed performing directory request, reason=%v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed reading directory response, reason=%v", err)
	}
	return body, resp.StatusCode, nil
}

type auth0Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type auth0GroupsPage struct {
	Groups []auth0Group `json:"groups"`
	Next   string       `json:"next"`
}

// listAuth0Groups walks the Auth0 Management API groups endpoint. The
// response is either a bare array or a {groups, next} cursor object.
func listAuth0Groups(ctx context.Context, accessToken, groupsURL string) ([]idptypes.DirectoryGroup, error) {
	var groups []idptypes.DirectoryGroup
	var next string
	for count := 0; ; count++ {
		if count >= directoryMaxPages {
			return nil, fmt.Errorf("reached max pagination (%v) listing directory groups", directoryMaxPages)
		}
		query := url.Values{"take": {fmt.Sprint(directoryPageSize)}}
		if next != "" {
			query.Set("from", next)
		}
		body, status, err := directoryGet(ctx, accessToken, groupsURL+"?"+query.Encode())
		if err != nil {
			return nil, fmt.Errorf("page=%v, %v", count, err)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("unable to list groups from auth0 management api, status=%v, body=%v", status, string(body))
		}
		var page auth0GroupsPage
		if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
			err = json.Unmarshal(body, &page.Groups)
		} else {
			err = json.Unmarshal(body, &page)
		}
		if err != nil {
			return nil, fmt.Errorf("failed decoding auth0 management api response, reason=%v", err)
		}
		for _, g := range page.Groups {
			if g.ID == "" {
				continue
			}
			groups = append(groups, idptypes.DirectoryGroup{ID: g.ID, Name: g.Name})
		}
		if page.Next == "" {
			return groups, nil
		}
		next = page.Next
	}
}

type entraGroupsPage struct {
	Value []struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	} `json:"value"`
	NextLink string `json:"@odata.nextLink"`
}

// listEntraGroups walks the Microsoft Graph groups collection following
// @odata.nextLink verbatim.
func listEntraGroups(ctx context.Context, accessToken, groupsURL string) ([]idptypes.DirectoryGroup, error) {
	var groups []idptypes.DirectoryGroup
	for count := 0; ; count++ {
		if count >= directoryMaxPages {
			return nil, fmt.Errorf("reached max pagination (%v) listing directory groups", directoryMaxPages)
		}
		body, status, err := directoryGet(ctx, accessToken, groupsURL)
		if err != nil {
			return nil, fmt.Errorf("page=%v, %v", count, err)
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("unable to list groups from microsoft graph, status=%v, body=%v", status, string(body))
		}
		var page entraGroupsPage
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("failed decoding microsoft graph response, reason=%v", err)
		}
		for _, g := range page.Value {
			if g.ID == "" {
				continue
			}
			groups = append(groups, idptypes.DirectoryGroup{ID: g.ID, Name: g.DisplayName})
		}
		if page.NextLink == "" {
			return groups, nil
		}
		groupsURL = page.NextLink
	}
}
