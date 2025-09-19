use crate::ws::proxy::start_rdp_proxy_session;
use crate::ws::session::SessionInfo;
use crate::{conf, session::Header, tasks::tasks::*};
use anyhow::Context;
use async_trait::async_trait;
use futures::stream::SplitSink;
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::RwLock;
use tokio::sync::mpsc::Receiver;
use tokio_tungstenite::WebSocketStream;
use uuid::Uuid;

use base64::Engine;
use futures::{SinkExt, StreamExt};
use serde_json::{self};
use tokio::sync::Mutex;
use tokio_tungstenite::{connect_async, tungstenite::protocol::Message};

#[derive(Clone)]
pub struct WebSocket {
    pub config_manager: conf::ConfigHandleManager,
    pub gateway_url: String,
    sessions: Arc<RwLock<HashMap<Uuid, SessionInfo>>>,
}

#[async_trait]
impl Task for WebSocket {
    type Output = anyhow::Result<()>;

    const NAME: &'static str = "agent listener";

    async fn run(self, mut shutdown_signal: ShutdownSignal) -> Self::Output {
        tokio::select! {
            result = self.run() => result,
            _ = shutdown_signal.wait() => Ok(()),
        }
    }
}

async fn started_session(
    header: Header,
    ws_sender: Arc<
        tokio::sync::Mutex<
            SplitSink<
                WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>,
                tungstenite::Message,
            >,
        >,
    >,

    message: serde_json::Value,
    sessions: Arc<RwLock<HashMap<Uuid, SessionInfo>>>,
) {
    let client_address = message
        .get("client_address")
        .and_then(|v| v.as_str())
        .unwrap_or("127.0.0.1:0")
        .to_string();

    let target_address = message
        .get("target_address")
        .and_then(|v| v.as_str())
        .unwrap()
        .to_string();

    let username = message
        .get("username")
        .and_then(|v| v.as_str())
        .unwrap()
        .to_string();

    let password = message
        .get("password")
        .and_then(|v| v.as_str())
        .unwrap()
        .to_string();

    let proxy_username = message
        .get("proxy_user")
        .and_then(|v| v.as_str())
        .unwrap()
        .to_string();

    let new_session_info = SessionInfo {
        session_id: header.sid,
        target_address: target_address.clone(),
        username: username.clone(),
        password: password.clone(),
        proxy_user: proxy_username,
        client_address: client_address.clone(),
        sender: ws_sender.clone(),
    };

    // Store session info in shared HashMap
    {
        let mut sessions = sessions.write().await;
        sessions.insert(header.sid, new_session_info.clone());
        println!("> Stored session {} in sessions map", header.sid);
    }

    //write back the gateway rdp_started response
    let rdp_started_response = serde_json::json!({
        "message_type": "rdp_started",
    });
    let response_header = Header {
        sid: header.sid,
        len: rdp_started_response.to_string().len() as u32,
    };
    let mut response_framed = Vec::with_capacity(20 + rdp_started_response.to_string().len());
    response_framed.extend_from_slice(&response_header.encode());
    response_framed.extend_from_slice(&rdp_started_response.to_string().into_bytes());
    let mut sender = ws_sender.lock().await;
    if let Err(e) = sender.send(Message::Binary(response_framed.into())).await {
        eprintln!("> Failed to send rdp_started response: {}", e);
    } else {
        println!("> Successfully sent rdp_started response");
    }
}

async fn process_rdp_data_for_session(
    rdp_data: &[u8],
    header: &Header,
    sessions: &Arc<RwLock<HashMap<Uuid, SessionInfo>>>,
    config_manager: &conf::ConfigHandleManager,
    ws_sender: &Arc<
        tokio::sync::Mutex<
            SplitSink<
                WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>,
                tungstenite::Message,
            >,
        >,
    >,
    active_proxies: &Arc<RwLock<HashMap<Uuid, tokio::task::JoinHandle<()>>>>,
    session_channels: &Arc<RwLock<HashMap<Uuid, (tokio::sync::mpsc::Sender<Vec<u8>>, Arc<Mutex<Receiver<Vec<u8>>>>)>>>,
) {
    // Check if we have session info for this session
    let sessions_read = sessions.read().await;
    if let Some(session_info) = sessions_read.get(&header.sid) {
        println!("> Found session {} in sessions map", header.sid);

        // Get or create per-session RDP data channel
        let (rdp_data_tx, rdp_data_rx) = {
            let mut channels = session_channels.write().await;
            if let Some((tx, rx)) = channels.get(&header.sid) {
                (tx.clone(), rx.clone())
            } else {
                // Create new channel for this session
                let (tx, rx) = tokio::sync::mpsc::channel::<Vec<u8>>(1024);
                let rx_arc = Arc::new(Mutex::new(rx));
                channels.insert(header.sid, (tx.clone(), rx_arc.clone()));
                (tx, rx_arc)
            }
        };

        // Check if RDP proxy is already running for this session
        let proxy_exists = {
            let proxies = active_proxies.read().await;
            proxies.contains_key(&header.sid)
        };

        // Start RDP proxy if not already started for this session
        if !proxy_exists {
            let config_clone = config_manager.conf.clone();
            let ws_sender_clone = ws_sender.clone();
            let session_info_clone = session_info.clone();
            let rdp_data_rx_clone = rdp_data_rx.clone();
            let active_proxies_clone = active_proxies.clone();
            let session_id = header.sid; // Copy the session ID to avoid lifetime issues

            let proxy_task = tokio::spawn(async move {
                match start_rdp_proxy_session(
                    session_info_clone,
                    ws_sender_clone,
                    rdp_data_rx_clone,
                    config_clone,
                )
                .await
                {
                    Ok(_) => println!("> RDP proxy session completed for session {}", session_id),
                    Err(e) => {
                        eprintln!("> RDP proxy session failed for session {}: {}", session_id, e)
                    }
                }
                
                // Clean up the proxy task from active_proxies when done
                {
                    let mut proxies = active_proxies_clone.write().await;
                    proxies.remove(&session_id);
                    println!("> Cleaned up RDP proxy task for session {}", session_id);
                }
            });

            // Store the proxy task for this session
            {
                let mut proxies = active_proxies.write().await;
                proxies.insert(header.sid, proxy_task);
                println!("> Started RDP proxy task for session {}", header.sid);
            }
        }

        // Forward the RDP data to the RDP proxy through the session-specific channel
        println!("> Forwarding RDP data to RDP proxy for session {}...", header.sid);
        if let Err(e) = rdp_data_tx.send(rdp_data.to_vec()).await {
            eprintln!("> Failed to forward RDP data to RDP proxy for session {}: {}", header.sid, e);
        }
    } else {
        println!("> Received RDP data for unknown session: {}", header.sid);
        // Debug: List all available sessions
        let sessions_read = sessions.read().await;
        println!(
            "> Available sessions: {:?}",
            sessions_read.keys().collect::<Vec<_>>()
        );
    }
}

