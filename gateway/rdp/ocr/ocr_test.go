package ocr

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func u32le(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

func TestEncodeBMP_HeaderAndLayout(t *testing.T) {
	// 3x2 image: width 3 -> 9 bytes/row padded to 12.
	w, h := 3, 2
	rgba := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		rgba[i*4] = byte(10 + i)   // R
		rgba[i*4+1] = byte(20 + i) // G
		rgba[i*4+2] = byte(30 + i) // B
		rgba[i*4+3] = 255          // A
	}
	bmp := encodeBMP(rgba, w, h)

	rowSize := 12
	wantSize := bmpHeaderSize + rowSize*h
	if len(bmp) != wantSize {
		t.Fatalf("file size: got %d, want %d", len(bmp), wantSize)
	}
	if bmp[0] != 'B' || bmp[1] != 'M' {
		t.Errorf("magic: got %q", bmp[:2])
	}
	if got := u32le(bmp[2:6]); got != uint32(wantSize) {
		t.Errorf("header file size: got %d, want %d", got, wantSize)
	}
	if got := u32le(bmp[10:14]); got != bmpHeaderSize {
		t.Errorf("data offset: got %d, want %d", got, bmpHeaderSize)
	}
	if got := u32le(bmp[14:18]); got != 40 {
		t.Errorf("info header size: got %d, want 40", got)
	}
	if got := u32le(bmp[18:22]); got != uint32(w) {
		t.Errorf("width: got %d, want %d", got, w)
	}
	if got := u32le(bmp[22:26]); got != uint32(h) {
		t.Errorf("height: got %d, want %d", got, h)
	}
	if bmp[26] != 1 || bmp[28] != 24 {
		t.Errorf("planes/bpp: got %d/%d, want 1/24", bmp[26], bmp[28])
	}

	// Bottom-up: the first stored row is the LAST image row (pixels 3,4,5).
	// BGR order: pixel 3 has R=13,G=23,B=33 -> stored as 33,23,13.
	firstStored := bmp[bmpHeaderSize : bmpHeaderSize+3]
	if !bytes.Equal(firstStored, []byte{33, 23, 13}) {
		t.Errorf("bottom-up BGR: got %v, want [33 23 13]", firstStored)
	}
	// Second stored row starts at rowSize offset and is image row 0 (pixel 0).
	secondStored := bmp[bmpHeaderSize+rowSize : bmpHeaderSize+rowSize+3]
	if !bytes.Equal(secondStored, []byte{30, 20, 10}) {
		t.Errorf("row order: got %v, want [30 20 10]", secondStored)
	}
}

func TestEncodeBMP_RowPadding(t *testing.T) {
	// Widths with different padding requirements: bytes/row = w*3 padded to 4.
	cases := []struct{ w, wantRow int }{
		{1, 4}, {2, 8}, {3, 12}, {4, 12}, {5, 16},
	}
	for _, tc := range cases {
		rgba := make([]byte, tc.w*1*4)
		bmp := encodeBMP(rgba, tc.w, 1)
		if got := len(bmp) - bmpHeaderSize; got != tc.wantRow {
			t.Errorf("width %d: row size got %d, want %d", tc.w, got, tc.wantRow)
		}
	}
}

func TestEncodeBMP_PaddingBytesAreZero(t *testing.T) {
	// width 1 -> 3 data bytes + 1 padding byte per row.
	rgba := []byte{255, 255, 255, 255, 255, 255, 255, 255} // 1x2 white
	bmp := encodeBMP(rgba, 1, 2)
	rowSize := 4
	for row := 0; row < 2; row++ {
		pad := bmp[bmpHeaderSize+row*rowSize+3]
		if pad != 0 {
			t.Errorf("row %d padding byte: got %d, want 0", row, pad)
		}
	}
}

func TestExtractWords_RejectsInvalidDimensions(t *testing.T) {
	ctx := t.Context()
	rgba := make([]byte, 16)
	for _, tc := range []struct{ w, h int }{{0, 2}, {2, 0}, {-1, 2}, {2, -1}} {
		if _, err := ExtractWords(ctx, rgba, tc.w, tc.h); err == nil {
			t.Errorf("dimensions %dx%d: expected error, got nil", tc.w, tc.h)
		}
	}
}

func TestParseTSV_MalformedNumericFields(t *testing.T) {
	tsv := "level\tpage\tblock\tpar\tline\tword\tleft\ttop\twidth\theight\tconf\ttext\n" +
		"x\t1\t1\t1\t1\t1\t1\t1\t1\t1\t90\tbadlevel\n" + // non-numeric level
		"5\t1\t1\t1\t1\t1\t1\t1\t1\t1\tnotanum\tbadconf\n" + // non-numeric conf -> 0, kept
		"5\t1\t1\t1\t1\t1\tx\ty\tz\tw\t90\tbadcoords\n" + // non-numeric coords -> 0
		"5\t1\t1\t1\t1\t1\n" // too few fields

	words := parseTSV(tsv, false)
	// badlevel and short rows are dropped; badconf (conf parses to 0, >= 0)
	// and badcoords (coords parse to 0) are kept with zeroed values.
	if len(words) != 2 {
		t.Fatalf("words: got %d (%+v), want 2", len(words), words)
	}
	for _, w := range words {
		if w.Left != 0 && w.Text == "badcoords" {
			t.Errorf("badcoords word should have zeroed coords, got %+v", w)
		}
	}
}

func TestNearestNeighbor2xRGBA(t *testing.T) {
	// 2x1 image with distinct pixels.
	src := []byte{
		1, 2, 3, 4, // pixel A
		5, 6, 7, 8, // pixel B
	}
	dst := nearestNeighbor2xRGBA(src, 2, 1)
	if len(dst) != 2*1*4*4 {
		t.Fatalf("dst length: got %d, want %d", len(dst), 2*1*4*4)
	}
	wantRow := []byte{
		1, 2, 3, 4, 1, 2, 3, 4, // A A
		5, 6, 7, 8, 5, 6, 7, 8, // B B
	}
	row1 := dst[:16]
	row2 := dst[16:]
	if !bytes.Equal(row1, wantRow) {
		t.Errorf("row1: got %v, want %v", row1, wantRow)
	}
	if !bytes.Equal(row2, wantRow) {
		t.Errorf("row2 (duplicated): got %v, want %v", row2, wantRow)
	}
}

func TestParseTSV_SkipsInvalidAndHalvesUpscaled(t *testing.T) {
	tsv := "level\tpage\tblock\tpar\tline\tword\tleft\ttop\twidth\theight\tconf\ttext\n" +
		"5\t1\t1\t1\t1\t1\t100\t200\t50\t20\t91.5\thello\n" + // valid
		"4\t1\t1\t1\t1\t0\t0\t0\t10\t10\t-1\t\n" + // not level 5
		"5\t1\t1\t1\t1\t2\t10\t10\t10\t10\t-1\tx\n" + // negative conf
		"5\t1\t1\t1\t1\t3\t10\t10\t10\t10\t80\t \n" // blank text

	words := parseTSV(tsv, true) // upscaled: coords halved
	if len(words) != 1 {
		t.Fatalf("words: got %d, want 1", len(words))
	}
	w := words[0]
	if w.Text != "hello" || w.Left != 50 || w.Top != 100 || w.Width != 25 || w.Height != 10 {
		t.Errorf("word: got %+v, want hello at 50,100 25x10", w)
	}
}
