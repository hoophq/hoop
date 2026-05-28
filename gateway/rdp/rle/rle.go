// Package rle implements RDP Interleaved Run-Length Encoding (RLE) bitmap decompression
// and pixel format conversion.
//
// Ported from the JavaScript implementation at webapp/resources/public/rdpclient/rle.js,
// which itself was ported from IronRDP's ironrdp-graphics/src/rle.rs.
//
// References:
//   - https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/b3b60873-16a8-4cbc-8aaa-5f0a93083280
//   - https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/b6a3f5c2-0804-4c10-9d25-a321720fd23e
package rle

import (
	"fmt"
)

// Order codes
const (
	regularBGRun  = 0x00
	regularFGRun  = 0x01
	regularFGBG   = 0x02
	regularColor  = 0x03
	regularCImage = 0x04

	megaMegaBGRun    = 0xF0
	megaMegaFGRun    = 0xF1
	megaMegaFGBG     = 0xF2
	megaMegaColor    = 0xF3
	megaMegaCImage   = 0xF4
	megaMegaSetFGRun = 0xF6
	megaMegaSetFGBG  = 0xF7
	megaMegaDithered = 0xF8

	liteSetFGRun = 0x0C
	liteSetFGBG  = 0x0D
	liteDithered = 0x0E

	specialFGBG1 = 0xF9
	specialFGBG2 = 0xFA
	specialWhite = 0xFD
	specialBlack = 0xFE

	maskRegularRunLength = 0x1F
	maskLiteRunLength    = 0x0F
)

// decodeCode extracts the order code from the header byte.
func decodeCode(header byte) byte {
	if header&0xC0 != 0xC0 {
		return header >> 5
	}
	if header&0xF0 == 0xF0 {
		return header
	}
	return header >> 4
}

