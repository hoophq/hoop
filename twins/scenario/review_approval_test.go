//go:build scenario

package scenario

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// One flow, no real account: OIDC login, exec on a connection with review,
// a reviewer approves in Slack, the exec output comes back.
func TestReviewApprovedInSlack(t *testing.T) {
	h := newHarness(t)
	h.buildBinaries()
	h.mark("binaries built")
	h.up()
	h.mark("stack up (twins + hoop standalone healthy)")

	h.must("POST", oidcAdmin+"/_twin/seed", "", map[string]any{
		"users": []map[string]any{
			{"sub": "alice-sub", "email": "alice@example.com", "name": "Alice", "groups": []string{"admin"}},
			{"sub": "bob-sub", "email": "bob@example.com", "name": "Bob", "groups": []string{"sre"}},
		},
	}, nil)
	h.must("POST", slackAdm+"/_twin/seed", "", map[string]any{
		"channels": []map[string]string{{"id": "C0REVIEWS", "name": "hoop-reviews"}},
		"users": []map[string]string{
			{"id": "U0ALICE", "name": "alice", "email": "alice@example.com"},
			{"id": "U0BOB", "name": "bob", "email": "bob@example.com"},
		},
	}, nil)
	h.mark("twins seeded")

	alice := h.login("alice@example.com")
	var me struct {
		IsAdmin bool     `json:"is_admin"`
		Groups  []string `json:"groups"`
	}
	h.must("GET", apiURL+"/api/userinfo", alice, nil, &me)
	if !me.IsAdmin {
		t.Fatalf("alice is the first user and in group admin, want is_admin, got %+v", me)
	}
	h.mark("alice logged in through the OIDC twin (admin)")

	bob := h.login("bob@example.com")
	h.must("GET", apiURL+"/api/userinfo", bob, nil, &me)
	if me.IsAdmin || !slices.Contains(me.Groups, "sre") {
		t.Fatalf("bob: want group sre and not admin, got %+v", me)
	}
	h.mark("bob logged in through the OIDC twin (sre)")

	h.must("PATCH", apiURL+"/api/users/self/slack", alice, map[string]string{"slack_id": "U0ALICE"}, nil)
	h.must("PATCH", apiURL+"/api/users/self/slack", bob, map[string]string{"slack_id": "U0BOB"}, nil)

	// /api/healthz answers before `hoop start standalone` provisions its agent.
	agentID := ""
	h.eventually("the standalone agent to connect", 60*time.Second, func() bool {
		var agents []struct{ ID, Name, Status string }
		h.must("GET", apiURL+"/api/agents", alice, nil, &agents)
		for _, a := range agents {
			if a.Name == "standalone" && a.Status == "CONNECTED" {
				agentID = a.ID
			}
		}
		return agentID != ""
	})
	var conn struct{ ID string }
	h.must("POST", apiURL+"/api/connections", alice, map[string]any{
		"name": "shell", "type": "custom", "subtype": "custom", "command": []string{"/bin/sh"},
		"agent_id": agentID, "reviewers": []string{"sre"},
		"access_mode_exec": "enabled", "access_mode_connect": "enabled",
		"access_mode_runbooks": "enabled", "access_schema": "disabled",
	}, &conn)
	h.mark("connection shell created (review by sre)")

	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	// The gateway bootstraps the slack plugin with no connections.
	h.must("PUT", apiURL+"/api/plugins/slack", alice, map[string]any{
		"name": "slack", "connections": []map[string]any{{"id": conn.ID, "config": []string{}}},
	}, nil)
	h.must("PUT", apiURL+"/api/plugins/slack/config", alice, map[string]string{
		"SLACK_BOT_TOKEN": b64("xoxb-twin"), "SLACK_APP_TOKEN": b64("xapp-twin"), "SLACK_CHANNEL": b64("C0REVIEWS"),
	}, nil)
	h.eventually("the gateway to open the Socket Mode websocket", 30*time.Second, func() bool {
		var st struct {
			State struct {
				Socket map[string]int `json:"socket"`
			} `json:"state"`
		}
		h.must("GET", slackAdm+"/_twin/state", "", nil, &st)
		return st.State.Socket["open_now"] > 0
	})
	h.mark("slack configured, Socket Mode connected to the twin")

	h.applyLicense(alice)
	h.mark("enterprise license applied")

	// alice runs a command; the gateway holds it for review.
	home := t.TempDir()
	cliEnv := append(os.Environ(), "HOME="+home, "HOOP_TOKEN="+alice)
	cfg := exec.Command(h.cli, "config", "create", "--api-url", apiURL, "--grpc-url", h.grpcURL)
	cfg.Env = cliEnv
	if b, err := cfg.CombinedOutput(); err != nil {
		t.Fatalf("hoop config create: %v\n%s", err, b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var out bytes.Buffer
	run := exec.CommandContext(ctx, h.cli, "exec", "shell", "--auto-approve", "-i", "echo hello-from-twins")
	run.Env, run.Stdout, run.Stderr = cliEnv, &out, &out
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- run.Wait() }()

	h.eventually("the review message in #hoop-reviews", 30*time.Second, func() bool {
		return strings.Contains(h.slackMessages(), "review-approved")
	})
	h.mark("review message posted to the Slack twin")

	var click struct {
		Acked bool `json:"acked"`
	}
	h.must("POST", slackAdm+"/_twin/act/click", "", map[string]string{
		"user": "U0BOB", "action_id": "review-approved", "group": "sre",
	}, &click)
	if !click.Acked {
		t.Fatal("the gateway did not ack the block_actions envelope")
	}
	h.mark("bob clicked Approve, gateway acked the envelope")

	select {
	case err := <-exited:
		if err != nil {
			t.Fatalf("hoop exec: %v\n%s", err, out.String())
		}
	case <-ctx.Done():
		t.Fatalf("hoop exec did not return after the approval\n%s", out.String())
	}
	if !strings.Contains(out.String(), "hello-from-twins") {
		t.Fatalf("exec output does not hold the command output:\n%s", out.String())
	}
	h.mark("hoop exec returned the output")
	_ = os.WriteFile(filepath.Join(h.artifacts, "exec-output.txt"), out.Bytes(), 0o644)

	// What the twins saw. hoop closes the review message and tells the owner.
	h.eventually("the review message rewritten and the owner's DM", 15*time.Second, func() bool {
		var st struct {
			State struct {
				Messages []struct {
					Updates []string `json:"updates"`
				} `json:"messages"`
				DMs []struct {
					Channel string `json:"channel"`
				} `json:"dms"`
			} `json:"state"`
		}
		h.must("GET", slackAdm+"/_twin/state", "", nil, &st)
		updated := len(st.State.Messages) > 0 && len(st.State.Messages[0].Updates) > 0
		dm := slices.ContainsFunc(st.State.DMs, func(m struct {
			Channel string `json:"channel"`
		}) bool {
			return m.Channel == "U0ALICE"
		})
		return updated && dm
	})
	h.mark("review message closed, alice got the DM")

	h.saveState("oidc", oidcAdmin)
	h.saveState("slack", slackAdm)
	if h.reach != "alias" {
		h.checkEgress()
	}
	_ = os.WriteFile(filepath.Join(h.artifacts, "timings.txt"), []byte(h.timings()), 0o644)
	t.Logf("exec output:\n%s\ntimings:\n%s", out.String(), h.timings())
}

// login runs the OIDC code flow over HTTP, the way `hoop login` does in a
// browser, and returns the access token the gateway hands the CLI.
func (h *harness) login(email string) string {
	h.t.Helper()
	var lr struct {
		LoginURL string `json:"login_url"`
	}
	h.must("GET", apiURL+"/api/login", "", nil, &lr)
	authz, err := url.Parse(lr.LoginURL)
	if err != nil {
		h.t.Fatal(err)
	}
	q := authz.Query()
	q.Set("login_hint", email)
	// The issuer host (oidc-twin) resolves inside the compose network only;
	// the twin serves the same handler on its admin port.
	reach, _ := url.Parse(oidcAdmin)
	authz.Scheme, authz.Host, authz.RawQuery = reach.Scheme, reach.Host, q.Encode()

	callback := h.redirect(authz.String(), http.StatusFound)
	final := h.redirect(callback, http.StatusTemporaryRedirect)
	u, _ := url.Parse(final)
	tok := u.Query().Get("token")
	if tok == "" {
		h.t.Fatalf("login %s: the gateway redirected to %s", email, final)
	}
	return tok
}

func (h *harness) redirect(target string, want int) string {
	h.t.Helper()
	resp, err := noRedirect.Get(target)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != want {
		h.t.Fatalf("GET %s: want %d, got %d", target, want, resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

// applyLicense loads an enterprise license. Exec on a connection with review
// is refused on OSS, and the license key is fixed in the binary, so the
// scenario cannot mint one.
func (h *harness) applyLicense(token string) {
	h.t.Helper()
	path := os.Getenv("TWINS_LICENSE_FILE")
	if path == "" {
		h.t.Fatal("TWINS_LICENSE_FILE is not set: exec on a connection with review needs an enterprise license")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatal(err)
	}
	var lic json.RawMessage = bytes.TrimSpace(raw)
	h.must("PUT", apiURL+"/api/orgs/license", token, lic, nil)
}

func (h *harness) slackMessages() string {
	var st struct {
		State struct {
			Messages json.RawMessage `json:"messages"`
		} `json:"state"`
	}
	h.must("GET", slackAdm+"/_twin/state", "", nil, &st)
	return string(st.State.Messages)
}

// knownEgress is traffic the gateway sends to a real third party with no
// twin behind it. The router refuses it; a scenario only fails on a host
// that is not listed here.
var knownEgress = map[string]string{
	// The OpenTelemetry exporter starts with the tracing interceptor, with
	// or without an API key (common/monitoring/monitoring.go:75).
	"api.honeycomb.io:443": "tracing exporter",
}

// checkEgress fails on any host the gateway tried that has no twin.
func (h *harness) checkEgress() {
	h.t.Helper()
	h.saveState("router", routerAdm)
	var st struct {
		State struct {
			Tunnels []struct {
				Host    string `json:"host"`
				Allowed bool   `json:"allowed"`
			} `json:"tunnels"`
		} `json:"state"`
	}
	h.must("GET", routerAdm+"/_twin/state", "", nil, &st)
	refused := map[string]int{}
	for _, tn := range st.State.Tunnels {
		if !tn.Allowed {
			refused[tn.Host]++
		}
	}
	for host, n := range refused {
		if why, ok := knownEgress[host]; ok {
			h.t.Logf("egress refused %dx to %s (%s)", n, host, why)
			continue
		}
		h.t.Errorf("the gateway tried %s %d times and no twin answers it, see router-state.json", host, n)
	}
}
