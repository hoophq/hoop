package directorysync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

const auth0PageSize = 100

// auth0Provider reads Auth0 roles as groups, through the Management API.
// Auth0 has no directory groups of its own; roles are how a tenant usually
// groups people, and what an Action puts in a groups claim.
type auth0Provider struct {
	baseURL string
	client  *http.Client
}

func newAuth0Provider(s Auth0Settings) *auth0Provider {
	base := strings.TrimSuffix(strings.TrimSpace(s.Domain), "/")
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	cfg := clientcredentials.Config{
		ClientID:       s.ClientID,
		ClientSecret:   s.ClientSecret,
		TokenURL:       base + "/oauth/token",
		EndpointParams: url.Values{"audience": {base + "/api/v2/"}},
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, httpClient)
	return &auth0Provider{baseURL: base, client: cfg.Client(ctx)}
}

func (p *auth0Provider) get(ctx context.Context, path string, query url.Values, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth0 request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth0 answered %d on %s: %s", resp.StatusCode, path, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, v)
}

func (p *auth0Provider) ListGroups(ctx context.Context) ([]Group, error) {
	var out []Group
	for page := 0; ; page++ {
		var resp struct {
			Roles []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"roles"`
			Total int `json:"total"`
		}
		q := url.Values{"page": {fmt.Sprint(page)}, "per_page": {fmt.Sprint(auth0PageSize)}, "include_totals": {"true"}}
		if err := p.get(ctx, "/api/v2/roles", q, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Roles {
			out = append(out, Group{ID: r.ID, Name: r.Name})
		}
		if len(resp.Roles) < auth0PageSize || len(out) >= resp.Total {
			return out, nil
		}
	}
}

// ListMembers returns the users of the role. The role's user listing does not
// say whether a user is blocked, so blocked users are read once and marked
// inactive.
func (p *auth0Provider) ListMembers(ctx context.Context, roleID string) ([]User, error) {
	var out []User
	for page := 0; ; page++ {
		var resp struct {
			Users []struct {
				UserID string `json:"user_id"`
				Email  string `json:"email"`
				Name   string `json:"name"`
			} `json:"users"`
			Total int `json:"total"`
		}
		q := url.Values{"page": {fmt.Sprint(page)}, "per_page": {fmt.Sprint(auth0PageSize)}, "include_totals": {"true"}}
		if err := p.get(ctx, "/api/v2/roles/"+url.PathEscape(roleID)+"/users", q, &resp); err != nil {
			return nil, err
		}
		for _, u := range resp.Users {
			out = append(out, User{ExternalID: u.UserID, Email: u.Email, Name: u.Name, Active: true})
		}
		if len(resp.Users) < auth0PageSize || len(out) >= resp.Total {
			break
		}
	}
	if len(out) == 0 {
		return out, nil
	}

	blocked, err := p.blockedUsers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if blocked[out[i].ExternalID] {
			out[i].Active = false
		}
	}
	return out, nil
}

func (p *auth0Provider) blockedUsers(ctx context.Context) (map[string]bool, error) {
	blocked := map[string]bool{}
	for page := 0; ; page++ {
		var resp struct {
			Users []struct {
				UserID string `json:"user_id"`
			} `json:"users"`
			Total int `json:"total"`
		}
		q := url.Values{
			"q": {"blocked:true"}, "search_engine": {"v3"}, "fields": {"user_id"},
			"page": {fmt.Sprint(page)}, "per_page": {fmt.Sprint(auth0PageSize)}, "include_totals": {"true"},
		}
		if err := p.get(ctx, "/api/v2/users", q, &resp); err != nil {
			return nil, err
		}
		for _, u := range resp.Users {
			blocked[u.UserID] = true
		}
		if len(resp.Users) < auth0PageSize || len(blocked) >= resp.Total {
			return blocked, nil
		}
	}
}
