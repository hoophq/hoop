//go:build integration && parity

package parity

import (
	"context"
	"encoding/base64"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	// slackDefaultChannel is the org channel of the Slack plugin. Every check
	// configures the same one: the plugin does not restart on a channel change.
	slackDefaultChannel = "CPARITYDEFAULT"
	// slackAdminUserID is the Slack user of the admin: users.info answers it
	// with adminEmail, and the gateway checks link it to the admin.
	slackAdminUserID = "UPARITYADMIN"
	// slackReviewGroup approves every review these checks file.
	slackReviewGroup = "admin"
	slackListener    = "appdb"
)

var slackSidecarRuns = map[Run]Expect{ControlPlane: Must, GatewayFlagOn: PendingOn("ENG-526")}

var slackGatewayRuns = map[Run]Expect{GatewayFlagOff: Must, GatewayFlagOn: Must}

var _ = register(Check{
	ID:    "SL-01",
	Title: "a sidecar review is posted to its listener's Slack channel, Approve in Slack approves it and the sidecar retry forwards",
	Runs:  slackSidecarRuns,
	Fn: func(c *C) {
		channel := slackChannel(c, "listener")
		rv := slackSidecarReview(c, []string{channel})
		msg := rv.message(c)
		if msg.Channel != channel {
			c.Fatalf("review posted to %q, want the listener channel %q", msg.Channel, channel)
		}
		if !msg.Shows("DELETE FROM accounts") {
			c.Fatalf("the review message does not show the held statement: %s", truncate(msg.Blocks))
		}
		// The listener has a channel, so the org channel is only a fallback.
		time.Sleep(2 * time.Second)
		if got := c.Slack.ReviewMessages(rv.id); len(got) != 1 {
			c.Fatalf("review posted %d times, want once on %s: %+v", len(got), channel, got)
		}

		if err := c.Slack.Approve(context.Background(), msg, slackAdminUserID, slackReviewGroup); err != nil {
			c.Fatalf("approving in Slack: %v", err)
		}
		st := rv.waitStatus(c, "APPROVED")
		if st.DecidedAt == nil {
			c.Fatalf("approved review has no decided_at")
		}
		slackWaitRewrite(c, msg, "Session ready to be executed")
		slackReviewedBy(c, rv.id, adminEmail)

		var retry slackSidecarReviewResponse
		rv.post(c).Expect(c, http.StatusOK).JSON(c, &retry)
		if !retry.Forward || retry.Review.ID != rv.id {
			c.Fatalf("retry after the Slack approval: forward=%v review=%s, want forward=true on %s",
				retry.Forward, retry.Review.ID, rv.id)
		}
	},
})

var _ = register(Check{
	ID:    "SL-02",
	Title: "Reject in Slack with a reason rejects the sidecar review; the waiting sidecar's claim does not forward",
	Runs:  slackSidecarRuns,
	Fn: func(c *C) {
		rv := slackSidecarReview(c, []string{slackChannel(c, "listener")})
		msg := rv.message(c)
		const reason = "parity: not on a Friday"
		if err := c.Slack.Reject(context.Background(), msg, slackAdminUserID, slackReviewGroup, reason); err != nil {
			c.Fatalf("rejecting in Slack: %v", err)
		}
		st := rv.waitStatus(c, "REJECTED")
		if st.RejectionReason == nil || *st.RejectionReason != reason {
			c.Fatalf("rejection_reason=%v, want %q", st.RejectionReason, reason)
		}
		slackWaitRewrite(c, msg, "Rejection reason")
		slackReviewedBy(c, rv.id, adminEmail)

		// The sidecar waiting on this review claims it by id and learns the
		// refusal: it must not forward.
		var claim slackSidecarReviewResponse
		c.Sidecar(rv.token).Do(c, http.MethodPost, "/sidecars/reviews/"+rv.id+"/claim", nil).
			Expect(c, http.StatusOK).JSON(c, &claim)
		if claim.Forward || claim.Review.ID != rv.id || claim.Review.Status != "REJECTED" {
			c.Fatalf("claim after the Slack rejection: forward=%v review=%s status=%s, want forward=false REJECTED on %s",
				claim.Forward, claim.Review.ID, claim.Review.Status, rv.id)
		}
	},
})

