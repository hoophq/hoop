//! Band-scoped framebuffer analysis: OCR over dirty bands, then Presidio.
//!
//! Port of `AnalyzeFramebufferBands` in `gateway/rdp/analyzer/region.go`:
//! only the rows that changed since the previous analysis are OCR'd, with
//! tall bands split into overlapping chunks OCR'd in parallel. Word
//! coordinates are translated back to full-screen space, so detections are
//! indistinguishable from full-frame analysis.

use std::sync::Arc;

use anyhow::Context as _;
use async_trait::async_trait;
use tokio::sync::Semaphore;
use tokio_util::sync::CancellationToken;

use super::bands::{split_bands, OcrChunk, YBand, DEFAULT_BAND_PADDING, MAX_CHUNK_ROWS};
use super::ocr::{join_words, OcrClient, Word};
use super::presidio::{analyze_text, AnalysisParams, PresidioClient, SnapshotResult};

/// The analysis stage the gate invokes per batch. A trait so tests can
/// substitute a deterministic detector (the signature detector) for the real
/// OCR+Presidio pipeline — the leak tests prove pipeline properties (what is
/// analyzed and what escapes) independently of detection accuracy.
#[async_trait]
pub trait Analyzer: Send + Sync + 'static {
    /// Analyzes the dirty bands of an RGBA framebuffer. Implementations must
    /// not retain the framebuffer slice after returning.
    async fn analyze(
        &self,
        fb: &[u8],
        fb_width: usize,
        fb_height: usize,
        bands: &[YBand],
    ) -> anyhow::Result<SnapshotResult>;
}

/// The production analyzer: OCR sidecar + Presidio.
pub struct BandAnalyzer {
    pub ocr: OcrClient,
    pub presidio: PresidioClient,
    pub params: AnalysisParams,
}

#[async_trait]
impl Analyzer for BandAnalyzer {
    async fn analyze(
        &self,
        fb: &[u8],
        fb_width: usize,
        fb_height: usize,
        bands: &[YBand],
    ) -> anyhow::Result<SnapshotResult> {
        if fb_width == 0 || fb_height == 0 {
            anyhow::bail!("invalid framebuffer dimensions {fb_width}x{fb_height}");
        }
        let stride = fb_width * 4;
        if fb.len() < stride * fb_height {
            anyhow::bail!(
                "framebuffer too short: got {} bytes, need {} for {fb_width}x{fb_height} RGBA",
                fb.len(),
                stride * fb_height
            );
        }
        for b in bands {
            if b.y1 > fb_height || b.y0 >= b.y1 {
                anyhow::bail!("invalid band [{},{}) for framebuffer height {fb_height}", b.y0, b.y1);
            }
        }

        // Tall bands are split into chunks OCR'd in parallel; the chunk
        // overlap follows the same padding policy as the dirty-band
        // accumulator.
        let pad = if self.params.band_padding == 0 {
            DEFAULT_BAND_PADDING
        } else {
            self.params.band_padding
        };
        let chunks = split_bands(bands, MAX_CHUNK_ROWS, pad);

        let concurrency = if self.params.max_ocr_concurrency == 0 {
            8usize.min(std::thread::available_parallelism().map_or(8, |n| n.get()))
        } else {
            self.params.max_ocr_concurrency
        };
        let semaphore = Arc::new(Semaphore::new(concurrency));
        // Cancel sibling OCR work as soon as one chunk fails (matches Go's
        // errgroup.WithContext): a failing batch fails open regardless, so
        // burning sidecar capacity on the rest only worsens backlog pressure.
        let cancel = CancellationToken::new();

        // Each chunk owns a copy of its window rows: the framebuffer slice
        // cannot be borrowed across spawned tasks (it is owned and reused by
        // the gate's analysis loop). Chunk windows are bounded
        // (MAX_CHUNK_ROWS + pad), so the copies are small. Indices preserve
        // chunk order for stable text concatenation.
        let mut handles = Vec::with_capacity(chunks.len());
        for (idx, chunk) in chunks.iter().enumerate() {
            let crop = fb[chunk.win.y0 * stride..chunk.win.y1 * stride].to_vec();
            let win_height = chunk.win.height();
            let ocr = self.ocr.clone();
            let chunk = *chunk;
            let semaphore = semaphore.clone();
            let cancel = cancel.clone();
            handles.push(tokio::spawn(async move {
                let _permit = semaphore
                    .acquire()
                    .await
                    .expect("piigate: OCR semaphore closed");
                if cancel.is_cancelled() {
                    return Ok((idx, Vec::new()));
                }
                let words = tokio::select! {
                    res = ocr.extract(&crop, fb_width, win_height) => res.with_context(|| {
                        format!("OCR failed on chunk [{},{})", chunk.win.y0, chunk.win.y1)
                    })?,
                    _ = cancel.cancelled() => return Ok((idx, Vec::new())),
                };
                Ok::<_, anyhow::Error>((idx, own_words(words, chunk)))
            }));
        }

        // Collect by chunk index so concatenation preserves top-to-bottom
        // reading order across seams (ownership-by-center assigns whole lines
        // to one chunk). On the first error, cancel the rest and propagate.
        let mut per_chunk: Vec<Vec<Word>> = vec![Vec::new(); chunks.len()];
        let mut first_err = None;
        for h in handles {
            match h.await.context("piigate: OCR task panicked")? {
                Ok((idx, words)) => per_chunk[idx] = words,
                Err(e) => {
                    cancel.cancel();
                    first_err = first_err.or(Some(e));
                }
            }
        }
        if let Some(e) = first_err {
            return Err(e);
        }
        let all_words: Vec<Word> = per_chunk.into_iter().flatten().collect();

        if all_words.is_empty() {
            return Ok(SnapshotResult::default());
        }
        let text = join_words(&all_words);
        // Gate-close cancellation is handled at the mod.rs boundary (the
        // whole analyze() future is dropped), which aborts this Presidio
        // request via reqwest's drop — no inner select needed here.
        analyze_text(&self.presidio, &text, &all_words, &self.params).await
    }
}

/// Shifts word coordinates into full-screen space and keeps only the words
/// the chunk owns, so overlap regions are not duplicated.
fn own_words(words: Vec<Word>, chunk: OcrChunk) -> Vec<Word> {
    words
        .into_iter()
        .map(|mut w| {
            w.top += chunk.win.y0;
            w
        })
        .filter(|w| chunk.owns(w.top, w.height))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn own_words_offsets_and_filters() {
        let chunk = OcrChunk {
            win: YBand { y0: 232, y1: 536 },
            own: YBand { y0: 256, y1: 512 },
        };
        let mk = |top: usize| Word {
            text: "w".into(),
            left: 0,
            top,
            width: 10,
            height: 12,
            conf: 90.0,
        };
        // Band-local top 30 → screen 262, center 268 ∈ [256,512): owned.
        // Band-local top 10 → screen 242, center 248 < 256: not owned.
        let out = own_words(vec![mk(30), mk(10)], chunk);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].top, 262);
    }
}
