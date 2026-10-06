package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
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
	label := func(group string) *slack.SectionBlock {
		return slack.NewSectionBlock(&slack.TextBlockObject{
			Type: slack.MarkdownType, Text: "*Approver groups:* " + group,
		}, nil, nil)
	}
	original := []slack.Block{
		slack.NewHeaderBlock(&slack.TextBlockObject{Type: slack.PlainTextType, Text: "Hoop Review"}),
		label("admin"),
		slack.NewActionBlock(revID + ":admin:0"),
		label("sre"),
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
	if len(blocks) != 6 {
		t.Fatalf("partial: got %d blocks, want 6", len(blocks))
	}
	sec, ok := blocks[2].(*slack.SectionBlock)
	if !ok {
		t.Fatalf("partial: reviewed group block not replaced, got %T", blocks[2])
	}
	if !strings.Contains(sec.Text.Text, "a@a.com") || !strings.Contains(sec.Text.Text, "`approved`") {
		t.Errorf("partial: unexpected outcome text: %s", sec.Text.Text)
	}
	if _, ok := blocks[4].(*slack.ActionBlock); !ok {
		t.Errorf("partial: pending group must keep its buttons, got %T", blocks[4])
	}
	if _, ok := blocks[5].(*slack.ContextBlock); !ok {
		t.Errorf("partial: missing progress context block, got %T", blocks[5])
	}

	// terminal approval (e.g. min_approvals reached): unreviewed sre button AND
	// its label are dropped, ready divider+section replaces the progress context
	req.IsApproved = true
	blocks = rebuildReviewBlocks(m, req, reviewed)
	if len(blocks) != 5 {
		t.Fatalf("approved: got %d blocks, want 5", len(blocks))
	}
	for _, b := range blocks {
		if _, ok := b.(*slack.ActionBlock); ok {
			t.Errorf("approved: terminal message must not keep buttons")
		}
		if sec, ok := b.(*slack.SectionBlock); ok && strings.Contains(sec.Text.Text, "sre") {
			t.Errorf("approved: orphaned label for dropped sre button: %s", sec.Text.Text)
		}
	}
	ready, ok := blocks[4].(*slack.SectionBlock)
	if !ok || !strings.Contains(ready.Text.Text, "Session ready") {
		t.Errorf("approved: missing ready section, got %T", blocks[4])
	}

	// terminal rejection appends nothing and drops remaining buttons + labels
	req.IsApproved = false
	req.IsRejected = true
	if blocks = rebuildReviewBlocks(m, req, reviewed); len(blocks) != 3 {
		t.Errorf("rejected: got %d blocks, want 3", len(blocks))
	}

	// a rejection with a reason shows it, quoted and escaped
	req.RejectionReason = "not in prod <@U1>"
	blocks = rebuildReviewBlocks(m, req, reviewed)
	if len(blocks) != 5 {
		t.Fatalf("rejected with reason: got %d blocks, want 5", len(blocks))
	}
	reason, ok := blocks[4].(*slack.SectionBlock)
	if !ok || !strings.Contains(reason.Text.Text, "Rejection reason") ||
		!strings.Contains(reason.Text.Text, "> not in prod &lt;@U1&gt;") {
		t.Errorf("rejected with reason: unexpected block %T %+v", blocks[4], reason)
	}
	req.RejectionReason = ""

	// synthetic reviewed group (admin/owner rejection, forced approval) matches
	// no action block: its outcome must still be rendered, never a silent drop
	synthetic := ReviewedGroup{Name: "owner-veto", Status: "REJECTED", ReviewerEmail: "b@b.com", ReviewedAt: reviewedAt}
	sreq := &UpdateReviewMessageRequest{ReviewID: revID, IsRejected: true,
		ReviewedGroups: []ReviewedGroup{synthetic}, TotalGroups: 2}
	blocks = rebuildReviewBlocks(m, sreq, map[string]ReviewedGroup{synthetic.Name: synthetic})
	var foundOutcome bool
	for _, b := range blocks {
		if _, ok := b.(*slack.ActionBlock); ok {
			t.Errorf("synthetic rejection: buttons must be dropped")
		}
		if sec, ok := b.(*slack.SectionBlock); ok && strings.Contains(sec.Text.Text, "b@b.com") && strings.Contains(sec.Text.Text, "`rejected`") {
			foundOutcome = true
		}
	}
	if !foundOutcome {
		t.Errorf("synthetic rejection: outcome section missing, blocks=%d", len(blocks))
	}

	// original blocks are shared across channels and must stay intact
	if _, ok := original[2].(*slack.ActionBlock); !ok {
		t.Errorf("original block set mutated: %T", original[2])
	}
}