var _ = register(Check{
	ID:    "SL-03",
	Title: "a sidecar review of a listener with no Slack channel is posted to the org's Slack channel",
	Runs:  slackSidecarRuns,
	Fn: func(c *C) {
		rv := slackSidecarReview(c, []string{})
		msg := rv.message(c)
		if msg.Channel != slackDefaultChannel {
			c.Fatalf("review posted to %q, want the org channel %q", msg.Channel, slackDefaultChannel)
		}
		if _, err := msg.Button("review-approved", slackReviewGroup); err != nil {
			c.Fatalf("the fallback message cannot be approved: %v", err)
		}
	},
})

var _ = register(Check{
	ID:    "SL-04",
	Title: "a Slack user whose email matches no hoop user cannot approve; the sidecar review stays pending",
	Runs:  slackSidecarRuns,
	Fn: func(c *C) {
		rv := slackSidecarReview(c, []string{slackChannel(c, "listener")})
		msg := rv.message(c)
		stranger := FakeSlackUser{ID: "USTRANGERCP", Name: "stranger", Email: c.Name("stranger") + "@parity.test"}
		c.Slack.AddUser(stranger)
		if err := c.Slack.Approve(context.Background(), msg, stranger.ID, slackReviewGroup); err != nil {
			c.Fatalf("clicking Approve: %v", err)
		}
		slackWaitRefusal(c, msg.Channel, stranger.ID, "No Hoop user has the email "+stranger.Email)
		rv.expectPending(c)
	},
})

var _ = register(Check{
	ID:    "SL-05",
	Title: "a Slack guest or a user of another workspace cannot approve with the admin's email; the admin still can",
	Runs:  slackSidecarRuns,
	Fn: func(c *C) {
		rv := slackSidecarReview(c, []string{slackChannel(c, "listener")})
		msg := rv.message(c)
		for _, tc := range []struct {
			user    FakeSlackUser
			refusal string
		}{
			{FakeSlackUser{ID: "UGUEST", Name: "guest", Email: adminEmail, Guest: true},
				"Slack guests cannot approve a review."},
			{FakeSlackUser{ID: "UFOREIGN", Name: "foreign", Email: adminEmail, TeamID: "TFOREIGN"},
				"Users from another Slack workspace cannot approve a review."},
		} {
			c.Slack.AddUser(tc.user)
			if err := c.Slack.Approve(context.Background(), msg, tc.user.ID, slackReviewGroup); err != nil {
				c.Fatalf("clicking Approve as %s: %v", tc.user.ID, err)
			}
			slackWaitRefusal(c, msg.Channel, tc.user.ID, tc.refusal)
			rv.expectPending(c)
		}
		if err := c.Slack.Approve(context.Background(), msg, slackAdminUserID, slackReviewGroup); err != nil {
			c.Fatalf("approving in Slack: %v", err)
		}
		rv.waitStatus(c, "APPROVED")
	},
})

var _ = register(Check{
	ID:    "SL-06",
	Title: "a reviewed exec through the agent is posted to Slack and Approve by the linked Slack user approves it",
	Runs:  slackGatewayRuns,
	Fn: func(c *C) {
		slackEnsurePlugin(c)
		slackLinkAdmin(c)
		channel := slackChannel(c, "conn")
		sid := slackReviewedExec(c, channel)
		msgs := slackWaitSessionMessages(c, sid, 2)
		if !slackHasChannels(msgs, channel, slackDefaultChannel) {
			c.Fatalf("review posted to %v, want the connection channel %s and the org channel %s",
				slackChannels(msgs), channel, slackDefaultChannel)
		}
		reviewID := msgs[0].ReviewID()
		if !msgs[0].Shows("echo " + strings.ToLower(c.id)) {
			c.Fatalf("the review message does not show the script: %s", truncate(msgs[0].Blocks))
		}

		if err := c.Slack.Approve(context.Background(), msgs[0], slackAdminUserID, slackReviewGroup); err != nil {
			c.Fatalf("approving in Slack: %v", err)
		}
		slackWaitReviewStatus(c, reviewID, "APPROVED")
		slackReviewedBy(c, reviewID, adminEmail)
		for _, m := range msgs {
			slackWaitRewrite(c, m, "Session ready to be executed")
		}
	},
})

