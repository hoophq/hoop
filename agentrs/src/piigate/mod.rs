//! Realtime hold-and-release PII guard on the RDP server->client stream.
//!
//! Port of `gateway/rdp/piigate.go` to the agent: the gate runs where the
//! plaintext already flows (between the proxy's two TLS legs), so the
//! gateway only ever receives frames the analyzer cleared.
//!
//! Bytes ingested from the server are framed into PDUs; bitmap payloads are
//! extracted and queued alongside the wire bytes. The analysis task
//! composites queued PDUs into a shadow framebuffer and releases them in
//! batches, where a batch never repaints rows it has already dirtied: a PDU
//! that would overwrite (or touch the padded text lines of) rows dirtied by
//! the current batch seals the batch, forcing the intermediate screen state
//! to be analyzed BEFORE the overwrite is released. On detection the held
//! bytes are dropped and a [`GateEvent::Detection`] is emitted.
//!
//! Enforcement semantics — the precise guarantee is:
//!
//! > Every forwarded pixel was analyzed in its final on-screen position for
//! > the batch that delivered it, and content that is painted and then
//! > overwritten is analyzed in its intermediate state before the overwrite
//! > is forwarded — regardless of how briefly it would have been visible.
//!
//! The remaining, deliberate exceptions:
//! - PDU atomicity floor: a PDU is the smallest forwardable unit. Patches
//!   that overwrite each other WITHIN one PDU are analyzed at the PDU-final
//!   state only.
//! - Progressive rendering: the client renders a batch PDU by PDU. Within a
//!   batch, partially-applied states mix already-analyzed old content with
//!   new content — but only across non-intersecting padded bands (same-band
//!   mixing seals the batch), so no unanalyzed text line is ever composed.
//! - Analysis errors and unframeable data fail OPEN (forwarded, loudly
//!   logged): availability wins over enforcement there. Backlog overflow
//!   (analyzer slower than the stream) fails CLOSED: the session is killed
//!   rather than letting unanalyzed frames through (see [`MAX_HELD_BYTES`]).
//! - Detection accuracy is bounded by OCR + Presidio: the pipeline
//!   guarantees every state is INSPECTED, not that the detector is
//!   infallible.

pub mod analyze;
pub mod bands;
pub mod canvas;
pub mod config;
pub mod framing;
pub mod ocr;
pub mod presidio;
pub mod report;
#[cfg(test)]
pub mod testpdu;

use std::sync::Arc;

use bytes::BytesMut;
use parking_lot::Mutex;
use tokio::io::{AsyncWrite, AsyncWriteExt as _};
use tokio::sync::{mpsc, Notify};
use tokio_util::sync::CancellationToken;
use tracing::{info, warn};

use analyze::Analyzer;
use bands::DirtyBands;
use canvas::{ShadowCanvas, MAX_CANVAS_DIM};
use framing::{pdu_size, BitmapPatch, FastPathParser};
use presidio::SnapshotResult;

/// Caps the per-session backlog awaiting analysis (PDU bytes plus their
/// extracted bitmap payloads). If the analyzer cannot keep up (or is being
/// flooded to force it to lag), the gate fails CLOSED: letting the backlog
/// through unanalyzed would be the obvious bypass, and letting it grow is an
/// OOM vector.
pub const MAX_HELD_BYTES: usize = 32 << 20;

/// Caps how many PDU bytes are coalesced into one sink write: enough to
/// amortize per-write overhead, small enough that a MAX_HELD_BYTES-sized
/// batch never doubles peak memory with a giant copy.
const FORWARD_CHUNK_BYTES: usize = 256 << 10;

/// Unframeable-tail cap: if buffered bytes that frame to nothing grow past
/// any sane PDU size, the stream is not something we can frame — fail open
/// so the session keeps working (bitmaps always arrive as Fast-Path, which
/// we CAN frame; this path carries no decodable pixels).
const MAX_UNFRAMEABLE_TAIL: usize = 128 * 1024;

/// Terminal gate events. The session owner must terminate the session on
/// either: the gate stops forwarding permanently once one is emitted.
#[derive(Debug)]
pub enum GateEvent {
    /// PII was detected; the held frames that contained it were dropped.
    Detection(SnapshotResult),
    /// The held backlog exceeded [`MAX_HELD_BYTES`]; the backlog was dropped
    /// (fail-closed).
    Overload { dropped_bytes: usize },
}

