//! Gate behavior and adversarial leak tests — port of
//! `gateway/rdp/piigate_test.go`. The leak tests prove the zero-leak
//! pipeline property with a perfect detector (the signature detector) and
//! the client oracle: no per-PDU client-renderable state may ever show
//! content the detector would have flagged.

use std::pin::Pin;
use std::sync::atomic::{AtomicBool, AtomicI32, Ordering};
use std::sync::Arc;
use std::task::{Context, Poll};
use std::time::Duration;

use async_trait::async_trait;
use parking_lot::Mutex;
use tokio::io::AsyncWrite;
use tokio::sync::mpsc;

use super::analyze::Analyzer;
use super::bands::YBand;
use super::canvas::ShadowCanvas;
use super::framing::{pdu_size, FastPathParser};
use super::presidio::{EntityDetection, SnapshotResult};
use super::testpdu::*;
use super::{GateEvent, GatePolicy, PiiGate, MAX_HELD_BYTES};

// --- Test harness -----------------------------------------------------------

/// An AsyncWrite sink capturing forwarded bytes, with an injectable failure.
#[derive(Clone, Default)]
struct TestSink {
    buf: Arc<Mutex<Vec<u8>>>,
    fail: Arc<AtomicBool>,
}

impl TestSink {
    fn bytes(&self) -> Vec<u8> {
        self.buf.lock().clone()
    }
}

impl AsyncWrite for TestSink {
    fn poll_write(
        self: Pin<&mut Self>,
        _cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<std::io::Result<usize>> {
        if self.fail.load(Ordering::SeqCst) {
            return Poll::Ready(Err(std::io::Error::other("client gone")));
        }
        self.buf.lock().extend_from_slice(buf);
        Poll::Ready(Ok(buf.len()))
    }

    fn poll_flush(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        Poll::Ready(Ok(()))
    }

    fn poll_shutdown(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        Poll::Ready(Ok(()))
    }
}

/// Collects terminal gate events for assertions.
#[derive(Clone, Default)]
struct EventLog {
    detections: Arc<AtomicI32>,
    overloads: Arc<AtomicI32>,
    analysis_errors: Arc<AtomicI32>,
}

impl EventLog {
    fn detections(&self) -> i32 {
        self.detections.load(Ordering::SeqCst)
    }
    fn overloads(&self) -> i32 {
        self.overloads.load(Ordering::SeqCst)
    }
    fn analysis_errors(&self) -> i32 {
        self.analysis_errors.load(Ordering::SeqCst)
    }
}

struct Harness {
    gate: PiiGate,
    sink: TestSink,
    events: EventLog,
}

fn new_test_gate(analyzer: Arc<dyn Analyzer>) -> Harness {
    new_test_gate_with_policy(analyzer, GatePolicy::Kill)
}

fn new_test_gate_with_policy(analyzer: Arc<dyn Analyzer>, policy: GatePolicy) -> Harness {
    let (tx, mut rx) = mpsc::unbounded_channel();
    let events = EventLog::default();
    let log = events.clone();
    tokio::spawn(async move {
        while let Some(ev) = rx.recv().await {
            match ev {
                GateEvent::Detection(_) => log.detections.fetch_add(1, Ordering::SeqCst),
                GateEvent::Overload { .. } => log.overloads.fetch_add(1, Ordering::SeqCst),
                GateEvent::AnalysisError => log.analysis_errors.fetch_add(1, Ordering::SeqCst),
            };
        }
    });
    let sink = TestSink::default();
    let gate = PiiGate::spawn("sid-test", analyzer, sink.clone(), tx, 0, policy);
    Harness { gate, sink, events }
}

/// Polls until `cond` returns true or the deadline expires.
async fn wait_for(what: &str, mut cond: impl FnMut() -> bool) {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(2);
    while !cond() {
        if tokio::time::Instant::now() > deadline {
            panic!("timeout waiting for {what}");
        }
        tokio::time::sleep(Duration::from_millis(2)).await;
    }
}

/// Reports whether every pixel of the rect matches the BGR color.
#[allow(clippy::too_many_arguments)]
fn rect_is_color(
    fb: &[u8],
    fb_w: usize,
    fb_h: usize,
    x: usize,
    y: usize,
    w: usize,
    h: usize,
    bgr: [u8; 3],
) -> bool {
    if x + w > fb_w || y + h > fb_h {
        return false;
    }
    for row in y..y + h {
        for col in x..x + w {
            let p = (row * fb_w + col) * 4;
            if fb[p] != bgr[2] || fb[p + 1] != bgr[1] || fb[p + 2] != bgr[0] {
                return false;
            }
        }
    }
    true
}

/// Reports whether any rendered pixel is the signature color.
fn any_magenta_pixel(fb: &[u8]) -> bool {
    fb.chunks_exact(4)
        .any(|p| p[0] == 0xff && p[1] == 0x00 && p[2] == 0xff)
}

/// A perfect detector for the planted magenta signature rect: it fires iff
/// the FULL rect is visible in the analyzed framebuffer. It stands in for
/// OCR+Presidio so tests prove pipeline properties (what is analyzed and
/// what escapes) independently of detection accuracy.
struct SignatureDetector {
    calls: Arc<AtomicI32>,
    x: usize,
    y: usize,
    w: usize,
    h: usize,
}

impl SignatureDetector {
    fn new(x: usize, y: usize, w: usize, h: usize) -> (Arc<Self>, Arc<AtomicI32>) {
        let calls = Arc::new(AtomicI32::new(0));
        (
            Arc::new(Self {
                calls: calls.clone(),
                x,
                y,
                w,
                h,
            }),
            calls,
        )
    }
}

#[async_trait]
impl Analyzer for SignatureDetector {
    async fn analyze(
        &self,
        fb: &[u8],
        fb_w: usize,
        fb_h: usize,
        _bands: &[YBand],
    ) -> anyhow::Result<SnapshotResult> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        let mut res = SnapshotResult::default();
        if rect_is_color(fb, fb_w, fb_h, self.x, self.y, self.w, self.h, MAGENTA) {
            res.counts.insert("TEST_SIGNATURE".into(), 1);
            res.detections.push(EntityDetection {
                entity_type: "TEST_SIGNATURE".into(),
                score: 1.0,
                x: self.x,
                y: self.y,
                width: self.w,
                height: self.h,
            });
        }
        Ok(res)
    }
}

