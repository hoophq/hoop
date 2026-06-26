//! Band-scoped framebuffer analysis: OCR over dirty bands, then Presidio.
//!
//! Port of `AnalyzeFramebufferBands` in `gateway/rdp/analyzer/region.go`:
//! only the rows that changed since the previous analysis are OCR'd, with
//! tall bands split into overlapping chunks OCR'd in parallel. Word
//! coordinates are translated back to full-screen space, so detections are
//! indistinguishable from full-frame analysis.

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use anyhow::Context as _;
use async_trait::async_trait;

use super::bands::YBand;
use super::metrics::LatencyAggregator;
use super::ocr::{join_words, LineBox, OcrClient, Word};
use super::presidio::{analyze_text, AnalysisParams, PresidioClient, SnapshotResult};

/// Per-LINE OCR result cache: recognition (rec) is the dominant cost, so we
/// skip re-recognizing text lines whose recognition input is byte-identical to
/// the last time that exact line was recognized.
///
/// Detection (det) still runs over the full dirty band every frame — it is
/// cheap and finds the line boxes — so no line is ever missed at the
/// segmentation stage. Only the per-line RECOGNITION is cached. On a typical
/// edit (one line changes) this turns N rec calls into ~1.
///
/// Correctness is the priority over the speedup:
///
/// - The key is the line's full-canvas position PLUS the SHA-256 the sidecar
///   computed over the EXACT perspective crop recognition will see (see
///   [`LineBox::crop_hash`]). The hash therefore covers precisely the pixels
///   that determine the recognized text — not an axis-aligned approximation —
///   so any change that would alter recognition changes the hash and forces a
///   fresh rec. A changed line is NEVER reused (no PII slips through unread),
///   and SHA-256 makes a stale-reuse collision cryptographically infeasible.
/// - The cached value is the line's recognized [`Word`] (full-canvas coords),
///   so a hit reproduces exactly the same word -> same Presidio text -> same
///   detections. Persistent on-screen PII keeps being detected on every frame
///   it remains visible; it is not "cleared" by a cache hit.
/// - The cache is PERSISTENT across frames (unchanged lines stay cached) but
///   bounded: each frame replaces it with exactly the lines detected this
///   frame, so it tracks the current screen and cannot grow unbounded.
#[derive(Default)]
struct LineCache {
    // (x0, y0, x1, y1, crop_sha256) -> the line's recognized word. The position
    // is included so a hit also requires the same on-screen placement (a line
    // that moved is re-analyzed in its new position).
    entries: HashMap<LineKey, Word>,
}

