package analyzer

import (
	"context"
	"reflect"
	"testing"

	"github.com/hoophq/hoop/gateway/rdp/ocr"
)

func TestDirtyBands_AddRect_PaddingAndClamping(t *testing.T) {
	d := NewDirtyBands(1000, 10)

	// Rect near the top: padding clamps at 0.
	d.AddRect(5, 20)
	want := []YBand{{Y0: 0, Y1: 35}}
	if got := d.Bands(); !reflect.DeepEqual(got, want) {
		t.Errorf("top clamp: got %v, want %v", got, want)
	}

	// Rect near the bottom: padding clamps at canvas height.
	d.Reset()
	d.AddRect(985, 20)
	want = []YBand{{Y0: 975, Y1: 1000}}
	if got := d.Bands(); !reflect.DeepEqual(got, want) {
		t.Errorf("bottom clamp: got %v, want %v", got, want)
	}
}

func TestDirtyBands_MergeOverlappingAndAdjacent(t *testing.T) {
	d := NewDirtyBands(1000, 5)

	d.AddRect(100, 50) // [95,155)
	d.AddRect(140, 50) // [135,195) overlaps -> [95,195)
	d.AddRect(195, 10) // [190,210) overlaps via pad -> [95,210)
	want := []YBand{{Y0: 95, Y1: 210}}
	if got := d.Bands(); !reflect.DeepEqual(got, want) {
		t.Errorf("merge: got %v, want %v", got, want)
	}
}

func TestDirtyBands_DisjointBandsStaySeparate(t *testing.T) {
	d := NewDirtyBands(2000, 10)

	d.AddRect(100, 20)  // [90,130)
	d.AddRect(1500, 20) // [1490,1530) — e.g. taskbar clock far away
	want := []YBand{{Y0: 90, Y1: 130}, {Y0: 1490, Y1: 1530}}
	if got := d.Bands(); !reflect.DeepEqual(got, want) {
		t.Errorf("disjoint: got %v, want %v", got, want)
	}
	if d.CoveredRows() != 80 {
		t.Errorf("CoveredRows: got %d, want 80", d.CoveredRows())
	}
}

func TestDirtyBands_OutOfOrderInsertMerges(t *testing.T) {
	d := NewDirtyBands(1000, 5)

	d.AddRect(500, 50)  // [495,555)
	d.AddRect(100, 50)  // [95,155) inserted before
	d.AddRect(140, 400) // [135,545) bridges both -> [95,555)
	want := []YBand{{Y0: 95, Y1: 555}}
	if got := d.Bands(); !reflect.DeepEqual(got, want) {
		t.Errorf("bridge merge: got %v, want %v", got, want)
	}
}

func TestDirtyBands_IgnoresInvalidRects(t *testing.T) {
	d := NewDirtyBands(1000, 10)

	d.AddRect(100, 0)   // zero height
	d.AddRect(100, -5)  // negative height
	d.AddRect(1200, 50) // entirely below the canvas
	d.AddRect(-100, 50) // mostly above: [0, -50+50+10=0+...] -> clamp check
	if !d.Empty() && len(d.Bands()) > 1 {
		t.Errorf("expected at most the clamped band, got %v", d.Bands())
	}
	for _, b := range d.Bands() {
		if b.Y0 < 0 || b.Y1 > 1000 || b.Y0 >= b.Y1 {
			t.Errorf("invalid band emitted: %v", b)
		}
	}
}

func TestDirtyBands_ResetAndEmpty(t *testing.T) {
	d := NewDirtyBands(1000, 10)
	if !d.Empty() {
		t.Errorf("new accumulator should be empty")
	}
	d.AddRect(10, 10)
	if d.Empty() {
		t.Errorf("accumulator should not be empty after AddRect")
	}
	d.Reset()
	if !d.Empty() {
		t.Errorf("accumulator should be empty after Reset")
	}
}

