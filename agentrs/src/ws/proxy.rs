use crate::conf::Conf;
use crate::proxy::ViolationReporter;
use crate::rdp_proxy::RdpProxy;
use crate::session::Header;
use crate::ws::message::WebSocketMessage;
use crate::ws::message_types::MessageType;
use crate::ws::session::SessionInfo;
use crate::ws::stream::{ChannelWebSocketStream, RESPONSE_QUEUE_SLOTS};
use crate::ws::types::{ViolationAckMap, WsWriter};
use anyhow::Context;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
use tokio::net::TcpStream;
use tracing::{debug, error, info};
use uuid::Uuid;

use futures::SinkExt;
use tokio::sync::{mpsc, oneshot};
use tokio_tungstenite::tungstenite::protocol::Message;
const VIOLATION_REPORT_ID_KEY: &str = "report_id";
const VIOLATION_REPORT_ATTEMPTS: usize = 3;
const VIOLATION_SEND_TIMEOUT: Duration = Duration::from_secs(5);
const VIOLATION_ACK_TIMEOUT: Duration = Duration::from_secs(5);
const RESPONSE_SEND_TIMEOUT: Duration = Duration::from_secs(5);

/// A session child task must never outlive the future that owns it. Tokio
/// detaches a JoinHandle on drop. Retain a separate AbortHandle so cancellation
/// stays safe even while `join` has moved the JoinHandle into its await point.
struct SessionTask<T> {
    handle: Option<tokio::task::JoinHandle<T>>,
    abort: tokio::task::AbortHandle,
}

impl<T> SessionTask<T> {
    fn new(handle: tokio::task::JoinHandle<T>) -> Self {
        let abort = handle.abort_handle();
        Self {
            handle: Some(handle),
            abort,
        }
    }

    async fn join(mut self) -> Result<T, tokio::task::JoinError> {
        self.handle
            .take()
            .expect("session task handle missing")
            .await
    }
}

impl<T> Drop for SessionTask<T> {
    fn drop(&mut self) {
        self.abort.abort();
    }
}

struct PendingAckRegistration {
    report_id: Uuid,
    pending: ViolationAckMap,
}

impl Drop for PendingAckRegistration {
    fn drop(&mut self) {
        self.pending
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .remove(&self.report_id);
    }
}

fn register_violation_ack(
    report_id: Uuid,
    pending: &ViolationAckMap,
) -> (oneshot::Receiver<()>, PendingAckRegistration) {
    let (tx, rx) = oneshot::channel();
    pending
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
        .insert(report_id, tx);
    (
        rx,
        PendingAckRegistration {
            report_id,
            pending: pending.clone(),
        },
    )
}

