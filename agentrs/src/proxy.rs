use std::io;

use anyhow::Context as _;
use bytes::BytesMut;

use tokio::io::{AsyncRead, AsyncReadExt as _, AsyncWrite, AsyncWriteExt as _};
use tracing::{info, warn};
use typed_builder::TypedBuilder;

use crate::guard::{Gate, GuardConfig};

/// Reports a guard violation upstream and resolves only after the gateway has
/// transactionally accepted it. A terminal report is part of session teardown:
/// bounded acknowledgement failure is returned so the guarded session fails
/// closed instead of silently losing audit evidence.
pub type ViolationReporter =
    Box<dyn Fn(Vec<u8>) -> futures::future::BoxFuture<'static, anyhow::Result<()>> + Send + Sync>;

async fn settle_gate_watch<W>(
    terminal: bool,
    stop_watch: tokio::sync::oneshot::Sender<()>,
    watch: std::pin::Pin<&mut W>,
) -> anyhow::Result<()>
where
    W: Future<Output = anyhow::Result<()>>,
{
    if !terminal {
        // An idle watcher exits. A watcher already inside report(...).await
        // cannot observe this until that popped report has settled.
        let _ = stop_watch.send(());
    }
    watch.await
}

#[derive(TypedBuilder)]
pub struct Proxy<A, B> {
    /// transport_a is the client side (browser, via the gateway tunnel);
    /// transport_b is the target RDP server.
    transport_a: A,
    transport_b: B,
    /// Bytes already buffered by the RDP handshake interceptor. They must enter
    /// the same forwarding path as subsequent reads so guarded server output
    /// cannot bypass the gate.
    #[builder(default)]
    initial_a_to_b: BytesMut,
    #[builder(default)]
    initial_b_to_a: BytesMut,
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

        if !self.initial_b_to_a.is_empty() {
            transport_a
                .write_all(&self.initial_b_to_a)
                .await
                .context("forward buffered server bytes to client")?;
        }
        if !self.initial_a_to_b.is_empty() {
            transport_b
                .write_all(&self.initial_a_to_b)
                .await
                .context("forward buffered client bytes to server")?;
        }
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

        // Client-side bytes are not subject to screen analysis, but they must
        // retain wire order ahead of subsequent reads from the client.
        if !self.initial_a_to_b.is_empty() {
            server_wr
                .write_all(&self.initial_a_to_b)
                .await
                .context("forward buffered client bytes to server")?;
        }

        // Fail CLOSED: the gateway suppressed its own gate on the strength of
        // this delegation. If we cannot build the guard here, running
        // transparently would be a silent enforcement bypass — refuse the
        // session instead. (Endpoint presence was already validated in
        // GuardConfig::resolve; a failure here is a client-construction error.)
        let (gate, mut gate_events) = Gate::spawn(self.session_id.clone(), guard, client_wr)?;
        if !self.initial_b_to_a.is_empty() {
            // This is the security-sensitive half: bytes read ahead by
            // TokioFramed may already include the first graphics update.
            gate.ingest(&self.initial_b_to_a);
        }
        info!(sid = %self.session_id, "piigate: realtime PII guard active (agent-side, hold-and-release)");

        let (stop_watch_tx, mut stop_watch_rx) = tokio::sync::oneshot::channel();

