//! Per-session latency aggregation for the PII guard hot path.
//!
//! The guard runs OCR + Presidio per held batch; on a busy RDP session that
//! is many batches per second. Logging every batch would flood the agent log
//! and is unreadable, so this module accumulates per-stage timings into a
//! fixed time window (default 5s) and emits ONE summary line per window with
//! count + avg/p50/p95/max for each stage. That is enough to find the
//! dominant offender (OCR vs Presidio vs pixel compositing vs redaction)
//! without per-batch spam.
//!
//! Design notes:
//! - There is no background timer task. The window is flushed lazily on the
//!   next record after it elapses, and once more on `flush_final` at gate
//!   close. A guard that stops producing batches simply stops logging, and
//!   nothing outlives the session — there is no task to cancel or leak.
//! - Stages are recorded independently (`record_ocr`, `record_presidio`, …)
//!   rather than as one combined per-batch struct, because the OCR/Presidio
//!   timings are measured inside the analyzer while compositing/redaction/
//!   end-to-end are measured by the gate loop around it. Decoupling the record
//!   calls keeps each scope from having to thread a shared struct across the
//!   analyzer/gate boundary.
//! - All durations are stored as microseconds (`u64`) to keep the samples
//!   compact and the percentile sort cheap; they are rendered as
//!   floating-point milliseconds in the summary.

use std::time::{Duration, Instant};

use parking_lot::Mutex;
use tracing::info;

/// How long a window accumulates before it is summarized and reset.
const WINDOW: Duration = Duration::from_secs(5);

/// Hard cap on samples retained per stage within one window. A busy session
/// can analyze many batches per second; without a cap a 5s window on a
/// pathological stream could retain a huge sample vector (and a costlier
/// percentile sort). Once the cap is hit, further samples for that stage are
/// dropped — the percentiles become an estimate over the first `CAP` samples
/// of the window, which is plenty to spot the dominant stage. Counts of
/// dropped samples are not tracked: this is diagnostic, not billing.
const MAX_SAMPLES_PER_STAGE: usize = 4096;

/// One stage's samples within the current window.
#[derive(Default)]
struct StageSamples {
    micros: Vec<u64>,
}

impl StageSamples {
    fn push(&mut self, us: u64) {
        // Zero-duration samples are excluded (a stage that did not run, or one
        // that rounded below 1µs and carries no signal) so `n=` reflects real
        // work and matches the Go side.
        if us == 0 || self.micros.len() >= MAX_SAMPLES_PER_STAGE {
            return;
        }
        self.micros.push(us);
    }

    /// Computes count/avg/p50/p95/max for the stage, or `None` if it never
    /// ran this window. Consumes (sorts) the collected samples.
    fn summarize(&mut self) -> Option<StageSummary> {
        if self.micros.is_empty() {
            return None;
        }
        self.micros.sort_unstable();
        let n = self.micros.len();
        let sum: u64 = self.micros.iter().copied().sum();
        Some(StageSummary {
            count: n,
            avg_ms: (sum as f64 / n as f64) / 1000.0,
            p50_ms: percentile(&self.micros, 0.50) / 1000.0,
            p95_ms: percentile(&self.micros, 0.95) / 1000.0,
            max_ms: (*self.micros.last().expect("non-empty") as f64) / 1000.0,
        })
    }
}

struct StageSummary {
    count: usize,
    avg_ms: f64,
    p50_ms: f64,
    p95_ms: f64,
    max_ms: f64,
}

impl std::fmt::Display for StageSummary {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "n={} avg={:.1}ms p50={:.1}ms p95={:.1}ms max={:.1}ms",
            self.count, self.avg_ms, self.p50_ms, self.p95_ms, self.max_ms
        )
    }
}

/// Nearest-rank percentile over an already-sorted ascending slice. `q` in
/// [0,1]. Returns microseconds as f64.
fn percentile(sorted: &[u64], q: f64) -> f64 {
    debug_assert!(!sorted.is_empty());
    let rank = (q * (sorted.len() as f64 - 1.0)).round() as usize;
    sorted[rank.min(sorted.len() - 1)] as f64
}

#[derive(Default)]
struct Window {
    started_at: Option<Instant>,
    batches: usize,
    detections: usize,
    forwarded_bytes: u64,
    composite: StageSamples,
    ocr: StageSamples,
    presidio: StageSamples,
    redact: StageSamples,
    total: StageSamples,
}

impl Window {
    /// Whether anything has been recorded since the last flush.
    fn is_empty(&self) -> bool {
        self.started_at.is_none()
    }

    /// Marks the window start on the first record after a flush and returns
    /// whether the window has now run for at least [`WINDOW`].
    fn touch_and_elapsed(&mut self) -> bool {
        match self.started_at {
            None => {
                self.started_at = Some(Instant::now());
                false
            }
            Some(t) => t.elapsed() >= WINDOW,
        }
    }
}

/// Per-session latency aggregator. Cheap to clone-share via `Arc`; both the
/// gate loop (compositing/redaction/total) and the analyzer (OCR/Presidio)
/// record into the same instance, so one window covers the whole pipeline.
pub struct LatencyAggregator {
    session_id: String,
    window: Mutex<Window>,
}