// Terminal updates must consume the tracked entry (no stale rewrites) and
// untracked reviews must be a silent no-op even without an api client.
func TestUpdateReviewMessageTracking(t *testing.T) {
	s := &SlackService{}
	s.mem.items = make(map[string][]sentReviewMessage)

	// untracked review: no-op, no network
	if err := s.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: "unknown", IsApproved: true}); err != nil {
		t.Fatalf("untracked review must be a no-op, got %v", err)
	}

	// terminal update rewrites the message once and consumes the tracked entry
	var updateCalls int
	var lastBlocks string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		updateCalls++
		_ = r.ParseForm()
		lastBlocks = r.FormValue("blocks")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"channel":"C1","ts":"1.0"}`)
	}))
	defer srv.Close()
	s.apiClient = slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/"))

	s.mem.items["rev-1"] = []sentReviewMessage{{channelID: "C1", timestamp: "1.0"}}
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

	// a message a post loop sends after the review settled is not tracked:
	// the caller gets the terminal state to rewrite it with
	if final := s.trackSentReviewMessage("rev-1", sentReviewMessage{channelID: "C3", timestamp: "3.0"}); final == nil || !final.IsApproved {
		t.Fatalf("a post after settlement must get the terminal state, got %+v", final)
	}
	if _, ok := s.mem.items["rev-1"]; ok {
		t.Errorf("a settled review must not be tracked again")
	}

	// eviction drops entries older than the retention window on new sends
	stale := time.Now().UTC().Add(-sentReviewRetention - time.Hour)
	s.mem.items["rev-old"] = []sentReviewMessage{{channelID: "C1", timestamp: "1.0", sentAt: stale}}
	s.trackSentReviewMessage("rev-new", sentReviewMessage{channelID: "C2", timestamp: "2.0", sentAt: time.Now().UTC()})
	if _, ok := s.mem.items["rev-old"]; ok {
		t.Errorf("expired entry survived eviction")
	}
	if _, ok := s.mem.items["rev-new"]; !ok {
		t.Errorf("fresh entry was not tracked")
	}

	// a sidecar review's limit can outlast the window: the window counts from
	// the deadline, so an expiry recorded after it still finds the message
	recent := time.Now().UTC().Add(-time.Hour)
	longPast := time.Now().UTC().Add(-sentReviewRetention - time.Hour)
	s.mem.items["rev-ttl"] = []sentReviewMessage{{channelID: "C1", timestamp: "1.1", sentAt: stale, deadline: &recent}}
	s.mem.items["rev-ttl-old"] = []sentReviewMessage{{channelID: "C1", timestamp: "1.2", sentAt: stale, deadline: &longPast}}
	s.mem.settledReviews["rev-approved"] = settledReview{at: stale, deadline: &recent}
	s.trackSentReviewMessage("rev-new-2", sentReviewMessage{channelID: "C2", timestamp: "2.1", sentAt: time.Now().UTC()})
	if _, ok := s.mem.items["rev-ttl"]; !ok {
		t.Errorf("a message whose deadline passed an hour ago was evicted")
	}
	if _, ok := s.mem.items["rev-ttl-old"]; ok {
		t.Errorf("a message past its deadline and the window survived eviction")
	}
	if _, ok := s.mem.settledReviews["rev-approved"]; !ok {
		t.Errorf("an approval's messages were evicted inside the window after its deadline")
	}
	approvalDeadline := time.Now().UTC().Add(time.Hour)
	s.mem.items["rev-approve"] = []sentReviewMessage{{channelID: "C1", timestamp: "1.3", sentAt: time.Now().UTC()}}
	if err := s.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: "rev-approve", IsApproved: true,
		ExpiresAt: &approvalDeadline}); err != nil {
		t.Fatalf("tracked approval failed: %v", err)
	}
	if d := s.mem.settledReviews["rev-approve"].deadline; d == nil || !d.Equal(approvalDeadline) {
		t.Errorf("an approval kept deadline %v, want %v", d, approvalDeadline)
	}

	// an expiry is terminal: it consumes the entry, and a late post gets it
	updateCalls = 0
	s.mem.items["rev-exp"] = []sentReviewMessage{{channelID: "C1", timestamp: "1.0", sentAt: time.Now().UTC()}}
	expired := &UpdateReviewMessageRequest{ReviewID: "rev-exp", IsExpired: true}
	if err := s.UpdateReviewMessage(expired); err != nil {
		t.Fatalf("tracked expiry failed: %v", err)
	}
	if updateCalls != 1 || !strings.Contains(lastBlocks, "Review expired.") {
		t.Fatalf("chat.update calls=%d blocks=%s, want one expired rewrite", updateCalls, lastBlocks)
	}
	if s.HasTrackedReviewMessages("rev-exp") {
		t.Errorf("an expiry must consume the tracked entry")
	}
	if final := s.trackSentReviewMessage("rev-exp", sentReviewMessage{channelID: "C4", timestamp: "4.0"}); final != expired {
		t.Errorf("a post after the expiry must get the expired state, got %+v", final)
	}
}

// The fields an approval checks come from users.info. Without users:read.email
// Slack answers the user with no email, and an API error surfaces as-is.
func TestGetUserInfo(t *testing.T) {
	body := `{"ok":true,"user":{"id":"U1","deleted":false,"is_bot":false,"is_restricted":true,"is_ultra_restricted":false,"is_email_confirmed":true,"profile":{"email":"Ana@Example.com"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users.info" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = r.ParseForm()
		if r.FormValue("user") != "U1" {
			t.Errorf("user=%q, want U1", r.FormValue("user"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	s := &SlackService{apiClient: slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/"))}

	got, err := s.GetUserInfo(context.Background(), "U1")
	if err != nil {
		t.Fatalf("GetUserInfo failed: %v", err)
	}
	want := SlackUser{ID: "U1", Email: "Ana@Example.com", IsRestricted: true, IsEmailConfirmed: true}
	if *got != want {
		t.Fatalf("got %+v, want %+v", *got, want)
	}

	body = `{"ok":true,"user":{"id":"U1","profile":{}}}`
	got, err = s.GetUserInfo(context.Background(), "U1")
	if err != nil || got.Email != "" {
		t.Fatalf("missing email scope: got %+v, err %v; want empty email, no error", got, err)
	}

	body = `{"ok":false,"error":"missing_scope"}`
	if _, err := s.GetUserInfo(context.Background(), "U1"); err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("want missing_scope error, got %v", err)
	}
}

func TestReviewChannels(t *testing.T) {
	for _, tt := range []struct {
		name     string
		msg      MessageReviewRequest
		fallback string
		want     []string
	}{
		{"gateway adds the default channel", MessageReviewRequest{SlackChannels: []string{"C1"}}, "CD", []string{"C1", "CD"}},
		{"gateway with no channels", MessageReviewRequest{}, "CD", []string{"CD"}},
		{"no repeat", MessageReviewRequest{SlackChannels: []string{"CD"}}, "CD", []string{"CD"}},
		{"fallback skipped when channels are set", MessageReviewRequest{SlackChannels: []string{"C1"}, DefaultChannelAsFallback: true}, "CD", []string{"C1"}},
		{"fallback used when no channel is set", MessageReviewRequest{DefaultChannelAsFallback: true}, "CD", []string{"CD"}},
		{"no default channel", MessageReviewRequest{SlackChannels: []string{"C1"}}, "", []string{"C1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := reviewChannels(&tt.msg, tt.fallback); !slices.Equal(got, tt.want) {
				t.Errorf("channels = %v, want %v", got, tt.want)
			}
		})
	}
}

// reviewDeadline is a non-UTC time, so the tests pin the UTC rendering.
var reviewDeadline = time.Date(2026, 9, 29, 10, 0, 0, 0, time.FixedZone("x", -3*3600))

func deadlineBlock() *slack.ContextBlock {
	return slack.NewContextBlock(reviewDeadlineBlockID,
		slack.NewTextBlockObject(slack.MarkdownType, "_Decide before Tue, 29 Sep 2026 13:00:00 UTC; after it the review expires._", false, false))
}

// An expired review takes no more input: the buttons and the deadline go, and
// the recorded outcomes stay.
func TestRebuildReviewBlocksExpired(t *testing.T) {
	const revID = "rev-1"
	label := func(group string) *slack.SectionBlock {
		return slack.NewSectionBlock(&slack.TextBlockObject{
			Type: slack.MarkdownType, Text: "*Approver groups:* " + group,
		}, nil, nil)
	}
	deadline := deadlineBlock()
	m := &sentReviewMessage{eventKind: EventKindOneTime, blocks: []slack.Block{
		slack.NewHeaderBlock(&slack.TextBlockObject{Type: slack.PlainTextType, Text: "Hoop Review"}),
		deadline,
		label("admin"),
		slack.NewActionBlock(revID + ":admin:0"),
		label("sre"),
		slack.NewActionBlock(revID + ":sre:1"),
	}}
	rg := ReviewedGroup{Name: "admin", Status: "APPROVED", ReviewerEmail: "a@a.com", ReviewedAt: reviewDeadline}
	reviewed := map[string]ReviewedGroup{"admin": rg}

	partial := &UpdateReviewMessageRequest{ReviewID: revID, ReviewedGroups: []ReviewedGroup{rg}, TotalGroups: 2}
	if blocks := rebuildReviewBlocks(m, partial, reviewed); !slices.ContainsFunc(blocks, func(b slack.Block) bool { return b == deadline }) {
		t.Errorf("a partial approval dropped the deadline: %+v", blocks)
	}

	req := &UpdateReviewMessageRequest{ReviewID: revID, IsExpired: true, ReviewedGroups: []ReviewedGroup{rg}, TotalGroups: 2}
	blocks := rebuildReviewBlocks(m, req, reviewed)
	if len(blocks) != 5 {
		t.Fatalf("got %d blocks, want header, admin label, outcome, divider, expired: %+v", len(blocks), blocks)
	}
	for _, b := range blocks {
		switch b := b.(type) {
		case *slack.ActionBlock:
			t.Errorf("an expired review kept a button: %s", b.BlockID)
		case *slack.ContextBlock:
			t.Errorf("an expired review kept a context block: %s", b.BlockID)
		case *slack.SectionBlock:
			if strings.Contains(b.Text.Text, "sre") {
				t.Errorf("orphaned label for the dropped sre button: %s", b.Text.Text)
			}
		}
	}
	if sec, ok := blocks[2].(*slack.SectionBlock); !ok || !strings.Contains(sec.Text.Text, "a@a.com") {
		t.Errorf("the reviewed group was dropped: %T %+v", blocks[2], blocks[2])
	}
	if _, ok := blocks[3].(*slack.DividerBlock); !ok {
		t.Errorf("block 3 = %T, want a divider", blocks[3])
	}
	if sec, ok := blocks[4].(*slack.SectionBlock); !ok || sec.Text.Text != expiredReviewText {
		t.Errorf("block 4 = %+v, want the expired section", blocks[4])
	}
	if _, ok := m.blocks[1].(*slack.ContextBlock); !ok || len(m.blocks) != 6 {
		t.Errorf("the posted block set was mutated")
	}

	// the expired text wins over a stale approval flag
	req.IsApproved = true
	if blocks := rebuildReviewBlocks(m, req, reviewed); blocks[len(blocks)-1].(*slack.SectionBlock).Text.Text != expiredReviewText {
		t.Errorf("an expired review rendered as approved")
	}
}

// With no deadline the approved message is today's; with one it names it.
func TestRebuildReviewBlocksApprovedShowsTheApprovalDeadline(t *testing.T) {
	header := slack.NewHeaderBlock(&slack.TextBlockObject{Type: slack.PlainTextType, Text: "Hoop Review"})
	rg := ReviewedGroup{Name: "sre", Status: "APPROVED", ReviewerEmail: "a@a.com", ReviewedAt: reviewDeadline}
	reviewed := map[string]ReviewedGroup{"sre": rg}
	ready := func(text string) []slack.Block {
		return []slack.Block{
			header,
			reviewOutcomeSection(rg),
			slack.NewDividerBlock(),
			slack.NewSectionBlock(&slack.TextBlockObject{Type: slack.MarkdownType, Text: text}, nil, nil),
		}
	}

	m := &sentReviewMessage{eventKind: EventKindOneTime, blocks: []slack.Block{header, slack.NewActionBlock("rev-1:sre:0")}}
	req := &UpdateReviewMessageRequest{ReviewID: "rev-1", IsApproved: true, ReviewedGroups: []ReviewedGroup{rg}, TotalGroups: 1}
	if got, want := rebuildReviewBlocks(m, req, reviewed), ready("*Session ready to be executed!*\n"); !reflect.DeepEqual(got, want) {
		t.Errorf("approved without a deadline changed\ngot:  %+v\nwant: %+v", got, want)
	}

	m.blocks = []slack.Block{header, deadlineBlock(), slack.NewActionBlock("rev-1:sre:0")}
	req.ExpiresAt = &reviewDeadline
	want := ready("*Session ready to be executed!*\n_The approval expires at Tue, 29 Sep 2026 13:00:00 UTC if the statement does not run again._")
	if got := rebuildReviewBlocks(m, req, reviewed); !reflect.DeepEqual(got, want) {
		t.Errorf("approved with a deadline\ngot:  %+v\nwant: %+v", got, want)
	}
}

// The decision deadline is one context block right after the metadata; no
// other block changes.
// A posted sidecar review keeps its deadline, so its tracking window counts from it.
func TestPostMessageReviewTracksTheDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"channel":"C1","ts":"1.0"}`)
	}))
	defer srv.Close()
	s := NewWithAPIClient(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")), "T1", "")
	msg := gatewayReviewRequest()
	msg.ExpiresAt = &reviewDeadline
	if res := s.PostMessageReview(msg); res.Posted != 1 {
		t.Fatalf("post result = %+v, want one post", res)
	}
	items := s.mem.items[msg.ID]
	if len(items) != 1 || items[0].deadline == nil || !items[0].deadline.Equal(reviewDeadline) {
		t.Fatalf("tracked %+v, want the deadline %v", items, reviewDeadline)
	}
}

func TestPostMessageReviewShowsTheDeadline(t *testing.T) {
	golden, err := os.ReadFile(gatewayReviewBlocksGolden)
	if err != nil {
		t.Fatal(err)
	}
	var want []json.RawMessage
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	decode := func(raw string) []json.RawMessage {
		var got []json.RawMessage
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	without := decode(postedBlocks(t, gatewayReviewRequest())[0])
	if len(without) != len(want) {
		t.Fatalf("without a deadline: got %d blocks, want %d", len(without), len(want))
	}

	msg := gatewayReviewRequest()
	msg.ExpiresAt = &reviewDeadline
	got := decode(postedBlocks(t, msg)[0])
	if len(got) != len(want)+1 {
		t.Fatalf("got %d blocks, want %d", len(got), len(want)+1)
	}
	var deadline struct {
		Type     string `json:"type"`
		BlockID  string `json:"block_id"`
		Elements []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"elements"`
	}
	if err := json.Unmarshal(got[2], &deadline); err != nil {
		t.Fatal(err)
	}
	if deadline.Type != "context" || deadline.BlockID != reviewDeadlineBlockID || len(deadline.Elements) != 1 ||
		deadline.Elements[0].Type != slack.MarkdownType ||
		deadline.Elements[0].Text != "_Decide before Tue, 29 Sep 2026 13:00:00 UTC; after it the review expires._" {
		t.Errorf("block 2 is not the deadline: %s", got[2])
	}
	rest := append(slices.Clone(got[:2]), got[3:]...)
	for i := range want {
		if !bytes.Equal(compactJSON(t, rest[i]), compactJSON(t, want[i])) {
			t.Errorf("block %d changed\ngot:  %s\nwant: %s", i, rest[i], want[i])
		}
	}
}

// A callback with no block action names no message to rewrite.
func TestUpdateMessageStatusWithoutBlockActions(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"channel":"C1","ts":"1.0"}`)
	}))
	defer srv.Close()
	s := NewWithAPIClient(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")), "T1", "")

	if err := s.UpdateMessageStatus(&MessageReviewResponse{ID: "rev-1"}, expiredReviewText); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if calls != 0 {
		t.Errorf("slack was called %d times, want 0", calls)
	}
}

// A revoke rewrites the message the approval settled: the outcome says
// revoked, no button is left, and the ready line is gone.
func TestRebuildReviewBlocksRevoked(t *testing.T) {
	const revID = "rev-1"
	original := []slack.Block{
		slack.NewHeaderBlock(&slack.TextBlockObject{Type: slack.PlainTextType, Text: "Hoop Review"}),
		slack.NewSectionBlock(&slack.TextBlockObject{Type: slack.MarkdownType, Text: "*Approver groups:* admin"}, nil, nil),
		slack.NewActionBlock(revID + ":admin:0"),
	}
	m := &sentReviewMessage{eventKind: EventKindOneTime, blocks: original}
	revoked := ReviewedGroup{Name: "admin", Status: "REVOKED", ReviewerEmail: "a@a.com",
		ReviewedAt: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)}
	req := &UpdateReviewMessageRequest{ReviewID: revID, IsRevoked: true,
		ReviewedGroups: []ReviewedGroup{revoked}, TotalGroups: 1}
	blocks := rebuildReviewBlocks(m, req, map[string]ReviewedGroup{"admin": revoked})

	var outcome, notice bool
	for _, b := range blocks {
		if _, ok := b.(*slack.ActionBlock); ok {
			t.Errorf("a revoked message must not keep buttons")
		}
		sec, ok := b.(*slack.SectionBlock)
		if !ok || sec.Text == nil {
			continue
		}
		if strings.Contains(sec.Text.Text, "Session ready") {
			t.Errorf("a revoked message must not say the session is ready")
		}
		outcome = outcome || strings.Contains(sec.Text.Text, "`revoked`")
		notice = notice || strings.Contains(sec.Text.Text, "Approval revoked")
	}
	if !outcome || !notice {
		t.Errorf("revoked outcome=%v notice=%v, want both", outcome, notice)
	}
}

// An approval drops the tracked messages but keeps them for a revoke, which
// rewrites them once more. Other terminal states keep nothing.
func TestUpdateReviewMessageRevokeAfterApproval(t *testing.T) {
	var updates int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		updates++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"channel":"C1","ts":"1.0"}`)
	}))
	defer srv.Close()
	s := &SlackService{
		apiClient: slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/"))}
	s.mem.items = make(map[string][]sentReviewMessage)

	s.mem.items["rev-1"] = []sentReviewMessage{{channelID: "C1", timestamp: "1.0"}}
	if err := s.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: "rev-1", IsApproved: true}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := s.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: "rev-1", IsRevoked: true}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if updates != 2 {
		t.Fatalf("chat.update calls = %d, want 2: the approval, then the revoke", updates)
	}
	if final := s.settledReview("rev-1"); final == nil || !final.IsRevoked {
		t.Errorf("settled state = %+v, want the revoke", final)
	}
	if err := s.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: "rev-1", IsRevoked: true}); err != nil || updates != 2 {
		t.Errorf("a second revoke rewrote again: calls=%d err=%v", updates, err)
	}

	s.mem.items["rev-2"] = []sentReviewMessage{{channelID: "C1", timestamp: "2.0"}}
	if err := s.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: "rev-2", IsRejected: true}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if err := s.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: "rev-2", IsRevoked: true}); err != nil || updates != 3 {
		t.Errorf("a revoke after a rejection rewrote a message: calls=%d err=%v", updates, err)
	}
}

