package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	clientconfig "github.com/hoophq/hoop/client/config"
)

func TestParseTagsFlag(t *testing.T) {
	tags, err := parseTagsFlag([]string{"environment=prod", "team=data", "empty="})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{"environment": "prod", "team": "data", "empty": ""}
	if len(tags) != len(want) {
		t.Fatalf("got %v, want %v", tags, want)
	}
	for k, v := range want {
		if tags[k] != v {
			t.Errorf("tag %q = %q, want %q", k, tags[k], v)
		}
	}
	// A typo must not create an agent without the tag.
	for _, bad := range []string{"team", "=data"} {
		if _, err := parseTagsFlag([]string{bad}); err == nil {
			t.Errorf("parseTagsFlag(%q) = nil error, want error", bad)
		}
	}
}

func TestJoinMapIsSorted(t *testing.T) {
	got := joinMap(map[string]any{"team": "data", "env": "prod", "b": "1"})
	if got != "b=1,env=prod,team=data" {
		t.Errorf("joinMap = %q", got)
	}
	if got := joinMap(nil); got != "-" {
		t.Errorf("joinMap(nil) = %q, want -", got)
	}
}

// The selector reaches the gateway whole: --query would split it on commas.
func TestSetTagSelectorKeepsCommas(t *testing.T) {
	apir := &apiResource{resourceType: "agents", conf: &clientconfig.Config{ApiURL: "http://gw"},
		suffixEndpoint: "/api/agents", queryAttributes: url.Values{}}
	if err := setTagSelector(apir, "env=prod,team!=data"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	endpoint, err := apir.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(endpoint)
	if got := u.Query().Get("tag_selector"); got != "env=prod,team!=data" {
		t.Errorf("tag_selector = %q", got)
	}
	if len(u.Query()) != 1 {
		t.Errorf("want one query attribute, got %v", u.Query())
	}

	for _, tc := range []struct{ resource, name string }{{"users", ""}, {"agents", "prod-eu"}} {
		apir := &apiResource{resourceType: tc.resource, name: tc.name, queryAttributes: url.Values{}}
		if err := setTagSelector(apir, "env=prod"); err == nil {
			t.Errorf("setTagSelector on %s/%s = nil error, want error", tc.resource, tc.name)
		}
	}
}

func TestCheckAgentListHasTags(t *testing.T) {
	withTags := []map[string]any{{"name": "a", "tags": map[string]any{}}}
	withoutTags := []map[string]any{{"name": "a"}}
	if err := checkAgentListHasTags(withTags, "x"); err != nil {
		t.Errorf("list with tags: %v", err)
	}
	if err := checkAgentListHasTags(withoutTags, "--show-tags has no tags to show"); err == nil ||
		!strings.Contains(err.Error(), "--show-tags has no tags to show") {
		t.Errorf("list from a gateway without agent tags: err = %v", err)
	}
	raw, _ := json.Marshal(withoutTags)
	if err := checkAgentListHasTags(raw, "x"); err == nil {
		t.Error("raw list from a gateway without agent tags: want error")
	}
	if err := checkAgentListHasTags([]map[string]any{}, "x"); err != nil {
		t.Errorf("empty list: %v", err)
	}
}

// Before a create with tags, the CLI checks the gateway knows agent tags: an
// older gateway drops the field and still creates the agent.
func TestCheckAgentTagsSupported(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		wantErr string
	}{
		{"new gateway", http.StatusOK, ""},
		{"old gateway", http.StatusNotFound, "does not support agent tags"},
		{"other failure", http.StatusInternalServerError, "failed checking the gateway for agent tags"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/agent-tags" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"items":[]}`))
			}))
			defer srv.Close()
			err := checkAgentTagsSupported(&clientconfig.Config{ApiURL: srv.URL, Token: "token"})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// A 404 on the tags update is an old gateway or an agent deleted since the
// existence check; GET /agent-tags tells them apart.
func TestExplainTagsUpdateError(t *testing.T) {
	put404 := errors.New(`failed performing request, status=404, body={"message":"agent not found"}`)
	for _, tc := range []struct {
		name        string
		probeStatus int
		err         error
		want        string
	}{
		{"agent deleted", http.StatusOK, put404, "agent prod-eu not found"},
		{"old gateway", http.StatusNotFound, put404, "does not support agent tags"},
		{"probe fails", http.StatusInternalServerError, put404, "failed updating the tags of agent prod-eu"},
		{"not a 404", http.StatusOK, errors.New("failed performing request, status=400, body=bad"), "status=400"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.probeStatus)
				_, _ = w.Write([]byte(`{"items":[]}`))
			}))
			defer srv.Close()
			got := explainTagsUpdateError(&clientconfig.Config{ApiURL: srv.URL, Token: "token"}, "prod-eu", tc.err)
			if !strings.Contains(got.Error(), tc.want) {
				t.Errorf("got %q, want it to contain %q", got, tc.want)
			}
			// The agent exists: an update error never talks about creating one.
			if strings.Contains(got.Error(), "created") {
				t.Errorf("update error mentions agent creation: %q", got)
			}
		})
	}
}

func TestGetAgentReportsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agents/exists" {
			_, _ = w.Write([]byte(`{"name":"exists","tags":{}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"agent not found"}`))
	}))
	defer srv.Close()
	conf := &clientconfig.Config{ApiURL: srv.URL, Token: "token"}
	if ok, err := getAgent(conf, "exists"); !ok || err != nil {
		t.Errorf("getAgent(exists) = %v, %v", ok, err)
	}
	if ok, err := getAgent(conf, "missing"); ok || err != nil {
		t.Errorf("getAgent(missing) = %v, %v", ok, err)
	}
}
