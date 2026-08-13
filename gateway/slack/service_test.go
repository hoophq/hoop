package slack

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

// The AI analysis text is model output derived from the user's script. Escaping
// expands "&" to "&amp;" (5x), so the caps must bound the ESCAPED text — an
// oversized section makes Slack reject the whole message (invalid_blocks) and
// the review notification never reaches approvers.
func TestAIAnalysisTextStaysWithinSlackLimits(t *testing.T) {
	hostile := strings.Repeat("&", 4000)

	title := truncateRunes(escapeSlackText(hostile), maxAITitleSize)
	summary := truncateRunes(escapeSlackText(hostile), maxLabelSize)

	if len(title) > maxAITitleSize {
		t.Errorf("escaped title = %d bytes, want <= %d", len(title), maxAITitleSize)
	}
	if len(summary) > maxLabelSize {
		t.Errorf("escaped summary = %d bytes, want <= %d", len(summary), maxLabelSize)
	}
	// Slack's section text object limit; the assembled block must fit.
	if total := len(title) + len(summary); total >= 3000 {
		t.Errorf("assembled analysis text = %d bytes, want < 3000", total)
	}
}

// Unescaped mrkdwn control sequences would render as channel pings and spoofed
// links inside the approve/reject message.
func TestEscapeSlackTextNeutralizesControlSequences(t *testing.T) {
	got := escapeSlackText("<!channel> see <https://evil.example|docs> & <@U123>")
	for _, raw := range []string{"<!channel>", "<https://", "<@U123>"} {
		if strings.Contains(got, raw) {
			t.Errorf("control sequence %q survived escaping: %s", raw, got)
		}
	}
	if !strings.Contains(got, "&amp;lt;!channel&amp;gt;") && !strings.Contains(got, "&lt;!channel&gt;") {
		t.Errorf("unexpected escaping result: %s", got)
	}
}

// Model prose is routinely multibyte; a byte-level cut would emit invalid UTF-8
// that renders as U+FFFD.
func TestTruncateRunesKeepsValidUTF8(t *testing.T) {
	// Each em dash is 3 bytes, so a naive cut at an odd budget splits one.
	s := strings.Repeat("—", 100)
	for _, max := range []int{10, 11, 12, 13, 50, 299} {
		got := truncateRunes(s, max)
		if !utf8.ValidString(got) {
			t.Errorf("truncateRunes(max=%d) produced invalid UTF-8: %q", max, got)
		}
		if len(got) > max {
			t.Errorf("truncateRunes(max=%d) = %d bytes, want <= %d", max, len(got), max)
		}
	}
	if got := truncateRunes("short", 100); got != "short" {
		t.Errorf("under-budget string modified: %q", got)
	}
	if got := truncateRunes("anything", 0); got != "" {
		t.Errorf("max=0 must yield empty, got %q", got)
	}
	if got := truncateRunes("anything", -5); got != "" {
		t.Errorf("negative max must yield empty, got %q", got)
	}
}

// The block id format "<review-id>:<group-name>:<index>" is the only link
// between a review group and its action block; group names may contain colons.
func TestReviewGroupFromBlockID(t *testing.T) {
	const revID = "8a4f4c5e-9f1d-4a89-b0f3-1c2d3e4f5a6b"
	for _, tc := range []struct{ blockID, want string }{
		{revID + ":admin:0", "admin"},
		{revID + ":sre:eu:west:2", "sre:eu:west"},
		{"other-review:admin:0", ""},
		{revID + ":admin", "admin"},
	} {
		if got := reviewGroupFromBlockID(tc.blockID, revID); got != tc.want {
			t.Errorf("reviewGroupFromBlockID(%q) = %q, want %q", tc.blockID, got, tc.want)
		}
	}
}

