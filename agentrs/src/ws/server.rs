use crate::ws::proxy::start_rdp_proxy_session;
use crate::ws::session::SessionInfo;
use crate::{conf, session::Header, tasks::tasks::*};
use anyhow::Context;
use async_trait::async_trait;
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::RwLock;
use uuid::Uuid;

use futures::{SinkExt, StreamExt};
use serde_json::{self};
use tokio::sync::Mutex;
use tokio_tungstenite::{connect_async, tungstenite::protocol::Message};

#[derive(Clone)]
pub struct WebSocketServer {
    pub config_manager: conf::ConfigHandleManager,
    pub gateway_url: String,
    pub sessions: Arc<RwLock<HashMap<Uuid, SessionInfo>>>,
}

#[async_trait]
impl Task for WebSocketServer {
    type Output = anyhow::Result<()>;

    const NAME: &'static str = "agent listener";

    async fn run(self, mut shutdown_signal: ShutdownSignal) -> Self::Output {
        tokio::select! {
            result = self.run() => result,
            _ = shutdown_signal.wait() => Ok(()),
        }
    }
}

impl WebSocketServer {
    pub fn new() -> anyhow::Result<Self> {
        let config_manager =
            conf::ConfigHandleManager::init().context("Failed to init config manager")?;

        let gateway_url = "ws://localhost:8080/ws";
        Ok(WebSocketServer {
            gateway_url: gateway_url.to_string(),
            config_manager: config_manager,
            sessions: Arc::new(RwLock::new(HashMap::new())),
        })
    }

    async fn run(self) -> anyhow::Result<()> {
        let (ws_stream, _) = connect_async(self.gateway_url)
            .await
            .expect("Failed to connect to gateway");

        let (ws_sender, ws_receiver) = ws_stream.split();
        println!("> Connected to gateway");

        // Clone config manager and sessions for use in the async task
        let config_manager = self.config_manager.clone();
        let sessions = self.sessions.clone();

        // > Handle incoming messages from gateway
        let ws_sender = std::sync::Arc::new(tokio::sync::Mutex::new(ws_sender));
        let ws_sender_clone = ws_sender.clone();
        let ws_receiver_clone = Arc::new(Mutex::new(ws_receiver));
        let config_manager_clone = config_manager.clone();
        let sessions_clone = sessions.clone();
        // Create a channel for forwarding RDP data to the RDP proxy
        let (rdp_data_tx, rdp_data_rx) = tokio::sync::mpsc::channel::<Vec<u8>>(1024);
        let rdp_data_rx = Arc::new(Mutex::new(rdp_data_rx));

        let gateway_to_agent = tokio::spawn(async move {
            println!("> Starting to receive messages from gateway...");
            let mut ws_receiver = ws_receiver_clone.lock().await;
            let mut current_session_info: Option<SessionInfo> = None;
            let mut rdp_proxy_task: Option<tokio::task::JoinHandle<()>> = None;

            while let Some(msg) = ws_receiver.next().await {
                match msg {
                    Ok(Message::Binary(data)) => {
                        // Try to decode as connection info first (with header)
                        if let Some((header, header_len)) = Header::decode(&data) {
                            if header_len <= data.len() {
                                let json_data = &data[header_len..];
                                if let Ok(connection_info) =
                                    serde_json::from_slice::<serde_json::Value>(json_data)
                                {
                                    println!(
                                        "> Received connection info for session: {}",
                                        header.sid
                                    );

                                    // Parse client address from connection info
                                    let client_address = connection_info
                                        .get("client_address")
                                        .and_then(|v| v.as_str())
                                        .unwrap_or("127.0.0.1:0")
                                        .to_string();

                                    let target_address = connection_info
                                        .get("target_address")
                                        .and_then(|v| v.as_str())
                                        .unwrap_or("10.211.55.6:3389")
                                        .to_string();

                                    let username = connection_info
                                        .get("username")
                                        .and_then(|v| v.as_str())
                                        .unwrap_or("fake")
                                        .to_string();

                                    let password = connection_info
                                        .get("password")
                                        .and_then(|v| v.as_str())
                                        .unwrap_or("fake")
                                        .to_string();

                                    current_session_info = Some(SessionInfo {
                                        session_id: header.sid,
                                        target_address: target_address.clone(),
                                        username,
                                        password,
                                        client_address: client_address.clone(),
                                    });

                                    println!("> Client address: {}", client_address);
                                    println!("> Target address: {}", target_address);

                                    // Start the RDP proxy task for this session
                                    if rdp_proxy_task.is_none() {
                                        let config_clone = config_manager_clone.conf.clone();
                                        let ws_receiver_clone_for_task = ws_receiver_clone.clone();
                                        let ws_sender_clone = ws_sender_clone.clone();
                                        let session_info_clone =
                                            current_session_info.as_ref().unwrap().clone();

                                        let rdp_data_rx_clone = rdp_data_rx.clone();
                                        rdp_proxy_task = Some(tokio::spawn(async move {
                                            match start_rdp_proxy_session(
                                                session_info_clone,
                                                ws_sender_clone,
                                                rdp_data_rx_clone,
                                                config_clone,
                                            )
                                            .await
                                            {
                                                Ok(_) => println!("> RDP proxy session completed"),
                                                Err(e) => {
                                                    eprintln!("> RDP proxy session failed: {}", e)
                                                }
                                            }
                                        }));
                                    }

                                    continue; // Skip to next message
                                }
                            }
                        }

                        // If we get here, it's raw RDP data - we need to forward it to the RDP proxy
                        if let Some(session_info) = &current_session_info {
                            println!(
                                "> Received RDP data for session: {}",
                                session_info.session_id
                            );
                            println!("> RDP data length: {} bytes", data.len());
                            println!(
                                "> RDP data (first 20 bytes): {:02x?}",
                                &data[..std::cmp::min(20, data.len())]
                            );

                            // Forward the RDP data to the RDP proxy through the channel
                            println!("> Forwarding RDP data to RDP proxy...");
                            if let Err(e) = rdp_data_tx.send(data.to_vec()).await {
                                eprintln!("> Failed to forward RDP data to RDP proxy: {}", e);
                            }
                        } else {
                            println!(">  Received RDP data but no session info available");
                        }
                    }
                    Ok(Message::Text(text)) => {
                        println!(" Text from gateway: {}", text);
                    }
                    Ok(Message::Close(_)) => {
                        println!(" Gateway closed connection");
                        break;
                    }
                    Ok(Message::Ping(data)) => {
                        println!(" Ping from gateway, sending pong");
                        // Send pong back via WebSocket
                        let mut sender = ws_sender_clone.lock().await;
                        if let Err(e) = sender.send(Message::Pong(data)).await {
                            eprintln!(" Failed to send pong: {}", e);
                            break;
                        }
                    }
                    Ok(Message::Pong(_)) => {
                        println!(" Pong from gateway");
                    }
                    Ok(Message::Frame(_)) => {
                        // Handle raw frames if needed
                    }
                    Err(e) => {
                        eprintln!(" WebSocket error: {}", e);
                        break;
                    }
                }
            }
        });

        // Wait for the gateway task to complete
        gateway_to_agent.await?;
        println!("> Gateway connection closed");

        println!("> Agent shutting down");
        Ok(())
    }
}
