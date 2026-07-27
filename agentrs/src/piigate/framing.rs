//! PDU framing and bitmap extraction from the server->client RDP stream.
//!
//! Port of `gateway/rdp/wasm/src/lib.rs` (the gateway's WASM parser), using
//! the same `ironrdp-pdu` types natively. Framing handles Fast-Path and
//! X224/TPKT; bitmap extraction handles Fast-Path bitmap updates including
//! Fast-Path fragmentation (First/Next/Last/Single — mirrors IronRDP's
//! `CompleteData`).

use ironrdp_core::{Decode as _, ReadCursor};
use ironrdp_pdu::bitmap::Compression;
use ironrdp_pdu::fast_path::{
    FastPathHeader, FastPathUpdate, FastPathUpdatePdu, Fragmentation, UpdateCode,
};
use tracing::{debug, warn};

/// One bitmap rectangle extracted from a Fast-Path bitmap update. Geometry is
/// converted to usize at extraction so downstream code never re-validates
/// wire-format integer types.
#[derive(Debug, Clone)]
pub struct BitmapPatch {
    pub x: usize,
    pub y: usize,
    pub width: usize,
    pub height: usize,
    pub bits_per_pixel: usize,
    pub compressed: bool,
    pub data: Vec<u8>,
}

/// Tries to find the size of a complete PDU at the start of `data`. Handles
/// both Fast-Path and X224/TPKT PDUs so non-Fast-Path data can be skipped.
/// Returns 0 if incomplete (need more bytes). Bytes with unknown action bits
/// are "framed" one byte at a time — never dropped.
///
/// Parity note: the Go gateway frames via a WASM `get_pdu_size` whose Go
/// wrapper (`parser.GetPduSize`) only returns an error on WASM-mechanism
/// faults (allocate/call) — never on framing. A zero-length or
/// not-yet-complete PDU returns 0 there too, which `Ingest` treats as
/// "incomplete, keep buffering". This function's single 0 return therefore
/// matches the Go behavior exactly; there is no immediate fail-open on a
/// zero-length frame in either implementation (the fail-open path is the
/// unframeable-tail cap in the gate).
pub fn pdu_size(data: &[u8]) -> usize {
    if data.len() < 2 {
        return 0;
    }
    let action = data[0] & 0x03;
    let pdu_length = match action {
        // X224/TPKT: 4-byte header with big-endian length in bytes 2-3.
        0x03 => {
            if data.len() < 4 {
                return 0;
            }
            ((data[2] as usize) << 8) | (data[3] as usize)
        }
        // Fast-Path: PER-encoded length starting at byte 1.
        0x00 => {
            let a = data[1];
            if a & 0x80 != 0 {
                if data.len() < 3 {
                    return 0;
                }
                (((a as usize) & 0x7f) << 8) | (data[2] as usize)
            } else {
                a as usize
            }
        }
        // Unknown action type — skip one byte to avoid getting stuck.
        _ => 1,
    };
    if pdu_length == 0 || pdu_length > data.len() {
        return 0; // incomplete, need more data
    }
    pdu_length
}

/// Stateful Fast-Path parser: extracts bitmap patches from complete PDUs,
/// reassembling fragmented updates across calls. One instance per stream —
/// fragment state must not be shared between sessions.
#[derive(Default)]
pub struct FastPathParser {
    fragment_data: Vec<u8>,
    fragment_update_code: Option<UpdateCode>,
}

impl FastPathParser {
    pub fn new() -> Self {
        Self::default()
    }