/// One framed PDU awaiting analysis clearance: the exact wire bytes to
/// forward plus the bitmap payloads it carried.
struct GatePdu {
    data: Vec<u8>,
    patches: Vec<BitmapPatch>,
}

impl GatePdu {
    fn size(&self) -> usize {
        self.data.len() + self.patches.iter().map(|p| p.data.len()).sum::<usize>()
    }
}

struct GateState {
    queue: Vec<GatePdu>,
    queued_bytes: usize,
    tail: BytesMut,
    parser: FastPathParser,
    killed: bool,
    closed: bool,
}

struct GateShared {
    session_id: String,
    state: Mutex<GateState>,
    /// Signals the analysis task that work is pending (one stored permit,
    /// like a buffered chan of 1).
    notify: Notify,
    /// Signals close (level-triggered): cancels any in-flight analysis (the
    /// future is dropped, aborting its HTTP requests) and unblocks the task.
    cancel: CancellationToken,
    events: mpsc::UnboundedSender<GateEvent>,
}

/// The hold-and-release valve. Create with [`PiiGate::spawn`]; feed
/// server->client bytes through [`ingest`](Self::ingest); terminate with
/// [`close`](Self::close).
pub struct PiiGate {
    shared: Arc<GateShared>,
    task: Mutex<Option<tokio::task::JoinHandle<()>>>,
}

impl PiiGate {
    /// Creates a gate and spawns its analysis task. `sink` receives cleared
    /// bytes (the client-bound transport); `analyzer` is the OCR+Presidio
    /// pipeline (or a test detector); terminal events arrive on the returned
    /// receiver's counterpart channel.
    pub fn spawn<W>(
        session_id: impl Into<String>,
        analyzer: Arc<dyn Analyzer>,
        sink: W,
        events: mpsc::UnboundedSender<GateEvent>,
        band_padding: usize,
    ) -> Self
    where
        W: AsyncWrite + Unpin + Send + 'static,
    {
        let session_id = session_id.into();
        let shared = Arc::new(GateShared {
            session_id: session_id.clone(),
            state: Mutex::new(GateState {
                queue: Vec::new(),
                queued_bytes: 0,
                tail: BytesMut::new(),
                parser: FastPathParser::new(),
                killed: false,
                closed: false,
            }),
            notify: Notify::new(),
            cancel: CancellationToken::new(),
            events,
        });

        let task = tokio::spawn(analysis_loop(
            shared.clone(),
            analyzer,
            sink,
            DirtyBands::new(MAX_CANVAS_DIM, band_padding),
            ShadowCanvas::new(session_id),
        ));

        Self {
            shared,
            task: Mutex::new(Some(task)),
        }
    }

    /// Consumes server->client bytes. Frames complete PDUs, extracts their
    /// bitmap payloads, and queues everything for the analysis task. Never
    /// blocks on analysis.
    pub fn ingest(&self, data: &[u8]) {
        let mut st = self.shared.state.lock();
        if st.closed || st.killed {
            return;
        }

        // Fail closed on backlog overflow: analysis cannot keep up and
        // letting the backlog through unanalyzed would be the trivial bypass.
        if st.queued_bytes + st.tail.len() + data.len() > MAX_HELD_BYTES {
            let dropped = st.queued_bytes + st.tail.len() + data.len();
            st.killed = true;
            st.queue.clear();
            st.queued_bytes = 0;
            st.tail.clear();
            drop(st);
            warn!(
                sid = %self.shared.session_id,
                "piigate: analysis backlog exceeded {MAX_HELD_BYTES} bytes, failing closed and terminating session"
            );
            // Exactly-once: killed=true (set under the lock) makes every
            // subsequent ingest return before reaching this point.
            let _ = self
                .shared
                .events
                .send(GateEvent::Overload { dropped_bytes: dropped });
            self.shared.notify.notify_one();
            return;
        }

        st.tail.extend_from_slice(data);

        loop {
            let size = pdu_size(&st.tail);
            if size == 0 {
                // Unframeable or incomplete: keep buffering up to the cap.
                if st.tail.len() > MAX_UNFRAMEABLE_TAIL {
                    warn!(
                        sid = %self.shared.session_id,
                        "piigate: {} unframeable bytes, failing open",
                        st.tail.len()
                    );
                    let pdu = GatePdu {
                        data: std::mem::take(&mut st.tail).to_vec(),
                        patches: Vec::new(),
                    };
                    enqueue(&mut st, pdu);
                }
                break;
            }
            if size > st.tail.len() {
                break; // incomplete PDU, wait for more bytes
            }

            // split_to is O(1) (BytesMut splits the buffer, no memmove of
            // the remaining tail); the gate's hot path frames many small
            // PDUs, so this avoids quadratic churn.
            let data = st.tail.split_to(size);
            // Parse failures fail open: the PDU is still queued and
            // forwarded, just with no pixels to analyze.
            let patches = st.parser.parse(&data);
            enqueue(&mut st, GatePdu { data: data.to_vec(), patches });
        }

        let has_work = !st.queue.is_empty();
        drop(st);
        if has_work {
            self.shared.notify.notify_one();
        }
    }

