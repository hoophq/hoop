//! RDP Parser WASM Module
//!
//! This module provides functions to parse RDP PDUs and extract bitmap data
//! for session recording and replay.

use ironrdp_core::{Decode, ReadCursor};
use ironrdp_pdu::bitmap::Compression;
use ironrdp_pdu::fast_path::{
    FastPathHeader, FastPathUpdate, FastPathUpdatePdu, Fragmentation, UpdateCode,
};

pub mod walloc;
pub mod wlog;

/// Bitmap rectangle extracted from RDP stream
#[repr(C, packed)]
pub struct BitmapRect {
    pub x: u16,
    pub y: u16,
    pub right: u16,
    pub bottom: u16,
    pub width: u16,
    pub height: u16,
    pub bits_per_pixel: u16,
    pub data_offset: u32,
    pub data_len: u32,
    /// 1 if RLE compressed, 0 if raw
    pub compressed: u16,
}

// Global storage for parsed results
static mut PARSED_BITMAPS: Vec<BitmapRect> = Vec::new();
static mut PARSED_DATA: Vec<u8> = Vec::new();
static mut ERROR_MSG: Vec<u8> = Vec::new();

// Fragment reassembly state (mirrors IronRDP's CompleteData)
static mut FRAGMENT_DATA: Vec<u8> = Vec::new();
static mut FRAGMENT_UPDATE_CODE: Option<UpdateCode> = None;

/// Get the version of this parser
#[no_mangle]
pub extern "C" fn parser_version() -> u32 {
    6 // Version 6: fix PDU framing for X224/TPKT and empty Fast-Path PDUs
}

/// Try to find the size of a complete PDU in the buffer.
/// Handles both Fast-Path and X224/TPKT PDUs so we can skip non-FP data.
/// Returns the total PDU size, or 0 if incomplete/not enough data.
#[no_mangle]
pub extern "C" fn get_pdu_size(data: *const u8, len: u32) -> u32 {
    if data.is_null() || len < 2 {
        return 0;
    }

    let slice = unsafe { std::slice::from_raw_parts(data, len as usize) };
    let action = slice[0] & 0x03;

    let pdu_length = match action {
        // X224/TPKT: 4-byte header with big-endian length in bytes 2-3
        0x03 => {
            if slice.len() < 4 {
                return 0;
            }
            ((slice[2] as usize) << 8) | (slice[3] as usize)
        }
        // Fast-Path: PER-encoded length starting at byte 1
        0x00 => {
            let a = slice[1];
            if a & 0x80 != 0 {
                if slice.len() < 3 {
                    return 0;
                }
                let b = slice[2];
                (((a as usize) & 0x7F) << 8) | (b as usize)
            } else {
                a as usize
            }
        }
        // Unknown action type — skip one byte to avoid getting stuck
        _ => 1,
    };

    if pdu_length == 0 || pdu_length > slice.len() {
        return 0; // Incomplete, need more data
    }

    pdu_length as u32
}

/// Parse RDP output data and extract bitmap updates.
/// Handles Fast-Path fragmentation (First/Next/Last/Single).
/// Returns compressed bitmap data as-is (decompression is done client-side).
#[no_mangle]
pub extern "C" fn parse_rdp_output(data: *const u8, len: u32) -> i32 {
    unsafe {
        PARSED_BITMAPS.clear();
        PARSED_DATA.clear();
        ERROR_MSG.clear();
    }

    if data.is_null() || len == 0 {
        return 0;
    }

    let slice = unsafe { std::slice::from_raw_parts(data, len as usize) };

    // Check if this is a Fast-Path PDU (action bits in byte[0] & 0x03 == 0x00)
    // X224/TPKT PDUs have action bits == 0x03 — skip them silently
    if slice[0] & 0x03 != 0x00 {
        return 0; // Not Fast-Path, skip silently
    }

    let mut cursor = ReadCursor::new(slice);

    // Parse Fast-Path header
    let header = match FastPathHeader::decode(&mut cursor) {
        Ok(h) => h,
        Err(e) => {
            return set_error(format!("not fast-path (len={}): {:?}", len, e));
        }
    };

    // Skip PDUs with no data payload (e.g., keep-alive or other empty frames)
    if header.data_length == 0 {
        return 0;
    }

    // Parse Fast-Path update PDU (contains fragmentation info)
    let update_pdu = match FastPathUpdatePdu::decode(&mut cursor) {
        Ok(pdu) => pdu,
        Err(e) => {
            return set_error(format!("failed to decode fast-path update PDU: {:?}", e));
        }
    };

    // Handle fragmentation (same logic as IronRDP's CompleteData)
    let complete_data = unsafe {
        match update_pdu.fragmentation {
            Fragmentation::Single => {
                if FRAGMENT_DATA.len() > 0 {
                    log::warn!(
                        "dropping pending fragment data ({} bytes)",
                        FRAGMENT_DATA.len()
                    );
                    FRAGMENT_DATA.clear();
                    FRAGMENT_UPDATE_CODE = None;
                }
                Some((update_pdu.update_code, update_pdu.data.to_vec()))
            }
            Fragmentation::First => {
                if FRAGMENT_DATA.len() > 0 {
                    log::warn!(
                        "dropping pending fragment data ({} bytes)",
                        FRAGMENT_DATA.len()
                    );
                }
                FRAGMENT_DATA = update_pdu.data.to_vec();
                FRAGMENT_UPDATE_CODE = Some(update_pdu.update_code);
                None
            }
            Fragmentation::Next => {
                if FRAGMENT_UPDATE_CODE.is_some() {
                    FRAGMENT_DATA.extend_from_slice(update_pdu.data);
                } else {
                    log::warn!("fragment Next without First, ignoring");
                }
                None
            }
            Fragmentation::Last => {
                if let Some(code) = FRAGMENT_UPDATE_CODE.take() {
                    FRAGMENT_DATA.extend_from_slice(update_pdu.data);
                    let data = core::mem::take(&mut FRAGMENT_DATA);
                    Some((code, data))
                } else {
                    log::warn!("fragment Last without First, ignoring");
                    None
                }
            }
        }
    };

    let (update_code, data) = match complete_data {
        Some(d) => d,
        None => return 0, // Buffering fragments
    };

    // Decode the complete update
    let update = match FastPathUpdate::decode_with_code(&data, update_code) {
        Ok(u) => u,
        Err(e) => {
            return set_error(format!(
                "failed to decode update code {:?}: {:?}",
                update_code, e
            ));
        }
    };

    // Extract bitmaps — store compressed data as-is
    match update {
        FastPathUpdate::Bitmap(bitmap_data) => {
            if bitmap_data.rectangles.is_empty() {
                return 0;
            }
            let bitmaps = extract_bitmaps(&bitmap_data);
            let count = bitmaps.len();
            unsafe {
                PARSED_BITMAPS = bitmaps;
            }
            count as i32
        }
        _ => 0,
    }
}

