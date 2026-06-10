// Package ocr provides text extraction from bitmap images using Tesseract OCR.
//
// Tesseract is invoked as a subprocess to avoid CGO/libtesseract-dev build dependencies.
// Images are encoded as uncompressed 24bpp BMP and written to a temp file
// (NOT stdin: leptonica's in-memory/stdin BMP reader measured ~7x slower
// than its file reader). BMP encoding is a plain pixel copy (no deflate pass
// like PNG) and leptonica's file-based BMP reader is its fastest decode path
// — measured ~10% faster than PNG and ~2.3x faster than PNM end-to-end on
// real RDP screenshots. Color is preserved on purpose: pre-converting to
// grayscale doubles tesseract's processing time on subpixel-antialiased
// (ClearType) text.
package ocr

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/common/log"
)

// minWidthForUpscale is the threshold below which frames are 2x upscaled
// to improve OCR accuracy. RDP-captured screenshots need upscaling even at
// high resolutions because ClearType rendering + RDP compression degrades
// text quality enough that Tesseract misses characters like '@'.
// Set high enough to always upscale typical RDP resolutions (up to 4K).
const minWidthForUpscale = 4096

// Word represents a single word extracted by Tesseract OCR with its bounding box.
// Coordinates are in the original image pixel space (before any upscaling).
type Word struct {
	Text   string
	Left   int
	Top    int
	Width  int
	Height int
	Conf   float64 // Confidence score (0-100)
}

// ExtractResult contains the full OCR output: reconstructed text and per-word bounding boxes.
type ExtractResult struct {
	// Text is the full reconstructed text (words joined by spaces).
	Text string
	// Words contains each detected word with its bounding box.
	Words []Word
	// Upscaled is true if the image was 2x upscaled for OCR.
	// When true, word coordinates have already been halved back to original scale.
	Upscaled bool
}

// ExtractText runs Tesseract OCR on RGBA pixel data and returns the extracted text.
//
// The RGBA data must be in top-down row order (as returned by rle.ToRGBA).
// width and height are the image dimensions.
//
// Tesseract is invoked with PSM 6 (assume a single uniform block of text),
// which measured both faster and more accurate than sparse-text mode on
// RDP-captured UI screenshots.
func ExtractText(ctx context.Context, rgba []byte, width, height int) (string, error) {
	result, err := ExtractWords(ctx, rgba, width, height)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// ExtractWords runs Tesseract OCR on RGBA pixel data and returns structured word-level results.
//
// Each word includes its bounding box in the original (pre-upscale) image coordinate space.
// The reconstructed text is the words joined by spaces, suitable for passing to Presidio.
func ExtractWords(ctx context.Context, rgba []byte, width, height int) (*ExtractResult, error) {
	if len(rgba) == 0 {
		return &ExtractResult{}, nil
	}

	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("ocr: invalid image dimensions %dx%d", width, height)
	}
	if width > math.MaxInt/4 || height > math.MaxInt/(width*4) {
		return nil, fmt.Errorf("ocr: image dimensions overflow %dx%d", width, height)
	}
	// rgba may be larger than width*height*4 (callers may pass a backing
	// slice); only the prefix is read.
	expectedLen := width * height * 4
	if len(rgba) < expectedLen {
		return nil, fmt.Errorf("ocr: RGBA data too short: got %d, expected %d", len(rgba), expectedLen)
	}

	// Upscale 2x if image is small (improves accuracy on small fonts)
	upscaled := false
	if width < minWidthForUpscale {
		rgba = nearestNeighbor2xRGBA(rgba, width, height)
		width *= 2
		height *= 2
		upscaled = true
	}

	// Encode as uncompressed 24bpp BMP: header plus raw BGR rows.
	bmp := encodeBMP(rgba, width, height)

	// Run tesseract with TSV output for word-level bounding boxes
	tsvOutput, err := runTesseractTSV(ctx, bmp)
	if err != nil {
		return nil, err
	}

	// Parse TSV output into words
	words := parseTSV(tsvOutput, upscaled)

	// Reconstruct full text from words
	var textParts []string
	for _, w := range words {
		textParts = append(textParts, w.Text)
	}

	return &ExtractResult{
		Text:     strings.Join(textParts, " "),
		Words:    words,
		Upscaled: upscaled,
	}, nil
}

// runTesseractTSV invokes tesseract with TSV output to get word-level bounding boxes.
//
// The image is passed as a temp file path rather than stdin: leptonica's
// in-memory (stdin) BMP reader is ~7x slower than its file reader, while a
// page-cache temp file write costs only a few milliseconds.
func runTesseractTSV(ctx context.Context, bmpData []byte) (string, error) {
	tmp, err := os.CreateTemp("", "rdp-ocr-*.bmp")
	if err != nil {
		return "", fmt.Errorf("ocr: failed to create temp image: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	_, werr := tmp.Write(bmpData)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", fmt.Errorf("ocr: failed to write temp image: %w", werr)
	}

	cmd := exec.CommandContext(ctx, "tesseract", tmpPath, "stdout", "--psm", "6", "-l", "eng", "tsv")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			log.Debugf("tesseract stderr: %s", stderrStr)
		}
		return "", fmt.Errorf("ocr: tesseract failed: %w (stderr: %s)", err, stderrStr)
	}

	return stdout.String(), nil
}