/// Builds a reporter that retries one stable report UUID until the gateway
/// acknowledges transactional persistence. Retries are idempotent gateway-side.
fn build_violation_reporter(
    sid: Uuid,
    ws_sender: WsWriter,
    pending_acks: ViolationAckMap,
) -> ViolationReporter {
    Box::new(move |payload: Vec<u8>| {
        let ws_sender = ws_sender.clone();
        let pending_acks = pending_acks.clone();
        Box::pin(async move {
            let report_id = Uuid::new_v4();
            let metadata =
                HashMap::from([(VIOLATION_REPORT_ID_KEY.to_owned(), report_id.to_string())]);
            let framed = WebSocketMessage::new(MessageType::GuardrailsViolation, metadata, payload)
                .encode_with_header(sid)
                .context("encode guardrails violation")?;
            let mut last_failure = "gateway did not acknowledge violation".to_owned();

            for attempt in 1..=VIOLATION_REPORT_ATTEMPTS {
                let (ack_rx, _registration) = register_violation_ack(report_id, &pending_acks);
                let send_outcome = tokio::time::timeout(VIOLATION_SEND_TIMEOUT, async {
                    let mut sender = ws_sender.lock().await;
                    sender.send(Message::Binary(framed.clone().into())).await
                })
                .await;
                match send_outcome {
                    Ok(Ok(())) => match tokio::time::timeout(VIOLATION_ACK_TIMEOUT, ack_rx).await {
                        Ok(Ok(())) => {
                            debug!(
                                "> Gateway acknowledged guardrails violation {} for session {}",
                                report_id, sid
                            );
                            return Ok(());
                        }
                        Ok(Err(_)) => {
                            last_failure = "acknowledgement waiter closed".to_owned();
                        }
                        Err(_) => {
                            last_failure = "acknowledgement timed out".to_owned();
                        }
                    },
                    Ok(Err(e)) => {
                        last_failure = format!("send failed: {e}");
                    }
                    Err(_) => {
                        last_failure = "send timed out".to_owned();
                    }
                }
                // Drop the registration before retrying so one report id never
                // has two live waiters.
                drop(_registration);
                if attempt < VIOLATION_REPORT_ATTEMPTS {
                    tokio::time::sleep(Duration::from_millis(100 * attempt as u64)).await;
                }

                debug!(
                    "> Retrying guardrails violation {} for session {} ({}/{})",
                    report_id, sid, attempt, VIOLATION_REPORT_ATTEMPTS
                );
            }

            anyhow::bail!(
                "guardrails violation {} for session {} was not acknowledged after {} attempts: {}",
                report_id,
                sid,
                VIOLATION_REPORT_ATTEMPTS,
                last_failure
            )
        })
    })
}
// Start a persistent RDP proxy session.
pub async fn start_rdp_proxy_session(
    session_info: SessionInfo,
    ws_sender: WsWriter,
    mut rdp_data_rx: mpsc::Receiver<Vec<u8>>,
    violation_acks: ViolationAckMap,
    config: Arc<Conf>,
) -> anyhow::Result<()> {
    let SessionInfo {
        sid,
        target_address,
        username,
        password,
        proxy_user,
        client_address,
        guard,
    } = session_info;
    info!("> Starting persistent RDP proxy for target: {target_address}");
    debug!("> Using client address: {client_address}");

    let server_target = target_address.clone();
    // Connect to target RDP server
    let target_addr = target_address
        .parse::<std::net::SocketAddr>()
        .context("Failed to parse target address")?;
    let server_stream = TcpStream::connect(target_addr)
        .await
        .context("Failed to connect to target RDP server")?;

    debug!("> Connected to target RDP server: {target_address}");

    // Extract credentials from the first RDP packet
    let first_rdp_data = rdp_data_rx
        .recv()
        .await
        .context("Failed to receive first RDP data")?;

    debug!(
        "> First RDP data (first 20 bytes): {:02x?}",
        &first_rdp_data[..std::cmp::min(20, first_rdp_data.len())]
    );

    // Each queued item is capped by ChannelWebSocketStream, so the slot count
    // is also a strict response-byte bound instead of an arbitrary Vec count.
    let (response_tx, mut response_rx) =
        tokio::sync::mpsc::channel::<Vec<u8>>(RESPONSE_QUEUE_SLOTS);
    let client_side = ChannelWebSocketStream::new(rdp_data_rx, response_tx);

    // The forwarder is owned by this session future. SessionTask aborts it if
    // this future is itself cancelled during reconnect cleanup.
    let ws_sender_clone = ws_sender.clone();
    let response_sid = sid;
    let response_task = SessionTask::new(tokio::spawn(async move {
        while let Some(data) = response_rx.recv().await {
            debug!(
                "> Forwarding {} bytes from RDP proxy to WebSocket",
                data.len()
            );

            let header_size = 20;
            let header = Header {
                sid: response_sid,
                len: data.len() as u32,
                data_size: header_size,
                control: false,
            };
            let mut framed_data = Vec::with_capacity(header_size + data.len());
            framed_data.extend_from_slice(&header.encode());
            framed_data.extend_from_slice(&data);

            let send_result = tokio::time::timeout(RESPONSE_SEND_TIMEOUT, async {
                let mut sender = ws_sender_clone.lock().await;
                sender.send(Message::Binary(framed_data.into())).await
            })
            .await;
            match send_result {
                Ok(Ok(())) => {}
                Ok(Err(error)) => {
                    anyhow::bail!("failed to send RDP response to WebSocket: {error}")
                }
                Err(_) => anyhow::bail!("timed out sending RDP response to WebSocket"),
            }
        }
        Ok::<(), anyhow::Error>(())
    }));

    // When the session is guarded, build a reporter over the same websocket.
    // An unguarded session never emits violation metadata.
    let report = guard
        .as_ref()
        .map(|_| build_violation_reporter(sid, ws_sender.clone(), violation_acks.clone()));

    // Move the only stored secret buffers into the RDP proxy. The proxy
    // zeroizes them immediately after CredSSP; no session-map or launcher copy
    // remains alive for the duration of the desktop session.
    let proxy = RdpProxy::builder()
        .server_target(server_target)
        .client_stream(client_side)
        .server_stream(server_stream)
        .config(config)
        .creds(proxy_user)
        .username(username)
        .password(password)
        .client_address(
            client_address
                .parse()
                .unwrap_or_else(|_| "127.0.0.1:0".parse().unwrap()),
        )
        .client_stream_leftover_bytes(bytes::BytesMut::from(first_rdp_data.as_slice()))
        .guard(guard)
        .session_id(sid.to_string())
        .report(report)
        .build();

    info!("> Starting RDP proxy run...");
    info!("> WebSocket stream adapter created, starting RDP proxy...");
    let proxy_result = proxy.run().await;
    // proxy.run owns the only response sender. Once it returns, the forwarder
    // drains any accepted tail and exits; each send is itself time-bounded.
    let response_result = response_task
        .join()
        .await
        .context("RDP response forwarder task failed")?;

    match proxy_result {
        Ok(()) => {
            response_result.context("RDP response forwarder failed")?;
            info!("> RDP proxy session completed successfully");
            Ok(())
        }
        Err(error) => {
            if let Err(response_error) = response_result {
                error!("> RDP response forwarder also failed: {response_error:#}");
            }
            error!("> RDP proxy failed: {error:#}");
            Err(error.context("RDP proxy failed"))
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    struct DropSignal(Option<oneshot::Sender<()>>);

    impl Drop for DropSignal {
        fn drop(&mut self) {
            if let Some(sender) = self.0.take() {
                let _ = sender.send(());
            }
        }
    }

    #[tokio::test]
    async fn dropping_session_task_aborts_its_child() {
        let (dropped_tx, dropped_rx) = oneshot::channel();
        let guard = DropSignal(Some(dropped_tx));
        let task = SessionTask::new(tokio::spawn(async move {
            let _guard = guard;
            std::future::pending::<()>().await;
        }));

        drop(task);
        tokio::time::timeout(Duration::from_secs(1), dropped_rx)
            .await
            .expect("child task remained detached")
            .expect("drop signal sender disappeared");
    }

    #[tokio::test]
    async fn cancelling_session_task_join_aborts_its_child() {
        let (dropped_tx, dropped_rx) = oneshot::channel();
        let guard = DropSignal(Some(dropped_tx));
        let task = SessionTask::new(tokio::spawn(async move {
            let _guard = guard;
            std::future::pending::<()>().await;
        }));
        let owner = tokio::spawn(task.join());
        tokio::task::yield_now().await;

        owner.abort();
        let _ = owner.await;
        tokio::time::timeout(Duration::from_secs(1), dropped_rx)
            .await
            .expect("child task detached while its join was cancelled")
            .expect("drop signal sender disappeared");
    }
}