/// An analyzer that must never be invoked (asserted via the call counter).
struct CountingNopAnalyzer {
    calls: Arc<AtomicI32>,
}

impl CountingNopAnalyzer {
    fn new() -> (Arc<Self>, Arc<AtomicI32>) {
        let calls = Arc::new(AtomicI32::new(0));
        (Arc::new(Self { calls: calls.clone() }), calls)
    }
}

#[async_trait]
impl Analyzer for CountingNopAnalyzer {
    async fn analyze(
        &self,
        _fb: &[u8],
        _w: usize,
        _h: usize,
        _bands: &[YBand],
    ) -> anyhow::Result<SnapshotResult> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        Ok(SnapshotResult::default())
    }
}

/// An analyzer that always errors (the fail-closed path).
struct FailingAnalyzer;

#[async_trait]
impl Analyzer for FailingAnalyzer {
    async fn analyze(
        &self,
        _fb: &[u8],
        _w: usize,
        _h: usize,
        _bands: &[YBand],
    ) -> anyhow::Result<SnapshotResult> {
        anyhow::bail!("presidio is down")
    }
}

/// Sets a flag when dropped — how a cancelled (dropped) analysis future is
/// observed from the outside.
struct DropFlag(Arc<AtomicBool>);

impl Drop for DropFlag {
    fn drop(&mut self) {
        self.0.store(true, Ordering::SeqCst);
    }
}

/// A stuck analyzer: signals entry, then never completes. Cancellation (the
/// gate dropping the future) is observable through `dropped`.
struct BlockingAnalyzer {
    entered: Arc<tokio::sync::Notify>,
    entered_flag: Arc<AtomicBool>,
    dropped: Arc<AtomicBool>,
}