impl LatencyAggregator {
    pub fn new(session_id: impl Into<String>) -> Self {
        Self {
            session_id: session_id.into(),
            window: Mutex::new(Window::default()),
        }
    }

    /// Records the OCR-phase wall-clock for one batch.
    pub fn record_ocr(&self, d: Duration) {
        self.push(|w| w.ocr.push(as_micros(d)));
    }

    /// Records the Presidio analyze HTTP call for one batch.
    pub fn record_presidio(&self, d: Duration) {
        self.push(|w| w.presidio.push(as_micros(d)));
    }

    /// Records PDU compositing (RLE decompress + pixel conversion) for one
    /// batch.
    pub fn record_composite(&self, d: Duration) {
        self.push(|w| w.composite.push(as_micros(d)));
    }

    /// Records the redaction (PDU rewrite + pixel blanking) for one batch.
    pub fn record_redact(&self, d: Duration) {
        self.push(|w| w.redact.push(as_micros(d)));
    }

    /// Records the end-to-end batch latency (analysis start to forward/drop)
    /// plus the batch's metadata. This is the per-batch anchor: it increments
    /// the batch counter, so it must be called once per analyzed batch.
    pub fn record_batch(&self, total: Duration, forwarded_bytes: u64, detection: bool) {
        self.push(|w| {
            w.batches += 1;
            if detection {
                w.detections += 1;
            }
            w.forwarded_bytes += forwarded_bytes;
            w.total.push(as_micros(total));
        });
    }

    /// Applies `f` to the current window. If the window has elapsed, the full
    /// window is detached under the lock and then summarized + logged OUTSIDE
    /// the lock, so the (sort + format + log) critical section never blocks
    /// concurrent recorders.
    fn push(&self, f: impl FnOnce(&mut Window)) {
        let detached = {
            let mut w = self.window.lock();
            let elapsed = w.touch_and_elapsed();
            f(&mut w);
            if elapsed {
                Some(std::mem::take(&mut *w))
            } else {
                None
            }
        };
        if let Some(w) = detached {
            self.log_window(w);
        }
    }

    /// Emits a final summary at gate close so the last (partial) window is not
    /// lost. A no-op if nothing was recorded since the last flush.
    pub fn flush_final(&self) {
        let detached = {
            let mut w = self.window.lock();
            if w.is_empty() {
                None
            } else {
                Some(std::mem::take(&mut *w))
            }
        };
        if let Some(w) = detached {
            self.log_window(w);
        }
    }

    /// Summarizes and logs a detached window. Must be called WITHOUT the lock
    /// held.
    fn log_window(&self, mut w: Window) {
        let elapsed = w.started_at.map_or(Duration::ZERO, |t| t.elapsed());
        let summarize = |s: &mut StageSamples| {
            s.summarize()
                .map(|sum| sum.to_string())
                .unwrap_or_else(|| "n=0".to_string())
        };
        info!(
            sid = %self.session_id,
            window_s = elapsed.as_secs_f64(),
            batches = w.batches,
            detections = w.detections,
            forwarded_kib = (w.forwarded_bytes / 1024),
            "piigate latency: composite[{}] ocr[{}] presidio[{}] redact[{}] total[{}]",
            summarize(&mut w.composite),
            summarize(&mut w.ocr),
            summarize(&mut w.presidio),
            summarize(&mut w.redact),
            summarize(&mut w.total),
        );
    }
}

