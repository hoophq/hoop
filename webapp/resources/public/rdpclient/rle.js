/**
 * RDP Interleaved Run-Length Encoding (RLE) Bitmap Codec
 *
 * Ported from IronRDP's ironrdp-graphics/src/rle.rs
 *
 * References:
 * - https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/b3b60873-16a8-4cbc-8aaa-5f0a93083280
 * - https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-rdpbcgr/b6a3f5c2-0804-4c10-9d25-a321720fd23e
 */

// Order codes
const REGULAR_BG_RUN = 0x00;
const REGULAR_FG_RUN = 0x01;
const REGULAR_FGBG_IMAGE = 0x02;
const REGULAR_COLOR_RUN = 0x03;
const REGULAR_COLOR_IMAGE = 0x04;

const MEGA_MEGA_BG_RUN = 0xF0;
const MEGA_MEGA_FG_RUN = 0xF1;
const MEGA_MEGA_FGBG_IMAGE = 0xF2;
const MEGA_MEGA_COLOR_RUN = 0xF3;
const MEGA_MEGA_COLOR_IMAGE = 0xF4;
const MEGA_MEGA_SET_FG_RUN = 0xF6;
const MEGA_MEGA_SET_FGBG_IMAGE = 0xF7;
const MEGA_MEGA_DITHERED_RUN = 0xF8;

const LITE_SET_FG_FG_RUN = 0x0C;
const LITE_SET_FG_FGBG_IMAGE = 0x0D;
const LITE_DITHERED_RUN = 0x0E;

const SPECIAL_FGBG_1 = 0xF9;
const SPECIAL_FGBG_2 = 0xFA;
const SPECIAL_WHITE = 0xFD;
const SPECIAL_BLACK = 0xFE;

const MASK_REGULAR_RUN_LENGTH = 0x1F;
const MASK_LITE_RUN_LENGTH = 0x0F;

function decodeCode(header) {
  if ((header & 0xC0) !== 0xC0) {
    return header >> 5;
  } else if ((header & 0xF0) === 0xF0) {
    return header;
  } else {
    return header >> 4;
  }
}

/**
 * Decompress RLE-compressed RDP bitmap data (16bpp).
 *
 * @param {Uint8Array} src - Compressed data
 * @param {number} width - Bitmap width in pixels
 * @param {number} height - Bitmap height in pixels
 * @param {number} bpp - Bits per pixel (8, 15, 16, or 24)
 * @returns {Uint8Array} Decompressed data
 */
window.rdpRleDecompress = rleDecompress;

