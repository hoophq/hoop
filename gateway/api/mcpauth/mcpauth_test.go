package mcpauth

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestIsFederatedJWT(t *testing.T) {
	mk := func(payload string) string {
		return "aGVhZGVy." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".c2ln"
	}
	const idp = "https://hoophq.us.auth0.com/"
	cases := []struct {
		name     string
		token    string
		expected string
		want     bool
	}{
		{"matching issuer", mk(`{"iss":"https://hoophq.us.auth0.com/","sub":"u"}`), idp, true},
		{"matching issuer no trailing slash", mk(`{"iss":"https://hoophq.us.auth0.com","sub":"u"}`), idp, true},
		{"different issuer (hoop local)", mk(`{"sub":"a@a.com","email":"a@a.com"}`), idp, false},
		{"different issuer explicit", mk(`{"iss":"https://other.example","sub":"u"}`), idp, false},
		{"empty expected issuer", mk(`{"iss":"anything"}`), "", false},
		{"not a jwt shape", "not-a-jwt", idp, false},
		{"single dot", "a.b", idp, false},
		{"bad base64 payload", "aGVhZGVy.!!!!.c2ln", idp, false},
		{"non-json payload", mk("not-json"), idp, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isFederatedJWT(tc.token, tc.expected))
		})
	}
}

func TestSanitizeChallenge(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain text", "plain text"},
		{"with \"quotes\"", "with 'quotes'"},
		{"with\r\ninjection: GET /admin", "with  injection: GET /admin"},
		{"\rhide\nme\"", " hide me'"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, sanitizeChallenge(tc.in))
	}
}

func TestExtractBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"valid bearer", "Bearer abc.def.ghi", "abc.def.ghi"},
		{"lowercase scheme", "bearer abc.def.ghi", "abc.def.ghi"},
		{"missing scheme", "abc.def.ghi", ""},
		{"empty header", "", ""},
		{"only scheme", "Bearer", ""},
		{"trailing whitespace", "Bearer   abc.def.ghi  ", "abc.def.ghi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/", nil)
			if tc.header != "" {
				c.Request.Header.Set("authorization", tc.header)
			}
			assert.Equal(t, tc.want, extractBearer(c))
		})
	}
}

func TestExtractGroups(t *testing.T) {
	cases := []struct {
		name      string
		claims    map[string]any
		claimName string
		want      []string
	}{
		{"single string", map[string]any{"groups": "admin"}, "groups", []string{"admin"}},
		{"empty string", map[string]any{"groups": ""}, "groups", nil},
		{"array of any", map[string]any{"groups": []any{"admin", "dba", ""}}, "groups", []string{"admin", "dba"}},
		{"array of string", map[string]any{"groups": []string{"admin", "dba"}}, "groups", []string{"admin", "dba"}},
		{"missing claim", map[string]any{}, "groups", nil},
		{"custom claim name", map[string]any{"my_groups": []any{"x"}}, "my_groups", []string{"x"}},
		{"wrong type ignored", map[string]any{"groups": 42}, "groups", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractGroups(tc.claims, tc.claimName)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDedupe(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"single", []string{"a"}, []string{"a"}},
		{"duplicates removed in order", []string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
		{"empty strings dropped", []string{"a", "", "b", ""}, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, dedupe(tc.in))
		})
	}
}

func TestWriteChallengeContainsRequiredFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/mcp", nil)

	writeChallenge(c, `bad "token"`+"\r\nGET /admin", "invalid_token")

	assert.Equal(t, 401, w.Code)
	hdr := w.Header().Get("WWW-Authenticate")
	assert.True(t, strings.HasPrefix(hdr, "Bearer "), "challenge must start with Bearer scheme, got: %s", hdr)
	assert.Contains(t, hdr, `resource_metadata="`)
	assert.Contains(t, hdr, `error="invalid_token"`)
	assert.NotContains(t, hdr, "\r")
	assert.NotContains(t, hdr, "\n")
	assert.NotContains(t, hdr, `"token"`, "unescaped attacker-controlled quotes must not leak into the header value")
}
