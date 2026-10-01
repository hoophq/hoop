package apisidecar

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/openapi"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertCleanValue holds for every value the plane stores or shows.
func assertCleanValue(t *testing.T, v string) {
	t.Helper()
	assert.LessOrEqual(t, len(v), maxRequesterBytes)
	assert.True(t, utf8.ValidString(v), "invalid UTF-8: %q", v)
	for _, r := range v {
		assert.False(t, unicode.In(r, unicode.Cc, unicode.Cf), "control or format rune %U in %q", r, v)
	}
}

func TestNewRequester(t *testing.T) {
	assert.Nil(t, newRequester(nil))
	assert.Nil(t, newRequester(&openapi.SidecarReviewRequester{}))
	assert.Nil(t, newRequester(&openapi.SidecarReviewRequester{Subject: "  ", Method: "google_identity"}),
		"a blank value names nobody")

	got := newRequester(&openapi.SidecarReviewRequester{
		Subject:  " alice ",
		Email:    "alice@example.com",
		PeerAddr: "10.0.0.1:5432",
		Method:   "database_user",
	})
	assert.Equal(t, &requester{subject: "alice", email: "alice@example.com", peerAddr: "10.0.0.1:5432", method: "database_user"}, got)

	for name, tc := range map[string]struct{ in, want string }{
		"nul":           {"a\x00b", "a�b"},
		"control":       {"a\x1bb\nc", "a�b�c"},
		"bidi override": {"a‮b", "a�b"},
		"zero width":    {"a​b", "a�b"},
		"invalid utf-8": {"a\xffb", "a�b"},
	} {
		t.Run(name, func(t *testing.T) {
			got := newRequester(&openapi.SidecarReviewRequester{Subject: tc.in})
			require.NotNil(t, got)
			assert.Equal(t, tc.want, got.subject)
			assertCleanValue(t, got.subject)
		})
	}

	t.Run("a long multibyte value is cut on a rune boundary", func(t *testing.T) {
		long := strings.Repeat("é", 150) // 300 bytes
		got := newRequester(&openapi.SidecarReviewRequester{Subject: long, Email: long, PeerAddr: long})
		require.NotNil(t, got)
		for _, v := range []string{got.subject, got.email, got.peerAddr} {
			assertCleanValue(t, v)
			assert.Equal(t, strings.Repeat("é", 127), v)
		}
	})
}

func TestNormalizeMethod(t *testing.T) {
	for _, tc := range []struct {
		method string
		named  bool
		want   string
	}{
		{"google_identity", true, "google_identity"},
		{"GOOGLE_IDENTITY", true, "google_identity"},
		{"database_user", true, "database_user"},
		{"future_method_2", true, "future_method_2"},
		{"bad value!", true, "unrecognized"},
		{strings.Repeat("a", 65), true, "unrecognized"},
		{strings.Repeat("a", 64), true, strings.Repeat("a", 64)},
		{"", true, "unspecified"},
		{"peer_address", true, "unspecified"},
		{"PEER_ADDRESS", true, "unspecified"},
		{"google_identity", false, "peer_address"},
		{"", false, "peer_address"},
	} {
		assert.Equal(t, tc.want, normalizeMethod(tc.method, tc.named), "method=%q named=%v", tc.method, tc.named)
	}
}

func TestRequesterLabels(t *testing.T) {
	var none *requester
	assert.Nil(t, none.labels())

	r := &requester{subject: "alice", peerAddr: "10.0.0.1:5432", method: "database_user"}
	assert.Equal(t, map[string]string{
		"sidecar.requester.subject":   "alice",
		"sidecar.requester.peer_addr": "10.0.0.1:5432",
		"sidecar.requester.method":    "database_user",
	}, r.labels(), "an empty value is left out")
}

func TestRequesterPrincipal(t *testing.T) {
	var none *requester
	assert.Equal(t, "", none.principal())
	assert.Equal(t, "alice", (&requester{subject: "alice", email: "a@x", peerAddr: "p"}).principal())
	assert.Equal(t, "a@x", (&requester{email: "a@x", peerAddr: "p"}).principal())
	assert.Equal(t, "p", (&requester{peerAddr: "p"}).principal())
}