function rleDecompress(src, width, height, bpp) {
  const colorDepth = bpp <= 8 ? 1 : bpp <= 16 ? 2 : 3;
  const rowDelta = colorDepth * width;
  const dst = new Uint8Array(rowDelta * height);

  let srcPos = 0;
  let dstPos = 0;

  const whitePixel = bpp === 24 ? 0xFFFFFF : bpp === 16 ? 0xFFFF : bpp === 15 ? 0x7FFF : 0xFF;
  const blackPixel = 0;
  let fgPel = whitePixel;
  let insertFgPel = false;
  let isFirstLine = true;

  function readU8() {
    return src[srcPos++];
  }

  function readU16() {
    const val = src[srcPos] | (src[srcPos + 1] << 8);
    srcPos += 2;
    return val;
  }

  function readPixel() {
    if (colorDepth === 1) return readU8();
    if (colorDepth === 2) return readU16();
    // 24bpp: 3 bytes LE
    const b0 = src[srcPos], b1 = src[srcPos + 1], b2 = src[srcPos + 2];
    srcPos += 3;
    return b0 | (b1 << 8) | (b2 << 16);
  }

  function writePixel(pixel) {
    if (colorDepth === 1) {
      dst[dstPos++] = pixel & 0xFF;
    } else if (colorDepth === 2) {
      dst[dstPos] = pixel & 0xFF;
      dst[dstPos + 1] = (pixel >> 8) & 0xFF;
      dstPos += 2;
    } else {
      dst[dstPos] = pixel & 0xFF;
      dst[dstPos + 1] = (pixel >> 8) & 0xFF;
      dst[dstPos + 2] = (pixel >> 16) & 0xFF;
      dstPos += 3;
    }
  }

  function readPixelAbove() {
    const abovePos = dstPos - rowDelta;
    if (colorDepth === 1) return dst[abovePos];
    if (colorDepth === 2) return dst[abovePos] | (dst[abovePos + 1] << 8);
    return dst[abovePos] | (dst[abovePos + 1] << 8) | (dst[abovePos + 2] << 16);
  }

  function extractRunLengthRegular(header) {
    const rl = header & MASK_REGULAR_RUN_LENGTH;
    return rl === 0 ? readU8() + 32 : rl;
  }

  function extractRunLengthLite(header) {
    const rl = header & MASK_LITE_RUN_LENGTH;
    return rl === 0 ? readU8() + 16 : rl;
  }

  function extractRunLengthFgBg(header, mask) {
    const rl = header & mask;
    return rl === 0 ? readU8() + 1 : rl * 8;
  }

  function extractRunLengthMegaMega() {
    return readU16();
  }

  function extractRunLength(code, header) {
    switch (code) {
      case REGULAR_FGBG_IMAGE:
        return extractRunLengthFgBg(header, MASK_REGULAR_RUN_LENGTH);
      case LITE_SET_FG_FGBG_IMAGE:
        return extractRunLengthFgBg(header, MASK_LITE_RUN_LENGTH);
      case REGULAR_BG_RUN:
      case REGULAR_FG_RUN:
      case REGULAR_COLOR_RUN:
      case REGULAR_COLOR_IMAGE:
        return extractRunLengthRegular(header);
      case LITE_SET_FG_FG_RUN:
      case LITE_DITHERED_RUN:
        return extractRunLengthLite(header);
      case MEGA_MEGA_BG_RUN:
      case MEGA_MEGA_FG_RUN:
      case MEGA_MEGA_SET_FG_RUN:
      case MEGA_MEGA_DITHERED_RUN:
      case MEGA_MEGA_COLOR_RUN:
      case MEGA_MEGA_FGBG_IMAGE:
      case MEGA_MEGA_SET_FGBG_IMAGE:
      case MEGA_MEGA_COLOR_IMAGE:
        return extractRunLengthMegaMega();
      default:
        return 0;
    }
  }

  function writeFgBgImage(bitmask, cBits) {
    let mask = 0x01;
    for (let i = 0; i < cBits; i++) {
      if (isFirstLine) {
        writePixel((bitmask & mask) !== 0 ? fgPel : blackPixel);
      } else {
        const above = readPixelAbove();
        writePixel((bitmask & mask) !== 0 ? (above ^ fgPel) : above);
      }
      mask <<= 1;
    }
  }

  while (srcPos < src.length) {
    if (isFirstLine && dstPos >= rowDelta) {
      isFirstLine = false;
      insertFgPel = false;
    }

    const header = readU8();
    const code = decodeCode(header);
    const runLength = extractRunLength(code, header);

    // Background Run
    if (code === REGULAR_BG_RUN || code === MEGA_MEGA_BG_RUN) {
      let count = runLength;
      if (insertFgPel) {
        if (isFirstLine) {
          writePixel(fgPel);
        } else {
          writePixel(readPixelAbove() ^ fgPel);
        }
        count--;
      }
      for (let i = 0; i < count; i++) {
        if (isFirstLine) {
          writePixel(blackPixel);
        } else {
          writePixel(readPixelAbove());
        }
      }
      insertFgPel = true;
      continue;
    }

    insertFgPel = false;

    // Foreground Run
    if (code === REGULAR_FG_RUN || code === MEGA_MEGA_FG_RUN ||
        code === LITE_SET_FG_FG_RUN || code === MEGA_MEGA_SET_FG_RUN) {
      if (code === LITE_SET_FG_FG_RUN || code === MEGA_MEGA_SET_FG_RUN) {
        fgPel = readPixel();
      }
      for (let i = 0; i < runLength; i++) {
        if (isFirstLine) {
          writePixel(fgPel);
        } else {
          writePixel(readPixelAbove() ^ fgPel);
        }
      }
    }
    // Dithered Run
    else if (code === LITE_DITHERED_RUN || code === MEGA_MEGA_DITHERED_RUN) {
      const pixelA = readPixel();
      const pixelB = readPixel();
      for (let i = 0; i < runLength; i++) {
        writePixel(pixelA);
        writePixel(pixelB);
      }
    }
    // Color Run
    else if (code === REGULAR_COLOR_RUN || code === MEGA_MEGA_COLOR_RUN) {
      const pixel = readPixel();
      for (let i = 0; i < runLength; i++) {
        writePixel(pixel);
      }
    }
    // Foreground/Background Image
    else if (code === REGULAR_FGBG_IMAGE || code === MEGA_MEGA_FGBG_IMAGE ||
             code === LITE_SET_FG_FGBG_IMAGE || code === MEGA_MEGA_SET_FGBG_IMAGE) {
      if (code === LITE_SET_FG_FGBG_IMAGE || code === MEGA_MEGA_SET_FGBG_IMAGE) {
        fgPel = readPixel();
      }
      let remaining = runLength;
      while (remaining > 0) {
        const cBits = Math.min(8, remaining);
        const bitmask = readU8();
        writeFgBgImage(bitmask, cBits);
        remaining -= cBits;
      }
    }
    // Color Image
    else if (code === REGULAR_COLOR_IMAGE || code === MEGA_MEGA_COLOR_IMAGE) {
      const byteCount = runLength * colorDepth;
      for (let i = 0; i < byteCount; i++) {
        dst[dstPos++] = src[srcPos++];
      }
    }
    // Special FgBg 1
    else if (code === SPECIAL_FGBG_1) {
      writeFgBgImage(0x03, 8);
    }
    // Special FgBg 2
    else if (code === SPECIAL_FGBG_2) {
      writeFgBgImage(0x05, 8);
    }
    // Special White
    else if (code === SPECIAL_WHITE) {
      writePixel(whitePixel);
    }
    // Special Black
    else if (code === SPECIAL_BLACK) {
      writePixel(blackPixel);
    }
  }

  return dst;
}

