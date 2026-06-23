//! Band-scoped framebuffer analysis: OCR over dirty bands, then Presidio.
//!
//! Port of `AnalyzeFramebufferBands` in `gateway/rdp/analyzer/region.go`:
//! only the rows that changed since the previous analysis are OCR'd, with
//! tall bands split into overlapping chunks OCR'd in parallel. Word
//! coordinates are translated back to full-screen space, so detections are
//! indistinguishable from full-frame analysis.

use std::collections::HashMap;
use std::hash::{Hash as _, Hasher as _};
use std::sync::{Arc, Mutex};

use anyhow::Context as _;
use async_trait::async_trait;
use tokio::sync::Semaphore;
use tokio_util::sync::CancellationToken;

use super::bands::{split_bands, OcrChunk, YBand, DEFAULT_BAND_PADDING, MAX_CHUNK_ROWS};
use super::ocr::{join_words, OcrClient, Word};
use super::presidio::{analyze_text, AnalysisParams, PresidioClient, SnapshotResult};

/// Per-window OCR result cache: skips re-OCR of a band whose pixels are
/// byte-identical to the last time that exact window was OCR'd.
///
/// Live RDP produces a storm of tiny repaints (blinking carets, ticking
/// clocks, focus rectangles) that re-dirty the same rows continuously. Without
/// this cache every repaint re-OCRs the whole band, saturating the engine.
///
/// Correctness is the priority over the speedup:
///
/// - The key is the window geometry PLUS a hash of the window's exact pixel
///   bytes. Any pixel change misses the cache and forces a fresh OCR, so a
///   changed band is NEVER skipped (no PII can slip through unread).
/// - The cached value is the OCR word list (window-local coordinates), so a
///   hit reproduces exactly the same words -> same Presidio text -> same
///   detections. Persistent PII therefore keeps being detected and redacted
///   on every frame it remains on screen; it is not "cleared" by a cache hit.
/// - A 64-bit non-crypto hash collision would reuse stale words for changed
///   pixels. The window bytes are also length-checked via the geometry key,
///   and the hash covers every byte; the residual collision probability over a
///   session is negligible, and the failure mode degrades detection on a
///   single frame (the next changed frame re-OCRs), it does not persistently
///   blind the guard.
#[derive(Default)]
struct OcrCache {
    // (win_y0, win_y1, pixel_hash) -> window-local OCR words.
    entries: HashMap<(usize, usize, u64), Arc<Vec<Word>>>,
}

/// Hashes a window's exact pixel bytes together with its dimensions. Two
/// windows collide in the map only if geometry AND every pixel byte match.
fn hash_window(crop: &[u8], width: usize, height: usize) -> u64 {
    let mut h = std::collections::hash_map::DefaultHasher::new();
    width.hash(&mut h);
    height.hash(&mut h);
    crop.hash(&mut h);
    h.finish()
}

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
    /// OCR result cache (see [`OcrCache`]). Interior-mutable so `analyze`
    /// keeps its `&self` signature; only ever locked briefly to read a hit or
    /// publish the new snapshot, never across an OCR/Presidio await.
    cache: Mutex<OcrCache>,
}

impl BandAnalyzer {
    /// Builds the production analyzer from a resolved guard config (gateway
    /// policy + agent-local endpoints).
    pub fn from_config(cfg: &super::config::GuardConfig) -> anyhow::Result<Self> {
        Ok(Self {
            ocr: OcrClient::new(&cfg.ocr_url)?,
            presidio: PresidioClient::new(&cfg.presidio_url)?,
            params: cfg.params.clone(),
            cache: Mutex::new(OcrCache::default()),
        })
    }
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
        //
        // Per chunk we first hash its exact pixels and consult the OCR cache.
        // A hit reuses the cached window-local words (no OCR round-trip) — the
        // repaint-storm fast path. A miss spawns an OCR task. Every chunk's
        // window-local words (hit or freshly OCR'd) are recorded so this
        // call's cache snapshot can be published at the end, byte-keyed.
        let cache_key = |c: &OcrChunk, hash: u64| (c.win.y0, c.win.y1, hash);

