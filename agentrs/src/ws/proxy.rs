use crate::conf::Conf;
use crate::rdp_proxy::RdpProxy;
use crate::session::Header;
use crate::ws::session::SessionInfo;
use crate::ws::stream::ChannelWebSocketStream;
use crate::ws::types::WsWriter;
use anyhow::Context;
use std::sync::Arc;
use tokio::net::TcpStream;
use tracing::{debug, error, info, instrument};

use futures::SinkExt;
use tokio::sync::Mutex;
use tokio_tungstenite::tungstenite::protocol::Message;

// Start a persistent RDP proxy session
#[instrument]
pub async fn start_rdp_proxy_session(
    session_info: SessionInfo,
    ws_sender: WsWriter,
    rdp_data_rx: Arc<Mutex<tokio::sync::mpsc::Receiver<Vec<u8>>>>,
    config: Arc<Conf>,
) -> anyhow::Result<()> {
    info!(
        "> Starting persistent RDP proxy for target: {}",
        session_info.target_address
    );
    debug!("> Using client address: {}", session_info.client_address);

    let server_target = session_info.target_address.clone();
    // Connect to target RDP server
    let target_addr = session_info
        .target_address
        .parse::<std::net::SocketAddr>()
        .context("Failed to parse target address")?;
    let server_stream = TcpStream::connect(target_addr)
        .await
        .context("Failed to connect to target RDP server")?;

    debug!(
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

    debug!(
        "> First RDP data (first 20 bytes): {:02x?}",
        &first_rdp_data[..std::cmp::min(20, first_rdp_data.len())]
    );

    // Create a custom stream that reads from the channel and writes to WebSocket
    // We need to create a separate channel for sending data back to the gateway
    let (response_tx, mut response_rx) = tokio::sync::mpsc::channel::<Vec<u8>>(1024);
    let client_side = ChannelWebSocketStream::new(rdp_data_rx, response_tx);

    // Create a task to forward responses back to the WebSocket
    let ws_sender_clone = ws_sender.clone();
    let session_id = session_info.session_id;
    tokio::spawn(async move {
        while let Some(data) = response_rx.recv().await {
            debug!(
                "> Forwarding {} bytes from RDP proxy to WebSocket",
                data.len()
            );

            // Frame the RDP response data with a Header
            let header_size = 20;
            let header = Header {
                sid: session_id,
                len: data.len() as u32,
                data_size: header_size,
            };
            let mut framed_data = Vec::with_capacity(header_size + data.len());
            framed_data.extend_from_slice(&header.encode());
            framed_data.extend_from_slice(&data);

            debug!(
                "> Framed RDP response (first 30 bytes): {:02x?}",
                &framed_data[..std::cmp::min(30, framed_data.len())]
            );

            let mut sender = ws_sender_clone.lock().await;
            if let Err(e) = sender.send(Message::Binary(framed_data.into())).await {
                error!("> Failed to send response to WebSocket: {}", e);
                break;
            }
            debug!("> Successfully sent framed RDP response to gateway");
        }
    });

    // Create RDP proxy with extracted credentials
    let proxy = RdpProxy::builder()
        .server_target(server_target)
        .client_stream(client_side)
        .server_stream(server_stream)
        .config(config)
        .creds(session_info.proxy_user.clone())
        .username(session_info.username.clone())
        .password(session_info.password.clone())
        .client_address(
            session_info
                .client_address
                .parse()
                .unwrap_or_else(|_| "127.0.0.1:0".parse().unwrap()),
        )
        .client_stream_leftover_bytes(bytes::BytesMut::from(first_rdp_data.as_slice()))
        .build();

    // Run the proxy
    info!("> Starting RDP proxy run...");
    info!("> WebSocket stream adapter created, starting RDP proxy...");
    match proxy.run().await {
        Ok(_) => {
            info!("> RDP proxy session completed successfully");
            Ok(())
        }
        Err(e) => {
            error!("> RDP proxy failed: {}", e);
            error!("> Error details: {:?}", e);
            Err(e.context("RDP proxy failed"))
        }
    }
}