fn set_error(msg: String) -> i32 {
    log::error!("parse_rdp_output error: {}", msg);
    unsafe {
        ERROR_MSG = format!("{}\0", msg).into_bytes();
    }
    -1
}

#[no_mangle]
pub extern "C" fn get_bitmap_count() -> u32 {
    unsafe { PARSED_BITMAPS.len() as u32 }
}

#[no_mangle]
pub extern "C" fn get_bitmap(index: u32) -> *const BitmapRect {
    unsafe {
        let idx = index as usize;
        if idx >= PARSED_BITMAPS.len() {
            return std::ptr::null();
        }
        &PARSED_BITMAPS[idx] as *const BitmapRect
    }
}

#[no_mangle]
pub extern "C" fn get_bitmap_data(offset: u32, len: u32) -> *const u8 {
    unsafe {
        let offset_usize = offset as usize;
        let len_usize = len as usize;
        if offset_usize + len_usize > PARSED_DATA.len() {
            return std::ptr::null();
        }
        PARSED_DATA[offset_usize..offset_usize + len_usize].as_ptr()
    }
}

#[no_mangle]
pub extern "C" fn get_error() -> *const u8 {
    unsafe {
        if ERROR_MSG.is_empty() {
            return std::ptr::null();
        }
        ERROR_MSG.as_ptr()
    }
}

#[no_mangle]
pub extern "C" fn get_error_len() -> u32 {
    unsafe { ERROR_MSG.len() as u32 }
}

#[no_mangle]
pub extern "C" fn clear_parsed() {
    unsafe {
        PARSED_BITMAPS.clear();
        PARSED_BITMAPS.shrink_to_fit();
        PARSED_DATA.clear();
        PARSED_DATA.shrink_to_fit();
        ERROR_MSG.clear();
        ERROR_MSG.shrink_to_fit();
    }
}

#[no_mangle]
pub extern "C" fn _initialize() {
    wlog::raw_log(
        log::LevelFilter::Info as u32,
        &"RDP parser module initializing...".to_string(),
    );
    log::set_logger(&wlog::LOGGER).unwrap();
    log::set_max_level(log::LevelFilter::Debug);
    log::info!("RDP parser module initialized (v6 - fix PDU framing)");
}

/// Extract bitmaps from a bitmap update — store raw data as-is (compressed or not)
fn extract_bitmaps(bitmap_update: &ironrdp_pdu::bitmap::BitmapUpdateData) -> Vec<BitmapRect> {
    unsafe {
        let mut bitmaps = Vec::new();

        for (_i, bmp) in bitmap_update.rectangles.iter().enumerate() {
            let is_compressed = bmp
                .compression_flags
                .contains(Compression::BITMAP_COMPRESSION);

            let data_offset = PARSED_DATA.len() as u32;

            // Store raw bitmap data as-is — no decompression
            PARSED_DATA.extend_from_slice(bmp.bitmap_data);

            bitmaps.push(BitmapRect {
                x: bmp.rectangle.left,
                y: bmp.rectangle.top,
                right: bmp.rectangle.right,
                bottom: bmp.rectangle.bottom,
                width: bmp.width,
                height: bmp.height,
                bits_per_pixel: bmp.bits_per_pixel,
                data_offset,
                data_len: bmp.bitmap_data.len() as u32,
                compressed: if is_compressed { 1 } else { 0 },
            });
        }

        bitmaps
    }
}
