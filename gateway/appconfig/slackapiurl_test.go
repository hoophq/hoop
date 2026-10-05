package appconfig

import "testing"

// SLACK_API_URL: unset keeps the Slack client default, a valid URL always
// ends in "/" (the client appends method names), and a malformed value stops
// startup instead of sending tokens somewhere unintended.
func TestLoadSlackAPIURL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     string
		wantErr bool
		want    string
	}{
		{"unset keeps the default", "", false, ""},
		{"trailing slash added", "http://127.0.0.1:9999/api", false, "http://127.0.0.1:9999/api/"},
		{"trailing slash kept once", "https://slack-proxy.internal/api/", false, "https://slack-proxy.internal/api/"},
		{"relative URL refused", "/api", true, ""},
		{"other scheme refused", "ftp://slack.example/api", true, ""},
		{"credentials refused", "https://user:pass@slack-proxy.internal/api", true, ""},
		{"query refused", "https://slack-proxy.internal/api?x=1", true, ""},
		{"empty query refused", "https://slack-proxy.internal/api?", true, ""},
		{"fragment refused", "https://slack-proxy.internal/api#x", true, ""},
		{"repeated trailing slashes collapsed", "https://slack-proxy.internal/api//", false, "https://slack-proxy.internal/api/"},
		{"bare host gets a root path", "http://127.0.0.1:9999", false, "http://127.0.0.1:9999/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("POSTGRES_DB_URI", "postgres://u:p@localhost:5432/db")
			t.Setenv("SLACK_API_URL", tc.env)
			runtimeConfig = Config{}

			err := Load(AppModeGateway)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected Load to fail, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := Get().SlackAPIURL(); got != tc.want {
				t.Fatalf("SlackAPIURL() = %q, want %q", got, tc.want)
			}
		})
	}
}
