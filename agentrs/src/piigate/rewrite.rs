//! Pixel redaction: overpaint ONLY the detected PII bounding boxes with
//! fresh uncompressed Fast-Path bitmap PDUs.
//!
//! Redaction turns the gate from hold-and-release (forward original bytes)
//! into hold-and-rewrite. The original batch's bitmap PDUs are dropped by the
//! caller; we then forward the batch's non-bitmap PDUs plus these small
//! "blanking" PDUs that paint solid black over each detected rect.
//!
//! IMPORTANT — only the detected rects are emitted, never whole bands. An
//! earlier version repainted the entire analyzed band as uncompressed 24bpp
//! bitmaps on every detection; on a full-width band that is hundreds of KB of
//! uncompressed pixels re-sent ~once per second, which floods the client link
//! and overruns downstream Fast-Path parsers (the PDUs exceed what the
//! gateway/browser bitmap parsers accept). Painting just the bbox keeps each
//! redaction to a few small PDUs. The rest of the screen is already correct:
//! the dropped original bitmaps were composited into the shadow canvas and the
//! client still shows whatever it last received for those pixels; only the PII
//! rect needed to change, and we blank exactly that.
//!
//! Emitting UNCOMPRESSED bitmap data is always valid and needs no RLE encoder.
//! The emitted bytes use the exact TS_UPDATE_BITMAP / Fast-Path layout the
//! framing parser decodes (verified end to end by the framing tests, which
//! decode these same bytes through the real ironrdp decoder).

use super::presidio::EntityDetection;

/// The Fast-Path PDU length is PER-encoded in 2 bytes (long form), capping a
/// whole PDU at 0x3FFF = 16383 bytes. That envelope — not the u16
/// bitmapLength — is the binding limit, so we tile the repainted region so
/// each emitted PDU's pixel payload fits comfortably under it. Budget the
/// pixels at 0x3FFF minus a margin for the Fast-Path + update + bitmap-data
/// headers (~64 bytes); the per-pixel byte count depends on the depth.
const MAX_PDU_PIXEL_BYTES: usize = 0x3fff - 64;

/// Builds the replacement wire bytes that blank the detected PII rects.
/// Returns one or more complete Fast-Path bitmap PDUs (concatenated) that
/// paint solid black over each detection, clipped to the analyzed bands and
/// the framebuffer. Returns an empty vec when there is nothing to blank.
///
/// A rectangle in screen-space pixels.
#[derive(Debug, Clone, Copy)]
pub struct Rect {
    pub x: usize,
    pub y: usize,
    pub w: usize,
    pub h: usize,
}

impl Rect {
    // Saturating ends: geometry originates from wire-format u16s clamped to the
    // 4096 canvas cap, so this never saturates in practice — but a hostile or
    // future-widened value must not overflow a security-path computation.
    fn x1(&self) -> usize {
        self.x.saturating_add(self.w)
    }
    fn y1(&self) -> usize {
        self.y.saturating_add(self.h)
    }
    /// Whether this rect overlaps `other` (touching edges do not count).
    pub fn overlaps(&self, other: &Rect) -> bool {
        self.x < other.x1() && other.x < self.x1() && self.y < other.y1() && other.y < self.y1()
    }
}