func TestRequesterSlackFiler(t *testing.T) {
	var none *requester
	assert.Nil(t, none.slackFiler())

	r := &requester{subject: "alice", email: "alice@example.com", peerAddr: "10.0.0.1:5432", method: "google_identity"}
	assert.Equal(t, &slackservice.ReviewFiler{
		Source:   requesterMethodLabel("google_identity"),
		Subject:  "alice",
		Email:    "alice@example.com",
		PeerAddr: "10.0.0.1:5432",
	}, r.slackFiler())

	same := &requester{subject: "alice@example.com", email: "alice@example.com", method: "google_identity"}
	assert.Empty(t, same.slackFiler().Email, "an email equal to the subject is not repeated")
}

func TestRequesterMethodLabel(t *testing.T) {
	for method, want := range map[string]string{
		"google_identity":        "Google token holder, checked with Google by the sidecar (audience not checked)",
		"ssh_certificate":        "SSH certificate signed by the listener's trusted CA, checked by the sidecar",
		"database_user":          "Login name the client claimed before authenticating, not checked",
		"identity_header":        "Header value; any client that can reach the listener can set it unless a proxy strips it",
		"tls_client_certificate": "TLS certificate name, not verified",
		"unspecified":            "Identity source not reported",
		"peer_address":           "Network address only, no identity",
		"unrecognized":           "Source reported as unrecognized",
		"future_method":          "Source reported as future_method",
	} {
		assert.Equal(t, want, requesterMethodLabel(method), method)
	}
}

func bindSidecarReviewRequest(t *testing.T, body string) (openapi.SidecarReviewRequest, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/sidecars/reviews", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	var req openapi.SidecarReviewRequest
	err := c.ShouldBindJSON(&req)
	return req, err
}

// A 400 makes the sidecar deny, so no requester shape may fail the bind.
func TestSidecarReviewRequestNeverFailsOnTheRequester(t *testing.T) {
	const base = `"listener_name":"appdb","payload":"c2VsZWN0IDE7","approval_rule":"payments-approvers"`

	t.Run("an older sidecar sends no requester", func(t *testing.T) {
		req, err := bindSidecarReviewRequest(t, `{`+base+`}`)
		require.NoError(t, err)
		assert.Nil(t, req.Requester)
		assert.Nil(t, newRequester(req.Requester))
	})

	for name, tc := range map[string]struct {
		requester string
		want      *requester
	}{
		"a string":               {`"alice"`, nil},
		"an array":               {`[1]`, nil},
		"null":                   {`null`, nil},
		"a number":               {`7`, nil},
		"a numeric subject":      {`{"subject":1}`, nil},
		"a numeric method":       {`{"subject":"alice","method":7}`, &requester{subject: "alice", method: "unspecified"}},
		"an object subject":      {`{"subject":{"a":1},"email":"a@x"}`, &requester{email: "a@x", method: "unspecified"}},
		"an unknown nested key":  {`{"subject":"alice","method":"database_user","groups":["admin"]}`, &requester{subject: "alice", method: "database_user"}},
		"only a peer":            {`{"peer_addr":"10.0.0.1:5432","method":"google_identity"}`, &requester{peerAddr: "10.0.0.1:5432", method: "peer_address"}},
		"a 1000 char subject":    {`{"subject":"` + strings.Repeat("a", 1000) + `"}`, &requester{subject: strings.Repeat("a", maxRequesterBytes), method: "unspecified"}},
		"a null subject":         {`{"subject":null,"email":"a@x"}`, &requester{email: "a@x", method: "unspecified"}},
		"a boolean peer address": {`{"peer_addr":true,"subject":"bob","method":"identity_header"}`, &requester{subject: "bob", method: "identity_header"}},
	} {
		t.Run(name, func(t *testing.T) {
			req, err := bindSidecarReviewRequest(t, `{`+base+`,"requester":`+tc.requester+`}`)
			require.NoError(t, err)
			assert.Equal(t, "appdb", req.ListenerName, "the required fields still bind")
			assert.Equal(t, tc.want, newRequester(req.Requester))
		})
	}

	t.Run("an unknown top-level key", func(t *testing.T) {
		req, err := bindSidecarReviewRequest(t, `{`+base+`,"requester":{"subject":"alice"},"requester_v2":{"x":1}}`)
		require.NoError(t, err)
		assert.Equal(t, &requester{subject: "alice", method: "unspecified"}, newRequester(req.Requester))
	})
}