        let (outcome, gate_closed) = {
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

            // Gate events are acknowledged by the gateway after transactional,
            // idempotent persistence. Once an event has been removed from the
            // stream, its report is deliberately awaited outside the stop
            // select: relay EOF or terminal gate shutdown must not cancel a
            // report that is already in flight.
            let session_id = self.session_id.clone();
            let watch = async {
                loop {
                    let event = tokio::select! {
                        biased;
                        event = gate_events.next() => event,
                        _ = &mut stop_watch_rx => return Ok(()),
                    };
                    let Some(ev) = event else {
                        return Ok(());
                    };
                    warn!(sid = %session_id, "{}", ev.log_message());
                    if let (Some(payload), Some(report)) = (ev.report_json(), &report) {
                        report(payload.to_vec())
                            .await
                            .context("gateway did not acknowledge PII guard violation")?;
                    }
                    if ev.is_terminal() {
                        return Ok(());
                    }
                    // Non-terminal (Redact detection): keep guarding and
                    // reporting.
                }
            };

            tokio::pin!(c2s);
            tokio::pin!(s2c);
            tokio::pin!(watch);

            enum Completion {
                Watch(anyhow::Result<()>),
                Relay,
            }

            // Prefer a ready event watcher over a relay arm that became ready
            // from the same terminal transition.
            let completion = tokio::select! {
                biased;
                result = &mut watch => Completion::Watch(result),
                _ = &mut c2s => Completion::Relay,
                _ = &mut s2c => Completion::Relay,
            };

            match completion {
                Completion::Watch(result) => (result, false),
                Completion::Relay => {
                    let terminal = gate.killed();
                    gate.close().await;
                    // A terminal gate always emits an event. The settlement
                    // helper waits for its bounded report/ack result. For a
                    // normal EOF it stops an idle watcher but cannot cancel a
                    // report the watcher already popped.
                    (
                        settle_gate_watch(terminal, stop_watch_tx, watch.as_mut()).await,
                        true,
                    )
                }
            }
        };

        if !gate_closed {
            gate.close().await;
        }
        let _ = server_wr.shutdown().await;
        outcome
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

#[cfg(test)]
mod tests {
    use super::*;
    use tokio::time::{Duration, timeout};

    #[tokio::test]
    async fn forwards_handshake_read_ahead_before_live_stream_bytes() {
        let (mut client_peer, client_transport) = tokio::io::duplex(1024);
        let (mut server_peer, server_transport) = tokio::io::duplex(1024);

        let task = tokio::spawn(
            Proxy::builder()
                .transport_a(client_transport)
                .transport_b(server_transport)
                .initial_a_to_b(BytesMut::from(&b"client-prefix"[..]))
                .initial_b_to_a(BytesMut::from(&b"server-prefix"[..]))
                .build()
                .forward(),
        );

        let mut from_server = [0u8; 13];
        timeout(
            Duration::from_secs(1),
            client_peer.read_exact(&mut from_server),
        )
        .await
        .expect("server prefix timed out")
        .expect("read server prefix");
        assert_eq!(&from_server, b"server-prefix");

        let mut from_client = [0u8; 13];
        timeout(
            Duration::from_secs(1),
            server_peer.read_exact(&mut from_client),
        )
        .await
        .expect("client prefix timed out")
        .expect("read client prefix");
        assert_eq!(&from_client, b"client-prefix");

        client_peer.write_all(b"client-live").await.unwrap();
        let mut client_live = [0u8; 11];
        server_peer.read_exact(&mut client_live).await.unwrap();
        assert_eq!(&client_live, b"client-live");

        server_peer.write_all(b"server-live").await.unwrap();
        let mut server_live = [0u8; 11];
        client_peer.read_exact(&mut server_live).await.unwrap();
        assert_eq!(&server_live, b"server-live");

        drop(client_peer);
        drop(server_peer);
        timeout(Duration::from_secs(1), task)
            .await
            .expect("proxy shutdown timed out")
            .expect("proxy task panicked")
            .expect("proxy forwarding failed");
    }

    #[tokio::test]
    async fn relay_teardown_waits_for_a_popped_terminal_report() {
        let (stop_tx, mut stop_rx) = tokio::sync::oneshot::channel();
        let (started_tx, started_rx) = tokio::sync::oneshot::channel();
        let (release_tx, mut release_rx) = tokio::sync::oneshot::channel();
        let mut settlement = tokio::spawn(async move {
            let watch = async move {
                let _ = started_tx.send(());
                tokio::select! {
                    _ = &mut stop_rx => anyhow::bail!("terminal report was cancelled"),
                    _ = &mut release_rx => anyhow::bail!("report acknowledgement failed"),
                }
            };
            tokio::pin!(watch);
            settle_gate_watch(true, stop_tx, watch.as_mut()).await
        });

        started_rx.await.expect("report started");
        assert!(
            timeout(Duration::from_millis(20), &mut settlement)
                .await
                .is_err(),
            "session returned before the terminal report settled"
        );

        release_tx.send(()).expect("release report");
        let error = settlement
            .await
            .expect("settlement task panicked")
            .expect_err("report failure must propagate");
        assert!(error.to_string().contains("acknowledgement failed"));
    }
}
