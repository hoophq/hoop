package rle

import (
	"bytes"
	"testing"
)

// TestDecodeCode verifies the header-to-order-code extraction.
func TestDecodeCode(t *testing.T) {
	tests := []struct {
		header byte
		want   byte
	}{
		// Regular codes: top 2 bits != 11, shift >> 5
		{0x00, regularBGRun},  // 0x00 >> 5 = 0
		{0x20, regularFGRun},  // 0x20 >> 5 = 1
		{0x40, regularFGBG},   // 0x40 >> 5 = 2
		{0x60, regularColor},  // 0x60 >> 5 = 3
		{0x80, regularCImage}, // 0x80 >> 5 = 4
		// Lite codes: top 2 bits = 11 but top 4 != 1111, shift >> 4
		{0xC0, liteSetFGRun}, // 0xC0 >> 4 = 0x0C
		{0xD0, liteSetFGBG},  // 0xD0 >> 4 = 0x0D
		{0xE0, liteDithered}, // 0xE0 >> 4 = 0x0E
		// Mega-mega / special: top 4 bits = 1111, return as-is
		{0xF0, megaMegaBGRun},
		{0xF1, megaMegaFGRun},
		{0xF9, specialFGBG1},
		{0xFA, specialFGBG2},
		{0xFD, specialWhite},
		{0xFE, specialBlack},
	}

	for _, tt := range tests {
		got := decodeCode(tt.header)
		if got != tt.want {
			t.Errorf("decodeCode(0x%02X) = 0x%02X, want 0x%02X", tt.header, got, tt.want)
		}
	}
}

// TestDecompressEmptySource verifies error on empty input.
func TestDecompressEmptySource(t *testing.T) {
	_, err := Decompress(nil, 10, 10, 16)
	if err == nil {
		t.Error("expected error for nil source, got nil")
	}
	_, err = Decompress([]byte{}, 10, 10, 16)
	if err == nil {
		t.Error("expected error for empty source, got nil")
	}
}

// TestDecompressSpecialBlack verifies the special black pixel code (0xFE).
// A single 0xFE byte should produce one black pixel.
func TestDecompressSpecialBlack(t *testing.T) {
	// 16bpp: black = 0x0000 (2 bytes)
	src := []byte{specialBlack}
	dst, err := Decompress(src, 1, 1, 16)
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte{0x00, 0x00}
	if !bytes.Equal(dst, expected) {
		t.Errorf("Decompress(specialBlack, 1x1, 16bpp) = %v, want %v", dst, expected)
	}
}

// TestDecompressSpecialWhite verifies the special white pixel code (0xFD).
func TestDecompressSpecialWhite(t *testing.T) {
	// 16bpp: white = 0xFFFF (2 bytes LE)
	src := []byte{specialWhite}
	dst, err := Decompress(src, 1, 1, 16)
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte{0xFF, 0xFF}
	if !bytes.Equal(dst, expected) {
		t.Errorf("Decompress(specialWhite, 1x1, 16bpp) = %v, want %v", dst, expected)
	}
}

// TestDecompressColorRun verifies a regular color run.
func TestDecompressColorRun(t *testing.T) {
	// Regular Color Run: header = 0x60 | runLength (1-31)
	// Let's make a run of 3 pixels with color 0x1234 (16bpp)
	// Header: 0x60 | 3 = 0x63
	// Then the pixel: 0x34, 0x12 (little-endian)
	src := []byte{0x63, 0x34, 0x12}
	dst, err := Decompress(src, 3, 1, 16)
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte{0x34, 0x12, 0x34, 0x12, 0x34, 0x12}
	if !bytes.Equal(dst, expected) {
		t.Errorf("Decompress(colorRun) = %v, want %v", dst, expected)
	}
}

// TestDecompressBGRun verifies a background run on the first line (black pixels).
func TestDecompressBGRun(t *testing.T) {
	// Regular BG Run on first line: should produce black pixels
	// Header: 0x00 | runLength = 0x04 (4 pixels)
	src := []byte{0x04}
	dst, err := Decompress(src, 4, 1, 16)
	if err != nil {
		t.Fatal(err)
	}
	expected := make([]byte, 8) // 4 pixels * 2 bytes = 8 bytes, all zeros
	if !bytes.Equal(dst, expected) {
		t.Errorf("Decompress(bgRun) = %v, want %v", dst, expected)
	}
}