var _ = register(Check{
	ID:    "SL-07",
	Title: "a Slack user linked to no hoop user cannot approve a reviewed exec; the review stays pending",
	Runs:  slackGatewayRuns,
	Fn: func(c *C) {
		slackEnsurePlugin(c)
		// Unknown by Slack ID and by email, so no way of naming the approver
		// accepts this user.
		stranger := FakeSlackUser{ID: "USTRANGERGW", Name: "stranger", Email: c.Name("stranger") + "@parity.test"}
		c.Slack.AddUser(stranger)
		sid := slackReviewedExec(c, slackChannel(c, "conn"))
		msgs := slackWaitSessionMessages(c, sid, 1)
		reviewID := msgs[0].ReviewID()
		if err := c.Slack.Approve(context.Background(), msgs[0], stranger.ID, slackReviewGroup); err != nil {
			c.Fatalf("clicking Approve: %v", err)
		}
		slackWaitRefusal(c, msgs[0].Channel, stranger.ID, "")
		if st := slackReviewStatus(c, reviewID); st != "PENDING" {
			c.Fatalf("review %s is %s after a refused click, want PENDING", reviewID, st)
		}
	},
})

var _ = register(Check{
	ID:    "SL-08",
	Title: "Reject in Slack with a reason rejects a reviewed exec and tells its owner in Slack",
	Runs:  slackGatewayRuns,
	Fn: func(c *C) {
		slackEnsurePlugin(c)
		slackLinkAdmin(c)
		sid := slackReviewedExec(c, slackChannel(c, "conn"))
		msgs := slackWaitSessionMessages(c, sid, 1)
		reviewID := msgs[0].ReviewID()
		const reason = "parity: not on a Friday"
		if err := c.Slack.Reject(context.Background(), msgs[0], slackAdminUserID, slackReviewGroup, reason); err != nil {
			c.Fatalf("rejecting in Slack: %v", err)
		}
		slackWaitReviewStatus(c, reviewID, "REJECTED")
		var rev struct {
			RejectionReason *string `json:"rejection_reason"`
		}
		c.Admin().Do(c, http.MethodGet, "/reviews/"+reviewID, nil).Expect(c, http.StatusOK).JSON(c, &rev)
		if rev.RejectionReason == nil || *rev.RejectionReason != reason {
			c.Fatalf("rejection_reason=%v, want %q", rev.RejectionReason, reason)
		}
		slackWaitRewrite(c, msgs[0], "Rejection reason")
		// The owner linked their Slack user, so the rejection reaches them.
		err := waitFor(context.Background(), 20*time.Second, func() (bool, error) {
			for _, m := range c.Slack.Posted() {
				if m.Channel == slackAdminUserID && m.Shows("was *rejected*") && m.Shows(reason) && m.Shows(sid) {
					return true, nil
				}
			}
			return false, nil
		})
		if err != nil {
			c.Fatalf("the owner got no rejection message in Slack: %v (%s)", err, c.Slack.Diagnostics())
		}
	},
})

// slackLinkAdmin links the admin to slackAdminUserID, as /users/self/slack
// does from the gateway's Slack association page.
func slackLinkAdmin(c *C) {
	var linked struct {
		SlackID string `json:"slack_id"`
	}
	c.Admin().Do(c, http.MethodPatch, "/users/self/slack", map[string]string{"slack_id": slackAdminUserID}).
		Expect(c, http.StatusOK).JSON(c, &linked)
	if linked.SlackID != slackAdminUserID {
		c.Fatalf("slack_id=%q after linking, want %q", linked.SlackID, slackAdminUserID)
	}
}

// slackChannel is a Slack channel id unique to the check.
func slackChannel(c *C, suffix string) string {
	return "C" + strings.ToUpper(strings.NewReplacer("-", "").Replace(c.Name(suffix)))
}

