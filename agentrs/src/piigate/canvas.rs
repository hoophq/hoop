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

    // Row-oriented conversion: the depth dispatch and truncation handling are
    // hoisted out of the pixel loop, and each row is a zip over exact-size
    // subslices (no per-pixel bounds checks — this is the composite hot
    // path's dominant CPU stage together with composite_bitmap). Truncated
    // source data converts whole pixels up to what is available and leaves
    // the rest zeroed, matching the previous per-pixel `break` behavior.
    for row in 0..height {
        // RDP bitmaps are bottom-up: first row in data = bottom row on screen.
        let src_row = height - 1 - row;
        let src_off = src_row * src_row_bytes;
        if src_off >= src.len() {
            continue;
        }
        let avail_cols = ((src.len() - src_off) / bytes_per_pixel).min(width);
        let src_row = &src[src_off..src_off + avail_cols * bytes_per_pixel];
        let dst_off = row * width * 4;
        let dst_row = &mut dst[dst_off..dst_off + avail_cols * 4];

        match bpp {
            16 => {
                for (d, s) in dst_row.chunks_exact_mut(4).zip(src_row.chunks_exact(2)) {
                    // RGB565 little-endian.
                    let pixel = u16::from_le_bytes([s[0], s[1]]);
                    let r = (pixel >> 11) & 0x1f;
                    let g = (pixel >> 5) & 0x3f;
                    let b = pixel & 0x1f;
                    d[0] = ((r << 3) | (r >> 2)) as u8;
                    d[1] = ((g << 2) | (g >> 4)) as u8;
                    d[2] = ((b << 3) | (b >> 2)) as u8;
                    d[3] = 255;
                }
            }
            24 => {
                for (d, s) in dst_row.chunks_exact_mut(4).zip(src_row.chunks_exact(3)) {
                    // BGR.
                    d[0] = s[2];
                    d[1] = s[1];
                    d[2] = s[0];
                    d[3] = 255;
                }
            }
            _ => {
                for (d, s) in dst_row.chunks_exact_mut(4).zip(src_row.chunks_exact(4)) {
                    // BGRX.
                    d[0] = s[2];
                    d[1] = s[1];
                    d[2] = s[0];
                    d[3] = 255;
                }
            }
        }
    }
    Some(dst)
}

