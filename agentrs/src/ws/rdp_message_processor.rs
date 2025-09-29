use crate::conf;
use crate::ws::message::{
    Header, MESSAGE_TYPE_DATA, MESSAGE_TYPE_SESSION_STARTED, PROTOCOL_RDP, WebSocketMessage,
};
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
        if let Ok((session_id, message)) = WebSocketMessage::decode_with_header(&data) {
            // Handle different message types
            match message.message_type.as_str() {
                MESSAGE_TYPE_SESSION_STARTED => {
                    info!(
                        "> Session {} started, processing connection info...",
                        session_id
                    );
                    self.handle_session_started(session_id, message).await
                }
                MESSAGE_TYPE_DATA => {
                    debug!(
                        "> Received data for session: {} ({} bytes)",
                        session_id,
                        message.payload.len()
                    );
                    self.handle_rdp_data(session_id, &message.payload).await
                }
                _ => {
                    info!(
                        "> Unknown message type: {} for session: {}",
                        message.message_type, session_id
                    );
                    Ok(())
                }
            }
        } else {
            // Try to decode as raw data with header (for RDP data)
            if let Some((header, header_len)) = Header::decode(&data) {
                if data.len() >= header_len {
                    let rdp_data = &data[header_len..];
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
        session_id: Uuid,
        message: WebSocketMessage,
    ) -> anyhow::Result<()> {
        // Debug: print the metadata to see what we're receiving
        debug!(
            "> Received session_started for {} with metadata: {:?}",
            session_id, message.metadata
        );

        // Check if session already exists to prevent duplicate processing
        {
            let sessions = self.sessions.read().await;
            if sessions.contains_key(&session_id) {
                debug!(
                    "> Session {} already exists, ignoring duplicate",
                    session_id
                );
                return Ok(());
            }
        }

        let session_info = SessionInfo {
            session_id,
            target_address: message
                .metadata
                .get("target_address")
                .context("Missing target_address")?
                .clone(),
            username: message
                .metadata
                .get("username")
                .context("Missing username")?
                .clone(),
            password: message
                .metadata
                .get("password")
                .context("Missing password")?
                .clone(),
            proxy_user: message
                .metadata
                .get("proxy_user")
                .context("Missing proxy_user")?
                .clone(),
            client_address: message
                .metadata
                .get("client_address")
                .unwrap_or(&"127.0.0.1:0".to_string())
                .clone(),
            sender: self.ws_sender.clone(),
        };

        // Store session info
        {
            let mut sessions = self.sessions.write().await;
            sessions.insert(session_id, session_info);
            debug!("> Stored session {} in sessions map", session_id);
        }

        // Send response
        self.send_rdp_started_response(session_id).await
    }

    async fn send_rdp_started_response(&self, session_id: Uuid) -> anyhow::Result<()> {
        let mut metadata = std::collections::HashMap::new();
        metadata.insert("protocol".to_string(), PROTOCOL_RDP.to_string());

        let response = WebSocketMessage::new(
            MESSAGE_TYPE_SESSION_STARTED.to_string(),
            metadata,
            Vec::new(),
        );

        let response_framed = response
            .encode_with_header(session_id)
            .context("Failed to encode rdp_started response")?;

        let mut sender = self.ws_sender.lock().await;
        sender
            .send(Message::Binary(response_framed.into()))
            .await
            .context("Failed to send rdp_started response")?;

        debug!(
            "> Successfully sent rdp_started response for session {}",
            session_id
        );
        Ok(())
    }

    #[instrument(level = "debug")]
    async fn handle_rdp_data(&self, session_id: Uuid, rdp_data: &[u8]) -> anyhow::Result<()> {
        debug!(
            "> Received RDP data for session: {} ({} bytes)",
            session_id,
            rdp_data.len()
        );

        // Check if we have session info
        let sessions = self.sessions.read().await;
        let Some(session_info) = sessions.get(&session_id) else {
            debug!("> Received RDP data for unknown session: {}", session_id);
            return Ok(());
        };

        let session_info = session_info.clone();
        drop(sessions);

        // Get or create RDP data channel for this session
        let (tx, rx) = self.get_or_create_session_channel(session_id).await;

        // Start RDP proxy if not already running
        if !self.is_proxy_running(session_id).await {
            self.start_rdp_proxy(session_id, session_info, rx).await?;
        }

        // Forward RDP data to proxy
        tx.send(rdp_data.to_vec())
            .await
            .map_err(|_| anyhow::anyhow!("Session channel closed"))?;

        Ok(())
    }

    async fn get_or_create_session_channel(
        &self,
        session_id: Uuid,
    ) -> (Sender<Vec<u8>>, Arc<Mutex<Receiver<Vec<u8>>>>) {
        let mut channels = self.session_channels.write().await;

        if let Some((tx, rx)) = channels.get(&session_id) {
            (tx.clone(), rx.clone())
        } else {
            let (tx, rx) = tokio::sync::mpsc::channel::<Vec<u8>>(1024);
            let rx_arc = Arc::new(Mutex::new(rx));
            channels.insert(session_id, (tx.clone(), rx_arc.clone()));
            (tx, rx_arc)
        }
    }

    async fn is_proxy_running(&self, session_id: Uuid) -> bool {
        let proxies = self.active_proxies.read().await;
        proxies.contains_key(&session_id)
    }

    async fn start_rdp_proxy(
        &self,
        session_id: Uuid,
        session_info: SessionInfo,
        rdp_data_rx: Arc<Mutex<Receiver<Vec<u8>>>>,
    ) -> anyhow::Result<()> {
        let config = self.config_manager.conf.clone();
        let ws_sender = self.ws_sender.clone();
        let active_proxies = self.active_proxies.clone();

        let proxy_task = tokio::spawn(async move {
            let result =
                start_rdp_proxy_session(session_info, ws_sender, rdp_data_rx, config).await;

            match result {
                Ok(_) => info!("> RDP proxy session completed for session {}", session_id),
                Err(e) => error!(
                    "> RDP proxy session failed for session {}: {}",
                    session_id, e
                ),
            }

            // Cleanup
            let mut proxies = active_proxies.write().await;
            proxies.remove(&session_id);
            info!("> Cleaned up RDP proxy task for session {}", session_id);
        });

        // Store the proxy task
        let mut proxies = self.active_proxies.write().await;
        proxies.insert(session_id, proxy_task);
        debug!("> Started RDP proxy task for session {}", session_id);

        Ok(())
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