impl BlockingAnalyzer {
    fn new() -> (Arc<Self>, Arc<AtomicBool>, Arc<AtomicBool>) {
        let entered_flag = Arc::new(AtomicBool::new(false));
        let dropped = Arc::new(AtomicBool::new(false));
        (
            Arc::new(Self {
                entered: Arc::new(tokio::sync::Notify::new()),
                entered_flag: entered_flag.clone(),
                dropped: dropped.clone(),
            }),
            entered_flag,
            dropped,
        )
    }
}

#[async_trait]
impl Analyzer for BlockingAnalyzer {
    async fn analyze(
        &self,
        _fb: &[u8],
        _w: usize,
        _h: usize,
        _bands: &[YBand],
    ) -> anyhow::Result<SnapshotResult> {
        self.entered_flag.store(true, Ordering::SeqCst);
        self.entered.notify_one();
        let _flag = DropFlag(self.dropped.clone());
        std::future::pending::<()>().await;
        unreachable!()
    }
}

/// Reconstructs what an RDP client would render from the bytes the gate
/// forwarded: it frames PDUs exactly like the gate, composites each PDU
/// atomically onto its own shadow canvas, and invokes `on_state` after every
/// PDU — i.e. for every intermediate screen state a client could display.
struct ClientOracle {
    parser: FastPathParser,
    canvas: ShadowCanvas,
    tail: Vec<u8>,
}

impl ClientOracle {
    fn new() -> Self {
        Self {
            parser: FastPathParser::new(),
            canvas: ShadowCanvas::new("oracle"),
            tail: Vec::new(),
        }
    }

    fn consume(&mut self, data: &[u8], mut on_state: impl FnMut(&[u8], usize, usize)) {
        self.tail.extend_from_slice(data);
        loop {
            let size = pdu_size(&self.tail);
            if size == 0 || size > self.tail.len() {
                break;
            }
            let pdu: Vec<u8> = self.tail.drain(..size).collect();
            for patch in self.parser.parse(&pdu) {
                self.canvas.composite(&patch);
            }
            on_state(&self.canvas.fb, self.canvas.w, self.canvas.h);
        }
    }
}

// --- Core behavior ----------------------------------------------------------

#[tokio::test]
async fn forwards_non_bitmap_pdus() {
    let (analyzer, calls) = CountingNopAnalyzer::new();
    let h = new_test_gate(analyzer);

    let pdu = tpkt_pdu(&[0xde, 0xad, 0xbe, 0xef]);
    h.gate.ingest(&pdu);

    wait_for("pdu forwarded", || h.sink.bytes() == pdu).await;
    assert_eq!(calls.load(Ordering::SeqCst), 0, "analyze must not be called for PDUs without bitmaps");
    h.gate.close().await;
}

#[tokio::test]
async fn holds_partial_pdu_until_complete() {
    let (analyzer, _) = CountingNopAnalyzer::new();
    let h = new_test_gate(analyzer);

    let pdu = tpkt_pdu(&[1, 2, 3, 4, 5, 6]);
    h.gate.ingest(&pdu[..3]); // incomplete header+payload
    tokio::time::sleep(Duration::from_millis(50)).await;
    assert!(h.sink.bytes().is_empty(), "partial PDU must be held");

    h.gate.ingest(&pdu[3..]);
    wait_for("completed pdu forwarded", || h.sink.bytes() == pdu).await;
    h.gate.close().await;
}

#[tokio::test]
async fn preserves_order_across_batches() {
    let (analyzer, _) = CountingNopAnalyzer::new();
    let h = new_test_gate(analyzer);

    let mut want = Vec::new();
    for i in 0..50u8 {
        let pdu = tpkt_pdu(&[i, i + 1, i + 2]);
        want.extend_from_slice(&pdu);
        h.gate.ingest(&pdu);
    }

    wait_for("all pdus forwarded in order", || h.sink.bytes() == want).await;
    h.gate.close().await;
}

