//! Band-scoped framebuffer analysis: OCR over dirty bands, then Presidio.
//!
//! Port of `AnalyzeFramebufferBands` in `gateway/rdp/analyzer/region.go`:
//! only the rows that changed since the previous analysis are OCR'd, with
//! tall bands split into overlapping chunks OCR'd in parallel. Word
//! coordinates are translated back to full-screen space, so detections are
//! indistinguishable from full-frame analysis.

use std::collections::HashMap;
use std::hash::{Hash as _, Hasher as _};
use std::sync::Arc;

use parking_lot::Mutex;

use anyhow::Context as _;
use async_trait::async_trait;
use tokio::sync::Semaphore;
use tokio_util::sync::CancellationToken;

use super::bands::{split_bands, OcrChunk, YBand, DEFAULT_BAND_PADDING, MAX_CHUNK_ROWS};
use super::metrics::LatencyAggregator;
use super::ocr::{join_words, OcrClient, Word};
use super::presidio::{analyze_text, AnalysisParams, PresidioClient, SnapshotResult};

/// Per-window OCR result cache: skips re-OCR of a band whose pixels are
/// byte-identical to a RECENT time that exact window was OCR'd.
///
/// Live RDP produces a storm of tiny repaints (blinking carets, ticking
/// clocks, focus rectangles) that re-dirty the same rows continuously. Without
/// this cache every repaint re-OCRs the whole band, saturating the engine.
///
/// The cache retains a bounded set of recently seen window states — not just
/// the last analyze's — because the canonical repaint storm ALTERNATES
/// between states: a blinking caret produces pixel states A/B/A/B, and a
/// cache that only remembers the current state misses on every single frame
/// (A evicts B, B evicts A, forever). Keeping the last few generations turns
/// that ping-pong into pure hits. Entries older than the newest
/// [`MAX_OCR_CACHE_ENTRIES`] states are evicted by recency.
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
    // Window state -> (recency stamp, window-local OCR words).
    entries: HashMap<WindowKey, (u64, Arc<Vec<Word>>)>,
    // Monotonic per-analyze stamp used for recency eviction.
    generation: u64,
}

/// Identifies one OCR'd window state: (win_y0, win_y1, pixel_hash).
type WindowKey = (usize, usize, u64);

/// Bounds [`OcrCache`]: enough distinct window states to absorb alternating
/// repaints (caret blink = 2 states/window) and a handful of concurrently
/// dirty windows, small enough that the retained word lists are negligible.
const MAX_OCR_CACHE_ENTRIES: usize = 64;

impl OcrCache {
    /// Records `key -> words` stamped with the current generation.
    fn insert(&mut self, key: WindowKey, words: Arc<Vec<Word>>) {
        self.entries.insert(key, (self.generation, words));
    }

    /// Looks up a window state, refreshing its recency stamp on a hit.
    fn get(&mut self, key: &WindowKey) -> Option<Arc<Vec<Word>>> {
        let generation = self.generation;
        self.entries.get_mut(key).map(|(stamp, words)| {
            *stamp = generation;
            words.clone()
        })
    }

    /// Drops the oldest PRIOR-generation entries (by stamp) until at most
    /// [`MAX_OCR_CACHE_ENTRIES`] remain. Entries touched by the CURRENT
    /// analyze (hit or insert — both stamp with the current generation) are
    /// never evicted: this analyze's own states must survive to the next
    /// frame or the cache would defeat itself. Consequently the cap can be
    /// exceeded transiently when a single analyze touches more than
    /// MAX_OCR_CACHE_ENTRIES windows (bounded by that analyze's chunk
    /// count); the overshoot is reclaimed on the next eviction. Called once
    /// per analyze; n is tiny.
    fn evict(&mut self) {
        let excess = self.entries.len().saturating_sub(MAX_OCR_CACHE_ENTRIES);
        if excess == 0 {
            return;
        }
        let generation = self.generation;
        let mut old: Vec<u64> = self
            .entries
            .values()
            .map(|(s, _)| *s)
            .filter(|s| *s < generation)
            .collect();
        if old.is_empty() {
            return; // everything is current-generation: keep it all
        }
        old.sort_unstable();
        let cutoff = old[excess.min(old.len()) - 1];
        // Keep current-generation entries unconditionally and prior entries
        // strictly newer than the cutoff; stamp ties at the cutoff are
        // dropped together (may undershoot the cap, never starve the
        // current frame).
        self.entries
            .retain(|_, (s, _)| *s == generation || *s > cutoff);
    }
}

