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
use serde::Deserialize;

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
    #[allow(dead_code)]
    duration_ms: f64,
    words: Vec<OcrServerWord>,
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
    ) -> anyhow::Result<Vec<Word>> {
        let bmp = encode_bmp(rgba, width, height);
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

        Ok(validate_words(out.words, width, height))
    }
}

/// Validates and normalizes raw server tokens before they enter the analyzer
/// contract: the HTTP path must not trust the sidecar more than the gateway
/// trusts tesseract TSV. Malformed entries are dropped (empty text,
/// non-finite/negative confidence, degenerate or off-canvas geometry);
/// confidence is clamped to 0..1 and rescaled to the 0-100 Word convention.
fn validate_words(raw: Vec<OcrServerWord>, width: usize, height: usize) -> Vec<Word> {
    raw.into_iter()
        .filter_map(|w| {
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
            // Reject tokens that fall outside the submitted image. The
            // sidecar is a separate (untrusted) process: absurd coordinates
            // would otherwise produce off-canvas bounding boxes and overflow
            // the bbox merge in analyze_text. The Go engine does not
            // bounds-check, but the agent-side sidecar runs in the customer
            // network and warrants the stricter contract — words it drops
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
        })
        .collect()
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