#[tokio::test]
async fn kills_on_detection() {
    let (det, _) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate(det);

    // A signature bitmap plus a trailing PDU: analysis must detect and kill.
    h.gate.ingest(&fast_path_bitmap_pdu(&[TestRect::new(40, 40, 8, 8, MAGENTA)]));
    h.gate.ingest(&tpkt_pdu(&[0xaa, 0xbb]));

    wait_for("detection event", || h.events.detections() == 1).await;
    assert!(h.sink.bytes().is_empty(), "held bytes must be dropped on detection");
    assert!(h.gate.killed(), "gate must report killed");

    // After the kill, further ingests are discarded.
    h.gate.ingest(&tpkt_pdu(&[0xcc]));
    tokio::time::sleep(Duration::from_millis(50)).await;
    assert!(h.sink.bytes().is_empty(), "post-kill ingest must not forward");
    assert_eq!(h.events.detections(), 1, "detection must fire exactly once");
    h.gate.close().await;
}

#[tokio::test]
async fn clean_analysis_forwards() {
    let (det, calls) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate(det);

    let bmp_pdu = fast_path_bitmap_pdu(&[TestRect::new(0, 0, 64, 32, WHITE)]);
    let tail_pdu = tpkt_pdu(&[0x01, 0x02]);
    h.gate.ingest(&bmp_pdu);
    h.gate.ingest(&tail_pdu);

    let want = [bmp_pdu, tail_pdu].concat();
    wait_for("clean batch forwarded", || h.sink.bytes() == want).await;
    assert!(calls.load(Ordering::SeqCst) > 0, "analysis must run when bands are dirty");
    assert_eq!(h.events.detections(), 0);
    h.gate.close().await;
}

#[tokio::test]
async fn analysis_error_fails_closed() {
    // An analyzer error (OCR/Presidio failure or timeout) must NOT forward the
    // unverified batch — that would leak PII exactly when the engine is
    // overloaded. The gate drops the held batch, emits AnalysisError, and
    // terminates (fail-closed), regardless of policy.
    let h = new_test_gate(Arc::new(FailingAnalyzer));

    let bmp_pdu = fast_path_bitmap_pdu(&[TestRect::new(0, 0, 32, 16, WHITE)]);
    h.gate.ingest(&bmp_pdu);

    wait_for("analysis error terminates gate", || h.gate.killed()).await;
    assert_eq!(
        h.events.analysis_errors(),
        1,
        "an AnalysisError event must be emitted on analyzer failure"
    );
    assert_eq!(h.events.detections(), 0, "an analyzer error is not a detection");
    assert!(
        h.sink.bytes().is_empty(),
        "the unverified bitmap batch must never be forwarded (fail-closed)"
    );

    // Post-termination ingest is dropped, not forwarded.
    let after = h.sink.bytes().len();
    h.gate.ingest(&tpkt_pdu(&[0x42]));
    tokio::time::sleep(std::time::Duration::from_millis(50)).await;
    assert_eq!(h.sink.bytes().len(), after, "post-kill ingest must not forward");
    h.gate.close().await;
}

#[tokio::test]
async fn unknown_bytes_are_never_dropped() {
    let (analyzer, _) = CountingNopAnalyzer::new();
    let h = new_test_gate(analyzer);

    // 0xFE has action bits 0x02: neither Fast-Path (0x00) nor TPKT (0x03).
    // The framer skips such bytes one at a time; they must still be
    // forwarded to the client in order — the gate never drops data. The
    // final byte stays buffered (a single byte cannot be framed).
    let garbage = vec![0xfeu8; 100];
    h.gate.ingest(&garbage);

    wait_for("unknown bytes forwarded", || h.sink.bytes() == garbage[..99]).await;
    h.gate.close().await;
}

#[tokio::test]
async fn pseudo_tpkt_garbage_is_forwarded_framed() {
    let (analyzer, _) = CountingNopAnalyzer::new();
    let h = new_test_gate(analyzer);

    // 0xFF has action bits 0x03 and is framed as a TPKT PDU with the length
    // taken from bytes 2-3 (0xFFFF). A complete pseudo-PDU must be
    // forwarded; the remainder stays buffered awaiting more data.
    let garbage = vec![0xffu8; 0xffff + 10];
    h.gate.ingest(&garbage);

    wait_for("framed garbage forwarded", || h.sink.bytes() == garbage[..0xffff]).await;
    h.gate.close().await;
}