/// Presidio result cache: skips the Presidio round trip when this batch's OCR
/// produced exactly the same words (text and geometry) as the previous one.
///
/// With the OCR cache absorbing repaint storms, consecutive batches routinely
/// yield an identical word set — same joined text, same coordinates — and
/// identical input to a deterministic analyzer yields the identical result.
/// The key is the full word vector, not just the text: the same text at a
/// different screen position (scroll) must re-map bounding boxes, and word
/// geometry participates in [`same_words`], so it does.
///
/// Only successful results are cached; an error always retries next batch.
struct PresidioCache {
    words: Vec<Word>,
    result: SnapshotResult,
}

/// Dedup equality for the Presidio cache: text and geometry only. OCR
/// confidence is deliberately EXCLUDED — Presidio never sees it (only the
/// joined text and, via the word ranges, the geometry), so two word sets that
/// differ only in confidence produce the identical analysis. Comparing floats
/// would make the dedup fragile against engines that report slightly
/// different confidences for identical reads.
fn same_words(a: &[Word], b: &[Word]) -> bool {
    a.len() == b.len()
        && a.iter().zip(b).all(|(x, y)| {
            x.text == y.text
                && x.left == y.left
                && x.top == y.top
                && x.width == y.width
                && x.height == y.height
        })
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
    /// Presidio result cache (see [`PresidioCache`]). Same locking
    /// discipline: never held across an await.
    presidio_cache: Mutex<Option<PresidioCache>>,
    /// Shared per-session latency aggregator. The analyzer records the two
    /// network-bound stages (OCR, Presidio); the gate records compositing /
    /// redaction / end-to-end into the same instance, so one window summary
    /// covers the whole pipeline.
    metrics: Arc<LatencyAggregator>,
}