    /// Reports whether the gate terminated the session (detection or
    /// overload).
    pub fn killed(&self) -> bool {
        self.shared.state.lock().killed
    }

    #[cfg(test)]
    fn is_closed(&self) -> bool {
        self.shared.state.lock().closed
    }

    /// Shuts the gate down. Held bytes that were never analyzed are dropped
    /// — the session is ending anyway, and forwarding unanalyzed frames on
    /// shutdown would bypass the guarantee. Any in-flight analysis is
    /// cancelled immediately (its batch is dropped): shutdown liveness wins
    /// over best-effort final evidence.
    pub async fn close(&self) {
        {
            let mut st = self.shared.state.lock();
            if st.closed {
                // Still join the task below (idempotent close must not leak
                // the join handle), but do not re-clear state.
            } else {
                st.closed = true;
                st.queue.clear();
                st.queued_bytes = 0;
                st.tail.clear();
            }
        }
        self.shared.cancel.cancel();
        self.shared.notify.notify_one();
        let task = self.task.lock().take();
        if let Some(task) = task {
            let _ = task.await;
        }
    }
}

fn enqueue(st: &mut GateState, pdu: GatePdu) {
    st.queued_bytes += pdu.size();
    st.queue.push(pdu);
}

/// The single consumer of queued PDUs: composites them into batches,
/// analyzes each batch's dirty bands, and either forwards or kills. Running
/// continuously (no ticker) means each batch is analyzed as soon as the
/// previous one finishes — analysis duration is the natural rate limit.
async fn analysis_loop<W>(
    shared: Arc<GateShared>,
    analyzer: Arc<dyn Analyzer>,
    mut sink: W,
    mut dirty: DirtyBands,
    mut canvas: ShadowCanvas,
) where
    W: AsyncWrite + Unpin + Send + 'static,
{
    loop {
        tokio::select! {
            _ = shared.notify.notified() => {}
            _ = shared.cancel.cancelled() => return,
        }

        loop {
            let pdus = {
                let mut st = shared.state.lock();
                if st.closed || st.killed || st.queue.is_empty() {
                    None
                } else {
                    st.queued_bytes = 0;
                    Some(std::mem::take(&mut st.queue))
                }
            };
            let Some(pdus) = pdus else { break };
            if !process_pdus(&shared, &*analyzer, &mut sink, &mut dirty, &mut canvas, pdus).await {
                return;
            }
        }

        if shared.state.lock().closed {
            return;
        }
    }
}

/// Composites and releases the taken PDUs as one or more analyzed batches. A
/// batch is sealed before any PDU that would repaint (or touch the padded
/// text lines of) rows the current batch already dirtied — that PDU waits
/// until the intermediate state has been analyzed. Returns false when the
/// loop must exit (kill, forward failure, or cancellation).
async fn process_pdus<W>(
    shared: &GateShared,
    analyzer: &dyn Analyzer,
    sink: &mut W,
    dirty: &mut DirtyBands,
    canvas: &mut ShadowCanvas,
    pdus: Vec<GatePdu>,
) -> bool
where
    W: AsyncWrite + Unpin,
{
    let mut i = 0;
    while i < pdus.len() {
        let mut j = i;
        while j < pdus.len() {
            if j > i && pdu_conflicts(dirty, &pdus[j]) {
                break;
            }
            for p in &pdus[j].patches {
                if canvas.composite(p) {
                    dirty.add_rect(p.y, p.height);
                }
            }
            j += 1;
        }
        if !analyze_and_forward(shared, analyzer, sink, dirty, canvas, &pdus[i..j]).await {
            return false;
        }
        i = j;
    }
    true
}