// TestToRGBA16bpp verifies RGB565 to RGBA conversion with bottom-up flip.
func TestToRGBA16bpp(t *testing.T) {
	// 1x2 image, 16bpp
	// Row 0 (bottom in RDP) = pure red: RGB565 = 0xF800 -> LE: 0x00, 0xF8
	// Row 1 (top in RDP)    = pure blue: RGB565 = 0x001F -> LE: 0x1F, 0x00
	src := []byte{
		0x00, 0xF8, // row 0 (bottom on screen)
		0x1F, 0x00, // row 1 (top on screen)
	}

	rgba, err := ToRGBA(src, 1, 2, 16)
	if err != nil {
		t.Fatal(err)
	}

	// After flip: row 0 on screen (top) = blue, row 1 on screen (bottom) = red
	// Blue: RGB565 0x001F -> R=0, G=0, B=0x1F -> expand: R=0, G=0, B=(0x1F<<3|0x1F>>2)=0xFF
	// Red: RGB565 0xF800 -> R=0x1F, G=0, B=0 -> expand: R=0xFF, G=0, B=0
	expectedTop := []byte{0, 0, 255, 255} // blue pixel (was row 1 in data)
	expectedBot := []byte{255, 0, 0, 255} // red pixel (was row 0 in data)
	expected := append(expectedTop, expectedBot...)

	if !bytes.Equal(rgba, expected) {
		t.Errorf("ToRGBA 16bpp:\ngot:  %v\nwant: %v", rgba, expected)
	}
}

// TestToRGBA24bpp verifies BGR->RGB conversion with bottom-up flip.
func TestToRGBA24bpp(t *testing.T) {
	// 1x1 image, 24bpp BGR
	// BGR = (0x11, 0x22, 0x33) -> RGB = (0x33, 0x22, 0x11)
	src := []byte{0x11, 0x22, 0x33}
	rgba, err := ToRGBA(src, 1, 1, 24)
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte{0x33, 0x22, 0x11, 0xFF}
	if !bytes.Equal(rgba, expected) {
		t.Errorf("ToRGBA 24bpp = %v, want %v", rgba, expected)
	}
}

// TestToRGBA32bpp verifies BGRX->RGBA conversion.
func TestToRGBA32bpp(t *testing.T) {
	// 1x1 image, 32bpp BGRX
	// BGRX = (0xAA, 0xBB, 0xCC, 0x00) -> RGBA = (0xCC, 0xBB, 0xAA, 0xFF)
	src := []byte{0xAA, 0xBB, 0xCC, 0x00}
	rgba, err := ToRGBA(src, 1, 1, 32)
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte{0xCC, 0xBB, 0xAA, 0xFF}
	if !bytes.Equal(rgba, expected) {
		t.Errorf("ToRGBA 32bpp = %v, want %v", rgba, expected)
	}
}

// TestDecompressToRGBA verifies the convenience function end-to-end.
func TestDecompressToRGBA(t *testing.T) {
	// Use a color run of 2 pixels, 16bpp
	// Color = 0x0000 (black), run length = 2
	src := []byte{0x62, 0x00, 0x00} // regularColor | 2, pixel 0x0000
	rgba, err := DecompressToRGBA(src, 2, 1, 16)
	if err != nil {
		t.Fatal(err)
	}
	// 2 black pixels in RGBA
	expected := []byte{0, 0, 0, 255, 0, 0, 0, 255}
	if !bytes.Equal(rgba, expected) {
		t.Errorf("DecompressToRGBA = %v, want %v", rgba, expected)
	}
}

// TestColorImageRun verifies color image (raw pixel copy).
func TestColorImageRun(t *testing.T) {
	// Regular Color Image: header = 0x80 | runLength
	// 2 pixels of 16bpp raw data
	// Header: 0x80 | 2 = 0x82
	src := []byte{0x82, 0xAB, 0xCD, 0xEF, 0x01}
	dst, err := Decompress(src, 2, 1, 16)
	if err != nil {
		t.Fatal(err)
	}
	expected := []byte{0xAB, 0xCD, 0xEF, 0x01}
	if !bytes.Equal(dst[:4], expected) {
		t.Errorf("Decompress(colorImage) = %v, want %v", dst[:4], expected)
	}
}
