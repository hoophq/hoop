//! Synthesized RDP PDUs for gate tests — port of the builders in
//! `gateway/rdp/piigate_test.go`. Compiled only for tests.

/// One solid-color bitmap rectangle for synthesized PDUs.
#[derive(Debug, Clone, Copy)]
pub struct TestRect {
    pub x: usize,
    pub y: usize,
    pub w: usize,
    pub h: usize,
    pub bgr: [u8; 3],
}

impl TestRect {
    pub fn new(x: usize, y: usize, w: usize, h: usize, bgr: [u8; 3]) -> Self {
        Self { x, y, w, h, bgr }
    }
}

pub const WHITE: [u8; 3] = [0xff, 0xff, 0xff];
/// BGR magenta: composites to RGBA (255, 0, 255) — the leak signature color.
pub const MAGENTA: [u8; 3] = [0xff, 0x00, 0xff];

/// Builds a minimal TPKT-framed PDU (0x03 0x00 len_hi len_lo payload). The
/// framer sizes TPKT PDUs but extracts no bitmaps from them, so these
/// exercise the hold/flush path without OCR.
pub fn tpkt_pdu(payload: &[u8]) -> Vec<u8> {
    let total = 4 + payload.len();
    assert!(total <= 0xffff, "test TPKT too large");
    let mut out = vec![0x03, 0x00, (total >> 8) as u8, (total & 0xff) as u8];
    out.extend_from_slice(payload);
    out
}

/// Builds a TS_UPDATE_BITMAP payload (updateType, nrect, TS_BITMAP_DATA...)
/// with one uncompressed 24bpp rectangle per TestRect.
pub fn bitmap_update_payload(rects: &[TestRect]) -> Vec<u8> {
    let mut upd = Vec::new();
    upd.extend_from_slice(&1u16.to_le_bytes()); // updateType = BITMAP
    upd.extend_from_slice(&(rects.len() as u16).to_le_bytes()); // numberRectangles
    for r in rects {
        assert!(r.w > 0 && r.h > 0 && r.w * r.h * 3 <= 0xffff, "invalid test rect");
        upd.extend_from_slice(&(r.x as u16).to_le_bytes()); // destLeft
        upd.extend_from_slice(&(r.y as u16).to_le_bytes()); // destTop
        upd.extend_from_slice(&((r.x + r.w - 1) as u16).to_le_bytes()); // destRight (inclusive)
        upd.extend_from_slice(&((r.y + r.h - 1) as u16).to_le_bytes()); // destBottom (inclusive)
        upd.extend_from_slice(&(r.w as u16).to_le_bytes()); // width
        upd.extend_from_slice(&(r.h as u16).to_le_bytes()); // height
        upd.extend_from_slice(&24u16.to_le_bytes()); // bitsPerPixel
        upd.extend_from_slice(&0u16.to_le_bytes()); // compressionFlags: uncompressed
        upd.extend_from_slice(&((r.w * r.h * 3) as u16).to_le_bytes()); // bitmapLength
        upd.extend(r.bgr.repeat(r.w * r.h)); // raw BGR rows
    }
    upd
}

/// Wraps an update payload chunk into one Fast-Path PDU with the given
/// fragmentation bits (0x0 Single, 0x1 Last, 0x2 First, 0x3 Next).
pub fn fast_path_pdu(frag: u8, payload: &[u8]) -> Vec<u8> {
    let mut pdu = Vec::new();
    // updateHeader: updateCode=1 (bitmap) in bits[0..4], fragmentation in
    // bits[4..6], no compression in bits[6..8].
    pdu.push(0x01 | (frag << 4));
    pdu.extend_from_slice(&(payload.len() as u16).to_le_bytes());
    pdu.extend_from_slice(payload);

    let total = 1 + 2 + pdu.len(); // fp header byte + 2-byte PER length + update PDU
    assert!(total <= 0x3fff, "test PDU too large for PER length");
    let mut out = vec![0x00, 0x80 | (total >> 8) as u8, (total & 0xff) as u8];
    out.extend_from_slice(&pdu);
    out
}

/// Synthesizes a complete (unfragmented) Fast-Path update PDU — the exact
/// wire format the parser frames and decodes. Multiple rects in one call
/// form a single (atomic) PDU.
pub fn fast_path_bitmap_pdu(rects: &[TestRect]) -> Vec<u8> {
    fast_path_pdu(0x0, &bitmap_update_payload(rects))
}

/// Splits one bitmap update across n Fast-Path fragments (First, Next...,
/// Last). The parser — like a real client — only reassembles and yields
/// bitmaps when the Last fragment arrives.
pub fn fragmented_fast_path_bitmap_pdu(n: usize, rects: &[TestRect]) -> Vec<Vec<u8>> {
    let payload = bitmap_update_payload(rects);
    assert!(n >= 2 && payload.len() >= n, "cannot split into {n} fragments");
    let chunk = payload.len().div_ceil(n);
    (0..n)
        .map(|i| {
            let lo = i * chunk;
            let hi = ((i + 1) * chunk).min(payload.len());
            let frag = if i == 0 {
                0x2 // First
            } else if i == n - 1 {
                0x1 // Last
            } else {
                0x3 // Next
            };
            fast_path_pdu(frag, &payload[lo..hi])
        })
        .collect()
}
