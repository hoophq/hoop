use std::io;
use std::sync::Arc;

use anyhow::Context as _;
use tokio::io::{AsyncRead, AsyncReadExt as _, AsyncWrite, AsyncWriteExt as _};
use tokio::sync::mpsc;
use tracing::{info, warn};
use typed_builder::TypedBuilder;

use crate::piigate::config::GuardConfig;
use crate::piigate::report::ViolationReport;
use crate::piigate::{analyze::BandAnalyzer, GateEvent, PiiGate};

/// Reports a guard violation upstream (to the gateway). Boxed so proxy.rs
/// stays decoupled from the websocket layer; the future is awaited before
/// the session tears down so the report is sent while the connection is
/// still open.
pub type ViolationReporter =
    Box<dyn Fn(ViolationReport) -> futures::future::BoxFuture<'static, ()> + Send + Sync>;

#[derive(TypedBuilder)]
pub struct Proxy<A, B> {
    /// transport_a is the client side (browser, via the gateway tunnel);
    /// transport_b is the target RDP server.
    transport_a: A,
    transport_b: B,
    /// When present, the server->client direction is gated: frames are held
    /// until OCR+Presidio clears them. None = transparent bidirectional copy.
    #[builder(default)]
    guard: Option<GuardConfig>,
    #[builder(default = String::new())]
    session_id: String,
    /// Sends guard violations to the gateway. None = no reporting (the
    /// session is still torn down on a terminal event).
    #[builder(default)]
    report: Option<ViolationReporter>,
}

