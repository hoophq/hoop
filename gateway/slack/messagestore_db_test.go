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
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/pglite"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/slack-go/slack"
)

const storeOrgID = "00000000-0000-0000-0000-0000000000c3"

// fakeSlack answers chat.postMessage with a fresh timestamp per call and
// records every chat.update body, so a test can tell which message was
// rewritten and with what.
type fakeSlack struct {
	mu      sync.Mutex
	posts   int
	updates []string
}

func (f *fakeSlack) handler(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/chat.postMessage":
		f.posts++
		fmt.Fprintf(w, `{"ok":true,"channel":%q,"ts":"%d.0"}`, r.FormValue("channel"), f.posts)
	case "/chat.update":
		f.updates = append(f.updates, r.FormValue("ts")+" "+r.FormValue("blocks"))
		fmt.Fprintf(w, `{"ok":true,"channel":%q,"ts":%q}`, r.FormValue("channel"), r.FormValue("ts"))
	}
}

func (f *fakeSlack) rewrites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.updates...)
}

// replica is one control plane process: its own SlackService, the shared
// database.
func replica(f *fakeSlack, srv *httptest.Server) *SlackService {
	s := NewWithAPIClient(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")), "T1", "C-default")
	s.instanceID = storeOrgID
	WithMessageStoreIn(models.DB)(s)
	return s
}

func startStoreDB(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	ctx := context.Background()
	inst, err := pglite.Start(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("start embedded database: %v", err)
	}
	t.Cleanup(func() { inst.Close(ctx) })
	if err := modelsbootstrap.MigrateDB(inst.MigrateDSN(), ""); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	// The embedded backend serves one session at a time.
	if err := models.InitDatabaseConnection(inst.DSN(), 1); err != nil {
		t.Fatalf("open gorm connection: %v", err)
	}
	if err := models.DB.Exec(`INSERT INTO private.orgs (id, name) VALUES (?, 'slack-store-test')`, storeOrgID).Error; err != nil {
		t.Fatalf("seed org: %v", err)
	}
}

// Two replicas share the database. Each case posts on one and settles on the
// other, which is what Slack does when it hands a click to a socket that did
// not post the message. One embedded database for every case: each boot
// costs tens of seconds.
func TestTheDatabaseStoreSharesReviewMessagesAcrossReplicas(t *testing.T) {
	startStoreDB(t)
	f := &fakeSlack{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	poster, clicker := replica(f, srv), replica(f, srv)

	post := func(t *testing.T) string {
		t.Helper()
		id := uuid.NewString()
		res := poster.PostMessageReview(&MessageReviewRequest{ID: id, SessionID: uuid.NewString(),
			ApprovalGroups: []string{"admin"}, SlackChannels: []string{"C1"}, DefaultChannelAsFallback: true})
		if res.Posted != 1 {
			t.Fatalf("posted %d messages, want 1: %v", res.Posted, res.Errors)
		}
		return id
	}

	t.Run("another replica rewrites the message and drops its buttons", func(t *testing.T) {
		id := post(t)
		if !clicker.HasTrackedReviewMessages(id) {
			t.Fatal("the other replica does not see the posted message")
		}
		before := len(f.rewrites())
		if err := clicker.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: id, IsRejected: true,
			RejectionReason: "not now"}); err != nil {
			t.Fatalf("reject: %v", err)
		}
		got := f.rewrites()[before:]
		if len(got) != 1 {
			t.Fatalf("chat.update calls = %d, want 1", len(got))
		}
		if strings.Contains(got[0], "review-approved") || !strings.Contains(got[0], "not now") {
			t.Errorf("the rewrite kept the buttons or lost the reason: %s", got[0])
		}
		if clicker.HasTrackedReviewMessages(id) || poster.HasTrackedReviewMessages(id) {
			t.Error("a settled review still reads as tracked")
		}
		if err := clicker.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: id, IsRejected: true}); err != nil ||
			len(f.rewrites()) != before+1 {
			t.Errorf("a second terminal update rewrote again: err=%v", err)
		}
	})

	t.Run("a revoke on a third replica rewrites what the approval did", func(t *testing.T) {
		id := post(t)
		before := len(f.rewrites())
		if err := clicker.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: id, IsApproved: true}); err != nil {
			t.Fatalf("approve: %v", err)
		}
		third := replica(f, srv)
		if err := third.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: id, IsRevoked: true}); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		got := f.rewrites()[before:]
		if len(got) != 2 || !strings.Contains(got[1], "Approval revoked") {
			t.Fatalf("rewrites = %v, want the approval then the revoke", got)
		}
		if err := third.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: id, IsRevoked: true}); err != nil ||
			len(f.rewrites()) != before+2 {
			t.Errorf("a second revoke rewrote again: err=%v", err)
		}
	})

	t.Run("a post after the review settled elsewhere is rewritten at once", func(t *testing.T) {
		id := uuid.NewString()
		if err := clicker.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: id, IsApproved: true}); err != nil {
			t.Fatalf("approve: %v", err)
		}
		if final := poster.trackSentReviewMessage(id, sentReviewMessage{channelID: "C9", timestamp: "9.0"}); final == nil || !final.IsApproved {
			t.Fatalf("a post after settlement got %+v, want the approval", final)
		}
	})

	t.Run("a partial approval rewrites without settling", func(t *testing.T) {
		id := post(t)
		before := len(f.rewrites())
		if err := clicker.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: id, TotalGroups: 2,
			ReviewedGroups: []ReviewedGroup{{Name: "admin", Status: "APPROVED"}}}); err != nil {
			t.Fatalf("partial: %v", err)
		}
		if len(f.rewrites()) != before+1 || !poster.HasTrackedReviewMessages(id) {
			t.Error("a partial approval must rewrite once and leave the message tracked")
		}
	})
}