/// Repaints a single bitmap region (`region`, screen-space — normally an
/// incoming bitmap patch's geometry that overlapped PII) from the shadow
/// canvas, with the detected PII bboxes blanked to black, as uncompressed
/// Fast-Path bitmap PDUs in the negotiated color depth.
///
/// This preserves the legitimate content of the region (it is copied from the
/// post-composite canvas) while guaranteeing the PII pixels are black — the
/// PII is never delivered. Only PII-overlapping regions are repainted; the
/// caller forwards non-overlapping bitmaps unmodified, so this stays small.
///
/// `fb` is the RGBA shadow canvas (`fb_w*fb_h*4`, top-down). `bits_per_pixel`
/// MUST be the negotiated session depth (the incoming bitmaps' bpp): a depth
/// mismatch is rejected by the client and nothing renders.
pub fn redact_region(
    fb: &[u8],
    fb_w: usize,
    fb_h: usize,
    region: Rect,
    detections: &[EntityDetection],
    bits_per_pixel: usize,
) -> Vec<u8> {
    let mut out = Vec::new();
    if fb_w == 0 || fb_h == 0 {
        return out;
    }
    // Defensive: the canvas framebuffer must be fb_w*fb_h RGBA. A short buffer
    // (canvas corruption or a future caller) must not panic the redaction path
    // via an out-of-bounds slice — emit nothing rather than crash.
    if fb.len() < fb_w.saturating_mul(fb_h).saturating_mul(4) {
        return out;
    }
    let (bpp, bytes_per_pixel) = match bits_per_pixel {
        16 => (16, 2),
        24 => (24, 3),
        // Unknown depth falls back to 32 (the IronRDP web client's preferred
        // pref_bits_per_pix). Never emit a zero-stride PDU.
        _ => (32, 4),
    };

    // Clip the region to the canvas.
    let rx0 = region.x.min(fb_w);
    let rx1 = region.x1().min(fb_w);
    let ry0 = region.y.min(fb_h);
    let ry1 = region.y1().min(fb_h);
    if rx0 >= rx1 || ry0 >= ry1 {
        return out;
    }
    // Tile the region in BOTH dimensions so every PDU's pixel payload fits the
    // Fast-Path PER length cap. Real RDP patches are small (a text line), so
    // this is normally a single tile; the 2D tiling guards pathological sizes.
    let max_tile_pixels = (MAX_PDU_PIXEL_BYTES / bytes_per_pixel).max(1);
    let tile_w_cap = max_tile_pixels.max(1);

    let mut tx = rx0;
    while tx < rx1 {
        let tile_w = (rx1 - tx).min(tile_w_cap);
        let max_rows = (max_tile_pixels / tile_w.max(1)).max(1);
        let mut ty = ry0;
        while ty < ry1 {
            let tile_h = (ry1 - ty).min(max_rows);
            // Copy this tile's RGBA rows/cols from the canvas, blanking any
            // detection that overlaps the tile.
            let mut rgba = vec![0u8; tile_w * tile_h * 4];
            for row in 0..tile_h {
                let src = ((ty + row) * fb_w + tx) * 4;
                let dst = row * tile_w * 4;
                rgba[dst..dst + tile_w * 4].copy_from_slice(&fb[src..src + tile_w * 4]);
            }
            for d in detections {
                blank_detection(&mut rgba, tile_w, tile_h, tx, ty, d);
            }
            let wire_pixels = rgba_to_wire_bottom_up(&rgba, tile_w, tile_h, bpp);
            out.extend_from_slice(&encode_fast_path_bitmap(
                tx as u16,
                ty as u16,
                tile_w as u16,
                tile_h as u16,
                bpp,
                bytes_per_pixel,
                &wire_pixels,
            ));
            ty += tile_h;
        }
        tx += tile_w;
    }
    out
}

/// Blanks (to black) the part of detection `d` that falls inside the tile at
/// canvas rows [tile_y, tile_y+tile_h) and columns [tile_x0, tile_x0+tile_w),
/// in the tile's own top-down RGBA coordinates.
fn blank_detection(
    rgba: &mut [u8],
    tile_w: usize,
    tile_h: usize,
    tile_x0: usize,
    tile_y: usize,
    d: &EntityDetection,
) {
    // Saturating adds: detection coordinates are clamped to the 4096 canvas in
    // practice, but the redaction path must never overflow on a malformed bbox.
    let dx0 = d.x.max(tile_x0);
    let dx1 = d.x.saturating_add(d.width).min(tile_x0.saturating_add(tile_w));
    let dy0 = d.y.max(tile_y);
    let dy1 = d.y.saturating_add(d.height).min(tile_y.saturating_add(tile_h));
    if dx0 >= dx1 || dy0 >= dy1 {
        return;
    }
    for cy in dy0..dy1 {
        let row = (cy - tile_y) * tile_w * 4;
        for cx in dx0..dx1 {
            let p = row + (cx - tile_x0) * 4;
            rgba[p..p + 4].copy_from_slice(&[0, 0, 0, 255]);
        }
    }
}

