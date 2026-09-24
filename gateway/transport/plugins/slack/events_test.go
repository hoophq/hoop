package slack

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
)

func TestCheckSlackUser(t *testing.T) {
	ok := slackservice.SlackUser{ID: "U1", Email: "ana@example.com", IsEmailConfirmed: true}
	for _, tt := range []struct {
		name         string
		mutate       func(u *slackservice.SlackUser)
		wantFallback bool
		wantRefusal  string
	}{
		{name: "vouched", mutate: func(u *slackservice.SlackUser) {}},
		{name: "deactivated", mutate: func(u *slackservice.SlackUser) { u.Deleted = true }, wantRefusal: "deactivated"},
		{name: "bot", mutate: func(u *slackservice.SlackUser) { u.IsBot = true }, wantRefusal: "bot"},
		{name: "bot without email is still a bot", mutate: func(u *slackservice.SlackUser) { u.IsBot = true; u.Email = "" }, wantRefusal: "bot"},
		// No email is what Slack answers when the app lacks users:read.email.
		{name: "no email falls back", mutate: func(u *slackservice.SlackUser) { u.Email = "" }, wantFallback: true},
		{name: "guest", mutate: func(u *slackservice.SlackUser) { u.IsRestricted = true }, wantRefusal: "guests"},
		{name: "single channel guest", mutate: func(u *slackservice.SlackUser) { u.IsUltraRestricted = true }, wantRefusal: "guests"},
		{name: "unconfirmed email", mutate: func(u *slackservice.SlackUser) { u.IsEmailConfirmed = false }, wantRefusal: "Confirm the email"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u := ok
			tt.mutate(&u)
			fallback, refusal := checkSlackUser(&u)
			if fallback != tt.wantFallback {
				t.Errorf("fallback = %v, want %v", fallback, tt.wantFallback)
			}
			if tt.wantRefusal == "" && refusal != "" {
				t.Errorf("refusal = %q, want none", refusal)
			}
			if tt.wantRefusal != "" && !strings.Contains(refusal, tt.wantRefusal) {
				t.Errorf("refusal = %q, want it to mention %q", refusal, tt.wantRefusal)
			}
		})
	}
}

func TestPickApprover(t *testing.T) {
	ana := models.User{ID: "u1", Email: "ana@example.com"}

	got, refusal := pickApprover([]models.User{ana}, "ana@example.com")
	if refusal != "" || got == nil || got.ID != "u1" {
		t.Fatalf("one user: got %+v, refusal %q; want u1", got, refusal)
	}

	got, refusal = pickApprover(nil, "ana@example.com")
	if got != nil || !strings.Contains(refusal, "No active Hoop user") || !strings.Contains(refusal, "ana@example.com") {
		t.Errorf("no user: got %+v, refusal %q", got, refusal)
	}

	got, refusal = pickApprover([]models.User{ana, {ID: "u2", Email: "ANA@example.com"}}, "ana@example.com")
	if got != nil || !strings.Contains(refusal, "More than one") {
		t.Errorf("two users: got %+v, refusal %q; want a refusal", got, refusal)
	}
}