// Decompress decompresses RLE-encoded RDP bitmap data.
//
// src is the compressed data, width and height are the bitmap dimensions,
// bpp is bits per pixel (8, 15, 16, or 24).
// Returns decompressed pixel data in the native RDP format (not RGBA).
func Decompress(src []byte, width, height, bpp int) ([]byte, error) {
	if len(src) == 0 {
		return nil, fmt.Errorf("rle: empty source data")
	}

	var colorDepth int
	switch {
	case bpp <= 8:
		colorDepth = 1
	case bpp <= 16:
		colorDepth = 2
	default:
		colorDepth = 3
	}

	rowDelta := colorDepth * width
	dst := make([]byte, rowDelta*height)

	srcPos := 0
	dstPos := 0

	var whitePixel uint32
	switch bpp {
	case 24:
		whitePixel = 0xFFFFFF
	case 16:
		whitePixel = 0xFFFF
	case 15:
		whitePixel = 0x7FFF
	default:
		whitePixel = 0xFF
	}
	const blackPixel uint32 = 0

	fgPel := whitePixel
	insertFgPel := false
	isFirstLine := true

	readU8 := func() byte {
		if srcPos >= len(src) {
			return 0
		}
		v := src[srcPos]
		srcPos++
		return v
	}

	readU16 := func() uint16 {
		if srcPos+1 >= len(src) {
			srcPos = len(src)
			return 0
		}
		v := uint16(src[srcPos]) | uint16(src[srcPos+1])<<8
		srcPos += 2
		return v
	}

	readPixel := func() uint32 {
		if colorDepth == 1 {
			return uint32(readU8())
		}
		if colorDepth == 2 {
			return uint32(readU16())
		}
		// 24bpp: 3 bytes LE
		if srcPos+2 >= len(src) {
			srcPos = len(src)
			return 0
		}
		b0, b1, b2 := src[srcPos], src[srcPos+1], src[srcPos+2]
		srcPos += 3
		return uint32(b0) | uint32(b1)<<8 | uint32(b2)<<16
	}

	writePixel := func(pixel uint32) {
		if dstPos >= len(dst) {
			return
		}
		if colorDepth == 1 {
			dst[dstPos] = byte(pixel)
			dstPos++
		} else if colorDepth == 2 {
			if dstPos+1 >= len(dst) {
				return
			}
			dst[dstPos] = byte(pixel)
			dst[dstPos+1] = byte(pixel >> 8)
			dstPos += 2
		} else {
			if dstPos+2 >= len(dst) {
				return
			}
			dst[dstPos] = byte(pixel)
			dst[dstPos+1] = byte(pixel >> 8)
			dst[dstPos+2] = byte(pixel >> 16)
			dstPos += 3
		}
	}

	readPixelAbove := func() uint32 {
		abovePos := dstPos - rowDelta
		if abovePos < 0 {
			return 0
		}
		if colorDepth == 1 {
			return uint32(dst[abovePos])
		}
		if colorDepth == 2 {
			return uint32(dst[abovePos]) | uint32(dst[abovePos+1])<<8
		}
		return uint32(dst[abovePos]) | uint32(dst[abovePos+1])<<8 | uint32(dst[abovePos+2])<<16
	}

	extractRunLengthRegular := func(header byte) int {
		rl := int(header & maskRegularRunLength)
		if rl == 0 {
			return int(readU8()) + 32
		}
		return rl
	}

	extractRunLengthLite := func(header byte) int {
		rl := int(header & maskLiteRunLength)
		if rl == 0 {
			return int(readU8()) + 16
		}
		return rl
	}

	extractRunLengthFgBg := func(header byte, mask byte) int {
		rl := int(header & mask)
		if rl == 0 {
			return int(readU8()) + 1
		}
		return rl * 8
	}

	extractRunLengthMegaMega := func() int {
		return int(readU16())
	}

	extractRunLength := func(code, header byte) int {
		switch code {
		case regularFGBG:
			return extractRunLengthFgBg(header, maskRegularRunLength)
		case liteSetFGBG:
			return extractRunLengthFgBg(header, maskLiteRunLength)
		case regularBGRun, regularFGRun, regularColor, regularCImage:
			return extractRunLengthRegular(header)
		case liteSetFGRun, liteDithered:
			return extractRunLengthLite(header)
		case megaMegaBGRun, megaMegaFGRun, megaMegaSetFGRun,
			megaMegaDithered, megaMegaColor,
			megaMegaFGBG, megaMegaSetFGBG, megaMegaCImage:
			return extractRunLengthMegaMega()
		default:
			return 0
		}
	}

	writeFgBgImage := func(bitmask byte, cBits int) {
		var mask byte = 0x01
		for i := 0; i < cBits; i++ {
			if isFirstLine {
				if bitmask&mask != 0 {
					writePixel(fgPel)
				} else {
					writePixel(blackPixel)
				}
			} else {
				above := readPixelAbove()
				if bitmask&mask != 0 {
					writePixel(above ^ fgPel)
				} else {
					writePixel(above)
				}
			}
			mask <<= 1
		}
	}

	for srcPos < len(src) {
		if isFirstLine && dstPos >= rowDelta {
			isFirstLine = false
			insertFgPel = false
		}

		header := readU8()
		code := decodeCode(header)
		runLength := extractRunLength(code, header)

		// Background Run
		if code == regularBGRun || code == megaMegaBGRun {
			count := runLength
			if insertFgPel {
				if isFirstLine {
					writePixel(fgPel)
				} else {
					writePixel(readPixelAbove() ^ fgPel)
				}
				count--
			}
			for i := 0; i < count; i++ {
				if isFirstLine {
					writePixel(blackPixel)
				} else {
					writePixel(readPixelAbove())
				}
			}
			insertFgPel = true
			continue
		}

		insertFgPel = false

		switch {
		// Foreground Run
		case code == regularFGRun || code == megaMegaFGRun ||
			code == liteSetFGRun || code == megaMegaSetFGRun:
			if code == liteSetFGRun || code == megaMegaSetFGRun {
				fgPel = readPixel()
			}
			for i := 0; i < runLength; i++ {
				if isFirstLine {
					writePixel(fgPel)
				} else {
					writePixel(readPixelAbove() ^ fgPel)
				}
			}

		// Dithered Run
		case code == liteDithered || code == megaMegaDithered:
			pixelA := readPixel()
			pixelB := readPixel()
			for i := 0; i < runLength; i++ {
				writePixel(pixelA)
				writePixel(pixelB)
			}

		// Color Run
		case code == regularColor || code == megaMegaColor:
			pixel := readPixel()
			for i := 0; i < runLength; i++ {
				writePixel(pixel)
			}

		// Foreground/Background Image
		case code == regularFGBG || code == megaMegaFGBG ||
			code == liteSetFGBG || code == megaMegaSetFGBG:
			if code == liteSetFGBG || code == megaMegaSetFGBG {
				fgPel = readPixel()
			}
			remaining := runLength
			for remaining > 0 {
				cBits := remaining
				if cBits > 8 {
					cBits = 8
				}
				bitmask := readU8()
				writeFgBgImage(bitmask, cBits)
				remaining -= cBits
			}

		// Color Image
		case code == regularCImage || code == megaMegaCImage:
			byteCount := runLength * colorDepth
			for i := 0; i < byteCount; i++ {
				if dstPos < len(dst) && srcPos < len(src) {
					dst[dstPos] = src[srcPos]
					dstPos++
					srcPos++
				}
			}

		// Special FgBg 1
		case code == specialFGBG1:
			writeFgBgImage(0x03, 8)

		// Special FgBg 2
		case code == specialFGBG2:
			writeFgBgImage(0x05, 8)

		// Special White
		case code == specialWhite:
			writePixel(whitePixel)

		// Special Black
		case code == specialBlack:
			writePixel(blackPixel)
		}
	}

	return dst, nil
}