// An approval that lapses rewrites the message the approval settled.
func TestUpdateReviewMessageExpiryAfterApproval(t *testing.T) {
	var updates int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		updates++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"channel":"C1","ts":"1.0"}`)
	}))
	defer srv.Close()
	s := &SlackService{
		apiClient: slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/"))}
	s.mem.items = make(map[string][]sentReviewMessage)

	s.mem.items["rev-1"] = []sentReviewMessage{{channelID: "C1", timestamp: "1.0"}}
	if err := s.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: "rev-1", IsApproved: true}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := s.UpdateReviewMessage(&UpdateReviewMessageRequest{ReviewID: "rev-1", IsExpired: true}); err != nil {
		t.Fatalf("expire: %v", err)
	}
	if updates != 2 {
		t.Fatalf("chat.update calls = %d, want 2: the approval, then the expiry", updates)
	}
	if final := s.settledReview("rev-1"); final == nil || !final.IsExpired {
		t.Errorf("settled state = %+v, want the expiry", final)
	}
}

// A service shutting down after a restart removes itself only. Removing by org
// alone dropped the service that replaced it, and Slack went quiet until the
// next restart.
func TestRemoveServiceInstanceIfKeepsTheReplacement(t *testing.T) {
	const org = "org-restart"
	old, replacement := &SlackService{}, &SlackService{}
	SetServiceInstance(org, replacement)
	t.Cleanup(func() { RemoveServiceInstance(org) })

	RemoveServiceInstanceIf(org, old)
	if GetServiceInstance(org) != replacement {
		t.Fatal("the old service removed its replacement")
	}
	RemoveServiceInstanceIf(org, replacement)
	if GetServiceInstance(org) != nil {
		t.Fatal("the current service was not removed")
	}
	(&SlackService{}).Close() // a zero service closes without a panic
}

// A reject submitted on a replica that did not open the modal carries the
// modal submission, with no button in it. The fallback rewrite refuses it
// rather than index an empty action list on the response goroutine.
func TestTheFallbackRewriteRefusesAnInteractionWithoutAButton(t *testing.T) {
	s := &SlackService{}
	msg := &MessageReviewResponse{ID: "rev-1", Status: "rejected"}
	if err := s.UpdateMessage(msg, false); err == nil {
		t.Error("UpdateMessage accepted an interaction with no button")
	}
	if err := s.UpdateMessagePartialApproval(msg, 1, 2); err == nil {
		t.Error("UpdateMessagePartialApproval accepted an interaction with no button")
	}
}