/**
 * Convert RDP bitmap pixel data to RGBA for canvas putImageData.
 * Handles bottom-up row order and BGR->RGB conversion.
 * Returns a Uint8ClampedArray of length width*height*4.
 *
 * @param {Uint8Array} src - Source pixel data (after RLE decompression if needed)
 * @param {number} width - Bitmap width in pixels
 * @param {number} height - Bitmap height in pixels
 * @param {number} bpp - Bits per pixel (16, 24, or 32)
 * @returns {Uint8ClampedArray} RGBA pixel data for ImageData
 */
window.rdpBitmapToRGBA = function(src, width, height, bpp) {
  const totalPixels = width * height;
  const dst = new Uint8ClampedArray(totalPixels * 4);
  const bytesPerPixel = bpp >> 3; // 2, 3, or 4
  const srcRowBytes = width * bytesPerPixel;

  for (let row = 0; row < height; row++) {
    // RDP bitmaps are bottom-up: first row in data = bottom row on screen
    const srcRow = height - 1 - row;
    const srcRowOffset = srcRow * srcRowBytes;
    const dstRowOffset = row * width * 4;

    if (bpp === 16) {
      // RGB565 little-endian
      for (let col = 0; col < width; col++) {
        const si = srcRowOffset + col * 2;
        const di = dstRowOffset + col * 4;
        const pixel = src[si] | (src[si + 1] << 8);
        const r = (pixel >> 11) & 0x1F;
        const g = (pixel >> 5) & 0x3F;
        const b = pixel & 0x1F;
        dst[di]     = (r << 3) | (r >> 2);
        dst[di + 1] = (g << 2) | (g >> 4);
        dst[di + 2] = (b << 3) | (b >> 2);
        dst[di + 3] = 255;
      }
    } else if (bpp === 24) {
      // BGR format
      for (let col = 0; col < width; col++) {
        const si = srcRowOffset + col * 3;
        const di = dstRowOffset + col * 4;
        dst[di]     = src[si + 2]; // R
        dst[di + 1] = src[si + 1]; // G
        dst[di + 2] = src[si];     // B
        dst[di + 3] = 255;
      }
    } else if (bpp === 32) {
      // BGRX format
      for (let col = 0; col < width; col++) {
        const si = srcRowOffset + col * 4;
        const di = dstRowOffset + col * 4;
        dst[di]     = src[si + 2]; // R
        dst[di + 1] = src[si + 1]; // G
        dst[di + 2] = src[si];     // B
        dst[di + 3] = 255;
      }
    }
  }

  return dst;
};
