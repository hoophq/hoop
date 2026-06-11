//! Pixel redaction: rewrite a cleared screen region with detected PII areas
//! blanked, as fresh uncompressed Fast-Path bitmap PDUs.
//!
//! Redaction turns the gate from hold-and-release (forward original bytes)
//! into hold-and-rewrite. Rather than surgically editing the original
//! (possibly RLE-compressed, possibly fragmented) wire PDUs, we re-emit the
//! affected bands from the shadow canvas — the post-composite, already-
//! analyzed pixels — with the detected bounding boxes painted over. Emitting
//! as UNCOMPRESSED bitmap data is always valid and needs no RLE encoder.
//!
//! The emitted bytes use the exact TS_UPDATE_BITMAP / Fast-Path layout the
//! framing parser decodes (verified end to end by the framing tests, which
//! decode these same bytes through the real ironrdp decoder). This keeps the
//! rewriter independent of the ironrdp encoder API surface.

use super::bands::YBand;
use super::presidio::EntityDetection;

/// The Fast-Path PDU length is PER-encoded in 2 bytes (long form), capping a
/// whole PDU at 0x3FFF = 16383 bytes. That envelope — not the u16
/// bitmapLength — is the binding limit, so we tile the redacted region so
/// each emitted PDU's pixel payload fits comfortably under it. Budget the
/// pixels at 0x3FFF minus a margin for the Fast-Path + update + bitmap-data
/// headers (~30 bytes); 24bpp = 3 bytes/pixel.
const MAX_PDU_PIXEL_BYTES: usize = 0x3fff - 64;
const MAX_TILE_PIXELS: usize = MAX_PDU_PIXEL_BYTES / 3;

/// A blanking color in RGBA (opaque black) — what a redacted region renders
/// as on the client. Black is unambiguous and matches the "blanked box"
/// described to the user.
const REDACT_RGBA: [u8; 4] = [0, 0, 0, 255];

/// Builds the replacement wire bytes for a cleared region of the shadow
/// canvas, with every detected bbox blanked. Returns one or more complete
/// Fast-Path bitmap PDUs (concatenated) that repaint exactly the analyzed
/// bands. Returns an empty vec when there is nothing to emit (no bands).
///
/// `fb` is the RGBA shadow canvas (top-down, `fb_w*fb_h*4` bytes); `bands`
/// are the analyzed dirty bands; `detections` are the PII bboxes to blank
/// (screen-space pixels). The function does not mutate `fb` — it copies the
/// band rows, blanks the detected rects in the copy, and encodes.
pub fn redact_bands(
    fb: &[u8],
    fb_w: usize,
    fb_h: usize,
    bands: &[YBand],
    detections: &[EntityDetection],
) -> Vec<u8> {
    let mut out = Vec::new();
    for band in bands {
        let y0 = band.y0.min(fb_h);
        let y1 = band.y1.min(fb_h);
        if y0 >= y1 || fb_w == 0 {
            continue;
        }
        // Tile the band into rectangles small enough for a u16 bitmapLength.
        let max_rows = (MAX_TILE_PIXELS / fb_w).max(1);
        let mut ty = y0;
        while ty < y1 {
            let tile_h = (y1 - ty).min(max_rows);
            out.extend_from_slice(&emit_tile(fb, fb_w, ty, tile_h, detections));
            ty += tile_h;
        }
    }
    out
}

/// Emits one uncompressed Fast-Path bitmap PDU covering the full-width tile
/// at rows [y, y+h), with detected rects blanked.
fn emit_tile(
    fb: &[u8],
    fb_w: usize,
    y: usize,
    h: usize,
    detections: &[EntityDetection],
) -> Vec<u8> {
    // Copy the tile rows out of the canvas (top-down RGBA), blanking any
    // detected rect that overlaps this tile.
    let mut tile = vec![0u8; fb_w * h * 4];
    for row in 0..h {
        let src = (y + row) * fb_w * 4;
        let dst = row * fb_w * 4;
        tile[dst..dst + fb_w * 4].copy_from_slice(&fb[src..src + fb_w * 4]);
    }
    for d in detections {
        blank_rect_in_tile(&mut tile, fb_w, h, y, d);
    }

    // Encode tile RGBA (top-down) -> 24bpp BGR bottom-up uncompressed bitmap.
    let bitmap = rgba_to_bgr_bottom_up(&tile, fb_w, h);
    encode_fast_path_bitmap(0, y as u16, fb_w as u16, h as u16, &bitmap)
}

