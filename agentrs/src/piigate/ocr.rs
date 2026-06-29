//! HTTP OCR client for the RapidOCR/PaddleOCR sidecar.
//!
//! Port of the `httpEngine` in `gateway/rdp/ocr/engine.go`. Frames are sent
//! as uncompressed 24bpp BMP (a plain pixel copy — PNG encoding would dwarf
//! the GPU inference it wraps); the response tokens are validated before
//! they enter the analyzer contract.
//!
//! The agent-side gate requires the sidecar: there is deliberately no local
//! tesseract fallback here (the sidecar deploys next to the agent, inside
//! the customer network — that placement is the point of agent-side
//! analysis). Without an OCR URL the gate reports itself unavailable and the
//! gateway falls back to its own guard.

use std::time::Duration;

use anyhow::Context as _;
use base64::Engine as _;
use serde::{Deserialize, Serialize};

/// One recognized token with its bounding box in full-image pixel space.
/// `conf` follows the tesseract convention (0-100) like the Go analyzer.
#[derive(Debug, Clone)]
pub struct Word {
    pub text: String,
    pub left: usize,
    pub top: usize,
    pub width: usize,
    pub height: usize,
    pub conf: f64,
}

/// Reconstructs the full text exactly as the analyzer expects: tokens joined
/// by single spaces, so Presidio character offsets line up with word ranges.
pub fn join_words(words: &[Word]) -> String {
    words
        .iter()
        .map(|w| w.text.as_str())
        .collect::<Vec<_>>()
        .join(" ")
}

/// Encodes top-down RGBA pixels as an uncompressed 24bpp bottom-up BMP
/// (BITMAPFILEHEADER + BITMAPINFOHEADER + BGR rows padded to 4 bytes).
/// Port of `encodeBMP` in `gateway/rdp/ocr/ocr.go`.
pub fn encode_bmp(rgba: &[u8], width: usize, height: usize) -> Vec<u8> {
    let row_size = (width * 3 + 3) & !3; // each row padded to a 4-byte boundary
    let pixel_bytes = row_size * height;
    let file_size = 14 + 40 + pixel_bytes;

    let mut out = Vec::with_capacity(file_size);
    // BITMAPFILEHEADER
    out.extend_from_slice(b"BM");
    out.extend_from_slice(&(file_size as u32).to_le_bytes());
    out.extend_from_slice(&0u32.to_le_bytes()); // reserved
    out.extend_from_slice(&54u32.to_le_bytes()); // pixel data offset
    // BITMAPINFOHEADER
    out.extend_from_slice(&40u32.to_le_bytes());
    out.extend_from_slice(&(width as i32).to_le_bytes());
    out.extend_from_slice(&(height as i32).to_le_bytes()); // positive: bottom-up
    out.extend_from_slice(&1u16.to_le_bytes()); // planes
    out.extend_from_slice(&24u16.to_le_bytes()); // bpp
    out.extend_from_slice(&[0u8; 24]); // compression, sizes, palette: all zero

    // Pixel rows, bottom-up, BGR, padded.
    let pad = row_size - width * 3;
    for row in (0..height).rev() {
        let src = row * width * 4;
        for col in 0..width {
            let si = src + col * 4;
            if si + 3 <= rgba.len() {
                out.push(rgba[si + 2]); // B
                out.push(rgba[si + 1]); // G
                out.push(rgba[si]); // R
            } else {
                out.extend_from_slice(&[0, 0, 0]);
            }
        }
        out.extend(std::iter::repeat_n(0u8, pad));
    }
    out
}

/// One token in the OCR server's response. Confidence is 0..1 (PP-OCR
/// convention); coordinates are pixels in the sent image space.
#[derive(Debug, Deserialize)]
struct OcrServerWord {
    text: String,
    conf: f64,
    x: i64,
    y: i64,
    w: i64,
    h: i64,
}

#[derive(Debug, Deserialize)]
struct OcrServerResponse {
    duration_ms: f64,
    words: Vec<OcrServerWord>,
}

/// Result of one OCR call: the recognized words plus diagnostics used by the
/// latency aggregator to separate sidecar inference time from the wall-clock
/// the agent observes (the gap is queueing/contention/transport).
#[derive(Debug)]
pub struct OcrExtract {
    pub words: Vec<Word>,
    /// The sidecar's self-reported inference time for this request.
    pub server_ms: f64,
    /// BMP bytes sent to the sidecar for this request.
    pub request_bytes: usize,
}