impl WebSocket {
    pub fn new() -> anyhow::Result<Self> {
        let config_manager =
            conf::ConfigHandleManager::init().context("Failed to init config manager")?;

        let gateway_url = "ws://localhost:8080/ws";
        Ok(WebSocket {
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

        // > Handle incoming messages from gateway
        let ws_sender = std::sync::Arc::new(tokio::sync::Mutex::new(ws_sender));
        let ws_sender_clone = ws_sender.clone();
        let ws_receiver_clone = Arc::new(Mutex::new(ws_receiver));
        let config_manager_clone = config_manager.clone();
        // Create a shared sessions HashMap for multi-session support
        let sessions: Arc<RwLock<HashMap<Uuid, SessionInfo>>> =
            Arc::new(RwLock::new(HashMap::new()));
        let sessions_clone = sessions.clone();

        // We'll create per-session RDP data channels instead of a shared one

        // Store active RDP proxy tasks per session
        let active_proxies: Arc<RwLock<HashMap<Uuid, tokio::task::JoinHandle<()>>>> = 
            Arc::new(RwLock::new(HashMap::new()));
        let active_proxies_clone = active_proxies.clone();

        // Store per-session RDP data channels
        let session_channels: Arc<RwLock<HashMap<Uuid, (tokio::sync::mpsc::Sender<Vec<u8>>, Arc<Mutex<Receiver<Vec<u8>>>>)>>> = 
            Arc::new(RwLock::new(HashMap::new()));
        let session_channels_clone = session_channels.clone();

        let gateway_to_agent = tokio::spawn(async move {
            println!("> Starting to receive messages from gateway...");
            let mut ws_receiver = ws_receiver_clone.lock().await;

            while let Some(msg) = ws_receiver.next().await {
                match msg {
                    Ok(Message::Binary(data)) => {
                        // Try to decode as handshake or connection info first (with header)
                        if let Some((header, header_len)) = Header::decode(&data) {
                            if header_len <= data.len() {
                                let json_data = &data[header_len..];
                                if let Ok(message) =
                                    serde_json::from_slice::<serde_json::Value>(json_data)
                                {
                                    println!("> Received message for session: {}", header.sid);
                                    println!("> Message: {:?}", message);
                                    if let Some(message_type) = message.get("message_type") {
                                        if message_type == "session_started" {
                                            println!(
                                                "> Session {} started, waiting for connection info...",
                                                header.sid
                                            );
                                            started_session(
                                                header,
                                                ws_sender_clone.clone(),
                                                message,
                                                sessions.clone(),
                                            )
                                            .await;
                                            continue; // Skip to next message - don't process as RDP data
                                        }
                                    }
                                }
                            }
                        }

                        //  here, it's raw RDP data with header - we need to forward it to the RDP proxy
                        if let Some((header, header_len)) = Header::decode(&data) {
                            if header_len <= data.len() {
                                let rdp_data = &data[header_len..];
                                println!(
                                    "> Received RDP data for session: {} ({} bytes)",
                                    header.sid,
                                    rdp_data.len()
                                );
                                println!(
                                    "> RDP data (first 20 bytes): {:02x?}",
                                    &rdp_data[..std::cmp::min(20, rdp_data.len())]
                                );

                                // Process RDP data for the session
                                process_rdp_data_for_session(
                                    rdp_data,
                                    &header,
                                    &sessions_clone,
                                    &config_manager_clone,
                                    &ws_sender_clone,
                                    &active_proxies_clone,
                                    &session_channels_clone,
                                )
                                .await;
                            }
                        } else {
                            println!("> Received data without valid header, ignoring");
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