// slackEnsurePlugin configures the Slack plugin the way the web app does, a
// plugin and then its config, unless an earlier check of the run already
// did, and waits for the Socket Mode connection.
func slackEnsurePlugin(c *C) {
	c.Slack.AddUser(FakeSlackUser{ID: slackAdminUserID, Name: "parity-admin", Email: adminEmail})
	var plugin struct {
		Config *struct {
			EnvVars map[string]string `json:"envvars"`
		} `json:"config"`
	}
	r := c.Admin().Do(c, http.MethodGet, "/plugins/slack", nil).Expect(c, http.StatusOK, http.StatusNotFound)
	if r.Status == http.StatusNotFound {
		c.Admin().Do(c, http.MethodPost, "/plugins", map[string]any{"name": "slack", "connections": []any{}}).
			Expect(c, http.StatusCreated)
	} else {
		r.JSON(c, &plugin)
	}
	if plugin.Config == nil || len(plugin.Config.EnvVars) == 0 {
		b64 := base64.StdEncoding.EncodeToString
		c.Admin().Do(c, http.MethodPut, "/plugins/slack/config", map[string]string{
			"SLACK_BOT_TOKEN": b64([]byte("xoxb-parity")),
			"SLACK_APP_TOKEN": b64([]byte("xapp-parity")),
			"SLACK_CHANNEL":   b64([]byte(slackDefaultChannel)),
		}).Expect(c, http.StatusOK)
	}
	if err := c.Slack.WaitSocket(context.Background(), 60*time.Second); err != nil {
		c.Fatalf("the Slack service opened no Socket Mode connection: %v (%s)", err, c.Slack.Diagnostics())
	}
}