#[tokio::test]
async fn backlog_overflow_fails_closed() {
    let (analyzer, _, _) = BlockingAnalyzer::new();
    let h = new_test_gate(analyzer);

    // Get the analyzer stuck on a first dirty batch.
    h.gate.ingest(&fast_path_bitmap_pdu(&[TestRect::new(0, 0, 32, 16, WHITE)]));
    h.gate.ingest(&tpkt_pdu(&[0x01]));

    // Flood past the cap while analysis is stuck. Each pseudo-TPKT PDU is
    // 64KiB; push > MAX_HELD_BYTES worth.
    let big_pdu = tpkt_pdu(&vec![0xab; 0xfff0]);
    for _ in 0..=(MAX_HELD_BYTES / big_pdu.len() + 1) {
        h.gate.ingest(&big_pdu);
    }

    wait_for("overload event", || h.events.overloads() == 1).await;
    assert!(h.gate.killed(), "gate must report killed after overflow");

    // Further ingests are discarded and never re-trigger the event.
    h.gate.ingest(&big_pdu);
    tokio::time::sleep(Duration::from_millis(20)).await;
    assert_eq!(h.events.overloads(), 1, "overload must fire exactly once");
    h.gate.close().await;
}

#[tokio::test]
async fn forward_error_stops_gate() {
    let (analyzer, _) = CountingNopAnalyzer::new();
    let h = new_test_gate(analyzer);
    h.sink.fail.store(true, Ordering::SeqCst);

    h.gate.ingest(&tpkt_pdu(&[0x01, 0x02]));

    // The forward failure must close the gate; later ingests are dropped and
    // close does not deadlock.
    wait_for("gate closed after forward error", || h.gate.is_closed()).await;
    h.gate.ingest(&tpkt_pdu(&[0x03]));
    h.gate.close().await;
    assert_eq!(h.events.detections(), 0);
    assert_eq!(h.events.overloads(), 0);
}

#[tokio::test]
async fn close_cancels_in_flight_analysis() {
    let (analyzer, entered, dropped) = BlockingAnalyzer::new();
    let h = new_test_gate(analyzer);

    // Get the analyzer stuck on a dirty batch.
    h.gate.ingest(&fast_path_bitmap_pdu(&[TestRect::new(0, 0, 32, 16, WHITE)]));
    h.gate.ingest(&tpkt_pdu(&[0x01]));

    wait_for("analysis started", || entered.load(Ordering::SeqCst)).await;

    // Close must cancel the in-flight analysis and return promptly.
    tokio::time::timeout(Duration::from_secs(2), h.gate.close())
        .await
        .expect("close did not cancel the in-flight analysis");
    assert!(dropped.load(Ordering::SeqCst), "analysis future must be dropped (cancelled)");
    assert!(h.sink.bytes().is_empty(), "cancelled batch must be dropped");
}

/// A sink whose write blocks forever (a dead peer with a full socket
/// buffer). close() must cancel the stalled write, not deadlock on it.
struct BlockingSink {
    entered: Arc<AtomicBool>,
}

impl AsyncWrite for BlockingSink {
    fn poll_write(
        self: Pin<&mut Self>,
        _cx: &mut Context<'_>,
        _buf: &[u8],
    ) -> Poll<std::io::Result<usize>> {
        self.entered.store(true, Ordering::SeqCst);
        Poll::Pending // never completes
    }
    fn poll_flush(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        Poll::Pending
    }
    fn poll_shutdown(self: Pin<&mut Self>, _cx: &mut Context<'_>) -> Poll<std::io::Result<()>> {
        Poll::Ready(Ok(()))
    }
}

