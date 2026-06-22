//! Shadow framebuffer: a growable RGBA canvas mirroring the client's screen.
//!
//! Port of `gateway/rdp/piigate.go` (shadowCanvas) and the pixel-format
//! conversion from `gateway/rdp/rle/rle.go` (ToRGBA). RLE decompression
//! itself uses `ironrdp_graphics::rle` — the canonical implementation the Go
//! port was derived from.

use tracing::{debug, warn};

use super::framing::BitmapPatch;

/// Bounds the shadow framebuffer the gate is willing to composite. RDP
/// allows up to 8192x8192, but a full-size framebuffer would cost ~268MB per
/// session; 4096x4096 (64MB) covers 4K desktops. Bitmaps beyond the cap are
/// not composited (logged, fail-open for that region).
pub const MAX_CANVAS_DIM: usize = 4096;

/// Converts decompressed RDP bitmap pixel data to RGBA. Handles bottom-up
/// row order (RDP bitmaps are bottom-up) and BGR->RGB conversion.
///
/// `src` is decompressed pixel data, `bpp` is 16, 24, or 32. Returns RGBA
/// pixels (4 bytes/pixel, top-down row order).
pub fn to_rgba(src: &[u8], width: usize, height: usize, bpp: usize) -> Option<Vec<u8>> {
    let bytes_per_pixel = match bpp {
        16 => 2,
        24 => 3,
        32 => 4,
        _ => return None,
    };
    let src_row_bytes = width * bytes_per_pixel;
    let mut dst = vec![0u8; width * height * 4];

    for row in 0..height {
        // RDP bitmaps are bottom-up: first row in data = bottom row on screen.
        let src_row = height - 1 - row;
        let src_off = src_row * src_row_bytes;
        let dst_off = row * width * 4;

        for col in 0..width {
            let si = src_off + col * bytes_per_pixel;
            let di = dst_off + col * 4;
            if si + bytes_per_pixel > src.len() {
                break;
            }
            match bpp {
                16 => {
                    // RGB565 little-endian.
                    let pixel = u16::from_le_bytes([src[si], src[si + 1]]);
                    let r = (pixel >> 11) & 0x1f;
                    let g = (pixel >> 5) & 0x3f;
                    let b = pixel & 0x1f;
                    dst[di] = ((r << 3) | (r >> 2)) as u8;
                    dst[di + 1] = ((g << 2) | (g >> 4)) as u8;
                    dst[di + 2] = ((b << 3) | (b >> 2)) as u8;
                    dst[di + 3] = 255;
                }
                24 | 32 => {
                    // BGR / BGRX.
                    dst[di] = src[si + 2];
                    dst[di + 1] = src[si + 1];
                    dst[di + 2] = src[si];
                    dst[di + 3] = 255;
                }
                _ => unreachable!(),
            }
        }
    }
    Some(dst)
}

/// Draws a decoded RGBA bitmap patch onto the framebuffer at (dst_x, dst_y).
/// Out-of-bounds regions are clipped.
#[allow(clippy::too_many_arguments)] // mirrors the Go CompositeBitmap contract
pub fn composite_bitmap(
    fb: &mut [u8],
    fb_width: usize,
    fb_height: usize,
    patch: &[u8],
    patch_w: usize,
    patch_h: usize,
    dst_x: usize,
    dst_y: usize,
) {
    for row in 0..patch_h {
        let fb_y = dst_y + row;
        if fb_y >= fb_height {
            continue;
        }
        for col in 0..patch_w {
            let fb_x = dst_x + col;
            if fb_x >= fb_width {
                continue;
            }
            let si = (row * patch_w + col) * 4;
            let di = (fb_y * fb_width + fb_x) * 4;
            if si + 4 <= patch.len() && di + 4 <= fb.len() {
                fb[di..di + 4].copy_from_slice(&patch[si..si + 4]);
            }
        }
    }
}

/// A growable RGBA framebuffer that mirrors the client's screen. Shared
/// between the gate's analysis task and the test-side leak oracle so both
/// composite pixels identically.
#[derive(Debug, Default)]
pub struct ShadowCanvas {
    pub session_id: String,
    pub fb: Vec<u8>,
    pub w: usize,
    pub h: usize,
}

impl ShadowCanvas {
    pub fn new(session_id: impl Into<String>) -> Self {
        Self {
            session_id: session_id.into(),
            ..Default::default()
        }
    }