/// Draws a decoded RGBA bitmap patch onto the framebuffer at (dst_x, dst_y).
/// Out-of-bounds regions are clipped. Returns whether any pixel actually
/// CHANGED: RDP routinely resends byte-identical tiles (idle repaints, cursor
/// trails, unchanged backgrounds), and a paint that changes nothing has no new
/// content to analyze. Reporting "unchanged" lets the caller skip marking the
/// region dirty, so it is not needlessly re-OCR'd — this is safe because the
/// shadow canvas already holds (and already analyzed) those exact pixels.
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
) -> bool {
    // Row-oriented compare/copy: clip once per row, then memcmp the visible
    // span and memcpy only when it differs. The per-pixel variant this
    // replaces spent its time on per-pixel bounds checks and 4-byte
    // compares; slice compare/copy vectorize and measured 5.5-10.6x faster
    // on the same inputs (identical repaints — the common storm case — and
    // full changed repaints alike). Behavior is identical, including the
    // changed-flag semantics and edge clipping; the only intentional
    // difference is copy granularity on a partially-changed row (the whole
    // visible span is copied instead of just the differing pixels), which is
    // invisible: the copied bytes are the row's own new content.
    if dst_x >= fb_width {
        return false;
    }
    let vis_w = patch_w.min(fb_width - dst_x); // right-edge clip
    let mut changed = false;
    for row in 0..patch_h {
        let fb_y = dst_y + row;
        if fb_y >= fb_height {
            continue;
        }
        let si = row * patch_w * 4;
        let di = (fb_y * fb_width + dst_x) * 4;
        // Truncated patch data contributes whole pixels up to what is
        // available on this row (same as the per-pixel bounds check did).
        if si >= patch.len() || di >= fb.len() {
            continue;
        }
        let n = (vis_w * 4).min(((patch.len() - si) / 4) * 4).min(((fb.len() - di) / 4) * 4);
        if n == 0 {
            continue;
        }
        let src = &patch[si..si + n];
        let dst = &mut fb[di..di + n];
        if dst != src {
            dst.copy_from_slice(src);
            changed = true;
        }
    }
    changed
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
    /// patch extends beyond it. Returns whether the canvas CHANGED (any pixel
    /// differs, or the canvas grew) — the caller marks the region dirty only
    /// then, so byte-identical RDP repaints are not needlessly re-OCR'd.
    /// Returns false when the patch cannot be composited (oversized extent or
    /// decode failure) — fail-open for that region — and also false for a
    /// successful but pixel-identical paint (nothing new to analyze).
    ///
    /// Skipping unchanged paints is safe for the guard: the shadow canvas
    /// already holds those exact pixels, which were analyzed when they first
    /// appeared, so there is no unanalyzed content to leak. The PDU is still
    /// forwarded by the caller regardless of this return — only the OCR/analysis
    /// decision is affected.
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

        let grew = if right > self.w || bottom > self.h {
            self.grow(right, bottom);
            true
        } else {
            false
        };
        let changed = composite_bitmap(
            &mut self.fb,
            self.w,
            self.h,
            &rgba,
            patch.width,
            patch.height,
            patch.x,
            patch.y,
        );
        // A grow always exposes new canvas area, so treat it as changed even if
        // the painted pixels happened to match the (zeroed) new region.
        changed || grew
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
    fn composite_reports_changed_only_on_pixel_change() {
        let mut c = ShadowCanvas::new("t");
        let red = [0x00, 0x00, 0xff]; // BGR red

        // First paint of a region changes pixels (and grows the canvas).
        assert!(c.composite(&patch(0, 0, 4, 4, red)), "first paint must report changed");
        // Repainting the SAME region with the SAME color changes nothing.
        assert!(
            !c.composite(&patch(0, 0, 4, 4, red)),
            "byte-identical repaint must report unchanged"
        );
        // Repainting with a different color changes pixels again.
        let blue = [0xff, 0x00, 0x00]; // BGR blue
        assert!(
            c.composite(&patch(0, 0, 4, 4, blue)),
            "different color must report changed"
        );
        // And again the identical (now-blue) repaint is unchanged.
        assert!(
            !c.composite(&patch(0, 0, 4, 4, blue)),
            "identical blue repaint must report unchanged"
        );
    }

    #[test]
    fn first_paint_of_zero_pixels_counts_as_changed() {
        // The canvas starts zeroed. A first paint whose decoded RGBA happens to
        // be black (0,0,0) is byte-identical to the zeroed framebuffer — but it
        // is NEW to the client and MUST be treated as changed (so it gets
        // analyzed), which the `grew` guard guarantees. If this ever returned
        // false, black-on-black content could reach the client unanalyzed.
        let mut c = ShadowCanvas::new("t");
        let black = [0x00, 0x00, 0x00]; // BGR black -> RGBA (0,0,0,255)
        assert!(
            c.composite(&patch(0, 0, 4, 4, black)),
            "first paint into new canvas area must be changed even if pixels are black"
        );
        // A second identical black paint over the same (now-black) region is a
        // true no-op and is correctly skipped.
        assert!(
            !c.composite(&patch(0, 0, 4, 4, black)),
            "identical black repaint over existing black must be unchanged"
        );
    }

    #[test]
    fn composite_clips_out_of_bounds() {
        let mut fb = vec![0u8; 4 * 4 * 4];
        let p = [1u8, 2, 3, 255].repeat(4); // 2x2 RGBA
        composite_bitmap(&mut fb, 4, 4, &p, 2, 2, 3, 3); // bottom-right corner, clips
        assert_eq!(&fb[(3 * 4 + 3) * 4..(3 * 4 + 3) * 4 + 4], &[1, 2, 3, 255]);
    }

    // ---- equivalence oracles for the row-wise hot-loop rewrites -----------
    //
    // The row-wise composite_bitmap and to_rgba replaced per-pixel loops for
    // speed (measured 5.5-10.6x on composite). These tests pin the new code
    // to a direct transcription of the ORIGINAL per-pixel implementations
    // across clipping, truncation and changed-flag cases, so the rewrite can
    // never silently change composite semantics (which the leak guarantees
    // build on).

    fn composite_bitmap_reference(
        fb: &mut [u8],
        fb_width: usize,
        fb_height: usize,
        patch: &[u8],
        patch_w: usize,
        patch_h: usize,
        dst_x: usize,
        dst_y: usize,
    ) -> bool {
        let mut changed = false;
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
                if si + 4 <= patch.len()
                    && di + 4 <= fb.len()
                    && fb[di..di + 4] != patch[si..si + 4]
                {
                    fb[di..di + 4].copy_from_slice(&patch[si..si + 4]);
                    changed = true;
                }
            }
        }
        changed
    }

    fn to_rgba_reference(src: &[u8], width: usize, height: usize, bpp: usize) -> Option<Vec<u8>> {
        let bytes_per_pixel = match bpp {
            16 => 2,
            24 => 3,
            32 => 4,
            _ => return None,
        };
        let src_row_bytes = width * bytes_per_pixel;
        let mut dst = vec![0u8; width * height * 4];
        for row in 0..height {
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
                        let pixel = u16::from_le_bytes([src[si], src[si + 1]]);
                        let r = (pixel >> 11) & 0x1f;
                        let g = (pixel >> 5) & 0x3f;
                        let b = pixel & 0x1f;
                        dst[di] = ((r << 3) | (r >> 2)) as u8;
                        dst[di + 1] = ((g << 2) | (g >> 4)) as u8;
                        dst[di + 2] = ((b << 3) | (b >> 2)) as u8;
                        dst[di + 3] = 255;
                    }
                    _ => {
                        dst[di] = src[si + 2];
                        dst[di + 1] = src[si + 1];
                        dst[di + 2] = src[si];
                        dst[di + 3] = 255;
                    }
                }
            }
        }
        Some(dst)
    }

    /// Deterministic pseudo-random bytes (no dev-dependency needed).
    fn pattern(len: usize, seed: u64) -> Vec<u8> {
        let mut state = seed.wrapping_mul(0x9E37_79B9_7F4A_7C15) | 1;
        (0..len)
            .map(|_| {
                state ^= state << 13;
                state ^= state >> 7;
                state ^= state << 17;
                (state >> 32) as u8
            })
            .collect()
    }

    #[test]
    fn composite_matches_perpixel_reference() {
        let (fw, fh) = (64usize, 48usize);
        // (patch_w, patch_h, dst_x, dst_y, truncate_bytes) covering: interior,
        // right-edge clip, bottom-edge clip, both-edge clip, fully off-canvas
        // x, truncated patch data (mid-row and mid-pixel), 1x1, full-canvas.
        let cases = [
            (16, 8, 4, 4, 0),
            (16, 8, 56, 4, 0),     // clips right
            (16, 8, 4, 44, 0),     // clips bottom
            (30, 30, 50, 40, 0),   // clips both
            (8, 8, 64, 0, 0),      // fully off right edge
            (8, 8, 70, 10, 0),     // beyond right edge
            (16, 8, 4, 4, 200),    // truncated: whole pixels lost
            (16, 8, 4, 4, 3),      // truncated mid-pixel
            (1, 1, 63, 47, 0),     // 1x1 at the last pixel
            (64, 48, 0, 0, 0),     // full canvas
        ];
        for (i, &(pw, ph, dx, dy, trunc)) in cases.iter().enumerate() {
            let mut patch = pattern(pw * ph * 4, i as u64 + 1);
            patch.truncate(patch.len().saturating_sub(trunc));
            // Start both canvases from the same non-trivial content so the
            // changed-flag has real work to do; then repaint identically to
            // check the unchanged path too.
            for repaint in 0..2 {
                let base = pattern(fw * fh * 4, 0xbeef + repaint);
                let mut fb_new = base.clone();
                let mut fb_ref = base;
                if repaint == 1 {
                    // Pre-paint so the second composite is byte-identical.
                    composite_bitmap(&mut fb_new, fw, fh, &patch, pw, ph, dx, dy);
                    composite_bitmap_reference(&mut fb_ref, fw, fh, &patch, pw, ph, dx, dy);
                }
                let c_new = composite_bitmap(&mut fb_new, fw, fh, &patch, pw, ph, dx, dy);
                let c_ref = composite_bitmap_reference(&mut fb_ref, fw, fh, &patch, pw, ph, dx, dy);
                assert_eq!(c_new, c_ref, "case {i} repaint {repaint}: changed flag diverged");
                assert_eq!(fb_new, fb_ref, "case {i} repaint {repaint}: framebuffer diverged");
            }
        }
    }

    #[test]
    fn to_rgba_matches_perpixel_reference() {
        for bpp in [16usize, 24, 32] {
            let bytes = bpp / 8;
            for &(w, h) in &[(1usize, 1usize), (7, 3), (33, 9), (64, 16)] {
                let full = pattern(w * h * bytes, (w * h * bpp) as u64);
                // Full data, truncated mid-row, truncated mid-pixel, and a
                // whole missing row.
                for trunc in [0usize, bytes * (w / 2), 1, w * bytes] {
                    let src = &full[..full.len().saturating_sub(trunc)];
                    assert_eq!(
                        to_rgba(src, w, h, bpp),
                        to_rgba_reference(src, w, h, bpp),
                        "bpp={bpp} {w}x{h} trunc={trunc}"
                    );
                }
            }
        }
        assert_eq!(to_rgba(&[0u8; 8], 2, 1, 8), None, "unsupported bpp still rejected");
    }
}