func TestDirtyBands_NonPositivePadFallsBackToDefault(t *testing.T) {
	for _, pad := range []int{-1, 0} {
		d := NewDirtyBands(1000, pad)
		d.AddRect(500, 10)
		want := []YBand{{Y0: 500 - DefaultBandPadding, Y1: 510 + DefaultBandPadding}}
		if got := d.Bands(); !reflect.DeepEqual(got, want) {
			t.Errorf("pad=%d: got %v, want %v", pad, got, want)
		}
	}
}

func TestDirtyBands_TakeAndReset(t *testing.T) {
	d := NewDirtyBands(1000, 10)
	if got := d.TakeAndReset(); got != nil {
		t.Errorf("empty accumulator: want nil, got %v", got)
	}

	d.AddRect(100, 20)
	taken := d.TakeAndReset()
	want := []YBand{{Y0: 90, Y1: 130}}
	if !reflect.DeepEqual(taken, want) {
		t.Errorf("taken: got %v, want %v", taken, want)
	}
	if !d.Empty() {
		t.Errorf("accumulator must be empty after TakeAndReset")
	}

	// The returned slice must be an owned copy: mutating the accumulator
	// afterwards must not affect it.
	d.AddRect(500, 20)
	if !reflect.DeepEqual(taken, want) {
		t.Errorf("taken slice aliased accumulator memory: got %v", taken)
	}
}

// TestSplitBands_SeamLineFullyVisibleToOwner verifies the seam guarantee: a
// text line whose words' centers fall in one chunk's ownership is always
// fully visible inside that chunk's OCR window, as long as the line height
// is at most 2*pad.
func TestSplitBands_SeamLineFullyVisibleToOwner(t *testing.T) {
	const pad = 24
	bands := []YBand{{Y0: 0, Y1: 1000}}
	chunks := splitBands(bands, 256, pad)

	// A line of height 2*pad straddling the first seam (row 256).
	line := ocr.Word{Top: 256 - pad, Height: 2 * pad} // rows [232, 280), center 256

	for _, c := range chunks {
		if !c.ownsWord(line) {
			continue
		}
		if line.Top < c.win.Y0 || line.Top+line.Height > c.win.Y1 {
			t.Errorf("owning chunk window %+v does not fully contain line [%d,%d)",
				c.win, line.Top, line.Top+line.Height)
		}
	}
}

func TestSplitBands_ShortBandSingleChunk(t *testing.T) {
	bands := []YBand{{Y0: 100, Y1: 300}} // 200 rows <= 256+24
	chunks := splitBands(bands, 256, 24)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].win != bands[0] || chunks[0].own != bands[0] {
		t.Errorf("short band must be a single identity chunk, got %+v", chunks[0])
	}
}

func TestSplitBands_TallBandChunksWithOverlap(t *testing.T) {
	bands := []YBand{{Y0: 0, Y1: 1000}}
	chunks := splitBands(bands, 256, 24)

	// Ownership must tile the band exactly: contiguous, no gaps, no overlap.
	if chunks[0].own.Y0 != 0 {
		t.Errorf("first chunk ownership must start at band start, got %d", chunks[0].own.Y0)
	}
	if chunks[len(chunks)-1].own.Y1 != 1000 {
		t.Errorf("last chunk ownership must end at band end, got %d", chunks[len(chunks)-1].own.Y1)
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i].own.Y0 != chunks[i-1].own.Y1 {
			t.Errorf("ownership gap/overlap between chunk %d and %d: %+v %+v",
				i-1, i, chunks[i-1].own, chunks[i].own)
		}
	}

	// Windows must cover ownership expanded by pad, clamped to the band.
	for i, c := range chunks {
		wantY0 := c.own.Y0 - 24
		if wantY0 < 0 {
			wantY0 = 0
		}
		wantY1 := c.own.Y1 + 24
		if wantY1 > 1000 {
			wantY1 = 1000
		}
		if c.win.Y0 != wantY0 || c.win.Y1 != wantY1 {
			t.Errorf("chunk %d window: got %+v, want [%d,%d)", i, c.win, wantY0, wantY1)
		}
	}
}

