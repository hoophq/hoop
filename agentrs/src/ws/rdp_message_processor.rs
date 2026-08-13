use crate::conf;
use crate::session::Header;
use crate::ws::control::{CAPABILITY_FRAME_PROTOCOL, CONTROL_SENTINEL_SID, FRAME_PROTOCOL_V2};
use crate::ws::message::{PROTOCOL_RDP, WebSocketMessage};
use crate::ws::message_types::MessageType;
use crate::ws::proxy::start_rdp_proxy_session;
use crate::ws::session::SessionInfo;
use crate::ws::types::{ChannelMap, ProxyMap, SessionMap, ViolationAckMap, WsWriter};
use anyhow::Context;
use tokio::sync::mpsc::Sender;
use tracing::{debug, error, info, instrument};
use uuid::Uuid;
use zeroize::Zeroizing;

use futures::{SinkExt, StreamExt};
use tokio_tungstenite::WebSocketStream;
use tokio_tungstenite::tungstenite::protocol::Message;

const RDP_INPUT_CHUNK_BYTES: usize = 32 * 1024;
const RDP_INPUT_QUEUE_SLOTS: usize = 32;

const MAX_LEGACY_CONTROL_FRAME_BYTES: usize = 1 << 20;

fn decode_inbound_control(
    data: &[u8],
    header: Header,
    frame_protocol_v2: bool,
) -> anyhow::Result<Option<(Uuid, WebSocketMessage)>> {
    if header.control {
        return WebSocketMessage::decode_with_header(data)
            .map(Some)
            .map_err(|error| anyhow::anyhow!("invalid control frame: {error}"));
    }
    if frame_protocol_v2 {
        return Ok(None);
    }

    // Legacy gateways did not set a frame-kind bit. Preserve that peer mode
    // only until v2 is acknowledged, and only for valid, bounded envelopes in
    // the gateway->agent direction. Invalid JSON remains raw RDP data.
    let payload = &data[header.data_size..];
    if payload.len() > MAX_LEGACY_CONTROL_FRAME_BYTES
        || payload
            .iter()
            .find(|byte| !byte.is_ascii_whitespace())
            .copied()
            != Some(b'{')
    {
        return Ok(None);
    }
    let Ok(message) = serde_json::from_slice::<WebSocketMessage>(payload) else {
        return Ok(None);
    };
    if !matches!(
        message.message_type,
        MessageType::SessionStarted | MessageType::Data | MessageType::GuardrailsViolationAck
    ) {
        return Ok(None);
    }
    Ok(Some((header.sid, message)))
}

#[derive(Clone)]
pub struct MessageProcessor {
    pub ws_sender: WsWriter,
    pub sessions: SessionMap,
    pub active_proxies: ProxyMap,
    pub session_channels: ChannelMap,
    pub violation_acks: ViolationAckMap,
    pub config_manager: conf::ConfigHandleManager,
}

impl MessageProcessor {
    pub async fn process_messages(
        self,
        mut ws_receiver: futures::stream::SplitStream<
            WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>,
        >,
    ) -> anyhow::Result<()> {
        info!("> Starting to receive messages from gateway...");
        let mut frame_protocol_v2 = false;

        while let Some(msg) = ws_receiver.next().await {
            match msg? {
                Message::Binary(data) => {
                    if let Err(e) = self
                        .handle_binary_message(data.into(), &mut frame_protocol_v2)
                        .await
                    {
                        error!("> Error handling binary message: {}", e);
                    }
                }
                Message::Text(text) => {
                    debug!("> Text from gateway: {}", text);
                }
                Message::Close(frame) => {
                    debug!("> Gateway closed connection: {:?}", frame);
                    return Err(anyhow::anyhow!("Gateway closed connection: {:?}", frame));
                }
                Message::Ping(data) => {
                    if let Err(e) = self.handle_ping(data.into()).await {
                        error!("> Failed to respond to ping: {}", e);
                    }
                }
                Message::Pong(_) => {
                    debug!("> Pong from gateway");
                }
                Message::Frame(_) => {
                    // Handle raw frames if needed
                }
            }
        }

        // If we exit the loop, it means the stream ended unexpectedly
        error!("> WebSocket stream ended unexpectedly");
        Err(anyhow::anyhow!("WebSocket stream ended unexpectedly"))
    }

