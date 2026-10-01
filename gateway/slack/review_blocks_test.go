package slack

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

var updateGolden = flag.Bool("update", false, "rewrite the testdata golden files")

const gatewayReviewBlocksGolden = "testdata/gateway_review_blocks.json"

// gatewayReviewRequest is shaped as the gateway slack plugin builds it.
func gatewayReviewRequest() *MessageReviewRequest {
	d := 90 * time.Minute
	return &MessageReviewRequest{
		ID:             "rev-1",
		Name:           "Ana",
		Email:          "ana@example.com",
		UserGroups:     []string{"admin", "sre"},
		ApprovalGroups: []string{"sre", "dba"},
		Connection:     "pg-prod",
		ConnectionType: "database",
		Script:         "SELECT 1 & <x>",
		SessionTime:    &d,
		WebappURL:      "https://hoop.example/sessions/sid-1",
		SessionID:      "sid-1",
		SlackChannels:  []string{"C1"},
		AIRiskLevel:    "high",
		AITitle:        "Drops <!channel>",
		AISummary:      "It reads one row & exits.",
	}
}

// postedBlocks posts msg to a fake Slack and returns the blocks form value
// of each chat.postMessage call.
func postedBlocks(t *testing.T, msg *MessageReviewRequest) []string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.postMessage" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = r.ParseForm()
		mu.Lock()
		got = append(got, r.FormValue("blocks"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"channel":"C1","ts":"1.0"}`)
	}))
	defer srv.Close()
	s := NewWithAPIClient(slack.New("xoxb-test", slack.OptionAPIURL(srv.URL+"/")), "T1", "")
	res := s.PostMessageReview(msg)
	if res.Posted != 1 || len(res.Errors) != 0 {
		t.Fatalf("post result = %+v, want one post and no errors", res)
	}
	mu.Lock()
	defer mu.Unlock()
	return got
}

func indentJSON(t *testing.T, raw string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		t.Fatalf("blocks are not JSON: %v\n%s", err, raw)
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}

// The golden pins the blocks a gateway review posts. Fields only a sidecar
// review sets must not change them.
func TestPostMessageReviewGatewayBlocksUnchanged(t *testing.T) {
	posted := postedBlocks(t, gatewayReviewRequest())
	if len(posted) != 1 {
		t.Fatalf("posted %d messages, want 1", len(posted))
	}
	got := indentJSON(t, posted[0])
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(gatewayReviewBlocksGolden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(gatewayReviewBlocksGolden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(gatewayReviewBlocksGolden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("gateway review blocks changed\ngot:\n%s\nwant:\n%s", got, want)
	}
}

const databaseUserSource = "Login name the client claimed before authenticating, not checked"

func TestFiledByBlockIsAbsentWithoutAFiler(t *testing.T) {
	if b := filedByBlock(gatewayReviewRequest()); b != nil {
		t.Fatalf("a request with no filer got a block: %+v", b)
	}
}

// A long caller value must not push the fixed text out of the section.
func TestFiledByBlockPutsTheSourceFirst(t *testing.T) {
	msg := gatewayReviewRequest()
	msg.FiledBy = &ReviewFiler{
		Source:   databaseUserSource,
		Subject:  strings.Repeat("&", 255),
		Email:    strings.Repeat("&", 255) + "@x",
		PeerAddr: strings.Repeat("&", 255),
	}
	b := filedByBlock(msg)
	if b == nil || b.Text == nil {
		t.Fatal("a filer must produce a block")
	}
	text := b.Text.Text
	if b.Text.Type != slack.MarkdownType {
		t.Errorf("text type = %q, want mrkdwn", b.Text.Type)
	}
	if !strings.HasPrefix(text, "*Filed by* — Login name the client claimed") {
		t.Errorf("text does not start with the source: %q", text)
	}
	note := strings.Index(text, sharedApprovalNote)
	value := strings.Index(text, "`")
	if note < 0 || value < 0 || note > value {
		t.Errorf("the shared-approval note must come before any value, note=%d value=%d", note, value)
	}
	if n := utf8.RuneCountInString(text); n >= 3000 {
		t.Errorf("section text = %d chars, want < 3000", n)
	}
	lines := strings.Split(text, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), text)
	}
	for _, v := range strings.Split(lines[2], " · ") {
		v = strings.TrimPrefix(v, "from ")
		if len(v) > maxFilerValueSize+2 {
			t.Errorf("value = %d bytes, want <= %d plus the backticks", len(v), maxFilerValueSize)
		}
	}
}

