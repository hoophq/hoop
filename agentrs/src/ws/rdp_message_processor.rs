use crate::conf;
use crate::session::Header;
use crate::ws::message::{PROTOCOL_RDP, WebSocketMessage};
use crate::ws::message_types::MessageType;
use crate::ws::proxy::start_rdp_proxy_session;
use crate::ws::session::SessionInfo;
use crate::ws::types::{ChannelMap, ProxyMap, SessionMap, WsWriter};
use anyhow::Context;
use std::sync::Arc;
use tokio::sync::mpsc::{Receiver, Sender};
use tokio_tungstenite::WebSocketStream;
use tracing::{debug, error, info, instrument};
use uuid::Uuid;

use futures::{SinkExt, StreamExt};
use tokio::sync::Mutex;
use tokio_tungstenite::tungstenite::protocol::Message;

#[derive(Clone, Debug)]
pub struct MessageProcessor {
    pub ws_sender: WsWriter,
    pub sessions: SessionMap,
    pub active_proxies: ProxyMap,
    pub session_channels: ChannelMap,
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

        while let Some(msg) = ws_receiver.next().await {
            match msg? {
                Message::Binary(data) => {
                    if let Err(e) = self.handle_binary_message(data.into()).await {
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

    async fn handle_binary_message(&self, data: Vec<u8>) -> anyhow::Result<()> {
        // Try to decode as WebSocketMessage first (for control messages)
        if let Ok((sid, message)) = WebSocketMessage::decode_with_header(&data) {
            // Handle different message types
            match message.message_type {
                MessageType::SessionStarted => {
                    info!(
                        "> Session {} started, processing connection info...",
                        sid
                    );
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
                MessageType::Capabilities => {
                    // Agent -> gateway only; never expected inbound.
                    info!("> Ignoring unexpected inbound capabilities frame");
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
            // Try to decode as raw data with header (for RDP data)
            if let Some(header) = Header::decode(&data) {
                if data.len() >= header.data_size {
                    let rdp_data = &data[header.data_size..];
                    debug!(
                        "> Received raw RDP data for session: {} ({} bytes)",
                        header.sid,
                        rdp_data.len()
                    );
                    self.handle_rdp_data(header.sid, rdp_data).await
                } else {
                    info!("> Insufficient data for payload, ignoring");
                    Ok(())
                }
            } else {
                info!("> Failed to decode message as WebSocketMessage or raw data, ignoring");
                Ok(())
            }
        }
    }

    #[instrument(level = "debug", skip(self, message))]
    async fn handle_session_started(
        &self,
        sid: Uuid,
        message: WebSocketMessage,
    ) -> anyhow::Result<()> {
        // Debug: print the metadata to see what we're receiving
        debug!(
            "> Received session_started for {} with metadata: {:?}",
            sid, message.metadata
        );

        // Check if session already exists to prevent duplicate processing
        {
            let sessions = self.sessions.read().await;
            if sessions.contains_key(&sid) {
                debug!("> Session {} already exists, ignoring duplicate", sid);
                return Ok(());
            }
        }

        let target_address = message
            .metadata
            .get("target_address")
            .context("Missing target_address")?
            .clone();
        let username = message
            .metadata
            .get("username")
            .context("Missing username")?
            .clone();

        let password = message
            .metadata
            .get("password")
            .context("Missing password")?
            .clone();

        let proxy_user = message
            .metadata
            .get("proxy_user")
            .context("Missing proxy_user")?
            .clone();

        let client_address = message
            .metadata
            .get("client_address")
            .context("Missing client_address")?
            .clone();

        // Resolve the PII guard from gateway policy (metadata) + agent env
        // (endpoints). Fail CLOSED on a delegation mismatch: if the gateway
        // delegated guarding (and therefore suppressed its own gate) but the
        // agent cannot build the guard, refuse the session rather than run it
        // unguarded — that would be a silent enforcement bypass.
        let guard = match crate::piigate::config::GuardConfig::resolve(
            &message.metadata,
            &sid.to_string(),
        ) {
            Ok(guard) => guard,
            Err(e) => {
                error!("> Refusing session {sid}: {e:#}");
                return Err(e);
            }
        };

        let session_info = SessionInfo {
            sid: sid,
            target_address,
            username,
            password,
            proxy_user,
            client_address,
            guard,
            sender: self.ws_sender.clone(),
        };

        // Store session info
        {
            let mut sessions = self.sessions.write().await;
            sessions.insert(sid, session_info);
            debug!("> Stored session {} in sessions map", sid);
        }

        // Send response
        self.send_rdp_started_response(sid).await
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

    #[instrument(level = "debug")]
    async fn handle_rdp_data(&self, sid: Uuid, rdp_data: &[u8]) -> anyhow::Result<()> {
        debug!(
            "> Received RDP data for session: {} ({} bytes)",
            sid,
            rdp_data.len()
        );

        // Check if we have session info
        let sessions = self.sessions.read().await;
        let Some(session_info) = sessions.get(&sid) else {
            debug!("> Received RDP data for unknown session: {}", sid);
            return Ok(());
        };

        let session_info = session_info.clone();
        drop(sessions);

        // Get or create RDP data channel for this session
        let (tx, rx) = self.get_or_create_session_channel(sid).await;

        // Start the RDP proxy exactly once per session. The check and the
        // spawn happen under a single write lock on active_proxies so two
        // concurrent data messages for the same SID cannot both spawn a proxy
        // (the websocket receive loop is serial today, but the guard now
        // depends on a single proxy/gate per session).
        self.start_rdp_proxy_once(sid, session_info, rx).await;

        // Forward RDP data to proxy
        tx.send(rdp_data.to_vec())
            .await
            .map_err(|_| anyhow::anyhow!("Session channel closed"))?;

        Ok(())
    }

    async fn get_or_create_session_channel(
        &self,
        sid: Uuid,
    ) -> (Sender<Vec<u8>>, Arc<Mutex<Receiver<Vec<u8>>>>) {
        let mut channels = self.session_channels.write().await;

        if let Some((tx, rx)) = channels.get(&sid) {
            (tx.clone(), rx.clone())
        } else {
            let (tx, rx) = tokio::sync::mpsc::channel::<Vec<u8>>(1024);
            let rx_arc = Arc::new(Mutex::new(rx));
            channels.insert(sid, (tx.clone(), rx_arc.clone()));
            (tx, rx_arc)
        }
    }

    async fn start_rdp_proxy_once(
        &self,
        sid: Uuid,
        session_info: SessionInfo,
        rdp_data_rx: Arc<Mutex<Receiver<Vec<u8>>>>,
    ) {
        // Hold the write lock across the check and the insert so the spawn is
        // atomic with respect to other data messages for the same SID.
        let mut proxies = self.active_proxies.write().await;
        if proxies.contains_key(&sid) {
            return;
        }

        let config = self.config_manager.conf.clone();
        let ws_sender = self.ws_sender.clone();
        let active_proxies = self.active_proxies.clone();

        let proxy_task = tokio::spawn(async move {
            let result =
                start_rdp_proxy_session(session_info, ws_sender, rdp_data_rx, config).await;

            match result {
                Ok(_) => info!("> RDP proxy session completed for session {}", sid),
                Err(e) => error!("> RDP proxy session failed for session {}: {}", sid, e),
            }

            // Cleanup
            let mut proxies = active_proxies.write().await;
            proxies.remove(&sid);
            info!("> Cleaned up RDP proxy task for session {}", sid);
        });

        proxies.insert(sid, proxy_task);
        debug!("> Started RDP proxy task for session {}", sid);
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
