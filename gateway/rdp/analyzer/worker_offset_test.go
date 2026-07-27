package analyzer

import (
	"testing"

	"github.com/hoophq/hoop/gateway/rdp/ocr"
)

// Presidio reports entity start/end as Unicode code-point (char) indices, so
// buildWordRanges must count runes, not bytes. A multi-byte char before the PII
// word (here the smart apostrophe U+2019 in "I'm", 3 bytes / 1 rune) would, with
// the old byte-offset logic, shift every later range and map the entity onto the
// wrong word — redacting the wrong line.
func TestBuildWordRangesUsesRuneOffsets(t *testing.T) {
	words := []ocr.Word{
		{Text: "I\u2019m", Left: 0, Top: 0, Width: 30, Height: 12}, // 3 runes, 5 bytes
		{Text: "secret", Left: 40, Top: 0, Width: 50, Height: 12},
	}
	ranges := buildWordRanges(words)
	// Joined "I’m secret": rune offsets I’m=[0,3), secret=[4,10).
	if ranges[0].start != 0 || ranges[0].end != 3 {
		t.Fatalf("first word range = [%d,%d), want [0,3) (runes, not 5 bytes)", ranges[0].start, ranges[0].end)
	}
	if ranges[1].start != 4 || ranges[1].end != 10 {
		t.Fatalf("second word range = [%d,%d), want [4,10)", ranges[1].start, ranges[1].end)
	}

	// Presidio flags "secret" at rune [4,10): must map to the second word's box.
	entity := AnalyzerResult{Start: 4, End: 10, Score: 0.99, EntityType: "EMAIL_ADDRESS"}
	bbox := mapEntityToPixelBBox(entity, ranges)
	if bbox == nil {
		t.Fatal("entity did not map to any word")
	}
	if bbox.x != 40 || bbox.y != 0 || bbox.w != 50 || bbox.h != 12 {
		t.Fatalf("bbox = %+v, want {x:40 y:0 w:50 h:12} (the 'secret' word)", *bbox)
	}
}
