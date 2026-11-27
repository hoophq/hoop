package slack

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/slack-go/slack"
)

// lookupSlack answers users.lookupByEmail from byEmail and counts the calls.
// An email it does not hold gets users_not_found, as Slack answers.
type lookupSlack struct {
	mu      sync.Mutex
	byEmail map[string]string
	calls   int
}

func (f *lookupSlack) handler(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path != "/users.lookupByEmail" {
		fmt.Fprint(w, `{"ok":false,"error":"unexpected_call"}`)
		return
	}
	f.calls++
	body, ok := f.byEmail[strings.ToLower(r.FormValue("email"))]
	if !ok {
		body = `{"ok":false,"error":"users_not_found"}`
	}
	fmt.Fprint(w, body)
}

func (f *lookupSlack) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// slackUserBody is a users.lookupByEmail answer for a full member of team T1
// with a confirmed email; extra is spliced into the user object.
func slackUserBody(id, email, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return fmt.Sprintf(`{"ok":true,"user":{"id":%q,"team_id":"T1","is_email_confirmed":true,"profile":{"email":%q}%s}}`, id, email, extra)
}

func seedLinkUser(t *testing.T, email, slackID string) string {
	t.Helper()
	id := uuid.NewString()
	var sid any
	if slackID != "" {
		sid = slackID
	}
	if err := models.DB.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status, slack_id)
		VALUES (?, ?, ?, ?, ?, 'active', ?)`, id, storeOrgID, "sub|"+id, email, email, sid).Error; err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

func storedSlackID(t *testing.T, userID string) string {
	t.Helper()
	var sid *string
	if err := models.DB.Raw(`SELECT slack_id FROM private.users WHERE id = ?`, userID).Scan(&sid).Error; err != nil {
		t.Fatalf("read slack id: %v", err)
	}
	if sid == nil {
		return ""
	}
	return *sid
}

// A user who signs in gets the Slack user with the same email, and only when
// Slack vouches for that user and nothing is ambiguous. One embedded database
// for every case: each boot costs tens of seconds.
func TestLinkUserByEmail(t *testing.T) {
	startStoreDB(t)
	f := &lookupSlack{byEmail: map[string]string{
		"ana@example.com":         slackUserBody("U-ANA", "Ana@Example.com", ""),
		"guest@example.com":       slackUserBody("U-GUEST", "guest@example.com", `"is_restricted":true`),
		"bot@example.com":         slackUserBody("U-BOT", "bot@example.com", `"is_bot":true`),
		"gone@example.com":        slackUserBody("U-GONE", "gone@example.com", `"deleted":true`),
		"elsewhere@example.com":   `{"ok":true,"user":{"id":"U-ELSE","team_id":"T2","is_email_confirmed":true,"profile":{"email":"elsewhere@example.com"}}}`,
		"unconfirmed@example.com": `{"ok":true,"user":{"id":"U-UNC","team_id":"T1","is_email_confirmed":false,"profile":{"email":"unconfirmed@example.com"}}}`,
		"other@example.com":       slackUserBody("U-OTHER", "someone-else@example.com", ""),
		"taken@example.com":       slackUserBody("U-TAKEN", "taken@example.com", ""),
		"twice@example.com":       slackUserBody("U-TWICE", "twice@example.com", ""),
		"linked@example.com":      slackUserBody("U-NEW", "linked@example.com", ""),
		"flagoff@example.com":     slackUserBody("U-OFF", "flagoff@example.com", ""),
	}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	svc := NewWithAPIClient(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")), "T1", "")
	SetServiceInstance(storeOrgID, svc)
	t.Cleanup(func() { RemoveServiceInstanceIf(storeOrgID, svc) })
	featureflag.Set(storeOrgID, featureflag.FlagSlackLookupByEmail, true)
	t.Cleanup(func() { featureflag.Set(storeOrgID, featureflag.FlagSlackLookupByEmail, false) })

	link := func(t *testing.T, email string) string {
		t.Helper()
		got, err := LinkUserByEmail(context.Background(), models.DB, storeOrgID, email)
		if err != nil {
			t.Fatalf("LinkUserByEmail(%s): %v", email, err)
		}
		return got
	}

	t.Run("the slack user with the same email is linked", func(t *testing.T) {
		id := seedLinkUser(t, "ana@example.com", "")
		if got := link(t, "ana@example.com"); got != "U-ANA" {
			t.Fatalf("returned %q, want U-ANA", got)
		}
		if got := storedSlackID(t, id); got != "U-ANA" {
			t.Fatalf("stored %q, want U-ANA", got)
		}
	})

	for _, email := range []string{
		"guest@example.com", "bot@example.com", "gone@example.com", "elsewhere@example.com",
		"unconfirmed@example.com",
		// Slack answered a user with another email.
		"other@example.com",
		// Slack has no user with this email.
		"nobody@example.com",
	} {
		t.Run("not linked: "+email, func(t *testing.T) {
			id := seedLinkUser(t, email, "")
			if got := link(t, email); got != "" {
				t.Fatalf("returned %q, want none", got)
			}
			if got := storedSlackID(t, id); got != "" {
				t.Fatalf("stored %q, want none", got)
			}
		})
	}

	t.Run("a slack id another user has is not linked again", func(t *testing.T) {
		seedLinkUser(t, "holder@example.com", "U-TAKEN")
		id := seedLinkUser(t, "taken@example.com", "")
		if got := link(t, "taken@example.com"); got != "" {
			t.Fatalf("returned %q, want none", got)
		}
		if got := storedSlackID(t, id); got != "" {
			t.Fatalf("stored %q, want none", got)
		}
	})

	t.Run("two users with the email link nobody and slack is not asked", func(t *testing.T) {
		a := seedLinkUser(t, "twice@example.com", "")
		b := seedLinkUser(t, "Twice@example.com", "")
		before := f.count()
		if got := link(t, "twice@example.com"); got != "" {
			t.Fatalf("returned %q, want none", got)
		}
		if storedSlackID(t, a) != "" || storedSlackID(t, b) != "" {
			t.Fatal("a slack id was stored for an ambiguous email")
		}
		if f.count() != before {
			t.Fatal("slack was asked about an ambiguous email")
		}
	})

	t.Run("a user with a slack id keeps it and slack is not asked", func(t *testing.T) {
		id := seedLinkUser(t, "linked@example.com", "U-BY-ADMIN")
		before := f.count()
		if got := link(t, "linked@example.com"); got != "U-BY-ADMIN" {
			t.Fatalf("returned %q, want U-BY-ADMIN", got)
		}
		if got := storedSlackID(t, id); got != "U-BY-ADMIN" {
			t.Fatalf("stored %q, want U-BY-ADMIN", got)
		}
		if f.count() != before {
			t.Fatal("slack was asked about a linked user")
		}
	})

	t.Run("with the flag off nothing is linked and slack is not asked", func(t *testing.T) {
		featureflag.Set(storeOrgID, featureflag.FlagSlackLookupByEmail, false)
		defer featureflag.Set(storeOrgID, featureflag.FlagSlackLookupByEmail, true)
		id := seedLinkUser(t, "flagoff@example.com", "")
		before := f.count()
		if got := link(t, "flagoff@example.com"); got != "" {
			t.Fatalf("returned %q, want none", got)
		}
		if storedSlackID(t, id) != "" || f.count() != before {
			t.Fatal("the flag is off and a link was tried")
		}
	})

	t.Run("a missing scope is an error, not a missing user", func(t *testing.T) {
		id := seedLinkUser(t, "scope@example.com", "")
		f.mu.Lock()
		f.byEmail["scope@example.com"] = `{"ok":false,"error":"missing_scope"}`
		f.mu.Unlock()
		_, err := LinkUserByEmail(context.Background(), models.DB, storeOrgID, "scope@example.com")
		if !IsMissingScope(err) {
			t.Fatalf("err = %v, want missing_scope", err)
		}
		if got := storedSlackID(t, id); got != "" {
			t.Fatalf("stored %q, want none", got)
		}
	})
}
