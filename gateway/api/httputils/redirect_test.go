package httputils

import (
	"net/url"
	"testing"
)

func TestWithQueryParam(t *testing.T) {
	for _, tc := range []struct{ in, key, value, want string }{
		{"https://app.example.com/auth/callback", "token", "a.b.c", "https://app.example.com/auth/callback?token=a.b.c"},
		{"https://app.example.com/auth/callback?next=%2Fsidecars%2Flogin%2FABCD", "error", "unexpected_error",
			"https://app.example.com/auth/callback?error=unexpected_error&next=%2Fsidecars%2Flogin%2FABCD"},
		{"http://127.0.0.1:3587/callback", "error", "user is inactive", "http://127.0.0.1:3587/callback?error=user+is+inactive"},
	} {
		got := WithQueryParam(tc.in, tc.key, tc.value)
		if got != tc.want {
			t.Errorf("WithQueryParam(%q, %q, %q) = %q, want %q", tc.in, tc.key, tc.value, got, tc.want)
		}
		u, err := url.Parse(got)
		if err != nil || u.Query().Get(tc.key) != tc.value {
			t.Errorf("%q does not carry %s=%q", got, tc.key, tc.value)
		}
	}
}
