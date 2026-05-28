// Package ocr provides text extraction from bitmap images using Tesseract OCR.
//
// Tesseract is invoked as a subprocess to avoid CGO/libtesseract-dev build dependencies.
// Images are encoded as PNG in memory and piped to tesseract via stdin.
package ocr

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
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
// Tesseract is invoked with PSM 11 (sparse text) which works best for UI screenshots
// where text is scattered across the screen rather than forming a single block.
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

	expectedLen := width * height * 4
	if len(rgba) < expectedLen {
		return nil, fmt.Errorf("ocr: RGBA data too short: got %d, expected %d", len(rgba), expectedLen)
	}

	// Build an image.NRGBA from the raw RGBA bytes
	img := &image.NRGBA{
		Pix:    rgba,
		Stride: width * 4,
		Rect:   image.Rect(0, 0, width, height),
	}

	// Upscale 2x if image is small (improves accuracy on small fonts)
	upscaled := false
	if width < minWidthForUpscale {
		img = nearestNeighbor2x(img)
		upscaled = true
	}

	// Encode as PNG in memory
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, fmt.Errorf("ocr: failed to encode PNG: %w", err)
	}

	// Run tesseract with TSV output for word-level bounding boxes
	tsvOutput, err := runTesseractTSV(ctx, pngBuf.Bytes())
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
func runTesseractTSV(ctx context.Context, pngData []byte) (string, error) {
	cmd := exec.CommandContext(ctx, "tesseract", "stdin", "stdout", "--psm", "6", "-l", "eng", "tsv")
	cmd.Stdin = bytes.NewReader(pngData)

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

// nearestNeighbor2x scales an NRGBA image by 2x using nearest-neighbor interpolation.
// This is intentionally simple — no smoothing, which preserves text edges for OCR.
func nearestNeighbor2x(src *image.NRGBA) *image.NRGBA {
	srcW := src.Rect.Dx()
	srcH := src.Rect.Dy()
	dstW := srcW * 2
	dstH := srcH * 2

	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))

	for y := 0; y < srcH; y++ {
		srcRowOff := y * src.Stride
		dstRow1Off := (y * 2) * dst.Stride
		dstRow2Off := (y*2 + 1) * dst.Stride

		for x := 0; x < srcW; x++ {
			si := srcRowOff + x*4
			r, g, b, a := src.Pix[si], src.Pix[si+1], src.Pix[si+2], src.Pix[si+3]

			di1 := dstRow1Off + x*2*4
			di2 := dstRow2Off + x*2*4

			// Write 2x2 block
			dst.Pix[di1], dst.Pix[di1+1], dst.Pix[di1+2], dst.Pix[di1+3] = r, g, b, a
			dst.Pix[di1+4], dst.Pix[di1+5], dst.Pix[di1+6], dst.Pix[di1+7] = r, g, b, a
			dst.Pix[di2], dst.Pix[di2+1], dst.Pix[di2+2], dst.Pix[di2+3] = r, g, b, a
			dst.Pix[di2+4], dst.Pix[di2+5], dst.Pix[di2+6], dst.Pix[di2+7] = r, g, b, a
		}
	}

	return dst
}

// IsAvailable checks whether the tesseract binary is available in PATH.
func IsAvailable() bool {
	_, err := exec.LookPath("tesseract")
	return err == nil
}