/// A detected text line: a 4-point quad in the sent image's pixel space exactly
/// as the sidecar's detector produced it, plus `crop_hash` — the SHA-256 of the
/// EXACT perspective-cropped pixels recognition will see for this line.
///
/// The agent keys its per-line OCR cache on `crop_hash` (with the quad geometry
/// for diagnostics/placement), so a line is reused only when the exact
/// recognition input recurs. Because the hash is computed by the sidecar over
/// the same `get_rotate_crop_image` crop that `/rec` recognizes, "unchanged
/// hash" provably means "recognition would see identical pixels" — there is no
/// bbox-vs-quad gap through which a changed line could reuse a stale word.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LineBox {
    /// Four [x, y] points (top-left, top-right, bottom-right, bottom-left).
    pub quad: [[f32; 2]; 4],
    /// SHA-256 (hex) of the perspective-cropped recognition pixels.
    pub crop_hash: String,
}

impl LineBox {
    /// Axis-aligned bounding rect (x0, y0, x1, y1) in image pixels, clamped to
    /// non-negative. Used only for placing the resulting word / redaction box,
    /// never for the cache validity check (that uses `crop_hash`).
    pub fn bounds(&self) -> (usize, usize, usize, usize) {
        let xs = self.quad.iter().map(|p| p[0]);
        let ys = self.quad.iter().map(|p| p[1]);
        let x0 = xs.clone().fold(f32::INFINITY, f32::min).max(0.0).floor() as usize;
        let y0 = ys.clone().fold(f32::INFINITY, f32::min).max(0.0).floor() as usize;
        let x1 = xs.fold(f32::NEG_INFINITY, f32::max).max(0.0).ceil() as usize;
        let y1 = ys.fold(f32::NEG_INFINITY, f32::max).max(0.0).ceil() as usize;
        (x0, y0, x1, y1)
    }
}

#[derive(Debug, Deserialize)]
struct DetServerBox {
    quad: [[f32; 2]; 4],
    crop_hash: String,
}

#[derive(Debug, Deserialize)]
struct DetServerResponse {
    duration_ms: f64,
    boxes: Vec<DetServerBox>,
}

/// Result of a detection-only call: the line boxes plus the sidecar's
/// self-reported inference time.
#[derive(Debug)]
pub struct DetResult {
    pub boxes: Vec<LineBox>,
    pub server_ms: f64,
}

#[derive(Debug, Serialize)]
struct RecRequest {
    image: String, // base64 of the same image det was run on
    boxes: Vec<[[f32; 2]; 4]>,
}

/// Result of a recognition-only call: EXACTLY one slot per requested box, in
/// the same order, in full-image coordinates. A slot is `None` when that box
/// recognized to nothing usable (empty/whitespace text, or geometry the agent
/// rejects) — a legitimate result for a blank line, an icon, or a separator.
///
/// The 1:1 positional alignment is load-bearing: the per-line cache pairs each
/// detected box with its recognition outcome, and the analyzer fails closed on
/// a count mismatch. Recognition outcomes must therefore never be *dropped*
/// (which would silently misalign boxes and words); an unusable result becomes
/// `None` in place, preserving the slot.
#[derive(Debug)]
pub struct RecResult {
    pub words: Vec<Option<Word>>,
    pub server_ms: f64,
    pub request_bytes: usize,
}

/// Bounds the response body read: even a pathological full-screen of dense
/// text is far below this.
const MAX_OCR_RESPONSE_BYTES: usize = 8 << 20;

/// HTTP client for the OCR sidecar.
#[derive(Debug, Clone)]
pub struct OcrClient {
    base_url: String,
    client: reqwest::Client,
}