#[tokio::test]
async fn close_cancels_stalled_write() {
    let (analyzer, _) = CountingNopAnalyzer::new();
    let (tx, _rx) = mpsc::unbounded_channel();
    let entered = Arc::new(AtomicBool::new(false));
    let sink = BlockingSink {
        entered: entered.clone(),
    };
    let gate = PiiGate::spawn("sid-test", analyzer, sink, tx, 0, GatePolicy::Kill);

    // A non-bitmap PDU goes straight to forward, where the sink stalls.
    gate.ingest(&tpkt_pdu(&[0x01, 0x02]));
    wait_for("write entered", || entered.load(Ordering::SeqCst)).await;

    tokio::time::timeout(Duration::from_secs(2), gate.close())
        .await
        .expect("close did not cancel the stalled write");
}

#[tokio::test]
async fn close_is_idempotent_and_unblocks() {
    let (analyzer, _) = CountingNopAnalyzer::new();
    let h = new_test_gate(analyzer);
    h.gate.ingest(&tpkt_pdu(&[0x01]));

    tokio::time::timeout(Duration::from_secs(2), async {
        h.gate.close().await;
        h.gate.close().await;
    })
    .await
    .expect("close deadlocked");
}

// --- Adversarial leak tests --------------------------------------------------

/// PII painted and overwritten within the SAME ingest burst (faster than one
/// analysis window). The overwrite must seal the batch, forcing the PII
/// state to be analyzed — and killed — before either PDU is forwarded.
#[tokio::test]
async fn flash_attack_never_leaks() {
    let (det, _) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate(det);

    let flash = fast_path_bitmap_pdu(&[TestRect::new(40, 40, 8, 8, MAGENTA)]);
    let cover = fast_path_bitmap_pdu(&[TestRect::new(40, 40, 8, 8, WHITE)]);
    h.gate.ingest(&[flash, cover].concat());

    wait_for("flash detection", || h.events.detections() == 1).await;
    assert!(h.gate.killed(), "gate must be killed by the flashed signature");

    let mut leaked = false;
    let mut oracle = ClientOracle::new();
    oracle.consume(&h.sink.bytes(), |fb, _, _| {
        if any_magenta_pixel(fb) {
            leaked = true;
        }
    });
    assert!(!leaked, "LEAK: signature pixels reached a client-renderable state");
    assert!(h.sink.bytes().is_empty(), "flash batch must be dropped entirely");
    h.gate.close().await;
}

/// Every repaint of the same region within one ingest burst forces its own
/// batch, so every intermediate state is analyzed — one analysis per
/// overwrite generation.
#[tokio::test]
async fn overwrite_splits_and_analyzes_each_state() {
    let (det, calls) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate(det);

    let mut burst = Vec::new();
    for _ in 0..5 {
        burst.extend_from_slice(&fast_path_bitmap_pdu(&[TestRect::new(40, 40, 8, 8, WHITE)]));
    }
    h.gate.ingest(&burst);

    wait_for("all clean repaints forwarded in order", || h.sink.bytes() == burst).await;
    assert_eq!(calls.load(Ordering::SeqCst), 5, "each overwrite generation must be analyzed");
    assert_eq!(h.events.detections(), 0, "clean repaints must not detect");
    h.gate.close().await;
}

/// PII assembled from two halves delivered in separate batches. Each
/// forwarded state is analyzed; the detector fires on the batch that
/// completes the signature, which is dropped — the client never sees the
/// assembled PII.
#[tokio::test]
async fn cross_batch_assembly_killed_on_completion() {
    let (det, _) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate(det);

    let left = fast_path_bitmap_pdu(&[TestRect::new(40, 40, 4, 8, MAGENTA)]);
    h.gate.ingest(&left);
    wait_for("clean left half forwarded", || h.sink.bytes() == left).await;

    let right = fast_path_bitmap_pdu(&[TestRect::new(44, 40, 4, 8, MAGENTA)]);
    h.gate.ingest(&right);
    wait_for("detection on completion", || h.events.detections() == 1).await;

    assert_eq!(h.sink.bytes(), left, "completing batch must be dropped");

    let mut assembled = false;
    let mut oracle = ClientOracle::new();
    oracle.consume(&h.sink.bytes(), |fb, w, hgt| {
        if rect_is_color(fb, w, hgt, 40, 40, 8, 8, MAGENTA) {
            assembled = true;
        }
    });
    assert!(!assembled, "LEAK: full signature visible in a client-renderable state");
    h.gate.close().await;
}

