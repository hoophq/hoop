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
	"sync"
	"testing"
	"time"

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

func compactJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