/// Reports whether any of the PDU's patches would touch rows already dirtied
/// by the current (unanalyzed) batch.
fn pdu_conflicts(dirty: &DirtyBands, pdu: &GatePdu) -> bool {
    pdu.patches.iter().any(|p| dirty.intersects(p.y, p.height))
}

/// Analyzes the current shadow framebuffer state (if the batch dirtied
/// anything) and forwards the batch on clearance. Returns false when the
/// loop must exit.
async fn analyze_and_forward<W>(
    shared: &GateShared,
    analyzer: &dyn Analyzer,
    sink: &mut W,
    dirty: &mut DirtyBands,
    canvas: &mut ShadowCanvas,
    batch: &[GatePdu],
) -> bool
where
    W: AsyncWrite + Unpin,
{
    let bands: Vec<bands::YBand> = dirty
        .take_and_reset()
        .into_iter()
        .filter_map(|mut b| {
            if b.y0 >= canvas.h {
                return None;
            }
            b.y1 = b.y1.min(canvas.h);
            Some(b)
        })
        .collect();

    if !bands.is_empty() {
        // Cancellation drops the analysis future (aborting its in-flight
        // HTTP requests) — a hung OCR sidecar must never wedge teardown.
        let res = tokio::select! {
            res = analyzer.analyze(&canvas.fb, canvas.w, canvas.h, &bands) => res,
            _ = shared.cancel.cancelled() => return false,
        };
        match res {
            Err(e) => {
                // Fail open: forwarding beats freezing the session on an
                // analyzer hiccup (see module doc).
                warn!(sid = %shared.session_id, "piigate: analysis failed, failing open: {e:#}");
            }
            Ok(res) if res.is_detection() => {
                // Terminal-state transition under the lock: if an overload
                // (or close) already terminated the gate while this analysis
                // was in flight, the session is going down anyway — do not
                // emit a second terminal event.
                let already_down = {
                    let mut st = shared.state.lock();
                    let already = st.killed || st.closed;
                    st.killed = true;
                    st.queue.clear();
                    st.queued_bytes = 0;
                    st.tail.clear();
                    already
                };
                if already_down {
                    return false;
                }
                warn!(
                    sid = %shared.session_id,
                    "piigate: PII detected ({:?}), dropping held batch and terminating session",
                    res.counts
                );
                let _ = shared.events.send(GateEvent::Detection(res));
                return false;
            }
            Ok(_) => {}
        }
    }

    forward_batch(shared, sink, batch).await
}

/// Delivers cleared PDUs downstream, coalescing them into bounded chunks
/// (PDU boundaries are preserved within the byte stream; the client
/// reassembles from arbitrary framing). A transport failure means the client
/// is gone; mark the gate closed so the loop exits.
async fn forward_batch<W>(shared: &GateShared, sink: &mut W, batch: &[GatePdu]) -> bool
where
    W: AsyncWrite + Unpin,
{
    let mut chunk: Vec<u8> = Vec::with_capacity(FORWARD_CHUNK_BYTES);
    for p in batch {
        if !chunk.is_empty()
            && chunk.len() + p.data.len() > FORWARD_CHUNK_BYTES
            && !flush(shared, sink, &mut chunk).await
        {
            return false;
        }
        chunk.extend_from_slice(&p.data);
    }
    flush(shared, sink, &mut chunk).await
}

async fn flush<W>(shared: &GateShared, sink: &mut W, chunk: &mut Vec<u8>) -> bool
where
    W: AsyncWrite + Unpin,
{
    if chunk.is_empty() {
        return true;
    }
    let write = async {
        sink.write_all(chunk).await?;
        sink.flush().await
    };
    // The cancel signal must also abort a write stalled on a dead peer.
    let res = tokio::select! {
        res = write => res,
        _ = shared.cancel.cancelled() => return false,
    };
    if let Err(e) = res {
        info!(sid = %shared.session_id, "piigate: forward failed, closing gate: {e}");
        shared.state.lock().closed = true;
        return false;
    }
    chunk.clear();
    true
}

#[cfg(test)]
mod tests;
