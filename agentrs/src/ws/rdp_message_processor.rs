use crate::ws::proxy::start_rdp_proxy_session;
use crate::ws::session::SessionInfo;
use crate::ws::types::{ChannelMap, ProxyMap, SessionMap, WsWriter};
use crate::{conf, session::Header};
use anyhow::Context;
use std::sync::Arc;
use tokio::sync::mpsc::{Receiver, Sender};
use tokio_tungstenite::WebSocketStream;
use tracing::{debug, error, info, instrument};
use uuid::Uuid;

use futures::{SinkExt, StreamExt};
use serde_json::{self, Value};
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
                    break;
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

        Ok(())
    }

    async fn handle_binary_message(&self, data: Vec<u8>) -> anyhow::Result<()> {
        let Some((header, header_len)) = Header::decode(&data) else {
            info!("> Received data without valid header, ignoring");
            return Ok(());
        };

        if header_len > data.len() {
            error!("> Invalid header length: {} > {}", header_len, data.len());
            return Ok(());
        }

        let payload = &data[header_len..];

        // Try to parse as JSON message first
        if let Ok(message) = serde_json::from_slice::<Value>(payload) {
            if let Some(message_type) = message.clone().get("message_type").and_then(|v| v.as_str())
            {
                return self
                    .handle_json_message(header, message_type, message)
                    .await;
            }
        }

        // Otherwise treat as RDP data
        self.handle_rdp_data(header, payload).await
    }

    async fn handle_json_message(
        &self,
        header: Header,
        message_type: &str,
        message: Value,
    ) -> anyhow::Result<()> {
        match message_type {
            "session_started" => {
                info!(
                    "> Session {} started, processing connection info...",
                    header.sid
                );
                self.handle_session_started(header, message).await
            }
            _ => {
                info!("> Unknown message type: {}", message_type);
                Ok(())
            }
        }
    }

    //TODO message should be more strongly typed for the protocol
    #[instrument(level = "debug", skip(self, message))]
    async fn handle_session_started(&self, header: Header, message: Value) -> anyhow::Result<()> {
        let session_info = SessionInfo {
            session_id: header.sid,
            target_address: message
                .get("target_address")
                .and_then(|v| v.as_str())
                .context("Missing target_address")?
                .to_string(),
            username: message
                .get("username")
                .and_then(|v| v.as_str())
                .context("Missing username")?
                .to_string(),
            password: message
                .get("password")
                .and_then(|v| v.as_str())
                .context("Missing password")?
                .to_string(),
            proxy_user: message
                .get("proxy_user")
                .and_then(|v| v.as_str())
                .context("Missing proxy_user")?
                .to_string(),
            client_address: message
                .get("client_address")
                .and_then(|v| v.as_str())
                .unwrap_or("127.0.0.1:0")
                .to_string(),
            sender: self.ws_sender.clone(),
        };

        // Store session info
        {
            let mut sessions = self.sessions.write().await;
            sessions.insert(header.sid, session_info);
            debug!("> Stored session {} in sessions map", header.sid);
        }

        // Send response
        self.send_rdp_started_response(header.sid).await
    }

    async fn send_rdp_started_response(&self, session_id: Uuid) -> anyhow::Result<()> {
        let response = serde_json::json!({
            "message_type": "rdp_started",
        });

        let response_str = response.to_string();
        let response_header = Header {
            sid: session_id,
            len: response_str.len() as u32,
        };

        let mut response_framed = Vec::with_capacity(20 + response_str.len());
        response_framed.extend_from_slice(&response_header.encode());
        response_framed.extend_from_slice(response_str.as_bytes());

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
    async fn handle_rdp_data(&self, header: Header, rdp_data: &[u8]) -> anyhow::Result<()> {
        debug!(
            "> Received RDP data for session: {} ({} bytes)",
            header.sid,
            rdp_data.len()
        );

        // Check if we have session info
        let sessions = self.sessions.read().await;
        let Some(session_info) = sessions.get(&header.sid) else {
            debug!("> Received RDP data for unknown session: {}", header.sid);
            return Ok(());
        };

        let session_info = session_info.clone();
        drop(sessions);

        // Get or create RDP data channel for this session
        let (tx, rx) = self.get_or_create_session_channel(header.sid).await;

        // Start RDP proxy if not already running
        if !self.is_proxy_running(header.sid).await {
            self.start_rdp_proxy(header.sid, session_info, rx).await?;
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