impl OcrClient {
    pub fn new(base_url: &str) -> anyhow::Result<Self> {
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(30))
            // Bound server think time separately from the total timeout: a
            // wedged sidecar should fail the request fast.
            .read_timeout(Duration::from_secs(20))
            .pool_max_idle_per_host(16)
            .pool_idle_timeout(Duration::from_secs(90))
            .build()
            .context("piigate: failed to build OCR HTTP client")?;
        Ok(Self {
            base_url: base_url.trim_end_matches('/').to_string(),
            client,
        })
    }

    /// OCRs a top-down RGBA image. Returned word coordinates are in the sent
    /// image's pixel space; validation mirrors the gateway engine — malformed
    /// entries are dropped (geometry) or clamped (confidence), never
    /// propagated.
    pub async fn extract(
        &self,
        rgba: &[u8],
        width: usize,
        height: usize,
    ) -> anyhow::Result<OcrExtract> {
        let bmp = encode_bmp(rgba, width, height);
        let request_bytes = bmp.len();
        let resp = self
            .client
            .post(format!("{}/ocr", self.base_url))
            .header("Content-Type", "application/octet-stream")
            .body(bmp)
            .send()
            .await
            .context("ocr: server request failed")?;

        let status = resp.status();
        if !status.is_success() {
            let body = resp.bytes().await.unwrap_or_default();
            let snippet = String::from_utf8_lossy(&body[..body.len().min(512)]).to_string();
            anyhow::bail!("ocr: server returned status {status}: {}", snippet.trim());
        }

        let body = resp.bytes().await.context("ocr: failed to read response")?;
        if body.len() > MAX_OCR_RESPONSE_BYTES {
            anyhow::bail!("ocr: response exceeds {MAX_OCR_RESPONSE_BYTES} bytes");
        }
        let out: OcrServerResponse =
            serde_json::from_slice(&body).context("ocr: invalid server response")?;

        Ok(OcrExtract {
            words: validate_words(out.words, width, height),
            server_ms: out.duration_ms,
            request_bytes,
        })
    }

    /// Detection only: returns the text-line boxes for a top-down RGBA image.
    /// Boxes are in the sent image's pixel space; the caller uses them to decide
    /// which lines changed (per-line OCR cache) and recognizes only those via
    /// [`recognize`](Self::recognize).
    pub async fn detect(&self, rgba: &[u8], width: usize, height: usize) -> anyhow::Result<DetResult> {
        let bmp = encode_bmp(rgba, width, height);
        let resp = self
            .client
            .post(format!("{}/det", self.base_url))
            .header("Content-Type", "application/octet-stream")
            .body(bmp)
            .send()
            .await
            .context("ocr: det request failed")?;

        let status = resp.status();
        if !status.is_success() {
            let body = resp.bytes().await.unwrap_or_default();
            let snippet = String::from_utf8_lossy(&body[..body.len().min(512)]).to_string();
            anyhow::bail!("ocr: det returned status {status}: {}", snippet.trim());
        }
        let body = resp.bytes().await.context("ocr: failed to read det response")?;
        if body.len() > MAX_OCR_RESPONSE_BYTES {
            anyhow::bail!("ocr: det response exceeds {MAX_OCR_RESPONSE_BYTES} bytes");
        }
        let out: DetServerResponse =
            serde_json::from_slice(&body).context("ocr: invalid det response")?;

        Ok(DetResult {
            boxes: out
                .boxes
                .into_iter()
                .map(|b| {
                    // The crop_hash is the security-critical cache validity
                    // token. Reject a malformed one (wrong length / non-hex)
                    // rather than trust it: a constant or empty hash from a
                    // misbehaving sidecar could otherwise cause a wrong reuse.
                    if !is_sha256_hex(&b.crop_hash) {
                        anyhow::bail!("ocr: det returned a malformed crop_hash");
                    }
                    Ok(LineBox {
                        quad: b.quad,
                        crop_hash: b.crop_hash,
                    })
                })
                .collect::<anyhow::Result<Vec<_>>>()?,
            server_ms: out.duration_ms,
        })
    }

    /// Recognition only: recognizes the given line boxes against `rgba` (the
    /// SAME image `detect` was run on). The sidecar crops each box with the same
    /// perspective transform as the combined pipeline, so the text is identical
    /// to what `/ocr` would have produced. Returns one word per box, in order,
    /// in full-image coordinates.
    pub async fn recognize(
        &self,
        rgba: &[u8],
        width: usize,
        height: usize,
        boxes: &[LineBox],
    ) -> anyhow::Result<RecResult> {
        if boxes.is_empty() {
            return Ok(RecResult { words: Vec::new(), server_ms: 0.0, request_bytes: 0 });
        }
        let want = boxes.len();
        let bmp = encode_bmp(rgba, width, height);
        let req = RecRequest {
            image: base64::engine::general_purpose::STANDARD.encode(&bmp),
            boxes: boxes.iter().map(|b| b.quad).collect(),
        };
        let payload = serde_json::to_vec(&req).context("ocr: encode rec request")?;
        let request_bytes = payload.len();
        let resp = self
            .client
            .post(format!("{}/rec", self.base_url))
            .header("Content-Type", "application/json")
            .body(payload)
            .send()
            .await
            .context("ocr: rec request failed")?;

        let status = resp.status();
        if !status.is_success() {
            let body = resp.bytes().await.unwrap_or_default();
            let snippet = String::from_utf8_lossy(&body[..body.len().min(512)]).to_string();
            anyhow::bail!("ocr: rec returned status {status}: {}", snippet.trim());
        }
        let body = resp.bytes().await.context("ocr: failed to read rec response")?;
        if body.len() > MAX_OCR_RESPONSE_BYTES {
            anyhow::bail!("ocr: rec response exceeds {MAX_OCR_RESPONSE_BYTES} bytes");
        }
        let out: OcrServerResponse =
            serde_json::from_slice(&body).context("ocr: invalid rec response")?;

        // The /rec contract is positional: the sidecar must return exactly one
        // entry per requested box, in order. Enforce it at the boundary so a
        // protocol violation is a clear error here rather than a misaligned
        // box/word pairing downstream. Empty/unusable recognitions are kept as
        // `None` slots by `validate_words_positional` — never dropped.
        if out.words.len() != want {
            anyhow::bail!(
                "ocr: rec returned {} entries for {} boxes (positional contract violated)",
                out.words.len(),
                want
            );
        }

        Ok(RecResult {
            words: validate_words_positional(out.words, width, height),
            server_ms: out.duration_ms,
            request_bytes,
        })
    }
}