// parseTSV parses Tesseract TSV output into a slice of Words.
// TSV columns: level, page_num, block_num, par_num, line_num, word_num, left, top, width, height, conf, text
// We only care about level 5 (individual words) with non-empty text.
// If upscaled is true, coordinates are halved back to original image scale.
func parseTSV(tsv string, upscaled bool) []Word {
	lines := strings.Split(tsv, "\n")
	var words []Word

	for i, line := range lines {
		if i == 0 {
			continue // Skip header line
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 12 {
			continue
		}

		// Only process word-level entries (level 5)
		level, err := strconv.Atoi(fields[0])
		if err != nil || level != 5 {
			continue
		}

		text := fields[11]
		if strings.TrimSpace(text) == "" {
			continue
		}

		conf, _ := strconv.ParseFloat(fields[10], 64)
		if conf < 0 {
			continue // Skip entries with invalid confidence
		}

		left, _ := strconv.Atoi(fields[6])
		top, _ := strconv.Atoi(fields[7])
		width, _ := strconv.Atoi(fields[8])
		height, _ := strconv.Atoi(fields[9])

		// If the image was 2x upscaled, halve coordinates back to original scale
		if upscaled {
			left /= 2
			top /= 2
			width /= 2
			height /= 2
		}

		words = append(words, Word{
			Text:   text,
			Left:   left,
			Top:    top,
			Width:  width,
			Height: height,
			Conf:   conf,
		})
	}

	return words
}

// nearestNeighbor2xRGBA scales an RGBA image by 2x using nearest-neighbor
// interpolation. This is intentionally simple — no smoothing, which preserves
// text edges for OCR.
func nearestNeighbor2xRGBA(src []byte, width, height int) []byte {
	srcStride := width * 4
	dstStride := srcStride * 2
	dst := make([]byte, dstStride*height*2)
	for y := 0; y < height; y++ {
		srcRow := src[y*srcStride : (y+1)*srcStride]
		dstRow1 := dst[(y*2)*dstStride : (y*2+1)*dstStride]
		dstRow2 := dst[(y*2+1)*dstStride : (y*2+2)*dstStride]
		for x := 0; x < width; x++ {
			si := x * 4
			di := x * 8
			copy(dstRow1[di:di+4], srcRow[si:si+4])
			copy(dstRow1[di+4:di+8], srcRow[si:si+4])
		}
		copy(dstRow2, dstRow1)
	}
	return dst
}

// bmpHeaderSize is the BITMAPFILEHEADER (14) + BITMAPINFOHEADER (40) size.
const bmpHeaderSize = 54

// encodeBMP serializes RGBA pixels as an uncompressed 24bpp bottom-up BMP.
// The encode is a plain pixel copy with no compression pass, and leptonica's
// BMP reader is its fastest decode path.
func encodeBMP(rgba []byte, width, height int) []byte {
	rowSize := (width*3 + 3) &^ 3 // 24bpp rows padded to 4-byte multiples
	imageSize := rowSize * height
	fileSize := bmpHeaderSize + imageSize
	out := make([]byte, fileSize)

	// BITMAPFILEHEADER
	out[0], out[1] = 'B', 'M'
	putU32 := func(off int, v uint32) {
		out[off] = byte(v)
		out[off+1] = byte(v >> 8)
		out[off+2] = byte(v >> 16)
		out[off+3] = byte(v >> 24)
	}
	putU32(2, uint32(fileSize))
	putU32(10, bmpHeaderSize) // pixel data offset

	// BITMAPINFOHEADER
	putU32(14, 40) // header size
	putU32(18, uint32(width))
	putU32(22, uint32(height)) // positive height = bottom-up rows
	out[26] = 1                // planes
	out[28] = 24               // bits per pixel
	putU32(34, uint32(imageSize))

	// Pixel data: bottom-up rows, BGR order.
	for y := 0; y < height; y++ {
		srcOff := y * width * 4
		dstOff := bmpHeaderSize + (height-1-y)*rowSize
		for x := 0; x < width; x++ {
			si := srcOff + x*4
			di := dstOff + x*3
			out[di] = rgba[si+2]   // B
			out[di+1] = rgba[si+1] // G
			out[di+2] = rgba[si]   // R
		}
	}
	return out
}

// IsAvailable checks whether the tesseract binary is available in PATH.
func IsAvailable() bool {
	_, err := exec.LookPath("tesseract")
	return err == nil
}