    /// Decodes one bitmap patch and draws it, growing the canvas if the
    /// patch extends beyond it. Returns false when the patch cannot be
    /// composited (oversized extent or decode failure) — fail-open for that
    /// region.
    pub fn composite(&mut self, patch: &BitmapPatch) -> bool {
        // Checked: geometry is wire-format u16 today, but a malformed or
        // future-widened extent must fail-open for the region (skip), never
        // panic in debug or wrap in release.
        let (Some(right), Some(bottom)) = (
            patch.x.checked_add(patch.width),
            patch.y.checked_add(patch.height),
        ) else {
            warn!(sid = %self.session_id, "piigate: bitmap extent overflow, skipping");
            return false;
        };
        if right > MAX_CANVAS_DIM || bottom > MAX_CANVAS_DIM {
            warn!(
                sid = %self.session_id,
                "piigate: bitmap extent {right}x{bottom} exceeds max canvas, skipping"
            );
            return false;
        }

        let rgba = if patch.compressed {
            let mut decompressed = Vec::new();
            match ironrdp_graphics::rle::decompress(
                &patch.data,
                &mut decompressed,
                patch.width,
                patch.height,
                patch.bits_per_pixel,
            ) {
                Ok(_) => to_rgba(&decompressed, patch.width, patch.height, patch.bits_per_pixel),
                Err(e) => {
                    debug!(sid = %self.session_id, "piigate: RLE decompress error: {e:?}");
                    None
                }
            }
        } else {
            to_rgba(&patch.data, patch.width, patch.height, patch.bits_per_pixel)
        };
        let Some(rgba) = rgba else {
            debug!(sid = %self.session_id, "piigate: bitmap decode error (bpp={})", patch.bits_per_pixel);
            return false;
        };

        if right > self.w || bottom > self.h {
            self.grow(right, bottom);
        }
        composite_bitmap(
            &mut self.fb,
            self.w,
            self.h,
            &rgba,
            patch.width,
            patch.height,
            patch.x,
            patch.y,
        );
        true
    }

    /// Reallocates the framebuffer to cover at least (width x height),
    /// preserving existing content.
    fn grow(&mut self, width: usize, height: usize) {
        let width = width.max(self.w);
        let height = height.max(self.h);
        let mut new_fb = vec![0u8; width * height * 4];
        for y in 0..self.h {
            let src = &self.fb[y * self.w * 4..(y + 1) * self.w * 4];
            new_fb[y * width * 4..y * width * 4 + self.w * 4].copy_from_slice(src);
        }
        self.fb = new_fb;
        self.w = width;
        self.h = height;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn patch(x: usize, y: usize, w: usize, h: usize, bgr: [u8; 3]) -> BitmapPatch {
        BitmapPatch {
            x,
            y,
            width: w,
            height: h,
            bits_per_pixel: 24,
            compressed: false,
            data: bgr.repeat(w * h),
        }
    }

    #[test]
    fn to_rgba_24bpp_flips_rows_and_swaps_bgr() {
        // 1x2 bitmap, bottom-up: first row in data is the BOTTOM row.
        let src = [
            0x01, 0x02, 0x03, // bottom pixel BGR
            0x0a, 0x0b, 0x0c, // top pixel BGR
        ];
        let rgba = to_rgba(&src, 1, 2, 24).unwrap();
        assert_eq!(&rgba[0..4], &[0x0c, 0x0b, 0x0a, 255]); // top row first
        assert_eq!(&rgba[4..8], &[0x03, 0x02, 0x01, 255]);
    }

    #[test]
    fn to_rgba_rejects_unsupported_bpp() {
        assert!(to_rgba(&[0u8; 8], 2, 1, 8).is_none());
    }

    #[test]
    fn to_rgba_16bpp_rgb565_expansion() {
        // 1x1 RGB565 pixel: full red = 0xF800 (r=31,g=0,b=0), LE bytes.
        let rgba = to_rgba(&[0x00, 0xf8], 1, 1, 16).unwrap();
        // r = (31<<3)|(31>>2) = 248|7 = 255; g=0; b=0.
        assert_eq!(&rgba[..4], &[255, 0, 0, 255]);
    }

    #[test]
    fn to_rgba_32bpp_bgrx_swaps_to_rgb() {
        // 1x1 BGRX pixel: B=0x10 G=0x20 R=0x30 X=0xff.
        let rgba = to_rgba(&[0x10, 0x20, 0x30, 0xff], 1, 1, 32).unwrap();
        assert_eq!(&rgba[..4], &[0x30, 0x20, 0x10, 255]);
    }

    #[test]
    fn composite_rejects_overflowing_extent() {
        let mut c = ShadowCanvas::new("t");
        let p = BitmapPatch {
            x: usize::MAX - 1,
            y: 0,
            width: 4,
            height: 4,
            bits_per_pixel: 24,
            compressed: false,
            data: [0u8; 3].repeat(16),
        };
        assert!(!c.composite(&p), "overflowing extent must be rejected, not panic");
    }

    #[test]
    fn grow_preserves_content() {
        let mut c = ShadowCanvas::new("t");
        let red = [0x00, 0x00, 0xff]; // BGR red

        assert!(c.composite(&patch(0, 0, 2, 2, red)));
        assert_eq!((c.w, c.h), (2, 2));
        assert!(c.composite(&patch(30, 30, 2, 2, red)));
        assert_eq!((c.w, c.h), (32, 32));
        assert_eq!(c.fb[0], 0xff, "pixel (0,0) red channel after grow");

        // Oversized extent must be rejected.
        assert!(!c.composite(&patch(MAX_CANVAS_DIM - 1, 0, 2, 2, red)));
    }

    #[test]
    fn composite_clips_out_of_bounds() {
        let mut fb = vec![0u8; 4 * 4 * 4];
        let p = [1u8, 2, 3, 255].repeat(4); // 2x2 RGBA
        composite_bitmap(&mut fb, 4, 4, &p, 2, 2, 3, 3); // bottom-right corner, clips
        assert_eq!(&fb[(3 * 4 + 3) * 4..(3 * 4 + 3) * 4 + 4], &[1, 2, 3, 255]);
    }
}