/// Whether `s` is a well-formed SHA-256 hex digest: exactly 64 lowercase hex
/// characters. The per-line cache validity depends on this token, so a
/// malformed value from a misbehaving sidecar must be rejected (fail closed),
/// never used as a cache key.
fn is_sha256_hex(s: &str) -> bool {
    s.len() == 64 && s.bytes().all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}

#[cfg(test)]
mod hash_tests {
    use super::is_sha256_hex;

    #[test]
    fn sha256_hex_validation() {
        assert!(is_sha256_hex(&"a".repeat(64)));
        assert!(is_sha256_hex(&"0123456789abcdef".repeat(4)));
        assert!(!is_sha256_hex(""), "empty rejected");
        assert!(!is_sha256_hex(&"a".repeat(63)), "too short rejected");
        assert!(!is_sha256_hex(&"a".repeat(65)), "too long rejected");
        assert!(!is_sha256_hex(&"A".repeat(64)), "uppercase rejected");
        assert!(!is_sha256_hex(&"g".repeat(64)), "non-hex rejected");
    }
}

/// Validates and normalizes raw server tokens before they enter the analyzer
/// contract: the HTTP path must not trust the sidecar more than the gateway
/// trusts tesseract TSV. Malformed entries are dropped (empty text,
/// non-finite/negative confidence, degenerate or off-canvas geometry);
/// confidence is clamped to 0..1 and rescaled to the 0-100 Word convention.
fn validate_words(raw: Vec<OcrServerWord>, width: usize, height: usize) -> Vec<Word> {
    raw.into_iter()
        .filter_map(|w| validate_word(w, width, height))
        .collect()
}

/// Positional sibling of [`validate_words`] for the `/rec` path: returns one
/// slot per input word, in order, with the same per-word sanitization but
/// WITHOUT dropping. An unusable entry (empty text, bad confidence, degenerate
/// or off-canvas geometry) becomes `None` in place rather than collapsing the
/// vector — preserving the box↔word alignment the per-line cache relies on.
fn validate_words_positional(
    raw: Vec<OcrServerWord>,
    width: usize,
    height: usize,
) -> Vec<Option<Word>> {
    raw.into_iter()
        .map(|w| validate_word(w, width, height))
        .collect()
}

