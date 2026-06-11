//! Presidio analyzer client and the post-OCR analysis stage.
//!
//! Port of `gateway/rdp/analyzer/presidio.go` and the `analyzeText` stage of
//! `gateway/rdp/analyzer/worker.go`: Presidio analysis, denylist filtering,
//! and mapping entity character ranges back to pixel bounding boxes.

use std::collections::HashMap;
use std::time::Duration;

use anyhow::Context as _;
use serde::{Deserialize, Serialize};

use super::ocr::Word;

/// Request payload for Presidio's /analyze endpoint.
#[derive(Debug, Serialize)]
struct AnalyzerRequest<'a> {
    text: &'a str,
    language: &'a str,
    score_threshold: f64,
}

/// A single PII entity found by Presidio.
#[derive(Debug, Clone, Deserialize)]
pub struct AnalyzerResult {
    pub start: usize,
    pub end: usize,
    pub score: f64,
    pub entity_type: String,
}

/// One detection with its on-screen bounding box (full framebuffer pixel
/// coordinates). Mirrors `models.RDPEntityDetection` — this is what gets
/// reported to the gateway for persistence.
#[derive(Debug, Clone, Serialize)]
pub struct EntityDetection {
    pub entity_type: String,
    pub score: f64,
    pub x: usize,
    pub y: usize,
    pub width: usize,
    pub height: usize,
}

/// The outcome of analyzing one framebuffer state.
#[derive(Debug, Default)]
pub struct SnapshotResult {
    pub detections: Vec<EntityDetection>,
    pub counts: HashMap<String, i64>,
}

impl SnapshotResult {
    pub fn is_detection(&self) -> bool {
        !self.counts.is_empty()
    }
}

/// HTTP client for the Presidio analyzer service.
#[derive(Debug, Clone)]
pub struct PresidioClient {
    analyzer_url: String,
    client: reqwest::Client,
}

impl PresidioClient {
    pub fn new(analyzer_url: &str) -> anyhow::Result<Self> {
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(30))
            .build()
            .context("piigate: failed to build Presidio HTTP client")?;
        Ok(Self {
            analyzer_url: analyzer_url.trim_end_matches('/').to_string(),
            client,
        })
    }

    /// Sends text to Presidio for PII detection. `score_threshold` is the
    /// minimum confidence (0 falls back to 0.5, matching the Go client).
    pub async fn analyze(
        &self,
        text: &str,
        score_threshold: f64,
    ) -> anyhow::Result<Vec<AnalyzerResult>> {
        if text.is_empty() {
            return Ok(Vec::new());
        }
        let threshold = if score_threshold > 0.0 { score_threshold } else { 0.5 };
        let resp = self
            .client
            .post(format!("{}/analyze", self.analyzer_url))
            .json(&AnalyzerRequest {
                text,
                language: "en",
                score_threshold: threshold,
            })
            .send()
            .await
            .context("presidio: request failed")?;

        let status = resp.status();
        let body = resp.bytes().await.context("presidio: failed to read response")?;
        if !status.is_success() {
            anyhow::bail!(
                "presidio: analyzer returned status {status}: {}",
                String::from_utf8_lossy(&body[..body.len().min(512)])
            );
        }
        serde_json::from_slice(&body).context("presidio: failed to decode response")
    }
}

/// Analysis tuning, delivered by the gateway in the session setup (mirrors
/// the gateway's `AnalysisParams`).
#[derive(Debug, Clone)]
pub struct AnalysisParams {
    /// Minimum Presidio score (gateway default 0.9).
    pub score_threshold: f64,
    /// Entity types to exclude (gateway default DATE_TIME, NRP).
    pub entity_denylist: Vec<String>,
    /// Vertical padding around dirty rects AND parallel-chunk overlap.
    pub band_padding: usize,
    /// Cap on concurrent OCR requests for chunked band analysis.
    pub max_ocr_concurrency: usize,
}

impl Default for AnalysisParams {
    fn default() -> Self {
        Self {
            score_threshold: 0.9,
            entity_denylist: vec!["DATE_TIME".into(), "NRP".into()],
            band_padding: super::bands::DEFAULT_BAND_PADDING,
            max_ocr_concurrency: 8,
        }
    }
}

/// A word's character range in the reconstructed text string.
struct WordRange<'a> {
    start: usize, // inclusive byte offset in full text
    end: usize,   // exclusive byte offset in full text
    word: &'a Word,
}