    async fn handle_binary_message(
        &self,
        data: Vec<u8>,
        frame_protocol_v2: &mut bool,
    ) -> anyhow::Result<()> {
        let header = Header::decode(&data).context("invalid gateway frame")?;
        if let Some((sid, message)) = decode_inbound_control(&data, header, *frame_protocol_v2)? {
            // Handle different message types
            match message.message_type {
                MessageType::SessionStarted => {
                    info!("> Session {} started, processing connection info...", sid);
                    self.handle_session_started(sid, message).await
                }
                MessageType::Data => {
                    debug!(
                        "> Received data for session: {} ({} bytes)",
                        sid,
                        message.payload.len()
                    );
                    self.handle_rdp_data(sid, &message.payload).await
                }
                MessageType::GuardrailsViolation => {
                    // Agent -> gateway only; never expected inbound.
                    info!(
                        "> Ignoring unexpected inbound guardrails_violation for session: {}",
                        sid
                    );
                    Ok(())
                }
                MessageType::GuardrailsViolationAck => {
                    self.handle_guardrails_violation_ack(sid, &message)
                }
                MessageType::Capabilities => {
                    if sid == CONTROL_SENTINEL_SID
                        && message
                            .metadata
                            .get(CAPABILITY_FRAME_PROTOCOL)
                            .is_some_and(|value| value == FRAME_PROTOCOL_V2)
                    {
                        *frame_protocol_v2 = true;
                        info!("> Gateway acknowledged frame protocol v2");
                    } else {
                        info!("> Ignoring unexpected inbound capabilities frame");
                    }
                    Ok(())
                }
                MessageType::Unknown => {
                    info!(
                        "> Unknown message type: {:#?} for session: {}",
                        message.message_type, sid
                    );
                    Ok(())
                }
            }
        } else {
            let rdp_data = &data[header.data_size..];
            debug!(
                "> Received raw RDP data for session: {} ({} bytes)",
                header.sid,
                rdp_data.len()
            );
            self.handle_rdp_data(header.sid, rdp_data).await
        }
    }

    #[instrument(level = "debug", skip(self, message))]
    async fn handle_session_started(
        &self,
        sid: Uuid,
        mut message: WebSocketMessage,
    ) -> anyhow::Result<()> {
        debug!(
            sid = %sid,
            guard_requested = crate::guard::guard_requested(&message.metadata),
            "> Received session_started"
        );

        // Resolve the PII guard before removing credentials from metadata.
        // The guard reads only policy keys; secrets must move immediately into
        // zeroizing containers instead of lingering in the decoded message.
        let guard = crate::guard::GuardConfig::resolve(&message.metadata, &sid.to_string())
            .map_err(|e| {
                error!("> Refusing session {sid}: {e:#}");
                e
            })?;
        let target_address = message
            .metadata
            .remove("target_address")
            .context("Missing target_address")?;
        let username = message
            .metadata
            .remove("username")
            .context("Missing username")?;
        let password = Zeroizing::new(
            message
                .metadata
                .remove("password")
                .context("Missing password")?,
        );
        let proxy_user = Zeroizing::new(
            message
                .metadata
                .remove("proxy_user")
                .context("Missing proxy_user")?,
        );
        let client_address = message
            .metadata
            .remove("client_address")
            .context("Missing client_address")?;

        // Check after wrapping credentials so duplicate start messages also
        // zeroize their secret values when this function returns. The lock
        // order matches startup/finalization: proxies, then sessions.
        {
            let proxies = self.active_proxies.read().await;
            let mut sessions = self.sessions.write().await;
            if proxies.contains_key(&sid) || sessions.contains_key(&sid) {
                debug!("> Session {} already exists, ignoring duplicate", sid);
                return Ok(());
            }
            sessions.insert(
                sid,
                SessionInfo {
                    sid,
                    target_address,
                    username,
                    password,
                    proxy_user,
                    client_address,
                    guard,
                },
            );
            debug!("> Stored pending session {}", sid);
        }

        if let Err(error) = self.send_rdp_started_response(sid).await {
            self.terminate_session(sid).await;
            return Err(error);
        }
        Ok(())
    }

    async fn send_rdp_started_response(&self, sid: Uuid) -> anyhow::Result<()> {
        let mut metadata = std::collections::HashMap::new();
        metadata.insert("protocol".to_string(), PROTOCOL_RDP.to_string());

        let response = WebSocketMessage::new(MessageType::SessionStarted, metadata, Vec::new());

        let response_framed = response
            .encode_with_header(sid)
            .context("Failed to encode rdp_started response")?;

        let mut sender = self.ws_sender.lock().await;
        sender
            .send(Message::Binary(response_framed.into()))
            .await
            .context("Failed to send rdp_started response")?;

        debug!(
            "> Successfully sent rdp_started response for session {}",
            sid
        );
        Ok(())
    }