/// Documents the granularity floor: a PDU is the smallest forwardable unit,
/// so patches overwriting each other WITHIN one PDU are analyzed at the
/// PDU-final state only. The client composites the PDU atomically, so the
/// oracle never renders the overwritten intermediate either.
#[tokio::test]
async fn intra_pdu_overwrite_is_atomic() {
    let (det, _) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate(det);

    let pdu = fast_path_bitmap_pdu(&[
        TestRect::new(40, 40, 8, 8, MAGENTA),
        TestRect::new(40, 40, 8, 8, WHITE),
    ]);
    h.gate.ingest(&pdu);

    wait_for("atomic pdu forwarded", || h.sink.bytes() == pdu).await;
    assert_eq!(h.events.detections(), 0, "PDU-final state is clean: no detection expected");

    let mut leaked = false;
    let mut oracle = ClientOracle::new();
    oracle.consume(&h.sink.bytes(), |fb, _, _| {
        if any_magenta_pixel(fb) {
            leaked = true;
        }
    });
    assert!(!leaked, "oracle rendered the intra-PDU intermediate state: PDU atomicity broken");
    h.gate.close().await;
}

/// Updates to disjoint padded bands within one ingest burst coalesce into a
/// single analyzed batch — the splitting rule must not degrade into per-PDU
/// analysis for normal traffic.
#[tokio::test]
async fn non_conflicting_pdus_share_one_batch() {
    let (det, calls) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate(det);

    // Default band padding is 24: y=0,h=8 -> [0,32); y=300,h=8 -> [276,332).
    let top = fast_path_bitmap_pdu(&[TestRect::new(0, 0, 32, 8, WHITE)]);
    let bottom = fast_path_bitmap_pdu(&[TestRect::new(0, 300, 32, 8, WHITE)]);
    let want = [top, bottom].concat();
    h.gate.ingest(&want);

    wait_for("batch forwarded", || h.sink.bytes() == want).await;
    assert_eq!(calls.load(Ordering::SeqCst), 1, "disjoint bands must share one analysis");
    h.gate.close().await;
}

/// A bitmap update split across Fast-Path fragments only becomes renderable
/// when the Last fragment arrives — which is exactly when the parser yields
/// its bitmaps and the gate analyzes it. First/Next fragments may flow
/// through earlier (they carry no renderable pixels on their own); the Last
/// fragment of a PII-bearing update must never be forwarded.
#[tokio::test]
async fn fragmented_update_held_until_reassembly() {
    let (det, _) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate(det);

    let frags = fragmented_fast_path_bitmap_pdu(3, &[TestRect::new(40, 40, 8, 8, MAGENTA)]);

    // First and Next fragments: no bitmaps yet, forwarded as plain bytes.
    h.gate.ingest(&frags[0]);
    h.gate.ingest(&frags[1]);
    let clean = [frags[0].clone(), frags[1].clone()].concat();
    wait_for("non-final fragments forwarded", || h.sink.bytes() == clean).await;
    assert_eq!(h.events.detections(), 0, "no detection expected before reassembly");

    // Last fragment: the parser reassembles, the gate composites + analyzes
    // the full update, detects the signature and kills.
    h.gate.ingest(&frags[2]);
    wait_for("detection on reassembled update", || h.events.detections() == 1).await;
    assert_eq!(h.sink.bytes(), clean, "Last fragment must be dropped");

    // Leak oracle: the forwarded fragments alone must not be renderable —
    // without the Last fragment a client cannot composite the signature.
    let mut leaked = false;
    let mut oracle = ClientOracle::new();
    oracle.consume(&h.sink.bytes(), |fb, _, _| {
        if any_magenta_pixel(fb) {
            leaked = true;
        }
    });
    assert!(!leaked, "LEAK: fragments without Last rendered the signature");
    h.gate.close().await;
}

// --- Redact-mode tests ------------------------------------------------------
//
// The leak oracle proves the same zero-leak property under redaction: the
// client renders the BLANKED region, never the PII pixels. The signature
// detector reports the planted magenta rect's bbox so the gate knows what to
// blank.