impl BandAnalyzer {
    /// Builds the production analyzer from a resolved guard config (gateway
    /// policy + agent-local endpoints), sharing `metrics` with the gate.
    pub fn from_config(
        cfg: &super::config::GuardConfig,
        metrics: Arc<LatencyAggregator>,
    ) -> anyhow::Result<Self> {
        Ok(Self {
            ocr: OcrClient::new(&cfg.ocr_url)?,
            presidio: PresidioClient::new(&cfg.presidio_url)?,
            params: cfg.params.clone(),
            cache: Mutex::new(OcrCache::default()),
            presidio_cache: Mutex::new(None),
            metrics,
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

        // Wall-clock for the whole OCR phase (cache lookups + parallel sidecar
        // round-trips + collection), recorded into the shared latency window.
        let ocr_started = std::time::Instant::now();

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

        // Each OCR'd chunk owns a copy of its window rows: the framebuffer
        // slice cannot be borrowed across spawned tasks (it is owned and
        // reused by the gate's analysis loop). Chunk windows are bounded
        // (MAX_CHUNK_ROWS + pad), so the copies are small. Indices preserve
        // chunk order for stable text concatenation.
        //
        // Per chunk we first hash its exact pixels IN PLACE (no copy) and
        // consult the OCR cache. A hit reuses the cached window-local words
        // (no OCR round-trip, no window copy) — the repaint-storm fast path.
        // Only a miss copies the window and spawns an OCR task. Every chunk's
        // window-local words (hit or freshly OCR'd) are recorded so this
        // call's states can be published at the end, byte-keyed.
        let cache_key = |c: &OcrChunk, hash: u64| (c.win.y0, c.win.y1, hash);

        // window-local words per chunk (the cache stores pre-shift words so a
        // hit is independent of where own_words later places them).
        let mut chunk_local: Vec<Option<Arc<Vec<Word>>>> = vec![None; chunks.len()];
        let mut chunk_hash: Vec<u64> = vec![0; chunks.len()];
        let mut handles = Vec::new();
        {
            let mut cache = self.cache.lock();
            cache.generation += 1;
            for (idx, chunk) in chunks.iter().enumerate() {
                let win = &fb[chunk.win.y0 * stride..chunk.win.y1 * stride];
                let win_height = chunk.win.height();
                let hash = hash_window(win, fb_width, win_height);
                chunk_hash[idx] = hash;
                if let Some(words) = cache.get(&cache_key(chunk, hash)) {
                    // Cache hit: identical pixels were OCR'd before. Reuse the
                    // words — persistent on-screen PII is therefore re-detected
                    // every frame, never silently cleared by a hit.
                    chunk_local[idx] = Some(words);
                    continue;
                }
                let crop = win.to_vec();
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
                        return Ok((idx, Vec::new(), 0.0, 0usize));
                    }
                    let extract = tokio::select! {
                        res = ocr.extract(&crop, fb_width, win_height) => res.with_context(|| {
                            format!("OCR failed on chunk [{},{})", win.y0, win.y1)
                        })?,
                        _ = cancel.cancelled() => return Ok((idx, Vec::new(), 0.0, 0usize)),
                    };
                    Ok::<_, anyhow::Error>((idx, extract.words, extract.server_ms, extract.request_bytes))
                }));
            }
        }

        // Collect the OCR'd (miss) chunks. On the first error, cancel the rest
        // and propagate (the gate fails closed on an analyzer error).
        //
        // The chunks OCR in PARALLEL, so the per-batch number comparable to the
        // OCR wall-clock is the SLOWEST chunk's sidecar inference time (the
        // critical path), not the sum. If wall-clock >> max chunk inference,
        // the gap is queueing/contention/transport, not the GPU. We also track
        // bytes/calls/chunks to gauge payload size and cache effectiveness.
        // Cache hits contribute nothing (no round-trip), which is the point.
        let mut first_err = None;
        let mut server_ms_max = 0.0f64;
        let mut request_bytes_sum = 0usize;
        let mut ocr_calls = 0usize;
        for h in handles {
            match h.await.context("piigate: OCR task panicked")? {
                Ok((idx, words, server_ms, request_bytes)) => {
                    chunk_local[idx] = Some(Arc::new(words));
                    if request_bytes > 0 {
                        if server_ms > server_ms_max {
                            server_ms_max = server_ms;
                        }
                        request_bytes_sum += request_bytes;
                        ocr_calls += 1;
                    }
                }
                Err(e) => {
                    cancel.cancel();
                    first_err = first_err.or(Some(e));
                }
            }
        }
        // Record OCR phase wall-clock and the sidecar-internal breakdown before
        // any early return, so a slow or failing OCR sidecar (the most likely
        // offender) still shows up in the latency window.
        self.metrics.record_ocr(ocr_started.elapsed());
        self.metrics
            .record_ocr_detail(server_ms_max, request_bytes_sum as u64, ocr_calls, chunks.len());
        if let Some(e) = first_err {
            return Err(e);
        }

        // Publish this call's freshly-OCR'd window states, keyed by
        // geometry+hash, MERGING with the retained recent states (hits were
        // already stamped by `get`). Merging — not replacing — is what
        // absorbs alternating repaints: a blinking caret's two pixel states
        // must both stay resident or every frame misses. Recency eviction
        // bounds the cache (see [`MAX_OCR_CACHE_ENTRIES`]).
        {
            let mut cache = self.cache.lock();
            for (idx, chunk) in chunks.iter().enumerate() {
                if let Some(words) = &chunk_local[idx] {
                    cache.insert(cache_key(chunk, chunk_hash[idx]), words.clone());
                }
            }
            cache.evict();
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

        // Presidio dedup: identical words (text AND geometry) to the previous
        // batch produce the identical result from a deterministic analyzer —
        // skip the round trip and return the cached outcome. Geometry
        // participates in the comparison, so the same text at a new screen
        // position (scroll) misses and re-maps its bounding boxes. Like OCR
        // cache hits, a skipped call records no presidio latency sample.
        {
            let cached = self.presidio_cache.lock();
            if let Some(entry) = cached.as_ref()
                && same_words(&entry.words, &all_words)
            {
                return Ok(entry.result.clone());
            }
        }

        let text = join_words(&all_words);
        // Gate-close cancellation is handled at the mod.rs boundary (the
        // whole analyze() future is dropped), which aborts this Presidio
        // request via reqwest's drop — no inner select needed here.
        let presidio_started = std::time::Instant::now();
        let res = analyze_text(&self.presidio, &text, &all_words, &self.params).await;
        self.metrics.record_presidio(presidio_started.elapsed());
        if let Ok(result) = &res {
            *self.presidio_cache.lock() = Some(PresidioCache {
                words: all_words,
                result: result.clone(),
            });
        }
        res
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

    use std::sync::atomic::AtomicBool;

    #[derive(Clone)]
    struct StubState {
        ocr_calls: Arc<AtomicUsize>,
        presidio_calls: Arc<AtomicUsize>,
        // The single word the stub OCR "reads"; its text drives Presidio.
        word_text: Arc<std::sync::Mutex<String>>,
        // When set, /analyze returns HTTP 500 (Presidio outage simulation).
        fail_presidio: Arc<AtomicBool>,
    }

    async fn stub_ocr(State(s): State<StubState>, _body: axum::body::Bytes) -> Json<Value> {
        let call = s.ocr_calls.fetch_add(1, Ordering::SeqCst);
        let text = s.word_text.lock().unwrap().clone();
        // Confidence varies per call ON PURPOSE: identical text/geometry with
        // drifting confidence is exactly what a real engine produces for
        // near-identical pixels, and the Presidio dedup must not key on it.
        let conf = 0.90 + (call % 10) as f64 * 0.005;
        Json(json!({
            "duration_ms": 1.0,
            "words": [{ "text": text, "conf": conf, "x": 4, "y": 4, "w": 40, "h": 12 }],
        }))
    }

    // Stub Presidio: flags the substring "SSN" as US_SSN, else nothing. Lets a
    // test assert detections survive a cache hit without a real analyzer.
    async fn stub_analyze(
        State(s): State<StubState>,
        body: axum::body::Bytes,
    ) -> Result<Json<Value>, axum::http::StatusCode> {
        s.presidio_calls.fetch_add(1, Ordering::SeqCst);
        if s.fail_presidio.load(Ordering::SeqCst) {
            return Err(axum::http::StatusCode::INTERNAL_SERVER_ERROR);
        }
        let req: Value = serde_json::from_slice(&body).unwrap();
        let text = req.get("text").and_then(|t| t.as_str()).unwrap_or("");
        if let Some(start) = text.find("SSN") {
            return Ok(Json(json!([{
                "start": start, "end": start + 3, "score": 0.99, "entity_type": "US_SSN"
            }])));
        }
        Ok(Json(json!([])))
    }

    /// Spawns the stub servers and returns (base_url, stub_state). One axum
    /// app serves both /ocr and /analyze; the state carries the call counters
    /// and the settable OCR word text.
    async fn spawn_stub() -> (String, StubState) {
        let state = StubState {
            ocr_calls: Arc::new(AtomicUsize::new(0)),
            presidio_calls: Arc::new(AtomicUsize::new(0)),
            word_text: Arc::new(std::sync::Mutex::new("hello world".to_string())),
            fail_presidio: Arc::new(AtomicBool::new(false)),
        };
        let app = Router::new()
            .route("/ocr", post(stub_ocr))
            .route("/analyze", post(stub_analyze))
            .with_state(state.clone());
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        (format!("http://{addr}"), state)
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
        BandAnalyzer::from_config(&cfg, Arc::new(LatencyAggregator::new("test"))).unwrap()
    }

    // A small framebuffer: width 64, height 64, RGBA. A band covers some rows.
    fn make_fb(width: usize, height: usize, fill: u8) -> Vec<u8> {
        vec![fill; width * height * 4]
    }

    #[tokio::test]
    async fn unchanged_pixels_skip_ocr_on_second_analyze() {
        let (base, stub) = spawn_stub().await;
        let ocr_calls = stub.ocr_calls;
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
        let (base, stub) = spawn_stub().await;
        let ocr_calls = stub.ocr_calls;
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
        let (base, stub) = spawn_stub().await;
        let ocr_calls = stub.ocr_calls;
        *stub.word_text.lock().unwrap() = "my SSN is here".to_string();
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

    /// The canonical repaint storm: a band ALTERNATES between two pixel
    /// states (blinking caret on/off). Both states must stay resident in the
    /// OCR cache so every frame after the first two is a hit — the old
    /// replace-on-publish policy evicted A when caching B and vice versa,
    /// missing on every single frame.
    #[tokio::test]
    async fn alternating_states_hit_cache_after_first_cycle() {
        let (base, stub) = spawn_stub().await;
        let ocr_calls = stub.ocr_calls;
        let analyzer = analyzer_for(&base);
        let (w, h) = (64usize, 64usize);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let fb_a = make_fb(w, h, 0xaa); // caret off
        let mut fb_b = fb_a.clone(); // caret on: a few pixels differ
        for px in 0..8 {
            fb_b[16 * w * 4 + px * 4] = 0x00;
        }

        let _ = analyzer.analyze(&fb_a, w, h, &bands).await.unwrap();
        let _ = analyzer.analyze(&fb_b, w, h, &bands).await.unwrap();
        let after_first_cycle = ocr_calls.load(Ordering::SeqCst);
        assert!(after_first_cycle >= 2, "both states OCR'd once");

        // Ten more blink cycles: every analyze must be a cache hit.
        for _ in 0..10 {
            let _ = analyzer.analyze(&fb_a, w, h, &bands).await.unwrap();
            let _ = analyzer.analyze(&fb_b, w, h, &bands).await.unwrap();
        }
        assert_eq!(
            ocr_calls.load(Ordering::SeqCst),
            after_first_cycle,
            "alternating known states must never re-OCR (blink ping-pong)"
        );
    }

    /// Presidio dedup: identical OCR words across batches skip the Presidio
    /// round trip and still return the same detection outcome.
    #[tokio::test]
    async fn identical_words_skip_presidio_round_trip() {
        let (base, stub) = spawn_stub().await;
        *stub.word_text.lock().unwrap() = "my SSN is here".to_string();
        let analyzer = analyzer_for(&base);
        let (w, h) = (64usize, 64usize);
        let fb = make_fb(w, h, 0xff);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let r1 = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        assert!(r1.is_detection());
        let after_first = stub.presidio_calls.load(Ordering::SeqCst);
        assert_eq!(after_first, 1, "first analyze calls Presidio");

        for _ in 0..5 {
            let r = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
            assert!(r.is_detection(), "cached result must preserve the detection");
            assert_eq!(r.detections.len(), r1.detections.len());
        }
        assert_eq!(
            stub.presidio_calls.load(Ordering::SeqCst),
            after_first,
            "identical words must not re-call Presidio"
        );
    }

    /// The Presidio dedup key is the words INCLUDING geometry: the same text
    /// at a different screen position (scroll) must re-call Presidio so the
    /// detection bounding boxes are re-mapped, never reused stale.
    #[tokio::test]
    async fn same_text_at_new_position_recalls_presidio() {
        let (base, stub) = spawn_stub().await;
        *stub.word_text.lock().unwrap() = "my SSN is here".to_string();
        let analyzer = analyzer_for(&base);
        let (w, h) = (64usize, 64usize);
        let fb = make_fb(w, h, 0xff);

        let r1 = analyzer
            .analyze(&fb, w, h, &[YBand { y0: 0, y1: 32 }])
            .await
            .unwrap();
        assert!(r1.is_detection());
        let after_first = stub.presidio_calls.load(Ordering::SeqCst);

        // Same pixels/text OCR'd from a band 32 rows lower: joined text is
        // identical but word geometry differs -> dedup must miss.
        let r2 = analyzer
            .analyze(&fb, w, h, &[YBand { y0: 32, y1: 64 }])
            .await
            .unwrap();
        assert!(
            stub.presidio_calls.load(Ordering::SeqCst) > after_first,
            "shifted geometry must re-call Presidio"
        );
        assert!(r2.is_detection());
        assert_ne!(
            r1.detections[0].y, r2.detections[0].y,
            "bounding boxes must be re-mapped at the new position"
        );
    }

    /// Presidio dedup keys on text+geometry, NOT confidence: a re-OCR that
    /// reads the same words at the same coordinates with drifted confidence
    /// (the stub bumps conf per call) must still skip the round trip —
    /// Presidio never sees confidence, so its result cannot differ.
    #[tokio::test]
    async fn conf_drift_alone_still_skips_presidio() {
        let (base, stub) = spawn_stub().await;
        *stub.word_text.lock().unwrap() = "my SSN is here".to_string();
        let analyzer = analyzer_for(&base);
        let (w, h) = (64usize, 64usize);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let fb1 = make_fb(w, h, 0xff);
        let r1 = analyzer.analyze(&fb1, w, h, &bands).await.unwrap();
        assert!(r1.is_detection());
        let after_first = stub.presidio_calls.load(Ordering::SeqCst);

        // One changed pixel forces a genuine re-OCR (no OCR cache hit); the
        // stub returns the same text/geometry with a different confidence.
        let mut fb2 = fb1.clone();
        fb2[5 * w * 4] ^= 0xff;
        let r2 = analyzer.analyze(&fb2, w, h, &bands).await.unwrap();
        assert!(stub.ocr_calls.load(Ordering::SeqCst) >= 2, "changed pixels re-OCR'd");
        assert_eq!(
            stub.presidio_calls.load(Ordering::SeqCst),
            after_first,
            "confidence drift alone must not re-call Presidio"
        );
        assert!(r2.is_detection(), "cached result preserved across conf drift");
    }

    /// A failed Presidio call must not be cached: the next batch with the
    /// same words retries and succeeds.
    #[tokio::test]
    async fn presidio_error_is_not_cached() {
        let (base, stub) = spawn_stub().await;
        *stub.word_text.lock().unwrap() = "my SSN is here".to_string();
        stub.fail_presidio.store(true, Ordering::SeqCst);
        let analyzer = analyzer_for(&base);
        let (w, h) = (64usize, 64usize);
        let fb = make_fb(w, h, 0xff);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        assert!(
            analyzer.analyze(&fb, w, h, &bands).await.is_err(),
            "presidio outage surfaces as an analyzer error (gate fails closed)"
        );
        let after_failure = stub.presidio_calls.load(Ordering::SeqCst);

        // Outage over: identical pixels (OCR cache hit -> identical words)
        // must RE-CALL Presidio — the error was never stored as a result.
        stub.fail_presidio.store(false, Ordering::SeqCst);
        let r = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        assert!(
            stub.presidio_calls.load(Ordering::SeqCst) > after_failure,
            "identical words after an error must retry Presidio"
        );
        assert!(r.is_detection());
    }

    /// The empty-words early return must not leak the previous batch's cached
    /// detections: a band that OCRs to nothing yields an empty result even
    /// when the prior (cached) batch detected PII.
    #[tokio::test]
    async fn empty_ocr_returns_empty_not_stale_cache() {
        let (base, stub) = spawn_stub().await;
        *stub.word_text.lock().unwrap() = "my SSN is here".to_string();
        let analyzer = analyzer_for(&base);
        let (w, h) = (64usize, 64usize);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let fb1 = make_fb(w, h, 0xff);
        let r1 = analyzer.analyze(&fb1, w, h, &bands).await.unwrap();
        assert!(r1.is_detection(), "first batch detects and populates the cache");

        // The text scrolls away: changed pixels, OCR now reads nothing.
        *stub.word_text.lock().unwrap() = String::new();
        let mut fb2 = fb1.clone();
        fb2[5 * w * 4] ^= 0xff;
        let r2 = analyzer.analyze(&fb2, w, h, &bands).await.unwrap();
        assert!(
            !r2.is_detection() && r2.detections.is_empty(),
            "empty OCR must yield an empty result, never the stale cached detection"
        );
    }

    // ---- OcrCache eviction unit tests --------------------------------------

    fn cache_entry() -> Arc<Vec<Word>> {
        Arc::new(Vec::new())
    }

    #[test]
    fn ocr_cache_evicts_oldest_prior_generations_only() {
        let mut c = OcrCache::default();
        // 80 entries across 80 old generations.
        for i in 0..80usize {
            c.generation = i as u64 + 1;
            c.insert((i, i + 1, i as u64), cache_entry());
        }
        // A new analyze touches one old entry (refreshing its stamp) and
        // inserts one new state.
        c.generation += 1;
        let touched = (0usize, 1usize, 0u64);
        assert!(c.get(&touched).is_some());
        c.insert((100, 101, 100), cache_entry());
        c.evict();

        assert!(c.entries.len() <= MAX_OCR_CACHE_ENTRIES, "cap enforced");
        assert!(
            c.get(&touched).is_some(),
            "an entry hit in the current analyze must survive eviction"
        );
        assert!(
            c.get(&(100, 101, 100)).is_some(),
            "an entry inserted in the current analyze must survive eviction"
        );
        // The oldest untouched generations are the ones that went.
        assert!(c.entries.keys().all(|k| *k == touched || k.0 >= 16 || k.0 == 100));
    }

    #[test]
    fn ocr_cache_never_evicts_current_generation_even_over_cap() {
        let mut c = OcrCache::default();
        c.generation = 1;
        // One analyze touches more windows than the cap (pathological
        // many-band frame): everything is current-generation, all kept.
        for i in 0..(MAX_OCR_CACHE_ENTRIES + 10) {
            c.insert((i, i + 1, i as u64), cache_entry());
        }
        c.evict();
        assert_eq!(
            c.entries.len(),
            MAX_OCR_CACHE_ENTRIES + 10,
            "current-generation entries are never evicted (transient overshoot)"
        );
        // The next analyze reclaims the overshoot.
        c.generation = 2;
        c.insert((999, 1000, 999), cache_entry());
        c.evict();
        assert!(c.entries.len() <= MAX_OCR_CACHE_ENTRIES);
        assert!(c.get(&(999, 1000, 999)).is_some());
    }
}