// Forwards traffic between the client (a) and the target RDP server (b). With
// no guard this is a transparent bidirectional copy; with a guard the
// server->client direction flows through the PII gate (hold-and-release).
impl<A, B> Proxy<A, B>
where
    A: AsyncWrite + AsyncRead + Unpin + Send + 'static,
    B: AsyncWrite + AsyncRead + Unpin,
{
    pub async fn forward(mut self) -> anyhow::Result<()> {
        match self.guard.take() {
            Some(guard) => {
                let report = self.report.take();
                self.forward_guarded(guard, report).await
            }
            None => self.forward_transparent().await,
        }
    }

    async fn forward_transparent(self) -> anyhow::Result<()> {
        let mut transport_a = self.transport_a;
        let mut transport_b = self.transport_b;

        let res = tokio::io::copy_bidirectional(&mut transport_a, &mut transport_b)
            .await
            .map(|_| ());

        // Ensure we close the transports cleanly at the end (ignore errors).
        let _ = tokio::join!(transport_a.shutdown(), transport_b.shutdown());

        match res {
            Ok(()) => Ok(()),
            Err(error) if is_error(&error) => Err(anyhow::Error::new(error).context("forward")),
            Err(_) => Ok(()),
        }
    }

    /// Gated forwarding. The two directions are handled explicitly:
    ///
    /// - client -> server: a plain copy (keystrokes/mouse are not gated).
    /// - server -> client: every read is fed to the gate via ingest(); the
    ///   gate's analysis task writes cleared bytes to the client sink.
    ///
    /// On detection or overload the gate emits a terminal event; we tear the
    /// whole proxy down so the held (PII-bearing) frames are never delivered.
    async fn forward_guarded(
        self,
        guard: GuardConfig,
        report: Option<ViolationReporter>,
    ) -> anyhow::Result<()> {
        let (mut client_rd, client_wr) = tokio::io::split(self.transport_a);
        let (mut server_rd, mut server_wr) = tokio::io::split(self.transport_b);

        // Fail CLOSED: the gateway suppressed its own gate on the strength of
        // this delegation. If we cannot build the analyzer here, running
        // transparently would be a silent enforcement bypass — refuse the
        // session instead. (Endpoint presence was already validated in
        // GuardConfig::resolve; a failure here is a client-construction error.)
        let analyzer = Arc::new(
            BandAnalyzer::from_config(&guard)
                .context("piigate: failed to build analyzer for a delegated-guard session")?,
        );

        let (events_tx, mut events_rx) = mpsc::unbounded_channel();
        let gate = Arc::new(PiiGate::spawn(
            self.session_id.clone(),
            analyzer,
            client_wr,
            events_tx,
            guard.params.band_padding,
            guard.policy,
        ));
        info!(sid = %self.session_id, "piigate: realtime PII guard active (agent-side, hold-and-release)");

        // client -> server (ungated).
        let c2s = async {
            let mut buf = vec![0u8; 32 * 1024];
            loop {
                let n = match client_rd.read(&mut buf).await {
                    Ok(0) | Err(_) => break,
                    Ok(n) => n,
                };
                if server_wr.write_all(&buf[..n]).await.is_err() {
                    break;
                }
            }
            let _ = server_wr.shutdown().await;
        };

        // server -> client (gated): feed every read to the gate.
        let s2c = async {
            let mut buf = vec![0u8; 32 * 1024];
            loop {
                let n = match server_rd.read(&mut buf).await {
                    Ok(0) | Err(_) => break,
                    Ok(n) => n,
                };
                gate.ingest(&buf[..n]);
                if gate.killed() {
                    break;
                }
            }
        };

        // Terminal gate events: a detection/overload kills the session. The
        // gate has already dropped the offending frames; we report the
        // violation upstream (entity metadata only — no pixels or OCR text)
        // before tearing down, while the gateway websocket is still open.
        //
        // Delivery is best-effort: the report shares the session websocket
        // with response traffic and the gateway's live-session lookup, so a
        // websocket failure or a racing session close can still drop it.
        // Enforcement already happened at the agent — this is audit evidence,
        // not part of the enforcement path.
        let session_id = self.session_id.clone();
        let watch = async {
            if let Some(ev) = events_rx.recv().await {
                let report_payload = match &ev {
                    GateEvent::Detection(res) => {
                        warn!(
                            sid = %session_id,
                            "piigate: PII detected ({:?}), terminating session", res.counts
                        );
                        ViolationReport::detection(res)
                    }
                    GateEvent::Overload { dropped_bytes } => {
                        warn!(
                            sid = %session_id,
                            "piigate: analysis backlog overflow ({dropped_bytes} bytes), terminating session"
                        );
                        ViolationReport::overload(*dropped_bytes)
                    }
                };
                if let Some(report) = &report {
                    report(report_payload).await;
                }
            }
        };

        // Run until any arm finishes: client gone, server gone, or a terminal
        // gate event. Then tear the session down explicitly (don't rely on
        // drop timing for security-sensitive teardown): close the gate first
        // — it drops held bytes, cancels in-flight analysis, and closes the
        // client write half it owns — then shut the server write half so the
        // upstream RDP server connection is released promptly.
        tokio::select! {
            _ = c2s => {}
            _ = s2c => {}
            _ = watch => {}
        }
        gate.close().await;
        let _ = server_wr.shutdown().await;
        Ok(())
    }
}

fn is_error(original_error: &io::Error) -> bool {
    use std::error::Error as _;

    let mut dyn_error: Option<&dyn std::error::Error> = Some(original_error);

    while let Some(source_error) = dyn_error.take() {
        if let Some(io_error) = source_error.downcast_ref::<io::Error>() {
            match io_error.kind() {
                io::ErrorKind::ConnectionReset
                | io::ErrorKind::UnexpectedEof
                | io::ErrorKind::ConnectionAborted => {
                    return false;
                }
                io::ErrorKind::Other => {
                    dyn_error = io_error.source();
                }
                _ => {
                    return true;
                }
            }
        } else if let Some(tungstenite_error) = source_error.downcast_ref::<tungstenite::Error>() {
            match tungstenite_error {
                tungstenite::Error::ConnectionClosed | tungstenite::Error::AlreadyClosed => {
                    return false;
                }
                tungstenite::Error::Protocol(
                    tungstenite::error::ProtocolError::ResetWithoutClosingHandshake,
                ) => {
                    return false;
                }
                tungstenite::Error::Io(io_error) => dyn_error = Some(io_error),
                _ => return true,
            }
        } else {
            dyn_error = source_error.source();
        }
    }

    true
}
