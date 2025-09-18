use crate::conf::Conf;
use crate::rdp::proxy::RdpProxy;
use crate::ws::session::SessionInfo;
use crate::ws::stream::ChannelWebSocketStream;
use anyhow::Context;
use std::sync::Arc;
use tokio::net::TcpStream;

use futures::SinkExt;
use futures::stream::SplitSink;
use tokio::sync::Mutex;
use tokio_tungstenite::tungstenite::protocol::Message;
use tokio_tungstenite::{MaybeTlsStream, WebSocketStream};

// Start a persistent RDP proxy session
pub async fn start_rdp_proxy_session(
    session_info: SessionInfo,
    ws_sender: Arc<Mutex<SplitSink<WebSocketStream<MaybeTlsStream<TcpStream>>, Message>>>,
    rdp_data_rx: Arc<Mutex<tokio::sync::mpsc::Receiver<Vec<u8>>>>,
    config: Arc<Conf>,
) -> anyhow::Result<()> {
    println!(
        "> Starting persistent RDP proxy for target: {}",
        session_info.target_address
    );
    println!("> Using client address: {}", session_info.client_address);

    // Connect to target RDP server
    let target_addr = session_info
        .target_address
        .parse::<std::net::SocketAddr>()
        .context("Failed to parse target address")?;
    let server_stream = TcpStream::connect(target_addr)
        .await
        .context("Failed to connect to target RDP server")?;

    println!(
        "> Connected to target RDP server: {}",
        session_info.target_address
    );

    // Extract credentials from the first RDP packet
    let mut rdp_data_rx_guard = rdp_data_rx.lock().await;
    let first_rdp_data = rdp_data_rx_guard
        .recv()
        .await
        .context("Failed to receive first RDP data")?;
    drop(rdp_data_rx_guard); // Release the lock

    println!(
        "> Received first RDP packet: {} bytes",
        first_rdp_data.len()
    );
    println!(
        "> First RDP data (first 20 bytes): {:02x?}",
        &first_rdp_data[..std::cmp::min(20, first_rdp_data.len())]
    );

    // Extract credentials from the first RDP packet
    let creds = match crate::client::parse_mstsc_cookie_from_x224(&first_rdp_data).await {
        Some(claims) => {
            println!("> Extracted credentials from RDP header: {}", claims);
            claims
        }
        None => {
            println!("> No credentials found in RDP header, using default");
            "fake".to_string()
        }
    };

    // Create a custom stream that reads from the channel and writes to WebSocket
    // We need to create a separate channel for sending data back to the gateway
    let (response_tx, mut response_rx) = tokio::sync::mpsc::channel::<Vec<u8>>(1024);
    let client_side = ChannelWebSocketStream::new(rdp_data_rx, response_tx);

    // Create a task to forward responses back to the WebSocket
    let ws_sender_clone = ws_sender.clone();
    tokio::spawn(async move {
        while let Some(data) = response_rx.recv().await {
            println!(
                "> Forwarding {} bytes from RDP proxy to WebSocket",
                data.len()
            );
            let mut sender = ws_sender_clone.lock().await;
            if let Err(e) = sender.send(Message::Binary(data.into())).await {
                eprintln!("> Failed to send response to WebSocket: {}", e);
                break;
            }
        }
    });

    // Create RDP proxy with extracted credentials
    let proxy = RdpProxy::builder()
        .client_stream(client_side)
        .server_stream(server_stream)
        .config(config)
        .creds(creds)
        .client_address(
            session_info
                .client_address
                .parse()
                .unwrap_or_else(|_| "127.0.0.1:0".parse().unwrap()),
        )
        .client_stream_leftover_bytes(bytes::BytesMut::from(first_rdp_data.as_slice()))
        .build();

    // Run the proxy
    println!("> Starting RDP proxy run...");
    println!("> WebSocket stream adapter created, starting RDP proxy...");
    match proxy.run().await {
        Ok(_) => {
            println!("> RDP proxy session completed successfully");
            Ok(())
        }
        Err(e) => {
            eprintln!("> RDP proxy failed: {}", e);
            eprintln!("> Error details: {:?}", e);
            Err(e.context("RDP proxy failed"))
        }
    }
}