type slackSidecarReviewResponse struct {
	Forward bool `json:"forward"`
	Review  struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"review"`
}

type slackSidecarReviewStatus struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	DecidedAt       *time.Time `json:"decided_at"`
	RejectionReason *string    `json:"rejection_reason"`
}

// slackReview is one review a sidecar filed and the token it filed it with.
type slackReview struct {
	id      string
	token   string
	rule    string
	payload string
}

// slackSidecarReview files a sidecar review whose listener posts to channels,
// with Slack configured, and returns it while it is pending.
func slackSidecarReview(c *C, channels []string) slackReview {
	slackEnsurePlugin(c)
	rule := c.Name("rule")
	c.Admin().Do(c, http.MethodPost, "/access-requests/rules", map[string]any{
		"name":                     rule,
		"access_type":              "sidecar",
		"connection_names":         []string{},
		"approval_required_groups": []string{},
		"reviewers_groups":         []string{slackReviewGroup},
		"force_approval_groups":    []string{},
		"all_groups_must_approve":  false,
	}).Expect(c, http.StatusCreated)

	name := c.Name("sidecar")
	var created struct {
		Token string `json:"token"`
	}
	c.Admin().Do(c, http.MethodPost, "/sidecars", map[string]any{
		"name": name,
		"configuration": map[string]any{
			"analyzer": map[string]any{"provider": "anthropic", "model": "parity"},
			"listeners": []map[string]any{{
				"name": slackListener, "protocol": "postgres", "listen": ":5432", "upstream": "db:5432",
				"analyzer": map[string]any{"high": "require_review", "approval_rule": rule},
			}},
		},
	}).Expect(c, http.StatusCreated).JSON(c, &created)
	if created.Token == "" {
		c.Fatalf("sidecar created with no token")
	}

	// No channels leaves the listener unset, as a sidecar nobody configured
	// for Slack is.
	if len(channels) > 0 {
		var saved struct {
			Listeners []struct {
				Name     string   `json:"name"`
				Channels []string `json:"channels"`
			} `json:"listeners"`
		}
		c.Admin().Do(c, http.MethodPut, "/sidecars/"+name+"/slack-channels", map[string]any{
			"listeners": []map[string]any{{"name": slackListener, "channels": channels}},
		}).Expect(c, http.StatusOK).JSON(c, &saved)
		if len(saved.Listeners) != 1 || !slices.Equal(saved.Listeners[0].Channels, channels) {
			c.Fatalf("slack channels saved as %+v, want %s=%v", saved.Listeners, slackListener, channels)
		}
	}

	rv := slackReview{token: created.Token, rule: rule,
		payload: base64.StdEncoding.EncodeToString([]byte("DELETE FROM accounts WHERE id = '" + c.Name("row") + "';"))}
	var filed slackSidecarReviewResponse
	rv.post(c).Expect(c, http.StatusCreated).JSON(c, &filed)
	if filed.Forward || filed.Review.ID == "" || filed.Review.Status != "PENDING" {
		c.Fatalf("filed review: forward=%v id=%q status=%s, want a PENDING review that does not forward",
			filed.Forward, filed.Review.ID, filed.Review.Status)
	}
	rv.id = filed.Review.ID
	return rv
}

func (rv slackReview) post(c *C) Resp {
	return c.Sidecar(rv.token).Do(c, http.MethodPost, "/sidecars/reviews", map[string]string{
		"listener_name": slackListener, "payload": rv.payload, "approval_rule": rv.rule,
	})
}

// message waits for the review message the control plane posts after it
// answered the sidecar.
func (rv slackReview) message(c *C) SlackMessage {
	var msgs []SlackMessage
	err := waitFor(context.Background(), 30*time.Second, func() (bool, error) {
		msgs = c.Slack.ReviewMessages(rv.id)
		return len(msgs) > 0, nil
	})
	if err != nil {
		c.Fatalf("no Slack message for review %s: %v (%s)", rv.id, err, c.Slack.Diagnostics())
	}
	return msgs[0]
}

func (rv slackReview) status(c *C) slackSidecarReviewStatus {
	var st slackSidecarReviewStatus
	c.Sidecar(rv.token).Do(c, http.MethodGet, "/sidecars/reviews/"+rv.id, nil).Expect(c, http.StatusOK).JSON(c, &st)
	return st
}

func (rv slackReview) waitStatus(c *C, want string) slackSidecarReviewStatus {
	var st slackSidecarReviewStatus
	err := waitFor(context.Background(), 30*time.Second, func() (bool, error) {
		st = rv.status(c)
		return st.Status == want, nil
	})
	if err != nil {
		c.Fatalf("review %s is %s, want %s: %v (%s)", rv.id, st.Status, want, err, c.Slack.Diagnostics())
	}
	return st
}

// expectPending checks the review is still pending and a retry still denies.
func (rv slackReview) expectPending(c *C) {
	if st := rv.status(c); st.Status != "PENDING" || st.DecidedAt != nil {
		c.Fatalf("review %s is %s (decided_at=%v) after a refused click, want PENDING", rv.id, st.Status, st.DecidedAt)
	}
	var retry slackSidecarReviewResponse
	rv.post(c).Expect(c, http.StatusOK).JSON(c, &retry)
	if retry.Forward || retry.Review.ID != rv.id || retry.Review.Status != "PENDING" {
		c.Fatalf("retry after a refused click: forward=%v review=%s status=%s, want the PENDING %s",
			retry.Forward, retry.Review.ID, retry.Review.Status, rv.id)
	}
}

// slackWaitRefusal waits for the ephemeral answer only the clicking user
// sees. want empty accepts any text.
func slackWaitRefusal(c *C, channel, userID, want string) {
	var seen []SlackEphemeral
	err := waitFor(context.Background(), 20*time.Second, func() (bool, error) {
		seen = c.Slack.Ephemerals()
		for _, e := range seen {
			if e.Channel == channel && e.User == userID && strings.Contains(e.Text, want) {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		c.Fatalf("no refusal %q told to %s on %s: %v; ephemerals=%+v", want, userID, channel, err, seen)
	}
}

// slackWaitRewrite waits for the gateway to rewrite m with no buttons left
// and with want in it.
func slackWaitRewrite(c *C, m SlackMessage, want string) {
	var last []SlackMessage
	err := waitFor(context.Background(), 20*time.Second, func() (bool, error) {
		last = nil
		for _, u := range c.Slack.Updated() {
			if u.Channel != m.Channel || u.TS != m.TS {
				continue
			}
			last = append(last, u)
			if buttons, err := u.buttons(); err == nil && len(buttons) == 0 && u.Shows(want) {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		c.Fatalf("message %s on %s not rewritten to %q without buttons: %v; rewrites=%d (%s)",
			m.TS, m.Channel, want, err, len(last), c.Slack.Diagnostics())
	}
}

// slackReviewedBy checks the review names the hoop user Slack resolved.
func slackReviewedBy(c *C, reviewID, email string) {
	var rev struct {
		Groups []struct {
			Group      string `json:"group"`
			Status     string `json:"status"`
			ReviewedBy *struct {
				Email string `json:"email"`
			} `json:"reviewed_by"`
		} `json:"review_groups_data"`
	}
	c.Admin().Do(c, http.MethodGet, "/reviews/"+reviewID, nil).Expect(c, http.StatusOK).JSON(c, &rev)
	for _, g := range rev.Groups {
		if g.Group == slackReviewGroup && g.ReviewedBy != nil && g.ReviewedBy.Email == email {
			return
		}
	}
	c.Fatalf("review %s groups %+v, want group %s reviewed by %s", reviewID, rev.Groups, slackReviewGroup, email)
}

// slackReviewedExec runs an exec on a connection that needs a review, with the
// connection's Slack channel set, and returns its session id.
func slackReviewedExec(c *C, channel string) string {
	if c.AgentID == "" {
		c.Fatalf("no agent is connected on this run")
	}
	name := c.Name("conn")
	var conn struct {
		ID string `json:"id"`
	}
	c.Admin().Do(c, http.MethodPost, "/connections", map[string]any{
		"name": name, "type": "custom", "subtype": "custom", "agent_id": c.AgentID,
		"command": []string{"/bin/bash"}, "secret": map[string]any{},
		"reviewers":            []string{slackReviewGroup},
		"access_mode_runbooks": "enabled", "access_mode_exec": "enabled",
		"access_mode_connect": "enabled", "access_schema": "disabled",
	}).Expect(c, http.StatusCreated).JSON(c, &conn)
	c.Admin().Do(c, http.MethodPut, "/plugins/slack/conn/"+conn.ID, map[string]any{"config": []string{channel}}).
		Expect(c, http.StatusOK)

	var exec struct {
		HasReview bool   `json:"has_review"`
		SessionID string `json:"session_id"`
	}
	c.Admin().Do(c, http.MethodPost, "/sessions", map[string]any{
		"connection": name, "script": "echo " + strings.ToLower(c.id),
	}).Expect(c, http.StatusOK, http.StatusAccepted).JSON(c, &exec)
	if !exec.HasReview || exec.SessionID == "" {
		c.Fatalf("exec has_review=%v session=%q, want a session waiting for review", exec.HasReview, exec.SessionID)
	}
	return exec.SessionID
}

// slackWaitSessionMessages waits for n review messages posted for the session.
func slackWaitSessionMessages(c *C, sid string, n int) []SlackMessage {
	var msgs []SlackMessage
	err := waitFor(context.Background(), 30*time.Second, func() (bool, error) {
		msgs = nil
		for _, m := range c.Slack.Posted() {
			if m.SessionID() == sid && m.ReviewID() != "" {
				msgs = append(msgs, m)
			}
		}
		return len(msgs) >= n, nil
	})
	if err != nil {
		c.Fatalf("%d Slack review messages for session %s, want %d: %v (%s)", len(msgs), sid, n, err, c.Slack.Diagnostics())
	}
	return msgs
}

func slackChannels(msgs []SlackMessage) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.Channel)
	}
	return out
}

func slackHasChannels(msgs []SlackMessage, want ...string) bool {
	got := slackChannels(msgs)
	for _, w := range want {
		if !slices.Contains(got, w) {
			return false
		}
	}
	return true
}

func slackReviewStatus(c *C, reviewID string) string {
	var rev struct {
		Status string `json:"status"`
	}
	c.Admin().Do(c, http.MethodGet, "/reviews/"+reviewID, nil).Expect(c, http.StatusOK).JSON(c, &rev)
	return rev.Status
}

func slackWaitReviewStatus(c *C, reviewID, want string) {
	var got string
	err := waitFor(context.Background(), 30*time.Second, func() (bool, error) {
		got = slackReviewStatus(c, reviewID)
		return got == want, nil
	})
	if err != nil {
		c.Fatalf("review %s is %s, want %s: %v (%s)", reviewID, got, want, err, c.Slack.Diagnostics())
	}
}