/// Blanks the part of a detection rect that overlaps the tile at canvas rows
/// [tile_y, tile_y+tile_h), in the tile's own (top-down RGBA) coordinates.
fn blank_rect_in_tile(
    tile: &mut [u8],
    fb_w: usize,
    tile_h: usize,
    tile_y: usize,
    d: &EntityDetection,
) {
    let rx0 = d.x.min(fb_w);
    let rx1 = (d.x + d.width).min(fb_w);
    let ry0 = d.y.max(tile_y);
    let ry1 = (d.y + d.height).min(tile_y + tile_h);
    if rx0 >= rx1 || ry0 >= ry1 {
        return;
    }
    for cy in ry0..ry1 {
        let row = (cy - tile_y) * fb_w * 4;
        for cx in rx0..rx1 {
            let p = row + cx * 4;
            tile[p..p + 4].copy_from_slice(&REDACT_RGBA);
        }
    }
}

/// Converts top-down RGBA to the RDP wire format: 24bpp BGR, bottom-up rows
/// (the inverse of canvas::to_rgba for 24bpp).
fn rgba_to_bgr_bottom_up(rgba: &[u8], w: usize, h: usize) -> Vec<u8> {
    let mut out = vec![0u8; w * h * 3];
    for row in 0..h {
        // bottom-up: wire row 0 is the bottom screen row.
        let src_row = h - 1 - row;
        let src = src_row * w * 4;
        let dst = row * w * 3;
        for col in 0..w {
            let si = src + col * 4;
            let di = dst + col * 3;
            out[di] = rgba[si + 2]; // B
            out[di + 1] = rgba[si + 1]; // G
            out[di + 2] = rgba[si]; // R
        }
    }
    out
}

