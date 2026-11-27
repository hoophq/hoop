package slack

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/gateway/liveevents"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/slack-go/slack"
)

// seedCodeUser stores a user of the store org and returns its id.
func seedCodeUser(t *testing.T, email, slackID string) string {
	t.Helper()
	id := uuid.NewString()
	var sid any
	if slackID != "" {
		sid = slackID
	}
	if err := models.DB.Exec(`INSERT INTO private.users (id, org_id, subject, email, name, status, slack_id)
		VALUES (?, ?, ?, ?, ?, 'active', ?)`, id, storeOrgID, "sub|"+email, email, email, sid).Error; err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

func slackIDOf(t *testing.T, userID string) string {
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

func newCode(t *testing.T, userID string) string {
	t.Helper()
	code, expiresAt, err := NewLinkCode(models.DB, storeOrgID, userID)
	if err != nil {
		t.Fatalf("NewLinkCode: %v", err)
	}
	if len(code) != linkCodeLen || strings.Trim(code, linkCodeAlphabet) != "" {
		t.Fatalf("code %q is not %d letters of the alphabet", code, linkCodeLen)
	}
	if left := time.Until(expiresAt); left <= 0 || left > linkCodeTTL {
		t.Fatalf("the code expires in %v, want within %v", left, linkCodeTTL)
	}
	return code
}

// The code proves the Hoop user and Slack names the Slack user. One embedded
// database for every case: each boot costs tens of seconds.
func TestLinkCommand(t *testing.T) {
	startStoreDB(t)
	s := NewWithAPIClient(slack.New("xoxb-test"), "T1", "")
	s.instanceID = storeOrgID
	featureflag.Set(storeOrgID, featureflag.FlagSlackConnectCode, true)
	t.Cleanup(func() { featureflag.Set(storeOrgID, featureflag.FlagSlackConnectCode, false) })

	send := func(slackUser, team, text string) (string, bool) {
		return s.linkCommand(slack.SlashCommand{Command: "/hoop", Text: text, UserID: slackUser, TeamID: team})
	}

	t.Run("the code links the slack user that sends it, once", func(t *testing.T) {
		ana := seedCodeUser(t, "ana@example.com", "")
		events, stop := liveevents.Subscribe(storeOrgID)
		defer stop()
		code := newCode(t, ana)

		// A person types it: lowercase, with a space in the middle.
		typed := strings.ToLower(code[:4]) + " " + strings.ToLower(code[4:])
		reply, ok := send("U-ANA", "T1", "link "+typed)
		if !ok || reply != "Your Slack user is now linked to the Hoop user ana@example.com." {
			t.Fatalf("reply %q, handled %v", reply, ok)
		}
		if got := slackIDOf(t, ana); got != "U-ANA" {
			t.Fatalf("stored %q, want U-ANA", got)
		}
		select {
		case ev := <-events:
			if ev.Type != liveevents.UserSlackLinked || ev.ID != "sub|ana@example.com" {
				t.Fatalf("event %+v, want user.slack_linked for the user's subject", ev)
			}
		case <-time.After(time.Second):
			t.Fatal("no live event after the link")
		}

		if reply, _ := send("U-MALLORY", "T1", "link "+code); reply != linkInvalidMsg {
			t.Fatalf("second use answered %q, want the invalid code answer", reply)
		}
		if got := slackIDOf(t, ana); got != "U-ANA" {
			t.Fatalf("a used code changed the link to %q", got)
		}
	})

	t.Run("a wrong code links nobody", func(t *testing.T) {
		bo := seedCodeUser(t, "bo@example.com", "")
		newCode(t, bo)
		if reply, _ := send("U-BO", "T1", "link ZZZZZZZZ"); reply != linkInvalidMsg {
			t.Fatalf("answered %q", reply)
		}
		if got := slackIDOf(t, bo); got != "" {
			t.Fatalf("stored %q, want none", got)
		}
	})

	t.Run("a new code replaces the previous one", func(t *testing.T) {
		cy := seedCodeUser(t, "cy@example.com", "")
		first := newCode(t, cy)
		second := newCode(t, cy)
		if reply, _ := send("U-CY", "T1", "link "+first); reply != linkInvalidMsg {
			t.Fatalf("the replaced code answered %q", reply)
		}
		if reply, _ := send("U-CY", "T1", "link "+second); !strings.Contains(reply, "cy@example.com") {
			t.Fatalf("the new code answered %q", reply)
		}
	})

	t.Run("an expired code links nobody", func(t *testing.T) {
		di := seedCodeUser(t, "di@example.com", "")
		code := newCode(t, di)
		if err := models.DB.Exec(`UPDATE private.slack_link_codes SET expires_at = NOW() - INTERVAL '1 second' WHERE user_id = ?`, di).Error; err != nil {
			t.Fatal(err)
		}
		if reply, _ := send("U-DI", "T1", "link "+code); reply != linkInvalidMsg {
			t.Fatalf("answered %q", reply)
		}
		if got := slackIDOf(t, di); got != "" {
			t.Fatalf("stored %q, want none", got)
		}
	})

	t.Run("a slack user of another workspace is refused and the code stays", func(t *testing.T) {
		ed := seedCodeUser(t, "ed@example.com", "")
		code := newCode(t, ed)
		if reply, _ := send("U-OUT", "T2", "link "+code); reply != linkOtherWorkspaceMsg {
			t.Fatalf("answered %q", reply)
		}
		if got := slackIDOf(t, ed); got != "" {
			t.Fatalf("stored %q, want none", got)
		}
		if reply, _ := send("U-ED", "T1", "link "+code); !strings.Contains(reply, "ed@example.com") {
			t.Fatalf("the code was spent by the refused command: %q", reply)
		}
	})

	t.Run("a slack user another hoop user has is refused and the code stays", func(t *testing.T) {
		seedCodeUser(t, "holder@example.com", "U-HELD")
		fi := seedCodeUser(t, "fi@example.com", "")
		code := newCode(t, fi)
		if reply, _ := send("U-HELD", "T1", "link "+code); reply != linkInUseMsg {
			t.Fatalf("answered %q", reply)
		}
		if got := slackIDOf(t, fi); got != "" {
			t.Fatalf("stored %q, want none", got)
		}
		if reply, _ := send("U-FI", "T1", "link "+code); !strings.Contains(reply, "fi@example.com") {
			t.Fatalf("the code was spent by the refused command: %q", reply)
		}
	})

	t.Run("a user can link another slack user again", func(t *testing.T) {
		gus := seedCodeUser(t, "gus@example.com", "U-OLD")
		code := newCode(t, gus)
		if reply, _ := send("U-NEW", "T1", "link "+code); !strings.Contains(reply, "gus@example.com") {
			t.Fatalf("answered %q", reply)
		}
		if got := slackIDOf(t, gus); got != "U-NEW" {
			t.Fatalf("stored %q, want U-NEW", got)
		}
	})

	t.Run("link with no code says how to get one", func(t *testing.T) {
		if reply, ok := send("U-X", "T1", "link"); !ok || reply != linkNoCodeMsg {
			t.Fatalf("reply %q, handled %v", reply, ok)
		}
	})

	t.Run("another command is not a link command", func(t *testing.T) {
		for _, text := range []string{"", "subscribe", "linked ABCDEFGH"} {
			if _, ok := send("U-X", "T1", text); ok {
				t.Fatalf("%q was handled as a link command", text)
			}
		}
	})

	t.Run("with the flag off the command is not handled and links nobody", func(t *testing.T) {
		hal := seedCodeUser(t, "hal@example.com", "")
		code := newCode(t, hal)
		featureflag.Set(storeOrgID, featureflag.FlagSlackConnectCode, false)
		defer featureflag.Set(storeOrgID, featureflag.FlagSlackConnectCode, true)
		if _, ok := send("U-HAL", "T1", "link "+code); ok {
			t.Fatal("the flag is off and the command was handled")
		}
		if got := slackIDOf(t, hal); got != "" {
			t.Fatalf("stored %q, want none", got)
		}
	})
}