    /// Parses one complete PDU (as framed by [`pdu_size`]) and returns any
    /// bitmap patches it yields. Non-Fast-Path PDUs, non-bitmap updates, and
    /// fragments still being buffered yield an empty vec. Decode errors are
    /// logged and yield an empty vec (fail-open: the PDU still forwards, it
    /// just carries no analyzable pixels).
    pub fn parse(&mut self, pdu: &[u8]) -> Vec<BitmapPatch> {
        if pdu.is_empty() || pdu[0] & 0x03 != 0x00 {
            return Vec::new(); // not Fast-Path
        }

        let mut cursor = ReadCursor::new(pdu);
        let header = match FastPathHeader::decode(&mut cursor) {
            Ok(h) => h,
            Err(e) => {
                debug!("piigate: not fast-path (len={}): {e:?}", pdu.len());
                return Vec::new();
            }
        };
        // PDUs with no data payload (e.g. keep-alives).
        if header.data_length == 0 {
            return Vec::new();
        }

        let update_pdu = match FastPathUpdatePdu::decode(&mut cursor) {
            Ok(p) => p,
            Err(e) => {
                debug!("piigate: failed to decode fast-path update PDU: {e:?}");
                return Vec::new();
            }
        };

        // Fragment reassembly (same logic as IronRDP's CompleteData).
        let complete = match update_pdu.fragmentation {
            Fragmentation::Single => {
                if !self.fragment_data.is_empty() {
                    warn!(
                        "piigate: dropping pending fragment data ({} bytes)",
                        self.fragment_data.len()
                    );
                    self.fragment_data.clear();
                    self.fragment_update_code = None;
                }
                Some((update_pdu.update_code, update_pdu.data.to_vec()))
            }
            Fragmentation::First => {
                if !self.fragment_data.is_empty() {
                    warn!(
                        "piigate: dropping pending fragment data ({} bytes)",
                        self.fragment_data.len()
                    );
                }
                self.fragment_data = update_pdu.data.to_vec();
                self.fragment_update_code = Some(update_pdu.update_code);
                None
            }
            Fragmentation::Next => {
                if self.fragment_update_code.is_some() {
                    self.fragment_data.extend_from_slice(update_pdu.data);
                } else {
                    warn!("piigate: fragment Next without First, ignoring");
                }
                None
            }
            Fragmentation::Last => match self.fragment_update_code.take() {
                Some(code) => {
                    self.fragment_data.extend_from_slice(update_pdu.data);
                    Some((code, std::mem::take(&mut self.fragment_data)))
                }
                None => {
                    warn!("piigate: fragment Last without First, ignoring");
                    None
                }
            },
        };

        let Some((update_code, data)) = complete else {
            return Vec::new(); // buffering fragments
        };

        let update = match FastPathUpdate::decode_with_code(&data, update_code) {
            Ok(u) => u,
            Err(e) => {
                debug!("piigate: failed to decode update code {update_code:?}: {e:?}");
                return Vec::new();
            }
        };

        match update {
            FastPathUpdate::Bitmap(bitmap_update) => bitmap_update
                .rectangles
                .iter()
                .filter_map(|bmp| {
                    let width = bmp.width as usize;
                    let height = bmp.height as usize;
                    if bmp.bitmap_data.is_empty() || width == 0 || height == 0 {
                        return None;
                    }
                    Some(BitmapPatch {
                        x: bmp.rectangle.left as usize,
                        y: bmp.rectangle.top as usize,
                        width,
                        height,
                        bits_per_pixel: bmp.bits_per_pixel as usize,
                        compressed: bmp
                            .compression_flags
                            .contains(Compression::BITMAP_COMPRESSION),
                        data: bmp.bitmap_data.to_vec(),
                    })
                })
                .collect(),
            _ => Vec::new(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::piigate::testpdu::{
        fast_path_bitmap_pdu, fragmented_fast_path_bitmap_pdu, tpkt_pdu, TestRect, MAGENTA,
    };

    #[test]
    fn pdu_size_tpkt() {
        let pdu = tpkt_pdu(&[1, 2, 3]);
        assert_eq!(pdu_size(&pdu), pdu.len());
        assert_eq!(pdu_size(&pdu[..3]), 0); // incomplete
    }

    #[test]
    fn pdu_size_fast_path_short_and_long_form() {
        // Short form: length fits in 7 bits.
        let short = [0x00u8, 0x04, 0xaa, 0xbb];
        assert_eq!(pdu_size(&short), 4);
        // Long form (0x80 bit set), as produced by the test builder.
        let pdu = fast_path_bitmap_pdu(&[TestRect::new(0, 0, 4, 4, MAGENTA)]);
        assert_eq!(pdu_size(&pdu), pdu.len());
        assert_eq!(pdu_size(&pdu[..2]), 0); // long form needs 3 header bytes
    }

    #[test]
    fn pdu_size_unknown_action_skips_one_byte() {
        assert_eq!(pdu_size(&[0xfe, 0xfe]), 1);
    }

    #[test]
    fn pdu_size_incomplete() {
        assert_eq!(pdu_size(&[0x03]), 0);
        let pdu = tpkt_pdu(&[1, 2, 3, 4, 5, 6]);
        assert_eq!(pdu_size(&pdu[..5]), 0);
    }

    #[test]
    fn parse_extracts_bitmap_patches() {
        let mut p = FastPathParser::new();
        let pdu = fast_path_bitmap_pdu(&[TestRect::new(40, 40, 8, 8, MAGENTA)]);
        let patches = p.parse(&pdu);
        assert_eq!(patches.len(), 1);
        let b = &patches[0];
        assert_eq!((b.x, b.y, b.width, b.height), (40, 40, 8, 8));
        assert_eq!(b.bits_per_pixel, 24);
        assert!(!b.compressed);
        assert_eq!(b.data.len(), 8 * 8 * 3);
    }

    #[test]
    fn parse_ignores_tpkt() {
        let mut p = FastPathParser::new();
        assert!(p.parse(&tpkt_pdu(&[1, 2, 3])).is_empty());
    }

    #[test]
    fn parse_reassembles_fragments() {
        let mut p = FastPathParser::new();
        let frags = fragmented_fast_path_bitmap_pdu(3, &[TestRect::new(40, 40, 8, 8, MAGENTA)]);
        assert!(p.parse(&frags[0]).is_empty());
        assert!(p.parse(&frags[1]).is_empty());
        let patches = p.parse(&frags[2]);
        assert_eq!(patches.len(), 1);
        assert_eq!(patches[0].data.len(), 8 * 8 * 3);
    }
}
