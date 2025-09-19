use crate::session::Header;
use crate::ws::proxy::start_rdp_proxy_session;
use crate::ws::session::SessionInfo;
use crate::ws::types::{ChannelMap, ProxyMap, SessionMap};
use crate::{conf, ws::types::WsWriter};
use std::sync::Arc;

use futures::{SinkExt};
use tokio::sync::Mutex;
use tokio_tungstenite::tungstenite::protocol::Message;

async fn started_session(
    header: Header,
    ws_sender: WsWriter,
    message: serde_json::Value,
    sessions: SessionMap,
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
    sessions: &SessionMap,
    config_manager: &conf::ConfigHandleManager,
    ws_sender: &WsWriter,
    active_proxies: &ProxyMap,
    session_channels: &ChannelMap,
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
                        eprintln!(
                            "> RDP proxy session failed for session {}: {}",
                            session_id, e
                        )
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
        println!(
            "> Forwarding RDP data to RDP proxy for session {}...",
            header.sid
        );
        if let Err(e) = rdp_data_tx.send(rdp_data.to_vec()).await {
            eprintln!(
                "> Failed to forward RDP data to RDP proxy for session {}: {}",
                header.sid, e
            );
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