// A review resolved via API/webapp must rewrite only the reviewed groups'
// action blocks, keep pending groups actionable, and never mutate the
// originally posted block set shared across channels.
func TestRebuildReviewBlocks(t *testing.T) {
	const revID = "rev-1"
	original := []slack.Block{
		slack.NewHeaderBlock(&slack.TextBlockObject{Type: slack.PlainTextType, Text: "Hoop Review"}),
		slack.NewActionBlock(revID + ":admin:0"),
		slack.NewActionBlock(revID + ":sre:1"),
	}
	m := &sentReviewMessage{eventKind: EventKindOneTime, blocks: original}
	reviewedAt := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	reviewed := map[string]ReviewedGroup{
		"admin": {Name: "admin", Status: "APPROVED", ReviewerEmail: "a@a.com", ReviewedAt: reviewedAt},
	}

	// partial approval: admin replaced, sre still actionable, progress context appended
	req := &UpdateReviewMessageRequest{ReviewID: revID, ReviewedGroups: []ReviewedGroup{reviewed["admin"]}, TotalGroups: 2}
	blocks := rebuildReviewBlocks(m, req, reviewed)
	if len(blocks) != 4 {
		t.Fatalf("partial: got %d blocks, want 4", len(blocks))
	}
	sec, ok := blocks[1].(*slack.SectionBlock)
	if !ok {
		t.Fatalf("partial: reviewed group block not replaced, got %T", blocks[1])
	}
	if !strings.Contains(sec.Text.Text, "a@a.com") || !strings.Contains(sec.Text.Text, "`approved`") {
		t.Errorf("partial: unexpected outcome text: %s", sec.Text.Text)
	}
	if _, ok := blocks[2].(*slack.ActionBlock); !ok {
		t.Errorf("partial: pending group must keep its buttons, got %T", blocks[2])
	}
	if _, ok := blocks[3].(*slack.ContextBlock); !ok {
		t.Errorf("partial: missing progress context block, got %T", blocks[3])
	}

	// terminal approval (e.g. min_approvals reached): unreviewed sre buttons
	// are dropped and the ready divider+section replaces the progress context
	req.IsApproved = true
	blocks = rebuildReviewBlocks(m, req, reviewed)
	if len(blocks) != 4 {
		t.Fatalf("approved: got %d blocks, want 4", len(blocks))
	}
	for _, b := range blocks {
		if _, ok := b.(*slack.ActionBlock); ok {
			t.Errorf("approved: terminal message must not keep buttons")
		}
	}
	ready, ok := blocks[3].(*slack.SectionBlock)
	if !ok || !strings.Contains(ready.Text.Text, "Session ready") {
		t.Errorf("approved: missing ready section, got %T", blocks[3])
	}

	// terminal rejection appends nothing and drops remaining buttons
	req.IsApproved = false
	req.IsRejected = true
	if blocks = rebuildReviewBlocks(m, req, reviewed); len(blocks) != 2 {
		t.Errorf("rejected: got %d blocks, want 2", len(blocks))
	}

	// original blocks are shared across channels and must stay intact
	if _, ok := original[1].(*slack.ActionBlock); !ok {
		t.Errorf("original block set mutated: %T", original[1])
	}
}

// Terminal updates must consume the tracked entry (no stale rewrites) and
// untracked reviews must be a silent no-op even without an api client.
func TestUpdateReviewMessageTracking(t *testing.T) {
	s := &SlackService{sentReviewItems: make(map[string][]sentReviewMessage)}

	// untracked review: no-op, no network
	if err := s.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: "unknown", IsApproved: true}); err != nil {
		t.Fatalf("untracked review must be a no-op, got %v", err)
	}

	// terminal update rewrites the message once and consumes the tracked entry
	var updateCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		updateCalls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"channel":"C1","ts":"1.0"}`)
	}))
	defer srv.Close()
	s.apiClient = slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/"))

	s.sentReviewItems["rev-1"] = []sentReviewMessage{{channelID: "C1", timestamp: "1.0"}}
	req := &UpdateReviewMessageRequest{ReviewID: "rev-1", IsApproved: true}
	if err := s.UpdateReviewMessage(req); err != nil {
		t.Fatalf("tracked terminal update failed: %v", err)
	}
	if updateCalls != 1 {
		t.Fatalf("chat.update called %d times, want 1", updateCalls)
	}
	if err := s.UpdateReviewMessage(req); err != nil || updateCalls != 1 {
		t.Fatalf("consumed entry must not be rewritten again, calls=%d err=%v", updateCalls, err)
	}

	// eviction drops entries older than the retention window on new sends
	stale := time.Now().UTC().Add(-sentReviewRetention - time.Hour)
	s.sentReviewItems["rev-old"] = []sentReviewMessage{{channelID: "C1", timestamp: "1.0", sentAt: stale}}
	s.trackSentReviewMessages("rev-new", []sentReviewMessage{{channelID: "C2", timestamp: "2.0", sentAt: time.Now().UTC()}})
	if _, ok := s.sentReviewItems["rev-old"]; ok {
		t.Errorf("expired entry survived eviction")
	}
	if _, ok := s.sentReviewItems["rev-new"]; !ok {
		t.Errorf("fresh entry was not tracked")
	}
}