// ToRGBA converts decompressed RDP bitmap pixel data to RGBA format.
// Handles bottom-up row order (RDP bitmaps are bottom-up) and BGR->RGB conversion.
//
// src is the decompressed pixel data (from Decompress or raw uncompressed data),
// width and height are the bitmap dimensions, bpp is bits per pixel (16, 24, or 32).
// Returns RGBA pixel data (4 bytes per pixel, top-down row order).
func ToRGBA(src []byte, width, height, bpp int) ([]byte, error) {
	bytesPerPixel := bpp >> 3 // 2, 3, or 4
	srcRowBytes := width * bytesPerPixel
	totalPixels := width * height
	dst := make([]byte, totalPixels*4)

	for row := 0; row < height; row++ {
		// RDP bitmaps are bottom-up: first row in data = bottom row on screen
		srcRow := height - 1 - row
		srcRowOffset := srcRow * srcRowBytes
		dstRowOffset := row * width * 4

		switch bpp {
		case 16:
			// RGB565 little-endian
			for col := 0; col < width; col++ {
				si := srcRowOffset + col*2
				di := dstRowOffset + col*4
				if si+1 >= len(src) {
					break
				}
				pixel := uint16(src[si]) | uint16(src[si+1])<<8
				r := (pixel >> 11) & 0x1F
				g := (pixel >> 5) & 0x3F
				b := pixel & 0x1F
				dst[di] = byte((r << 3) | (r >> 2))
				dst[di+1] = byte((g << 2) | (g >> 4))
				dst[di+2] = byte((b << 3) | (b >> 2))
				dst[di+3] = 255
			}
		case 24:
			// BGR format
			for col := 0; col < width; col++ {
				si := srcRowOffset + col*3
				di := dstRowOffset + col*4
				if si+2 >= len(src) {
					break
				}
				dst[di] = src[si+2]   // R
				dst[di+1] = src[si+1] // G
				dst[di+2] = src[si]   // B
				dst[di+3] = 255
			}
		case 32:
			// BGRX format
			for col := 0; col < width; col++ {
				si := srcRowOffset + col*4
				di := dstRowOffset + col*4
				if si+3 >= len(src) {
					break
				}
				dst[di] = src[si+2]   // R
				dst[di+1] = src[si+1] // G
				dst[di+2] = src[si]   // B
				dst[di+3] = 255
			}
		default:
			return nil, fmt.Errorf("rle: unsupported bpp %d for RGBA conversion", bpp)
		}
	}

	return dst, nil
}

// DecompressToRGBA is a convenience function that decompresses RLE data and converts to RGBA.
// For uncompressed frames, pass the raw data directly to ToRGBA instead.
func DecompressToRGBA(src []byte, width, height, bpp int) ([]byte, error) {
	decompressed, err := Decompress(src, width, height, bpp)
	if err != nil {
		return nil, err
	}
	return ToRGBA(decompressed, width, height, bpp)
}
