package oidcprovider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
)

func TestDirectoryVendor(t *testing.T) {
	for issuer, want := range map[string]directoryVendor{
		"https://hoophq.us.auth0.com/":                     vendorAuth0,
		"https://login.microsoftonline.com/tenant-id/v2.0": vendorEntra,
		"https://sts.windows.net/tenant-id/":               vendorEntra,
		"https://accounts.google.com":                      vendorGoogle,
		"https://dev-1.okta.com/oauth2/default":            vendorOkta,
		"https://x.oktapreview.com":                        vendorOkta,
		"https://idp.example.com/realms/a":                 vendorUnknown,
		"://bad":                                           vendorUnknown,
	} {
		p := &Provider{Options: Options{IssuerURL: issuer}}
		assert.Equal(t, want, p.directoryVendor(), "issuer=%s", issuer)
	}
}

func TestListDirectoryGroupsUnsupported(t *testing.T) {
	for _, issuer := range []string{
		"https://accounts.google.com",
		"https://dev-1.okta.com/oauth2/default",
		"https://idp.example.com/realms/a",
	} {
		p := &Provider{Options: Options{IssuerURL: issuer}}
		groups, err := p.ListDirectoryGroups(context.Background())
		assert.Nil(t, groups, "issuer=%s", issuer)
		assert.True(t, errors.Is(err, idptypes.ErrDirectoryGroupsUnsupported), "issuer=%s err=%v", issuer, err)
	}
}

func TestListAuth0Groups(t *testing.T) {
	t.Run("cursor pagination", func(t *testing.T) {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
			assert.Equal(t, "100", r.URL.Query().Get("take"))
			switch atomic.AddInt32(&calls, 1) {
			case 1:
				assert.Empty(t, r.URL.Query().Get("from"))
				_, _ = w.Write([]byte(`{"groups":[{"id":"grp_1","name":"devops"}],"next":"c1"}`))
			case 2:
				assert.Equal(t, "c1", r.URL.Query().Get("from"))
				_, _ = w.Write([]byte(`{"groups":[{"id":"grp_2","name":"sre"},{"id":"","name":"skip"}]}`))
			default:
				t.Errorf("unexpected request %d", calls)
			}
		}))
		defer srv.Close()

		groups, err := listAuth0Groups(context.Background(), "tok", srv.URL+"/api/v2/groups")
		assert.NoError(t, err)
		assert.Equal(t, []idptypes.DirectoryGroup{{ID: "grp_1", Name: "devops"}, {ID: "grp_2", Name: "sre"}}, groups)
		assert.EqualValues(t, 2, calls)
	})

	t.Run("bare array", func(t *testing.T) {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			_, _ = w.Write([]byte(`[{"id":"grp_9","name":"solo"}]`))
		}))
		defer srv.Close()

		groups, err := listAuth0Groups(context.Background(), "tok", srv.URL)
		assert.NoError(t, err)
		assert.Equal(t, []idptypes.DirectoryGroup{{ID: "grp_9", Name: "solo"}}, groups)
		assert.EqualValues(t, 1, calls)
	})

	t.Run("forbidden", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"insufficient_scope"}`))
		}))
		defer srv.Close()

		groups, err := listAuth0Groups(context.Background(), "tok", srv.URL)
		assert.Nil(t, groups)
		assert.ErrorContains(t, err, "status=403")
		assert.ErrorContains(t, err, "insufficient_scope")
	})
}

func TestListEntraGroups(t *testing.T) {
	t.Run("nextLink pagination", func(t *testing.T) {
		var srv *httptest.Server
		var paths []string
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
			paths = append(paths, r.URL.Path)
			switch r.URL.Path {
			case "/page2":
				_, _ = w.Write([]byte(`{"value":[{"id":"b","displayName":"B"}]}`))
			default:
				_, _ = w.Write([]byte(`{"value":[{"id":"a","displayName":"A"}],"@odata.nextLink":"` + srv.URL + `/page2"}`))
			}
		}))
		defer srv.Close()

		groups, err := listEntraGroups(context.Background(), "tok", srv.URL+"/v1.0/groups")
		assert.NoError(t, err)
		assert.Equal(t, []idptypes.DirectoryGroup{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}, groups)
		assert.Equal(t, []string{"/v1.0/groups", "/page2"}, paths)
	})

	t.Run("unauthorized", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"InvalidAuthenticationToken"}}`))
		}))
		defer srv.Close()

		groups, err := listEntraGroups(context.Background(), "tok", srv.URL)
		assert.Nil(t, groups)
		assert.ErrorContains(t, err, "status=401")
	})
}

func TestListDirectoryGroupsPageCap(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"value":[{"id":"x","displayName":"X"}],"@odata.nextLink":"` + srv.URL + `"}`))
	}))
	defer srv.Close()

	groups, err := listEntraGroups(context.Background(), "tok", srv.URL)
	assert.Nil(t, groups)
	assert.ErrorContains(t, err, "reached max pagination")
}

func TestClientCredentialsTokenAudience(t *testing.T) {
	const audience = "https://t.auth0.com/api/v2/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/token", r.URL.Path)
		assert.NoError(t, r.ParseForm())
		assert.Equal(t, "client_credentials", r.PostForm.Get("grant_type"))
		assert.Equal(t, audience, r.PostForm.Get("audience"))
		id, secret, ok := r.BasicAuth()
		if !ok {
			id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		assert.Equal(t, "id", id)
		assert.Equal(t, "sec", secret)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	p := &Provider{
		Options:      Options{ClientID: "id", ClientSecret: "sec"},
		oauth2Config: oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: srv.URL + "/token"}},
	}
	tok, err := p.clientCredentialsToken(context.Background(), nil, audience)
	assert.NoError(t, err)
	assert.Equal(t, "tok", tok.AccessToken)
}