    fn handle_guardrails_violation_ack(
        &self,
        sid: Uuid,
        message: &WebSocketMessage,
    ) -> anyhow::Result<()> {
        let report_id = message
            .metadata
            .get("report_id")
            .context("guardrails violation acknowledgement missing report_id")?
            .parse::<Uuid>()
            .context("invalid guardrails violation acknowledgement report_id")?;
        let waiter = self
            .violation_acks
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .remove(&report_id);
        if let Some(waiter) = waiter {
            let _ = waiter.send(());
            debug!(
                "> Matched guardrails violation acknowledgement {} for session {}",
                report_id, sid
            );
        } else {
            debug!(
                "> Ignoring stale guardrails violation acknowledgement {} for session {}",
                report_id, sid
            );
        }
        Ok(())
    }

    #[instrument(level = "debug", skip(self, rdp_data), fields(bytes = rdp_data.len()))]
    async fn handle_rdp_data(&self, sid: Uuid, rdp_data: &[u8]) -> anyhow::Result<()> {
        debug!(
            "> Received RDP data for session: {} ({} bytes)",
            sid,
            rdp_data.len()
        );

        let tx = self.get_or_start_rdp_proxy(sid).await?;
        for chunk in rdp_data.chunks(RDP_INPUT_CHUNK_BYTES) {
            match tx.try_send(chunk.to_vec()) {
                Ok(()) => {}
                Err(tokio::sync::mpsc::error::TrySendError::Full(_)) => {
                    self.terminate_session(sid).await;
                    anyhow::bail!(
                        "session {sid} exceeded its {}-byte RDP input budget",
                        RDP_INPUT_CHUNK_BYTES * RDP_INPUT_QUEUE_SLOTS
                    );
                }
                Err(tokio::sync::mpsc::error::TrySendError::Closed(_)) => {
                    self.terminate_session(sid).await;
                    anyhow::bail!("session {sid} RDP input channel closed");
                }
            }
        }
        Ok(())
    }

    /// Returns the one bounded input channel for `sid`, spawning its proxy
    /// exactly once. Pending session credentials are moved out of the map at
    /// startup; active sessions retain only their channel and task handle. The
    /// lock order (proxies -> sessions -> channels) matches teardown.
    async fn get_or_start_rdp_proxy(&self, sid: Uuid) -> anyhow::Result<Sender<Vec<u8>>> {
        let mut proxies = self.active_proxies.write().await;
        if proxies.contains_key(&sid) {
            let channels = self.session_channels.read().await;
            return channels
                .get(&sid)
                .cloned()
                .with_context(|| format!("session {sid} proxy has no input channel"));
        }

        let mut sessions = self.sessions.write().await;
        let session_info = sessions
            .remove(&sid)
            .with_context(|| format!("RDP data for unknown or completed session {sid}"))?;
        let mut channels = self.session_channels.write().await;

        let (tx, rx) = tokio::sync::mpsc::channel::<Vec<u8>>(RDP_INPUT_QUEUE_SLOTS);
        let (start_tx, start_rx) = tokio::sync::oneshot::channel();
        let config = self.config_manager.conf.clone();
        let ws_sender = self.ws_sender.clone();
        let active_proxies = self.active_proxies.clone();
        let session_channels = self.session_channels.clone();
        let all_sessions = self.sessions.clone();
        let violation_acks = self.violation_acks.clone();

        let proxy_task = tokio::spawn(async move {
            // Do not let a fast setup failure run cleanup before the handle and
            // channel have been inserted atomically below.
            if start_rx.await.is_err() {
                return;
            }
            let result =
                start_rdp_proxy_session(session_info, ws_sender, rx, violation_acks, config).await;
            match result {
                Ok(()) => info!("> RDP proxy session completed for session {}", sid),
                Err(e) => error!("> RDP proxy session failed for session {}: {}", sid, e),
            }

            let detached =
                detach_session_state(sid, &active_proxies, &all_sessions, &session_channels).await;
            drop(detached);
            info!("> Finalized RDP proxy state for session {}", sid);
        });

        channels.insert(sid, tx.clone());
        proxies.insert(sid, proxy_task);
        if start_tx.send(()).is_err() {
            channels.remove(&sid);
            let handle = proxies.remove(&sid);
            drop(channels);
            drop(sessions);
            drop(proxies);
            if let Some(handle) = handle {
                handle.abort();
                let _ = handle.await;
            }
            anyhow::bail!("session {sid} proxy failed before startup");
        }
        debug!("> Started RDP proxy task for session {}", sid);
        Ok(tx)
    }