/// Inverse of [`canvas::to_rgba`]: top-down RGBA -> bottom-up wire pixels in
/// the given depth (16=RGB565 LE, 24=BGR, 32=BGRX).
fn rgba_to_wire_bottom_up(rgba: &[u8], w: usize, h: usize, bpp: usize) -> Vec<u8> {
    let bytes_per_pixel = match bpp {
        16 => 2,
        24 => 3,
        _ => 4,
    };
    let mut out = vec![0u8; w * h * bytes_per_pixel];
    let stride = w * bytes_per_pixel;
    for row in 0..h {
        let src_row = h - 1 - row; // bottom-up
        let src = src_row * w * 4;
        let dst = row * stride;
        for col in 0..w {
            let si = src + col * 4;
            let r = rgba[si];
            let g = rgba[si + 1];
            let b = rgba[si + 2];
            let di = dst + col * bytes_per_pixel;
            match bpp {
                16 => {
                    let p: u16 = (((r as u16) >> 3) << 11)
                        | (((g as u16) >> 2) << 5)
                        | ((b as u16) >> 3);
                    out[di..di + 2].copy_from_slice(&p.to_le_bytes());
                }
                24 => {
                    out[di] = b;
                    out[di + 1] = g;
                    out[di + 2] = r;
                }
                _ => {
                    out[di] = b;
                    out[di + 1] = g;
                    out[di + 2] = r;
                    out[di + 3] = 0; // X (ignored by the client)
                }
            }
        }
    }
    out
}