// Caller-chosen values are mrkdwn-inert and cannot imitate the plane's text.
func TestFiledByBlockDisarmsTheValues(t *testing.T) {
	msg := gatewayReviewRequest()
	msg.FiledBy = &ReviewFiler{
		Source:  databaseUserSource,
		Subject: "*x* :white_check_mark: `y` <!channel> <https://x|y>",
	}
	text := filedByBlock(msg).Text.Text
	lines := strings.Split(text, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %q", len(lines), text)
	}
	want := "`*x* :white_check_mark: 'y' &lt;!channel&gt; &lt;https://x|y&gt;`"
	if lines[2] != want {
		t.Errorf("value line = %q, want %q", lines[2], want)
	}
	for _, raw := range []string{"<!channel>", "<https://", "`y`"} {
		if strings.Contains(text, raw) {
			t.Errorf("%q survived: %q", raw, text)
		}
	}

	msg.FiledBy = &ReviewFiler{
		Source:  databaseUserSource,
		Subject: "Google token holder, checked with Google\n*Filed by* — x",
		Email:   "a@b",
	}
	text = filedByBlock(msg).Text.Text
	if i := strings.Index(text, "Google token holder"); i < strings.Index(text, databaseUserSource) {
		t.Errorf("a spoofed source must come after the real one: %q", text)
	}
	if n := strings.Count(text, "\n"); n != 2 {
		t.Errorf("a value must not add lines, got %d newlines: %q", n, text)
	}
	if !strings.HasSuffix(text, "`Google token holder, checked with Google *Filed by* — x` · `a@b`") {
		t.Errorf("values line = %q", text)
	}
}

// A filer adds one section at index 2 and changes no other block.
func TestPostMessageReviewAddsOnlyTheFiler(t *testing.T) {
	msg := gatewayReviewRequest()
	msg.FiledBy = &ReviewFiler{Source: databaseUserSource, Subject: "alice", PeerAddr: "10.0.0.1:5432"}
	posted := postedBlocks(t, msg)
	if len(posted) != 1 {
		t.Fatalf("posted %d messages, want 1", len(posted))
	}
	var got, want []json.RawMessage
	if err := json.Unmarshal([]byte(posted[0]), &got); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(gatewayReviewBlocksGolden)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want)+1 {
		t.Fatalf("got %d blocks, want %d", len(got), len(want)+1)
	}
	var filer struct {
		Type string `json:"type"`
		Text struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"text"`
	}
	if err := json.Unmarshal(got[2], &filer); err != nil {
		t.Fatal(err)
	}
	if filer.Type != "section" || !strings.HasPrefix(filer.Text.Text, "*Filed by* — "+databaseUserSource) {
		t.Errorf("block 2 is not the filer: %s", got[2])
	}
	if !strings.HasSuffix(filer.Text.Text, "`alice` · from `10.0.0.1:5432`") {
		t.Errorf("filer values = %q", filer.Text.Text)
	}
	rest := append(slices.Clone(got[:2]), got[3:]...)
	for i := range want {
		if !bytes.Equal(compactJSON(t, rest[i]), compactJSON(t, want[i])) {
			t.Errorf("block %d changed\ngot:  %s\nwant: %s", i, rest[i], want[i])
		}
	}
}

func compactJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The filer section is not an action block, so a decision keeps it.
func TestRebuildReviewBlocksKeepsTheFiler(t *testing.T) {
	msg := gatewayReviewRequest()
	msg.FiledBy = &ReviewFiler{Source: databaseUserSource, Subject: "alice"}
	filer := filedByBlock(msg)
	m := &sentReviewMessage{eventKind: EventKindOneTime, blocks: []slack.Block{
		slack.NewHeaderBlock(&slack.TextBlockObject{Type: slack.PlainTextType, Text: "Hoop Review"}),
		filer,
		slack.NewSectionBlock(&slack.TextBlockObject{Type: slack.MarkdownType, Text: "*Approver groups:* sre"}, nil, nil),
		slack.NewActionBlock("rev-1:sre:0"),
	}}
	rg := ReviewedGroup{Name: "sre", Status: "APPROVED", ReviewerEmail: "a@a.com", ReviewedAt: time.Now().UTC()}
	req := &UpdateReviewMessageRequest{ReviewID: "rev-1", IsApproved: true, ReviewedGroups: []ReviewedGroup{rg}, TotalGroups: 1}
	blocks := rebuildReviewBlocks(m, req, map[string]ReviewedGroup{"sre": rg})
	if !slices.ContainsFunc(blocks, func(b slack.Block) bool { return b == filer }) {
		t.Fatalf("the filer section was dropped: %+v", blocks)
	}
}