    async fn terminate_session(&self, sid: Uuid) {
        if let Some(handle) = detach_session_state(
            sid,
            &self.active_proxies,
            &self.sessions,
            &self.session_channels,
        )
        .await
        {
            handle.abort();
            let _ = handle.await;
        }
        info!("> Terminated RDP session {}", sid);
    }

    async fn handle_ping(&self, data: Vec<u8>) -> anyhow::Result<()> {
        debug!("> Ping from gateway, sending pong");
        let mut sender = self.ws_sender.lock().await;
        sender
            .send(Message::Pong(data.into()))
            .await
            .context("Failed to send pong response")
    }
}

async fn detach_session_state(
    sid: Uuid,
    active_proxies: &ProxyMap,
    sessions: &SessionMap,
    session_channels: &ChannelMap,
) -> Option<tokio::task::JoinHandle<()>> {
    let mut proxies = active_proxies.write().await;
    let mut sessions = sessions.write().await;
    let mut channels = session_channels.write().await;
    let handle = proxies.remove(&sid);
    sessions.remove(&sid);
    channels.remove(&sid);
    handle
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;
    use std::sync::Arc;
    use tokio::sync::RwLock;

    #[tokio::test]
    async fn finalization_removes_all_sid_owned_state() {
        let sid = Uuid::new_v4();
        let sessions: SessionMap = Arc::new(RwLock::new(HashMap::new()));
        let proxies: ProxyMap = Arc::new(RwLock::new(HashMap::new()));
        let channels: ChannelMap = Arc::new(RwLock::new(HashMap::new()));
        let (tx, _rx) = tokio::sync::mpsc::channel(1);

        sessions.write().await.insert(
            sid,
            SessionInfo {
                sid,
                target_address: "127.0.0.1:3389".to_owned(),
                username: "user".to_owned(),
                password: Zeroizing::new("password".to_owned()),
                proxy_user: Zeroizing::new("proxy-secret".to_owned()),
                client_address: "127.0.0.1:1234".to_owned(),
                guard: None,
            },
        );
        channels.write().await.insert(sid, tx);
        proxies
            .write()
            .await
            .insert(sid, tokio::spawn(std::future::pending()));

        let handle = detach_session_state(sid, &proxies, &sessions, &channels)
            .await
            .expect("active proxy handle");

        assert!(!sessions.read().await.contains_key(&sid));
        assert!(!channels.read().await.contains_key(&sid));
        assert!(!proxies.read().await.contains_key(&sid));
        handle.abort();
    }

    fn frame(control: bool, sid: Uuid, message: &WebSocketMessage) -> Vec<u8> {
        let payload = serde_json::to_vec(message).unwrap();
        let header = Header {
            sid,
            len: payload.len() as u32,
            data_size: crate::session::HEADER_LEN,
            control,
        };
        let mut framed = header.encode();
        framed.extend_from_slice(&payload);
        framed
    }

    #[test]
    fn legacy_envelope_is_accepted_before_negotiation() {
        let sid = Uuid::new_v4();
        let message = WebSocketMessage::new(MessageType::Data, HashMap::new(), b"rdp".to_vec());
        let framed = frame(false, sid, &message);
        let decoded = decode_inbound_control(&framed, Header::decode(&framed).unwrap(), false)
            .unwrap()
            .expect("legacy envelope");
        assert_eq!(decoded.0, sid);
        assert_eq!(decoded.1.message_type, MessageType::Data);
        assert_eq!(decoded.1.payload, b"rdp");
    }

    #[test]
    fn legacy_envelope_is_raw_after_negotiation() {
        let sid = Uuid::new_v4();
        let message = WebSocketMessage::new(MessageType::Data, HashMap::new(), b"rdp".to_vec());
        let framed = frame(false, sid, &message);
        assert!(
            decode_inbound_control(&framed, Header::decode(&framed).unwrap(), true)
                .unwrap()
                .is_none()
        );
    }

    #[test]
    fn target_json_is_not_legacy_control() {
        let sid = Uuid::new_v4();
        let payload = br#"{"type":"capabilities","metadata":{},"payload":""}"#;
        let header = Header {
            sid,
            len: payload.len() as u32,
            data_size: crate::session::HEADER_LEN,
            control: false,
        };
        let mut framed = header.encode();
        framed.extend_from_slice(payload);
        assert!(
            decode_inbound_control(&framed, Header::decode(&framed).unwrap(), false)
                .unwrap()
                .is_none()
        );
    }
}