/// Cache key: full-canvas bounding rect plus the SHA-256 (hex) of the exact
/// recognition crop. Position + content together: same pixels at the same place.
type LineKey = (usize, usize, usize, usize, String);

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
    /// Per-line OCR result cache (see [`LineCache`]). Interior-mutable so
    /// `analyze` keeps its `&self` signature; only ever locked briefly to read
    /// hits or publish the new snapshot, never across an OCR/Presidio await.
    cache: Mutex<LineCache>,
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
            cache: Mutex::new(LineCache::default()),
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

        // Wall-clock for the whole OCR phase (det + per-line cache lookups +
        // rec of changed lines), recorded into the shared latency window.
        let ocr_started = std::time::Instant::now();

        // Per dirty band: run detection over the band, then for each detected
        // line consult the per-line cache and recognize only the changed lines.
        // Detection always runs (cheap, scales with band height), so no line is
        // ever missed at segmentation; only the expensive recognition is cached.
        //
        // Words are collected in detection order within each band and bands in
        // top-to-bottom order, matching the previous reading order so Presidio
        // sees the same text. A fresh cache snapshot is built from exactly the
        // lines detected this frame (persistent across frames, but bounded to
        // the current screen).
        let mut all_words: Vec<Word> = Vec::new();
        let mut new_cache = LineCache::default();
        let mut server_ms_max = 0.0f64;
        let mut request_bytes_sum = 0usize;
        let mut lines_total = 0usize;
        let mut lines_recognized = 0usize;

        for band in bands {
            let band_h = band.y1 - band.y0;
            // Crop the band's full-width rows (band-local image) for det/rec.
            let band_rgba = fb[band.y0 * stride..band.y1 * stride].to_vec();

            let det = self
                .ocr
                .detect(&band_rgba, fb_width, band_h)
                .await
                .with_context(|| format!("OCR detect failed on band [{},{})", band.y0, band.y1))?;
            if det.server_ms > server_ms_max {
                server_ms_max = det.server_ms;
            }

            // For each detected line, look it up in the cache by its
            // recognition-crop hash + full-canvas position. Hits reuse the
            // cached word; misses are collected for a single rec call on this
            // band.
            //
            // `miss_boxes` are band-local (for the rec request); `slots` records
            // per-detection whether the line was a hit (with its word) or a miss
            // (with its miss-box index), so cached and freshly-recognized words
            // are reassembled in detection order. `keys[i]` is the full-canvas
            // cache key for detection i, or None if the line is uncacheable.
            #[derive(Clone)]
            enum Slot {
                Cached(Word),
                Miss(usize), // index into miss_boxes
            }
            let mut slots: Vec<Slot> = Vec::with_capacity(det.boxes.len());
            let mut miss_boxes: Vec<LineBox> = Vec::new();
            let mut keys: Vec<Option<LineKey>> = Vec::with_capacity(det.boxes.len());

            {
                let cache = self.cache.lock().expect("piigate: line cache poisoned");
                for line in &det.boxes {
                    lines_total += 1;
                    // Position in full-canvas coords (for placement + cache
                    // position component); content validity comes from the
                    // sidecar-computed crop hash, not these bounds.
                    let (bx0, by0, bx1, by1) = line.bounds(); // band-local
                    let key: LineKey = (
                        bx0,
                        by0 + band.y0,
                        bx1,
                        by1 + band.y0,
                        line.crop_hash.clone(),
                    );
                    keys.push(Some(key.clone()));
                    if let Some(word) = cache.entries.get(&key) {
                        // Cache hit: this exact line (position + recognition
                        // pixels) was recognized before. Reuse its word —
                        // persistent PII is re-detected every frame, never
                        // cleared by a hit.
                        slots.push(Slot::Cached(word.clone()));
                    } else {
                        slots.push(Slot::Miss(miss_boxes.len()));
                        miss_boxes.push(line.clone());
                    }
                }
            }

            // Recognize the changed lines in a single rec call (band-local
            // boxes; the sidecar crops and recognizes, returning full-image
            // coords within the band).
            let rec_words = if miss_boxes.is_empty() {
                Vec::new()
            } else {
                let rec = self
                    .ocr
                    .recognize(&band_rgba, fb_width, band_h, &miss_boxes)
                    .await
                    .with_context(|| {
                        format!("OCR recognize failed on band [{},{})", band.y0, band.y1)
                    })?;
                // Fail closed on a cardinality mismatch: /rec must return exactly
                // one word per requested box. A short response would otherwise
                // silently drop detected lines from analysis (unanalyzed pixels
                // reaching the client) — never acceptable in a fail-closed gate.
                if rec.words.len() != miss_boxes.len() {
                    anyhow::bail!(
                        "OCR recognize returned {} words for {} boxes on band [{},{})",
                        rec.words.len(),
                        miss_boxes.len(),
                        band.y0,
                        band.y1
                    );
                }
                if rec.server_ms > server_ms_max {
                    server_ms_max = rec.server_ms;
                }
                request_bytes_sum += rec.request_bytes;
                lines_recognized += miss_boxes.len();
                rec.words
            };

            // Reassemble words in detection order, shifting band-local Y into
            // full-canvas space, and publish each line into the new cache.
            for (det_idx, slot) in slots.into_iter().enumerate() {
                let word = match slot {
                    Slot::Cached(w) => w, // already full-canvas
                    Slot::Miss(mi) => {
                        // rec returns one word per requested box, in order
                        // (cardinality validated above, so the index is valid).
                        let mut w = rec_words[mi].clone();
                        w.top += band.y0; // band-local -> full-canvas
                        w
                    }
                };
                if let Some(key) = &keys[det_idx] {
                    new_cache.entries.insert(key.clone(), word.clone());
                }
                all_words.push(word);
            }
        }

        // Record OCR phase wall-clock + the sidecar-internal breakdown and the
        // line-cache effectiveness before any early return.
        self.metrics.record_ocr(ocr_started.elapsed());
        self.metrics.record_ocr_detail(
            server_ms_max,
            request_bytes_sum as u64,
            lines_recognized,
            lines_total,
        );
        self.metrics.record_lines(lines_total as u64, lines_recognized as u64);

        // Publish the new cache snapshot (replace, not merge): the cache now
        // holds exactly the lines on screen this frame, so it tracks the screen
        // and stays bounded.
        {
            let mut cache = self.cache.lock().expect("piigate: line cache poisoned");
            *cache = new_cache;
        }

        if all_words.is_empty() {
            return Ok(SnapshotResult::default());
        }
        let text = join_words(&all_words);
        // Gate-close cancellation is handled at the mod.rs boundary (the
        // whole analyze() future is dropped), which aborts this Presidio
        // request via reqwest's drop — no inner select needed here.
        let presidio_started = std::time::Instant::now();
        let res = analyze_text(&self.presidio, &text, &all_words, &self.params).await;
        self.metrics.record_presidio(presidio_started.elapsed());
        res
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn line_box_bounds_axis_aligned() {
        let b = LineBox {
            quad: [[4.0, 24.0], [206.0, 24.0], [206.0, 58.0], [4.0, 58.0]],
            crop_hash: "deadbeef".into(),
        };
        assert_eq!(b.bounds(), (4, 24, 206, 58));
    }

    #[test]
    fn line_box_bounds_ceils_max_floors_min() {
        // Fractional detector output: min floored, max ceiled so the bounds
        // never exclude a partially-covered edge pixel.
        let b = LineBox {
            quad: [[4.4, 24.6], [205.2, 24.6], [205.2, 57.1], [4.4, 57.1]],
            crop_hash: "x".into(),
        };
        assert_eq!(b.bounds(), (4, 24, 206, 58));
    }

    // ---- Per-line OCR cache correctness -----------------------------------
    //
    // These drive a REAL BandAnalyzer against a stub sidecar (/det + /rec) and
    // stub Presidio so the production line-cache path is exercised end to end.
    // The stub counts REC calls (the cached stage) and lines recognized; the
    // assertions prove the security-critical invariants: an unchanged line
    // skips rec (the speedup) while ANY changed line is always re-recognized
    // (no PII slips through), and a cache hit still yields the same word
    // (persistent on-screen PII keeps being detected).

    use std::sync::atomic::{AtomicUsize, Ordering};

    use axum::{extract::State, routing::post, Json, Router};
    use serde_json::{json, Value};

    use super::super::config::GuardConfig;
    use super::super::presidio::AnalysisParams;

    #[derive(Clone)]
    struct StubState {
        // Number of /rec calls and total boxes recognized.
        rec_calls: Arc<AtomicUsize>,
        rec_boxes: Arc<AtomicUsize>,
        // The single word the stub "reads" per detected box; drives Presidio.
        word_text: Arc<std::sync::Mutex<String>>,
    }

    // Stub /det: returns ONE line box covering the top-left of the band, with a
    // crop_hash derived from the box's pixels in the sent BMP. Mirrors the real
    // sidecar contract: the hash changes iff the recognition pixels change, so
    // the agent's per-line cache hits on identical content and misses on a
    // change. We hash the box region of the decoded image (the stub sends a BMP
    // identical to the real client) so a changed pixel inside [4,4]-(44,16)
    // produces a different hash.
    async fn stub_det(body: axum::body::Bytes) -> Json<Value> {
        let hash = stub_crop_hash(&body);
        Json(json!({
            "duration_ms": 1.0,
            "boxes": [{
                "quad": [[4.0, 4.0], [44.0, 4.0], [44.0, 16.0], [4.0, 16.0]],
                "crop_hash": hash,
            }],
        }))
    }

    // Hash the box region [4,4)-(44,16) out of a 24bpp bottom-up BMP body,
    // approximating what the real sidecar does (hash the exact recognition
    // crop). Good enough for the cache tests: identical body -> identical hash,
    // a changed pixel in the region -> different hash.
    fn stub_crop_hash(bmp: &[u8]) -> String {
        use std::hash::{Hash as _, Hasher as _};
        // BMP pixel data starts at offset 54 (BITMAPFILEHEADER+INFOHEADER).
        // The stub frames are tiny (64x*); just hash the whole pixel area — any
        // pixel change anywhere changes it, which is a superset of "box region
        // changed" and keeps the test's changed->miss invariant exact. Rendered
        // as 64-char hex so it passes the agent's SHA-256 format validation.
        let mut h = std::collections::hash_map::DefaultHasher::new();
        bmp.get(54..).unwrap_or(bmp).hash(&mut h);
        let v = h.finish();
        format!("{v:016x}{v:016x}{v:016x}{v:016x}")
    }

    // Stub /rec: returns one word per requested box, counting the call.
    async fn stub_rec(State(s): State<StubState>, body: axum::body::Bytes) -> Json<Value> {
        s.rec_calls.fetch_add(1, Ordering::SeqCst);
        let req: Value = serde_json::from_slice(&body).unwrap();
        let boxes = req.get("boxes").and_then(|b| b.as_array()).cloned().unwrap_or_default();
        s.rec_boxes.fetch_add(boxes.len(), Ordering::SeqCst);
        let text = s.word_text.lock().unwrap().clone();
        let words: Vec<Value> = boxes
            .iter()
            .map(|_| json!({ "text": text, "conf": 95.0, "x": 4, "y": 4, "w": 40, "h": 12 }))
            .collect();
        Json(json!({ "duration_ms": 1.0, "words": words }))
    }

    // Stub Presidio: flags the substring "SSN" as US_SSN, else nothing.
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

    struct Stub {
        base: String,
        rec_calls: Arc<AtomicUsize>,
        rec_boxes: Arc<AtomicUsize>,
        word_text: Arc<std::sync::Mutex<String>>,
    }

    /// Spawns the stub sidecar (/det, /rec) + Presidio (/analyze).
    async fn spawn_stub() -> Stub {
        let rec_calls = Arc::new(AtomicUsize::new(0));
        let rec_boxes = Arc::new(AtomicUsize::new(0));
        let word_text = Arc::new(std::sync::Mutex::new("hello world".to_string()));
        let state = StubState {
            rec_calls: rec_calls.clone(),
            rec_boxes: rec_boxes.clone(),
            word_text: word_text.clone(),
        };
        let app = Router::new()
            .route("/det", post(stub_det))
            .route("/rec", post(stub_rec))
            .route("/analyze", post(stub_analyze))
            .with_state(state);
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        Stub {
            base: format!("http://{addr}"),
            rec_calls,
            rec_boxes,
            word_text,
        }
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
    async fn unchanged_line_skips_rec_on_second_analyze() {
        let stub = spawn_stub().await;
        let analyzer = analyzer_for(&stub.base);
        let (w, h) = (64usize, 64usize);
        let fb = make_fb(w, h, 0xff);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let _ = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        let after_first = stub.rec_boxes.load(Ordering::SeqCst);
        assert!(after_first >= 1, "first analyze must recognize the detected line");

        // Identical framebuffer -> det finds the same line, same pixels -> cache
        // hit -> NO new rec (det still runs, but rec is skipped).
        let _ = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        assert_eq!(
            stub.rec_boxes.load(Ordering::SeqCst),
            after_first,
            "unchanged line must NOT trigger another rec call"
        );
    }

    #[tokio::test]
    async fn changed_line_forces_rerec() {
        let stub = spawn_stub().await;
        let analyzer = analyzer_for(&stub.base);
        let (w, h) = (64usize, 64usize);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let fb1 = make_fb(w, h, 0xff);
        let _ = analyzer.analyze(&fb1, w, h, &bands).await.unwrap();
        let after_first = stub.rec_boxes.load(Ordering::SeqCst);

        // A changed pixel INSIDE the detected line's rect (the stub box is
        // [4,4]-[44,16]) must miss the cache and re-recognize: a changed line is
        // NEVER reused (no unread PII).
        let mut fb2 = fb1.clone();
        fb2[8 * w * 4 + 10 * 4] ^= 0xff; // row 8, col 10 -> inside [4,4)-(44,16)
        let _ = analyzer.analyze(&fb2, w, h, &bands).await.unwrap();
        assert!(
            stub.rec_boxes.load(Ordering::SeqCst) > after_first,
            "a changed line MUST trigger a fresh rec (no stale words)"
        );
    }

    #[tokio::test]
    async fn persistent_pii_still_detected_on_cache_hit() {
        let stub = spawn_stub().await;
        *stub.word_text.lock().unwrap() = "my SSN is here".to_string();
        let analyzer = analyzer_for(&stub.base);
        let (w, h) = (64usize, 64usize);
        let fb = make_fb(w, h, 0xff);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let r1 = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        assert!(r1.is_detection(), "first frame detects the SSN");
        let after_first = stub.rec_boxes.load(Ordering::SeqCst);

        // Same pixels next frame: rec is skipped (cache hit) BUT the SSN must
        // still be detected — a persistent on-screen secret is re-flagged, not
        // silently cleared by the cache.
        let r2 = analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        assert_eq!(
            stub.rec_boxes.load(Ordering::SeqCst),
            after_first,
            "cache hit: no re-rec"
        );
        assert!(
            r2.is_detection(),
            "persistent PII must remain detected on a cache hit"
        );
    }

    #[tokio::test]
    async fn rec_call_count_reflects_caching() {
        // Two analyses of the identical frame: exactly one rec CALL total (the
        // first); the second is fully served from the line cache.
        let stub = spawn_stub().await;
        let analyzer = analyzer_for(&stub.base);
        let (w, h) = (64usize, 64usize);
        let fb = make_fb(w, h, 0xff);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        analyzer.analyze(&fb, w, h, &bands).await.unwrap();
        assert_eq!(
            stub.rec_calls.load(Ordering::SeqCst),
            1,
            "second identical frame must issue no rec call (full cache hit)"
        );
    }

    // A /det that returns two boxes but a /rec that returns only one word: the
    // analyzer must FAIL (fail-closed), never silently drop the second line.
    async fn stub_det_two(_body: axum::body::Bytes) -> Json<Value> {
        // Two distinct, well-formed (64-hex) crop hashes.
        let ha = "a".repeat(64);
        let hb = "b".repeat(64);
        Json(json!({
            "duration_ms": 1.0,
            "boxes": [
                {"quad": [[4.0,4.0],[44.0,4.0],[44.0,16.0],[4.0,16.0]], "crop_hash": ha},
                {"quad": [[4.0,20.0],[44.0,20.0],[44.0,30.0],[4.0,30.0]], "crop_hash": hb},
            ],
        }))
    }
    async fn stub_rec_one(_body: axum::body::Bytes) -> Json<Value> {
        Json(json!({
            "duration_ms": 1.0,
            "words": [{ "text": "only-one", "conf": 95.0, "x": 4, "y": 4, "w": 40, "h": 12 }],
        }))
    }
    async fn stub_analyze_empty(_body: axum::body::Bytes) -> Json<Value> {
        Json(json!([]))
    }

    #[tokio::test]
    async fn rec_cardinality_mismatch_fails_closed() {
        let app = Router::new()
            .route("/det", post(stub_det_two))
            .route("/rec", post(stub_rec_one))
            .route("/analyze", post(stub_analyze_empty));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        let analyzer = analyzer_for(&format!("http://{addr}"));
        let (w, h) = (64usize, 64usize);
        let fb = make_fb(w, h, 0xff);
        let bands = vec![YBand { y0: 0, y1: 32 }];

        let res = analyzer.analyze(&fb, w, h, &bands).await;
        assert!(
            res.is_err(),
            "a /rec returning fewer words than boxes must fail the analysis (fail-closed), not drop lines"
        );
    }

    #[tokio::test]
    async fn moved_line_is_reanalyzed_not_reused() {
        // Same pixels (same crop_hash from the stub) but the analyzer keys also
        // on POSITION. A first frame caches the line at band [0,32); analyzing a
        // band at a different y-range must NOT reuse it (the position component
        // of the key differs), so rec runs again.
        let stub = spawn_stub().await;
        let analyzer = analyzer_for(&stub.base);
        let (w, h) = (64usize, 96usize);
        let fb = make_fb(w, h, 0xff);

        analyzer.analyze(&fb, w, h, &[YBand { y0: 0, y1: 32 }]).await.unwrap();
        let after_first = stub.rec_boxes.load(Ordering::SeqCst);
        // Different band position -> different full-canvas key -> miss -> re-rec.
        analyzer.analyze(&fb, w, h, &[YBand { y0: 40, y1: 72 }]).await.unwrap();
        assert!(
            stub.rec_boxes.load(Ordering::SeqCst) > after_first,
            "a line at a new screen position must be re-recognized, not reused"
        );
    }
}
