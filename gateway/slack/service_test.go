package slack

import (
	"strings"
	"testing"
	"unicode/utf8"
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