func TestOcrChunk_OwnsWord(t *testing.T) {
	c := ocrChunk{win: YBand{Y0: 76, Y1: 224}, own: YBand{Y0: 100, Y1: 200}}

	cases := []struct {
		name string
		word ocr.Word
		want bool
	}{
		{"center inside", ocr.Word{Top: 150, Height: 16}, true},
		{"center above", ocr.Word{Top: 80, Height: 16}, false},
		{"center below", ocr.Word{Top: 195, Height: 16}, false}, // center 203
		{"center exactly at Y0", ocr.Word{Top: 92, Height: 16}, true},
		{"center exactly at Y1", ocr.Word{Top: 192, Height: 16}, false},
	}
	for _, tc := range cases {
		if got := c.ownsWord(tc.word); got != tc.want {
			t.Errorf("%s: ownsWord(Top=%d,H=%d) = %v, want %v",
				tc.name, tc.word.Top, tc.word.Height, got, tc.want)
		}
	}
}

func TestSplitBands_OwnershipCoversEveryRowExactlyOnce(t *testing.T) {
	bands := []YBand{{Y0: 50, Y1: 700}, {Y0: 900, Y1: 1531}}
	chunks := splitBands(bands, 256, 24)

	covered := map[int]int{}
	for _, c := range chunks {
		for y := c.own.Y0; y < c.own.Y1; y++ {
			covered[y]++
		}
	}
	for _, b := range bands {
		for y := b.Y0; y < b.Y1; y++ {
			if covered[y] != 1 {
				t.Fatalf("row %d covered %d times, want exactly 1", y, covered[y])
			}
		}
	}
	total := 0
	for _, b := range bands {
		total += b.Height()
	}
	if len(covered) != total {
		t.Errorf("ownership covers %d rows, want %d", len(covered), total)
	}
}

func TestAnalyzeFramebufferBands_RejectsInvalidInputs(t *testing.T) {
	ctx := context.Background()
	params := AnalysisParams{ScoreThreshold: 0.9, BandPadding: DefaultBandPadding}
	validFB := make([]byte, 100*100*4)
	validBands := []YBand{{Y0: 0, Y1: 10}}

	cases := []struct {
		name  string
		fb    []byte
		w, h  int
		bands []YBand
	}{
		{"zero width", validFB, 0, 100, validBands},
		{"negative width", validFB, -1, 100, validBands},
		{"zero height", validFB, 100, 0, validBands},
		{"short framebuffer", make([]byte, 10), 100, 100, validBands},
		{"band negative Y0", validFB, 100, 100, []YBand{{Y0: -1, Y1: 10}}},
		{"band beyond height", validFB, 100, 100, []YBand{{Y0: 0, Y1: 101}}},
		{"band inverted", validFB, 100, 100, []YBand{{Y0: 50, Y1: 50}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Validation must fail before any OCR/Presidio call, so a nil
			// Presidio client and no tesseract are fine here — a panic or
			// a nil error means the validation regressed.
			_, err := AnalyzeFramebufferBands(ctx, tc.fb, tc.w, tc.h, tc.bands,
				"sid", 0, 0, nil, params)
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
		})
	}
}

func TestOffsetWords(t *testing.T) {
	words := []ocr.Word{
		{Text: "a", Left: 10, Top: 5, Width: 20, Height: 15},
		{Text: "b", Left: 40, Top: 8, Width: 20, Height: 15},
	}
	offsetWords(words, 100)
	if words[0].Top != 105 || words[1].Top != 108 {
		t.Errorf("offsetWords: got tops %d,%d, want 105,108", words[0].Top, words[1].Top)
	}
	if words[0].Left != 10 || words[1].Left != 40 {
		t.Errorf("offsetWords must not change Left: got %d,%d", words[0].Left, words[1].Left)
	}
}