/// Encodes one uncompressed 24bpp Fast-Path bitmap update PDU for a single
/// rectangle at (x, y) of size (w, h). `bitmap` is 24bpp BGR bottom-up,
/// `w*h*3` bytes. Layout matches the framing parser's expectations.
fn encode_fast_path_bitmap(x: u16, y: u16, w: u16, h: u16, bitmap: &[u8]) -> Vec<u8> {
    // Self-defensive: this is security-sensitive serialization. A future
    // refactor that violates these invariants must fail loudly, not emit a
    // malformed PDU (e.g. underflow in the inclusive destRight/destBottom).
    assert!(w > 0 && h > 0, "bitmap dimensions must be > 0");
    assert_eq!(bitmap.len(), w as usize * h as usize * 3, "bitmap length mismatch");

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
    payload.extend_from_slice(&24u16.to_le_bytes()); // bitsPerPixel
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
    // redact_bands keeps the pixel payload well under this; assert the
    // invariant so a future change to the tile budget can't silently emit an
    // unparseable PDU.
    debug_assert!(total <= 0x3fff, "Fast-Path PDU {total} exceeds PER length cap 0x3FFF");
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

    /// A solid-magenta canvas, redact a sub-rect, and verify the emitted PDUs
    /// (a) decode through the real parser, (b) composite to a canvas where the
    /// redacted rect is black and the rest is preserved.
    #[test]
    fn redacted_pdus_decode_and_blank_only_the_bbox() {
        let (w, h) = (64usize, 40usize);
        // magenta RGBA canvas.
        let mut fb = vec![0u8; w * h * 4];
        for px in fb.chunks_exact_mut(4) {
            px.copy_from_slice(&[255, 0, 255, 255]);
        }
        let bands = [YBand { y0: 0, y1: h }];
        let dets = [det(10, 8, 20, 12)];

        let wire = redact_bands(&fb, w, h, &bands, &dets);
        assert!(!wire.is_empty());

        // Decode + composite via the real parser/canvas onto a fresh canvas.
        let mut parser = FastPathParser::new();
        let mut canvas = crate::piigate::canvas::ShadowCanvas::new("t");
        let mut tail = wire.as_slice();
        while !tail.is_empty() {
            let size = crate::piigate::framing::pdu_size(tail);
            assert!(size > 0 && size <= tail.len(), "emitted PDU must frame");
            for patch in parser.parse(&tail[..size]) {
                assert!(canvas.composite(&patch));
            }
            tail = &tail[size..];
        }
        assert_eq!((canvas.w, canvas.h), (w, h));

        // Inside the redacted rect: black. Outside: still magenta.
        let pixel = |cx: usize, cy: usize| {
            let p = (cy * w + cx) * 4;
            [canvas.fb[p], canvas.fb[p + 1], canvas.fb[p + 2]]
        };
        assert_eq!(pixel(15, 12), [0, 0, 0], "redacted rect must be black");
        assert_eq!(pixel(0, 0), [255, 0, 255], "outside rect must be preserved");
        assert_eq!(pixel(40, 30), [255, 0, 255], "outside rect must be preserved");
        // Edges: (10,8) inclusive start, (29,19) inclusive end.
        assert_eq!(pixel(10, 8), [0, 0, 0]);
        assert_eq!(pixel(29, 19), [0, 0, 0]);
        assert_eq!(pixel(30, 20), [255, 0, 255]);
    }

    #[test]
    fn tiles_large_bands_into_valid_rectangles() {
        // A wide canvas forces multiple tiles (max_rows = 0xffff/3/w).
        let w = 1024usize;
        let h = 100usize;
        let fb = vec![0u8; w * h * 4];
        let bands = [YBand { y0: 0, y1: h }];
        let wire = redact_bands(&fb, w, h, &bands, &[]);

        // Every emitted PDU must frame and decode, and each bitmapLength must
        // be <= 0xffff (the reason for tiling).
        let mut parser = FastPathParser::new();
        let mut tail = wire.as_slice();
        let mut tiles = 0;
        while !tail.is_empty() {
            let size = crate::piigate::framing::pdu_size(tail);
            assert!(size > 0 && size <= tail.len());
            assert!(size <= 0x3fff, "Fast-Path PER length caps a PDU at 0x3FFF");
            let patches = parser.parse(&tail[..size]);
            assert_eq!(patches.len(), 1);
            assert!(patches[0].data.len() <= MAX_PDU_PIXEL_BYTES);
            tiles += 1;
            tail = &tail[size..];
        }
        // 1024*100*3 = 307200 > 65535, so it must have tiled.
        assert!(tiles > 1, "large band must tile into multiple rectangles");
    }

    #[test]
    fn no_bands_emits_nothing() {
        let fb = vec![0u8; 64 * 40 * 4];
        assert!(redact_bands(&fb, 64, 40, &[], &[]).is_empty());
    }

    /// The security-critical preservation property: redaction blanks ONLY the
    /// detected rect and reproduces every other pixel of the analyzed bands
    /// exactly — across tile/band seams, on a non-uniform canvas.
    #[test]
    fn redaction_preserves_all_non_detected_pixels_across_tiles() {
        let (w, h) = (4096usize, 8usize); // width forces 1 row per tile
        let mut fb = vec![0u8; w * h * 4];
        for y in 0..h {
            for x in 0..w {
                let i = (y * w + x) * 4;
                fb[i] = (x % 251) as u8;
                fb[i + 1] = (y % 251) as u8;
                fb[i + 2] = ((x + y) % 251) as u8;
                fb[i + 3] = 255;
            }
        }
        let bands = [YBand { y0: 0, y1: h }];
        let dets = [det(1000, 1, 200, 5)]; // spans tile (row) seams

        let wire = redact_bands(&fb, w, h, &bands, &dets);
        let mut parser = FastPathParser::new();
        let mut canvas = crate::piigate::canvas::ShadowCanvas::new("t");
        let mut tail = wire.as_slice();
        while !tail.is_empty() {
            let size = crate::piigate::framing::pdu_size(tail);
            assert!(size > 0 && size <= tail.len());
            for patch in parser.parse(&tail[..size]) {
                assert!(canvas.composite(&patch));
            }
            tail = &tail[size..];
        }

        for y in 0..h {
            for x in 0..w {
                let i = (y * w + x) * 4;
                let in_det = (1000..1200).contains(&x) && (1..6).contains(&y);
                if in_det {
                    assert_eq!(&canvas.fb[i..i + 3], &[0, 0, 0], "detected px ({x},{y}) must be black");
                } else {
                    assert_eq!(&canvas.fb[i..i + 4], &fb[i..i + 4], "non-detected px ({x},{y}) must be preserved");
                }
            }
        }
    }

    /// Max-width (4K) canvas: every emitted PDU must frame, stay under the
    /// Fast-Path PER cap, and decode to exactly one row (the tiling floor).
    #[test]
    fn max_width_canvas_tiles_one_row_per_pdu() {
        let (w, h) = (4096usize, 3usize);
        let fb = vec![0u8; w * h * 4];
        let bands = [YBand { y0: 0, y1: h }];
        let wire = redact_bands(&fb, w, h, &bands, &[]);

        let mut parser = FastPathParser::new();
        let mut tail = wire.as_slice();
        let mut rows = 0;
        while !tail.is_empty() {
            let size = crate::piigate::framing::pdu_size(tail);
            assert!(size > 0 && size <= tail.len());
            assert!(size <= 0x3fff, "PDU must fit the Fast-Path PER cap");
            let patches = parser.parse(&tail[..size]);
            assert_eq!(patches.len(), 1);
            assert_eq!(patches[0].width, w);
            assert_eq!(patches[0].height, 1, "4K width must tile to one row per PDU");
            rows += 1;
            tail = &tail[size..];
        }
        assert_eq!(rows, h);
    }

    #[test]
    fn rgba_bgr_roundtrip_is_inverse_of_to_rgba() {
        // rgba_to_bgr_bottom_up then canvas::to_rgba must be identity.
        let (w, h) = (4usize, 3usize);
        let mut rgba = vec![0u8; w * h * 4];
        for (i, px) in rgba.chunks_exact_mut(4).enumerate() {
            px.copy_from_slice(&[(i * 7) as u8, (i * 11) as u8, (i * 13) as u8, 255]);
        }
        let bgr = rgba_to_bgr_bottom_up(&rgba, w, h);
        let back = crate::piigate::canvas::to_rgba(&bgr, w, h, 24).unwrap();
        assert_eq!(back, rgba);
    }
}