/// Encodes one uncompressed Fast-Path bitmap update PDU for a single rectangle
/// at (x, y) of size (w, h) in `bpp` color depth. `bitmap` is `w*h*bpp/8`
/// bytes. Layout matches the framing parser's expectations.
fn encode_fast_path_bitmap(
    x: u16,
    y: u16,
    w: u16,
    h: u16,
    bpp: usize,
    bytes_per_pixel: usize,
    bitmap: &[u8],
) -> Vec<u8> {
    // Self-defensive: this is security-sensitive serialization. A future
    // refactor that violates these invariants must fail loudly, not emit a
    // malformed PDU (e.g. underflow in the inclusive destRight/destBottom).
    assert!(w > 0 && h > 0, "bitmap dimensions must be > 0");
    assert_eq!(
        bitmap.len(),
        w as usize * h as usize * bytes_per_pixel,
        "bitmap length mismatch"
    );
    // bitmapLength is a u16 field; the tiling budget keeps it well under this,
    // but enforce at runtime so a future tiling change can't silently truncate.
    assert!(bitmap.len() <= u16::MAX as usize, "bitmap payload exceeds u16 bitmapLength");

    // TS_UPDATE_BITMAP_DATA: updateType + numberRectangles + one TS_BITMAP_DATA.
    let mut payload = Vec::with_capacity(2 + 2 + 18 + bitmap.len());
    payload.extend_from_slice(&1u16.to_le_bytes()); // updateType = BITMAP
    payload.extend_from_slice(&1u16.to_le_bytes()); // numberRectangles
    payload.extend_from_slice(&x.to_le_bytes()); // destLeft
    payload.extend_from_slice(&y.to_le_bytes()); // destTop
    payload.extend_from_slice(&(x + w - 1).to_le_bytes()); // destRight (inclusive)
    payload.extend_from_slice(&(y + h - 1).to_le_bytes()); // destBottom (inclusive)
    payload.extend_from_slice(&w.to_le_bytes()); // width
    payload.extend_from_slice(&h.to_le_bytes()); // height
    payload.extend_from_slice(&(bpp as u16).to_le_bytes()); // bitsPerPixel (negotiated depth)
    payload.extend_from_slice(&0u16.to_le_bytes()); // compressionFlags: uncompressed
    payload.extend_from_slice(&(bitmap.len() as u16).to_le_bytes()); // bitmapLength
    payload.extend_from_slice(bitmap);

    // Fast-Path update PDU: updateHeader(code=1 bitmap, frag=Single) + size.
    let mut update = Vec::with_capacity(3 + payload.len());
    update.push(0x01); // updateCode=1 (bitmap), fragmentation=Single, no compression
    update.extend_from_slice(&(payload.len() as u16).to_le_bytes());
    update.extend_from_slice(&payload);

    // Fast-Path PDU header: action(0) + PER-encoded length over the whole PDU.
    let total = 3 + update.len(); // fp byte + 2-byte length + update
    // The 2-byte PER long form caps the whole PDU at 0x3FFF. The tiling in
    // redact_region keeps the pixel payload well under this; enforce at RUNTIME
    // (not debug_assert) so a future change to the tile budget can never
    // silently emit an unparseable PDU into a security-sensitive stream.
    assert!(total <= 0x3fff, "Fast-Path PDU {total} exceeds PER length cap 0x3FFF");
    let mut pdu = Vec::with_capacity(total);
    pdu.push(0x00); // action = Fast-Path
    pdu.push(0x80 | (total >> 8) as u8); // PER long-form length high byte
    pdu.push((total & 0xff) as u8);
    pdu.extend_from_slice(&update);
    pdu
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::piigate::framing::FastPathParser;

    fn det(x: usize, y: usize, w: usize, h: usize) -> EntityDetection {
        EntityDetection {
            entity_type: "TEST".into(),
            score: 1.0,
            x,
            y,
            width: w,
            height: h,
        }
    }

    /// Build an RGBA canvas with a recognizable per-pixel pattern so a test
    /// can tell preserved pixels from blanked ones.
    fn patterned_canvas(w: usize, h: usize) -> Vec<u8> {
        let mut fb = vec![0u8; w * h * 4];
        for y in 0..h {
            for x in 0..w {
                let i = (y * w + x) * 4;
                fb[i] = (x % 251 + 1) as u8; // +1 so it's never pure black
                fb[i + 1] = (y % 251 + 1) as u8;
                fb[i + 2] = ((x + y) % 251 + 1) as u8;
                fb[i + 3] = 255;
            }
        }
        fb
    }

    fn decode_to_canvas(wire: &[u8], name: &str) -> crate::piigate::canvas::ShadowCanvas {
        let mut parser = FastPathParser::new();
        let mut canvas = crate::piigate::canvas::ShadowCanvas::new(name);
        let mut tail = wire;
        while !tail.is_empty() {
            let size = crate::piigate::framing::pdu_size(tail);
            assert!(size > 0 && size <= tail.len(), "emitted PDU must frame");
            assert!(size <= 0x3fff, "PDU must fit the Fast-Path PER cap");
            for patch in parser.parse(&tail[..size]) {
                assert!(canvas.composite(&patch));
            }
            tail = &tail[size..];
        }
        canvas
    }

    /// THE preservation property (the black-screen fix): repainting a region
    /// reproduces every non-PII pixel of that region exactly, and blanks only
    /// the detected bbox. Verified for 16/24/32 bpp through the real decoder.
    #[test]
    fn region_repaint_preserves_content_and_blanks_only_pii() {
        for bpp in [16usize, 24, 32] {
            let (w, h) = (1280usize, 200usize);
            let fb = patterned_canvas(w, h);
            // The incoming bitmap region (e.g. a repainted text line) and the
            // PII bbox inside it.
            let region = Rect { x: 0, y: 100, w: 1280, h: 24 };
            let dets = [det(200, 104, 180, 14)];

            let wire = redact_region(&fb, w, h, region, &dets, bpp);
            assert!(!wire.is_empty(), "bpp {bpp} must emit");
            let canvas = decode_to_canvas(&wire, "t");
            assert_eq!((canvas.w, canvas.h), (w, 124), "canvas spans the repainted region");

            // 16bpp is lossy (RGB565), so compare with a tolerance; 24/32 exact.
            let tol: i32 = if bpp == 16 { 8 } else { 0 };
            for y in region.y..region.y + region.h {
                for x in 0..w {
                    let i = (y * canvas.w + x) * 4;
                    let in_pii = (200..380).contains(&x) && (104..118).contains(&y);
                    if in_pii {
                        assert_eq!(
                            &canvas.fb[i..i + 3],
                            &[0, 0, 0],
                            "PII px ({x},{y}) must be black at bpp {bpp}"
                        );
                    } else {
                        for c in 0..3 {
                            let got = canvas.fb[i + c] as i32;
                            let want = fb[(y * w + x) * 4 + c] as i32;
                            assert!(
                                (got - want).abs() <= tol,
                                "non-PII px ({x},{y}) ch{c} must be preserved at bpp {bpp}: got {got} want {want}"
                            );
                        }
                    }
                }
            }
        }
    }

    /// The emitted PDUs use the negotiated color depth: bitsPerPixel field and
    /// payload stride must match (a mismatch is rejected by the client).
    #[test]
    fn region_repaint_honors_negotiated_bpp() {
        for (bpp, bytes) in [(16usize, 2usize), (24, 3), (32, 4)] {
            let (w, h) = (256usize, 64usize);
            let fb = patterned_canvas(w, h);
            let region = Rect { x: 0, y: 0, w, h };
            let wire = redact_region(&fb, w, h, region, &[det(8, 4, 40, 12)], bpp);
            assert!(!wire.is_empty());
            // bitsPerPixel field at fixed offset 22 in our PDU layout.
            let bpp_field = u16::from_le_bytes([wire[22], wire[23]]) as usize;
            assert_eq!(bpp_field, bpp, "emitted bitsPerPixel must equal negotiated depth");

            let mut parser = FastPathParser::new();
            let mut tail = wire.as_slice();
            while !tail.is_empty() {
                let size = crate::piigate::framing::pdu_size(tail);
                for patch in parser.parse(&tail[..size]) {
                    assert_eq!(patch.bits_per_pixel, bpp);
                    assert_eq!(patch.data.len(), patch.width * patch.height * bytes);
                }
                tail = &tail[size..];
            }
        }
    }

    #[test]
    fn empty_region_or_canvas_emits_nothing() {
        let fb = vec![0u8; 64 * 40 * 4];
        // Zero-area region.
        assert!(redact_region(&fb, 64, 40, Rect { x: 0, y: 0, w: 0, h: 10 }, &[], 32).is_empty());
        // Empty canvas.
        assert!(redact_region(&[], 0, 0, Rect { x: 0, y: 0, w: 10, h: 10 }, &[], 32).is_empty());
    }

    /// A region wider than one PDU's pixel budget tiles into PER-cap-safe PDUs.
    #[test]
    fn wide_region_tiles_under_per_cap() {
        let (w, h) = (8192usize, 40usize);
        let fb = patterned_canvas(w, h);
        let region = Rect { x: 0, y: 0, w, h };
        let wire = redact_region(&fb, w, h, region, &[], 32);
        let mut parser = FastPathParser::new();
        let mut tail = wire.as_slice();
        let mut tiles = 0;
        while !tail.is_empty() {
            let size = crate::piigate::framing::pdu_size(tail);
            assert!(size > 0 && size <= tail.len());
            assert!(size <= 0x3fff, "every tile must fit the Fast-Path PER cap");
            let patches = parser.parse(&tail[..size]);
            assert_eq!(patches.len(), 1);
            assert!(patches[0].data.len() <= MAX_PDU_PIXEL_BYTES);
            tiles += 1;
            tail = &tail[size..];
        }
        assert!(tiles > 1, "a wide region must tile");
    }

    /// Rect overlap detection (drives the selective forward-vs-repaint choice).
    #[test]
    fn rect_overlap() {
        let a = Rect { x: 10, y: 10, w: 20, h: 20 }; // [10,30)x[10,30)
        assert!(a.overlaps(&Rect { x: 25, y: 25, w: 10, h: 10 }));
        assert!(!a.overlaps(&Rect { x: 30, y: 10, w: 5, h: 5 }), "touching edge is not overlap");
        assert!(!a.overlaps(&Rect { x: 0, y: 0, w: 5, h: 5 }));
    }
}