/// Sanitizes a single raw sidecar word, returning `None` if it is unusable.
/// Shared by the dropping (`/ocr`, `/det` discovery) and positional (`/rec`)
/// validators so both apply identical rules — only their handling of `None`
/// differs (drop vs. keep-as-empty-slot).
fn validate_word(w: OcrServerWord, width: usize, height: usize) -> Option<Word> {
    let text = w.text.trim();
    if text.is_empty() {
        return None;
    }
    if !w.conf.is_finite() || w.conf < 0.0 {
        return None;
    }
    if w.x < 0 || w.y < 0 || w.w <= 0 || w.h <= 0 {
        return None;
    }
    let (x, y, ww, hh) = (w.x as usize, w.y as usize, w.w as usize, w.h as usize);
    // Reject tokens that fall outside the submitted image. The sidecar is a
    // separate (untrusted) process: absurd coordinates would otherwise produce
    // off-canvas bounding boxes and overflow the bbox merge in analyze_text.
    // The Go engine does not bounds-check, but the agent-side sidecar runs in
    // the customer network and warrants the stricter contract — words rejected
    // here are off-screen anyway.
    if x >= width || y >= height || ww > width - x || hh > height - y {
        return None;
    }
    Some(Word {
        text: text.to_string(),
        left: x,
        top: y,
        width: ww,
        height: hh,
        // Server confidence is 0..1; Word.conf is 0-100.
        conf: w.conf.min(1.0) * 100.0,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bmp_layout_and_padding() {
        // 3x2 RGBA: row padding = (3*3+3)&!3 - 9 = 12 - 9 = 3 bytes.
        let rgba: Vec<u8> = (0..3 * 2 * 4).map(|i| i as u8).collect();
        let bmp = encode_bmp(&rgba, 3, 2);
        assert_eq!(&bmp[..2], b"BM");
        let file_size = u32::from_le_bytes(bmp[2..6].try_into().unwrap()) as usize;
        assert_eq!(file_size, bmp.len());
        assert_eq!(file_size, 54 + 12 * 2);
        // First pixel row in the file is the BOTTOM image row (row 1).
        // Image row 1, col 0 RGBA = bytes [12..16] = (12,13,14,15) → BGR (14,13,12).
        assert_eq!(&bmp[54..57], &[14, 13, 12]);
    }

    #[test]
    fn server_word_validation() {
        let raw = vec![
            OcrServerWord { text: " ok ".into(), conf: 0.9, x: 0, y: 0, w: 10, h: 10 },
            OcrServerWord { text: "".into(), conf: 0.9, x: 0, y: 0, w: 5, h: 5 }, // empty
            OcrServerWord { text: "nan".into(), conf: f64::NAN, x: 0, y: 0, w: 5, h: 5 },
            OcrServerWord { text: "neg".into(), conf: -0.1, x: 0, y: 0, w: 5, h: 5 },
            OcrServerWord { text: "degenerate".into(), conf: 0.9, x: 0, y: 0, w: 0, h: 5 },
            OcrServerWord { text: "offcanvas".into(), conf: 0.9, x: 90, y: 0, w: 20, h: 5 },
            OcrServerWord { text: "clamp".into(), conf: 5.0, x: 1, y: 1, w: 4, h: 4 },
        ];
        let kept = validate_words(raw, 100, 100);
        assert_eq!(kept.len(), 2);
        assert_eq!(kept[0].text, "ok"); // trimmed
        assert_eq!(kept[1].text, "clamp");
        assert_eq!(kept[1].conf, 100.0); // 5.0 clamped to 1.0 → 100
    }

    #[test]
    fn positional_validation_keeps_one_slot_per_word() {
        // Same inputs as the dropping validator, but positional: every input
        // gets a slot; unusable ones become `None` IN PLACE (no collapse), so
        // the i-th result still corresponds to the i-th requested /rec box.
        let raw = vec![
            OcrServerWord { text: "ok".into(), conf: 0.9, x: 0, y: 0, w: 10, h: 10 },
            OcrServerWord { text: "".into(), conf: 0.0, x: 0, y: 0, w: 5, h: 5 }, // blank line
            OcrServerWord { text: "bad".into(), conf: f64::NAN, x: 0, y: 0, w: 5, h: 5 },
            OcrServerWord { text: "two".into(), conf: 0.8, x: 1, y: 1, w: 4, h: 4 },
        ];
        let slots = validate_words_positional(raw, 100, 100);
        assert_eq!(slots.len(), 4, "one slot per input, never dropped");
        assert_eq!(slots[0].as_ref().unwrap().text, "ok");
        assert!(slots[1].is_none(), "empty text -> None slot, not removed");
        assert!(slots[2].is_none(), "NaN conf -> None slot, not removed");
        assert_eq!(slots[3].as_ref().unwrap().text, "two");
    }

    #[test]
    fn join_words_single_spaces() {
        let w = |t: &str| Word {
            text: t.into(),
            left: 0,
            top: 0,
            width: 1,
            height: 1,
            conf: 90.0,
        };
        assert_eq!(join_words(&[w("a"), w("b c")]), "a b c");
        assert_eq!(join_words(&[]), "");
    }
}