/// Constructs a character-offset index from OCR words. The full text is the
/// words joined by single spaces; each range records the word's start/end
/// byte offsets in that string.
fn build_word_ranges(words: &[Word]) -> Vec<WordRange<'_>> {
    let mut ranges = Vec::with_capacity(words.len());
    let mut offset = 0;
    for w in words {
        let end = offset + w.text.len();
        ranges.push(WordRange { start: offset, end, word: w });
        offset = end + 1; // +1 for the space separator
    }
    ranges
}

/// Maps a Presidio result (character offsets) to a merged bounding box from
/// the OCR words overlapping the entity's character range.
fn map_entity_to_bbox(entity: &AnalyzerResult, ranges: &[WordRange<'_>]) -> Option<(usize, usize, usize, usize)> {
    let mut bbox: Option<(usize, usize, usize, usize)> = None; // (min_x, min_y, max_x2, max_y2)
    for wr in ranges {
        if wr.end <= entity.start || wr.start >= entity.end {
            continue; // no overlap
        }
        let (x, y) = (wr.word.left, wr.word.top);
        let (x2, y2) = (x + wr.word.width, y + wr.word.height);
        bbox = Some(match bbox {
            None => (x, y, x2, y2),
            Some((mx, my, mx2, my2)) => (mx.min(x), my.min(y), mx2.max(x2), my2.max(y2)),
        });
    }
    bbox.map(|(x, y, x2, y2)| (x, y, x2 - x, y2 - y))
}

/// The post-OCR stage: Presidio analysis, denylist filtering, and mapping
/// entity character ranges back to pixel bounding boxes. Word coordinates
/// must already be in full-screen space and `text` must be the words joined
/// by single spaces (so Presidio character offsets line up with word ranges).
pub async fn analyze_text(
    presidio: &PresidioClient,
    text: &str,
    words: &[Word],
    params: &AnalysisParams,
) -> anyhow::Result<SnapshotResult> {
    let mut results = presidio.analyze(text, params.score_threshold).await?;
    results.retain(|r| !params.entity_denylist.iter().any(|d| d == &r.entity_type));

    let mut res = SnapshotResult::default();
    for r in &results {
        *res.counts.entry(r.entity_type.clone()).or_insert(0) += 1;
    }

    if !results.is_empty() {
        let ranges = build_word_ranges(words);
        for r in &results {
            if let Some((x, y, width, height)) = map_entity_to_bbox(r, &ranges) {
                res.detections.push(EntityDetection {
                    entity_type: r.entity_type.clone(),
                    score: r.score,
                    x,
                    y,
                    width,
                    height,
                });
            }
        }
    }
    Ok(res)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn word(text: &str, left: usize, top: usize, w: usize, h: usize) -> Word {
        Word {
            text: text.into(),
            left,
            top,
            width: w,
            height: h,
            conf: 95.0,
        }
    }

    #[test]
    fn word_ranges_offsets_match_joined_text() {
        let words = [word("alpha", 0, 0, 10, 5), word("beta", 20, 0, 8, 5)];
        let ranges = build_word_ranges(&words);
        // "alpha beta": alpha=[0,5), beta=[6,10)
        assert_eq!((ranges[0].start, ranges[0].end), (0, 5));
        assert_eq!((ranges[1].start, ranges[1].end), (6, 10));
    }

    #[test]
    fn entity_bbox_merges_overlapping_words() {
        let words = [
            word("john@", 100, 50, 40, 12),
            word("doe.com", 142, 50, 60, 12),
            word("unrelated", 0, 200, 50, 12),
        ];
        let ranges = build_word_ranges(&words);
        // Entity spans "john@ doe.com" = chars [0, 13).
        let entity = AnalyzerResult {
            start: 0,
            end: 13,
            score: 0.95,
            entity_type: "EMAIL_ADDRESS".into(),
        };
        let bbox = map_entity_to_bbox(&entity, &ranges).unwrap();
        assert_eq!(bbox, (100, 50, 102, 12)); // x=100..202, y=50..62
    }

    #[test]
    fn entity_bbox_none_when_no_overlap() {
        let words = [word("abc", 0, 0, 10, 5)];
        let ranges = build_word_ranges(&words);
        let entity = AnalyzerResult {
            start: 10,
            end: 14,
            score: 0.9,
            entity_type: "X".into(),
        };
        assert!(map_entity_to_bbox(&entity, &ranges).is_none());
    }
}