        // window-local words per chunk (the cache stores pre-shift words so a
        // hit is independent of where own_words later places them).
        let mut chunk_local: Vec<Option<Arc<Vec<Word>>>> = vec![None; chunks.len()];
        let mut chunk_hash: Vec<u64> = vec![0; chunks.len()];
        let mut handles = Vec::new();
        {
            let cache = self.cache.lock().expect("piigate: OCR cache poisoned");
            for (idx, chunk) in chunks.iter().enumerate() {
                let crop = fb[chunk.win.y0 * stride..chunk.win.y1 * stride].to_vec();
                let win_height = chunk.win.height();
                let hash = hash_window(&crop, fb_width, win_height);
                chunk_hash[idx] = hash;
                if let Some(words) = cache.entries.get(&cache_key(chunk, hash)) {
                    // Cache hit: identical pixels were OCR'd before. Reuse the
                    // words — persistent on-screen PII is therefore re-detected
                    // every frame, never silently cleared by a hit.
                    chunk_local[idx] = Some(words.clone());
                    continue;
                }
                let ocr = self.ocr.clone();
                let win = chunk.win;
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
                            format!("OCR failed on chunk [{},{})", win.y0, win.y1)
                        })?,
                        _ = cancel.cancelled() => return Ok((idx, Vec::new())),
                    };
                    Ok::<_, anyhow::Error>((idx, words))
                }));
            }
        }

        // Collect the OCR'd (miss) chunks. On the first error, cancel the rest
        // and propagate (the gate fails closed on an analyzer error).
        let mut first_err = None;
        for h in handles {
            match h.await.context("piigate: OCR task panicked")? {
                Ok((idx, words)) => chunk_local[idx] = Some(Arc::new(words)),
                Err(e) => {
                    cancel.cancel();
                    first_err = first_err.or(Some(e));
                }
            }
        }
        if let Some(e) = first_err {
            return Err(e);
        }

        // Publish this call's cache snapshot: only the windows analyzed now,
        // keyed by geometry+hash. Replacing (not merging) bounds the cache to
        // the current dirty set and naturally evicts windows no longer dirty.
        {
            let mut cache = self.cache.lock().expect("piigate: OCR cache poisoned");
            cache.entries.clear();
            for (idx, chunk) in chunks.iter().enumerate() {
                if let Some(words) = &chunk_local[idx] {
                    cache
                        .entries
                        .insert(cache_key(chunk, chunk_hash[idx]), words.clone());
                }
            }
        }

        // Shift each chunk's window-local words into full-screen space and
        // keep only the lines the chunk owns (overlap seams deduplicated),
        // preserving top-to-bottom order across chunks.
        let all_words: Vec<Word> = chunks
            .iter()
            .enumerate()
            .flat_map(|(idx, chunk)| {
                let local = chunk_local[idx].as_ref().expect("every chunk resolved");
                own_words(local.as_ref().clone(), *chunk)
            })
            .collect();

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

    // ---- OCR cache (content-dedup) correctness ----------------------------
    //
    // These drive a REAL BandAnalyzer against stub OCR + Presidio HTTP servers
    // so the production cache path is exercised end to end. The stub counts
    // OCR calls; the assertions prove the security-critical invariants:
    // unchanged pixels skip OCR (the speedup) while changed pixels are ALWAYS
    // re-OCR'd (no PII slips through), and a cache hit still yields the same
    // words (persistent on-screen PII keeps being detected).

    use std::sync::atomic::{AtomicUsize, Ordering};

    use axum::{extract::State, routing::post, Json, Router};
    use serde_json::{json, Value};

    use super::super::config::GuardConfig;
    use super::super::presidio::AnalysisParams;

    #[derive(Clone)]
    struct StubState {
        ocr_calls: Arc<AtomicUsize>,
        // The single word the stub OCR "reads"; its text drives Presidio.
        word_text: Arc<std::sync::Mutex<String>>,
    }

    async fn stub_ocr(State(s): State<StubState>, _body: axum::body::Bytes) -> Json<Value> {
        s.ocr_calls.fetch_add(1, Ordering::SeqCst);
        let text = s.word_text.lock().unwrap().clone();
        Json(json!({
            "duration_ms": 1.0,
            "words": [{ "text": text, "conf": 95.0, "x": 4, "y": 4, "w": 40, "h": 12 }],
        }))
    }

    // Stub Presidio: flags the substring "SSN" as US_SSN, else nothing. Lets a
    // test assert detections survive a cache hit without a real analyzer.
    async fn stub_analyze(body: axum::body::Bytes) -> Json<Value> {
        let req: Value = serde_json::from_slice(&body).unwrap();
        let text = req.get("text").and_then(|t| t.as_str()).unwrap_or("");
        if let Some(start) = text.find("SSN") {
            return Json(json!([{
                "start": start, "end": start + 3, "score": 0.99, "entity_type": "US_SSN"
            }]));
        }
        Json(json!([]))
    }

    /// Spawns the stub servers and returns (base_url, ocr_call_counter,
    /// settable_word_text). One axum app serves both /ocr and /analyze.
    async fn spawn_stub() -> (String, Arc<AtomicUsize>, Arc<std::sync::Mutex<String>>) {
        let ocr_calls = Arc::new(AtomicUsize::new(0));
        let word_text = Arc::new(std::sync::Mutex::new("hello world".to_string()));
        let state = StubState {
            ocr_calls: ocr_calls.clone(),
            word_text: word_text.clone(),
        };
        let app = Router::new()
            .route("/ocr", post(stub_ocr))
            .route("/analyze", post(stub_analyze))
            .with_state(state);
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        (format!("http://{addr}"), ocr_calls, word_text)
    }

    fn analyzer_for(base: &str) -> BandAnalyzer {
        let cfg = GuardConfig {
            ocr_url: base.to_string(),
            presidio_url: base.to_string(),
            params: AnalysisParams {
                score_threshold: 0.5,
                entity_denylist: vec![],
                band_padding: 8,
                max_ocr_concurrency: 4,
            },
            policy: super::super::GatePolicy::Redact,
        };
        BandAnalyzer::from_config(&cfg).unwrap()
    }

    // A small framebuffer: width 64, height 64, RGBA. A band covers some rows.
    fn make_fb(width: usize, height: usize, fill: u8) -> Vec<u8> {
        vec![fill; width * height * 4]
    }

    #[tokio::test]
    async fn unchanged_pixels_skip_ocr_on_second_analyze() {
        let (base, ocr_calls, _txt) = spawn_stub().await;
        let analyzer = analyzer_for(&base);
        let (w, h) = (64usize, 64usize);
        let fb = make_fb(w, h, 0xff);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let _ = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        let after_first = ocr_calls.load(Ordering::SeqCst);
        assert!(after_first >= 1, "first analyze must OCR the band");

        // Identical framebuffer + identical bands -> cache hit -> no new OCR.
        let _ = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        assert_eq!(
            ocr_calls.load(Ordering::SeqCst),
            after_first,
            "unchanged pixels must NOT trigger another OCR call"
        );
    }

    #[tokio::test]
    async fn changed_pixels_force_reocr() {
        let (base, ocr_calls, _txt) = spawn_stub().await;
        let analyzer = analyzer_for(&base);
        let (w, h) = (64usize, 64usize);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let fb1 = make_fb(w, h, 0xff);
        let _ = analyzer.analyze(&fb1, w, h, &bands).await.unwrap();
        let after_first = ocr_calls.load(Ordering::SeqCst);

        // A single changed pixel in the band must miss the cache and re-OCR:
        // a changed band is NEVER skipped (no unread PII).
        let mut fb2 = fb1.clone();
        fb2[10 * w * 4 + 20] ^= 0xff;
        let _ = analyzer.analyze(&fb2, w, h, &bands).await.unwrap();
        assert!(
            ocr_calls.load(Ordering::SeqCst) > after_first,
            "changed pixels MUST trigger a fresh OCR (no stale words)"
        );
    }

    #[tokio::test]
    async fn persistent_pii_still_detected_on_cache_hit() {
        let (base, ocr_calls, txt) = spawn_stub().await;
        *txt.lock().unwrap() = "my SSN is here".to_string();
        let analyzer = analyzer_for(&base);
        let (w, h) = (64usize, 64usize);
        let fb = make_fb(w, h, 0xff);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let r1 = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        assert!(r1.is_detection(), "first frame detects the SSN");
        let after_first = ocr_calls.load(Ordering::SeqCst);

        // Same pixels next frame: OCR is skipped (cache hit) BUT the SSN must
        // still be detected — a persistent on-screen secret is re-flagged, not
        // silently cleared by the cache.
        let r2 = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        assert_eq!(ocr_calls.load(Ordering::SeqCst), after_first, "cache hit: no re-OCR");
        assert!(
            r2.is_detection(),
            "persistent PII must remain detected on a cache hit"
        );
    }
}