/// Saturating microsecond conversion (a >584,000-year duration would be
/// needed to overflow u64 micros, so saturation is purely defensive).
fn as_micros(d: Duration) -> u64 {
    d.as_micros().min(u64::MAX as u128) as u64
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc;

    #[test]
    fn percentile_nearest_rank() {
        let s = [10u64, 20, 30, 40, 50];
        assert_eq!(percentile(&s, 0.0), 10.0);
        assert_eq!(percentile(&s, 0.5), 30.0);
        assert_eq!(percentile(&s, 1.0), 50.0);
        // p95 of 5 samples -> rank round(0.95*4)=round(3.8)=4 -> 50
        assert_eq!(percentile(&s, 0.95), 50.0);
    }

    #[test]
    fn percentile_single_sample() {
        assert_eq!(percentile(&[42], 0.0), 42.0);
        assert_eq!(percentile(&[42], 0.95), 42.0);
        assert_eq!(percentile(&[42], 1.0), 42.0);
    }

    #[test]
    fn summary_stats() {
        let mut s = StageSamples::default();
        for us in [1000u64, 2000, 3000, 4000] {
            s.push(us);
        }
        let sum = s.summarize().expect("has samples");
        assert_eq!(sum.count, 4);
        assert!((sum.avg_ms - 2.5).abs() < 1e-9, "avg {}", sum.avg_ms);
        assert_eq!(sum.max_ms, 4.0);
    }

    #[test]
    fn empty_stage_has_no_summary() {
        let mut s = StageSamples::default();
        assert!(s.summarize().is_none());
    }

    #[test]
    fn independent_stage_recording() {
        let agg = LatencyAggregator::new("sid");
        agg.record_ocr(Duration::from_millis(5));
        agg.record_presidio(Duration::from_millis(3));
        agg.record_batch(Duration::from_millis(9), 2048, false);
        let w = agg.window.lock();
        assert_eq!(w.ocr.micros.len(), 1);
        assert_eq!(w.presidio.micros.len(), 1);
        assert_eq!(w.total.micros.len(), 1);
        // redact/composite were never recorded -> no samples.
        assert!(w.redact.micros.is_empty());
        assert!(w.composite.micros.is_empty());
        assert_eq!(w.batches, 1);
        assert_eq!(w.forwarded_bytes, 2048);
    }

    #[test]
    fn detection_and_bytes_accumulate() {
        let agg = LatencyAggregator::new("sid");
        agg.record_batch(Duration::from_millis(1), 2048, true);
        agg.record_batch(Duration::from_millis(1), 1024, false);
        let w = agg.window.lock();
        assert_eq!(w.batches, 2);
        assert_eq!(w.detections, 1);
        assert_eq!(w.forwarded_bytes, 3072);
    }

    #[test]
    fn window_starts_lazily() {
        // A fresh aggregator has no window start until the first record, so
        // flush_final on an untouched aggregator is a no-op.
        let agg = LatencyAggregator::new("sid");
        assert!(agg.window.lock().is_empty());
        agg.flush_final();
        assert!(agg.window.lock().is_empty());
        agg.record_ocr(Duration::from_millis(1));
        assert!(!agg.window.lock().is_empty());
    }

    #[test]
    fn flush_final_resets_window() {
        let agg = LatencyAggregator::new("sid");
        agg.record_batch(Duration::from_millis(1), 100, false);
        agg.flush_final();
        let w = agg.window.lock();
        assert!(w.is_empty());
        assert_eq!(w.batches, 0);
    }

    #[test]
    fn as_micros_conversion() {
        assert_eq!(as_micros(Duration::from_millis(1)), 1000);
        assert_eq!(as_micros(Duration::ZERO), 0);
    }

    #[test]
    fn zero_duration_excluded() {
        let mut s = StageSamples::default();
        s.push(0);
        s.push(1000);
        assert_eq!(s.micros.len(), 1, "zero-µs sample must be dropped");
    }

    #[test]
    fn samples_capped_per_window() {
        let mut s = StageSamples::default();
        for _ in 0..(MAX_SAMPLES_PER_STAGE + 100) {
            s.push(1000);
        }
        assert_eq!(s.micros.len(), MAX_SAMPLES_PER_STAGE);
        // The summary still computes over the retained cap.
        assert!(s.summarize().is_some());
    }

    /// A `MakeWriter` that appends every log line into a shared buffer so the
    /// test can assert the aggregator actually emits a `tracing` line (not
    /// just that it mutates state).
    #[derive(Clone, Default)]
    struct BufWriter(Arc<parking_lot::Mutex<Vec<u8>>>);

    impl std::io::Write for BufWriter {
        fn write(&mut self, buf: &[u8]) -> std::io::Result<usize> {
            self.0.lock().extend_from_slice(buf);
            Ok(buf.len())
        }
        fn flush(&mut self) -> std::io::Result<()> {
            Ok(())
        }
    }

    impl<'a> tracing_subscriber::fmt::MakeWriter<'a> for BufWriter {
        type Writer = BufWriter;
        fn make_writer(&'a self) -> Self::Writer {
            self.clone()
        }
    }

    #[test]
    fn flush_emits_log_line() {
        let buf = Arc::new(parking_lot::Mutex::new(Vec::new()));
        let writer = BufWriter(buf.clone());
        let subscriber = tracing_subscriber::fmt()
            .with_writer(writer)
            .with_ansi(false)
            .finish();

        tracing::subscriber::with_default(subscriber, || {
            let agg = LatencyAggregator::new("sid-log-test");
            agg.record_composite(Duration::from_micros(400));
            agg.record_ocr(Duration::from_millis(58));
            agg.record_presidio(Duration::from_millis(12));
            agg.record_redact(Duration::from_millis(3));
            agg.record_batch(Duration::from_millis(74), 8 * 1024 * 1024, true);
            agg.flush_final();
        });

        let out = String::from_utf8(buf.lock().clone()).expect("utf8 log");
        assert!(out.contains("piigate latency:"), "missing prefix in: {out}");
        assert!(out.contains("sid-log-test"), "missing sid in: {out}");
        assert!(out.contains("batches=1"), "missing batches in: {out}");
        assert!(out.contains("detections=1"), "missing detections in: {out}");
        // Each stage rendered with its summary.
        for stage in ["composite[", "ocr[", "presidio[", "redact[", "total["] {
            assert!(out.contains(stage), "missing {stage} in: {out}");
        }
        // OCR avg should reflect the 58ms sample.
        assert!(out.contains("ocr[n=1 avg=58.0ms"), "wrong ocr summary in: {out}");
    }
}