/// Redact mode: a PII region is painted; the gate detects it, forwards a
/// REWRITTEN frame with the region blanked, and the SESSION CONTINUES. The
/// oracle must never render magenta; the gate stays alive.
#[tokio::test]
async fn redact_blanks_pii_and_continues() {
    let (det, _) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate_with_policy(det, GatePolicy::Redact);

    h.gate.ingest(&fast_path_bitmap_pdu(&[TestRect::new(40, 40, 8, 8, MAGENTA)]));

    // A detection event fires (for audit) but the gate is NOT killed.
    wait_for("redact detection", || h.events.detections() == 1).await;
    assert!(!h.gate.killed(), "redact mode must keep the session alive");

    // The forwarded bytes must render the region blanked (black), never
    // magenta — for every client-renderable state.
    wait_for("redacted frame forwarded", || !h.sink.bytes().is_empty()).await;
    let mut leaked = false;
    let mut saw_black = false;
    let mut oracle = ClientOracle::new();
    oracle.consume(&h.sink.bytes(), |fb, w, hgt| {
        if any_magenta_pixel(fb) {
            leaked = true;
        }
        if rect_is_color(fb, w, hgt, 40, 40, 8, 8, [0, 0, 0]) {
            saw_black = true;
        }
    });
    assert!(!leaked, "LEAK: redact mode delivered PII pixels");
    assert!(saw_black, "redacted region must render as black");

    // The session keeps guarding: a subsequent clean frame still flows.
    h.gate.ingest(&fast_path_bitmap_pdu(&[TestRect::new(0, 0, 16, 16, WHITE)]));
    tokio::time::sleep(Duration::from_millis(50)).await;
    assert!(!h.gate.killed(), "gate must still be alive after a clean repaint");
    h.gate.close().await;
}

/// RedactAndKill: the PII region is blanked AND delivered, then the session
/// terminates. The oracle never sees magenta; the gate ends killed.
#[tokio::test]
async fn redact_and_kill_blanks_then_terminates() {
    let (det, _) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate_with_policy(det, GatePolicy::RedactAndKill);

    h.gate.ingest(&fast_path_bitmap_pdu(&[TestRect::new(40, 40, 8, 8, MAGENTA)]));

    wait_for("detection", || h.events.detections() == 1).await;
    wait_for("gate killed", || h.gate.killed()).await;

    let mut leaked = false;
    let mut saw_black = false;
    let mut oracle = ClientOracle::new();
    oracle.consume(&h.sink.bytes(), |fb, w, hgt| {
        if any_magenta_pixel(fb) {
            leaked = true;
        }
        if rect_is_color(fb, w, hgt, 40, 40, 8, 8, [0, 0, 0]) {
            saw_black = true;
        }
    });
    assert!(!leaked, "LEAK: redact+kill delivered PII pixels");
    assert!(saw_black, "redacted region must render as black before termination");

    // Killed: a later ingest is dropped.
    h.gate.ingest(&fast_path_bitmap_pdu(&[TestRect::new(0, 0, 8, 8, WHITE)]));
    let after = h.sink.bytes().len();
    tokio::time::sleep(Duration::from_millis(50)).await;
    assert_eq!(h.sink.bytes().len(), after, "post-kill ingest must not forward");
    h.gate.close().await;
}

/// Redact mode with a clean batch behaves exactly like the release path: no
/// detection, original bytes forwarded unchanged (no needless rewrite).
#[tokio::test]
async fn redact_mode_clean_batch_forwards_original() {
    let (det, _) = SignatureDetector::new(40, 40, 8, 8);
    let h = new_test_gate_with_policy(det, GatePolicy::Redact);

    let clean = fast_path_bitmap_pdu(&[TestRect::new(0, 0, 16, 16, WHITE)]);
    h.gate.ingest(&clean);

    wait_for("clean batch forwarded", || h.sink.bytes() == clean).await;
    assert_eq!(h.events.detections(), 0);
    h.gate.close().await;
}
