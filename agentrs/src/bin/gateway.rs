// Copyright (c) 2025, hoop.dev
// Author: Matheus Marsiglio (and maybe Sam Altman, indirectly)
// This is a simple dummy server to simulate hoop gateway to receive WebSocket connections
// from the agent. Most of it was made by ChatGPT, don't expect beautiful code here.
use std::{net::SocketAddr, sync::Arc};
use uuid::{Uuid, uuid};
use axum::{
    extract::State,
    extract::ws::{Message, WebSocket, WebSocketUpgrade},
    response::IntoResponse,
    routing::get,
    Router,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
    sync::{mpsc, RwLock},
};
use futures::{SinkExt, StreamExt};

#[derive(Clone, Default)]
struct Shared {
    /// Gateway → WS (send messages out to the agent WS).
    ws_out_tx: Arc<RwLock<Option<mpsc::Sender<Message>>>>,

    /// WS → current TCP session (binary payloads from agent to TCP peer).
    session_in_tx: Arc<RwLock<Option<mpsc::Sender<Vec<u8>>>>>,
}

#[tokio::main]
async fn main() {
    // Shared state between WS handler and TCP accept loop
    let shared = Shared::default();

    // ---- Axum for the agent's WS ----
    let app = Router::new()
        .route("/ws", get(ws_handler))
        .with_state(shared.clone());

    let http_listener = TcpListener::bind("0.0.0.0:8080").await.expect("bind 8080");
    println!("🛰️  WebSocket server listening on :8080 (path /ws)");

    // ---- TCP listener for client side (e.g., RDP) ----
    tokio::spawn(run_tcp_acceptor(shared.clone(), "0.0.0.0:3389"));

    axum::serve(http_listener, app).await.expect("server");
}

async fn ws_handler(State(shared): State<Shared>, ws: WebSocketUpgrade) -> impl IntoResponse {
    println!("🔗 WebSocket connection request");
    ws.on_upgrade(move |socket| handle_socket(shared, socket))
}

#[derive(Debug, Clone, Copy)]
struct Header {
    sid: Uuid,
    len: u32,
}

impl Header {
    fn encode(self) -> [u8; 20] {
        let mut buf = [0u8; 20];
        buf[..16].copy_from_slice(self.sid.as_bytes());
        buf[16..].copy_from_slice(&self.len.to_be_bytes());
        buf
    }
    fn decode(buf: &[u8]) -> Option<(Header, usize)> {
        // Check we have at least 20 bytes for UUID (16) + length (4)
        if buf.len() < 20 {
            return None;
        }

        // Read UUID bytes
        let uuid_bytes = &buf[..16];
        let sid = Uuid::from_slice(uuid_bytes).ok()?;

        // Read length (big-endian, network order)
        let len_bytes = &buf[16..20];
        let len = u32::from_be_bytes(len_bytes.try_into().ok()?);

        // Return the header + how many bytes we consumed
        Some((Header { sid, len }, 20))
    }

}

async fn handle_socket(shared: Shared, socket: WebSocket) {
    println!("✅ WebSocket upgraded");

    // Channel used by others to send messages out over this WS.
    let (tx_out, mut rx_out) = mpsc::channel::<Message>(1024);

    // Publish this WS sender handle (indirect) so TCP→WS can push bytes.
    {
        let mut guard = shared.ws_out_tx.write().await;
        *guard = Some(tx_out.clone());
    }

    // Split the WS into sink (tx) and stream (rx)
    let (mut ws_tx, mut ws_rx) = socket.split();
    // loops and prints a message every one second

    // Dummy SID for testing
    let dummy_sid: Uuid = uuid!("67e55044-10b1-426f-9247-bb680e5fe0c8");

    // Task 1: pump outbound messages (Gateway→WS)
    let outbound = tokio::spawn(async move {
        while let Some(msg) = rx_out.recv().await {
            match msg {
                Message::Binary(b) => {
                    // prints the length of the binary message
                    //println!(
                    //    "{} bytes: \n{}",
                    //    b.len(),
                    //    b.iter().map(|b| format!("{:02X}", b)).collect::<Vec<_>>().join(" ")
                    //);
                    println!("📤 WS outbound: session ID: {dummy_sid}, payload length: {}", b.len());
                    let header = Header {
                        sid: dummy_sid,
                        len: b.len() as u32,
                    }.encode();

                    let mut framed = Vec::with_capacity(20 + b.len());
                    framed.extend_from_slice(&header);
                    framed.extend_from_slice(&b);

                    if let Err(e) = ws_tx.send(Message::Binary(framed.into())).await {
                        eprintln!("WS send error: {e}");
                        break;
                    }
                }
                other => {
                    println!("📤 WS outbound: non-binary message: {other:?}");
                    // Non-binary messages pass through unchanged
                    if let Err(e) = ws_tx.send(other).await {
                        eprintln!("WS send error: {e}");
                        break;
                    }
                }
            }
        }
        let _ = ws_tx.close().await; // Best-effort close
    });

    // Task 2: receive inbound messages (WS→Gateway)
    let inbound = {
        let shared = shared.clone();
        tokio::spawn(async move {
            while let Some(Ok(msg)) = ws_rx.next().await {
                match msg {
                    Message::Binary(b) => {
                        // Expect: [sid(16) | len(4, BE) | payload]
                        // if b is exactly 20 bytes, it's a header-only with zero-length payload
                        // so we need to shut down the write side of the TCP session
                        if b.len() < 20 {
                            eprintln!("WS inbound: frame too short: {} bytes", b.len());
                            continue;
                        }

                        let Some((header, header_size)) = Header::decode(&b) else {
                            eprintln!("WS inbound: bad header");
                            continue;
                        };

                        let need = header_size + header.len as usize;
                        println!("📥 WS inbound: header parsed, need {need} bytes total");
                        if need > b.len() {
                            eprintln!("WS inbound: truncated payload (need {need}, got {})", b.len());
                            continue;
                        }

                        // Parse header
                        let sid = header.sid;

                        let payload = &b[header_size..need];
                        println!("📦 Inbound Session ID: {sid}, Payload length: {}", payload.len());

                        // (Optional) assert sid matches the dummy we used outbound
                        if sid != dummy_sid {
                            eprintln!("WS inbound: unexpected SID {sid} (expected {dummy_sid})");
                            // For now still forward; or `continue;` if you want strictness
                        }

                        // Route payload to the active TCP session
                        if let Some(tx) = shared.session_in_sender().await {
                            println!("📥 WS inbound: forwarding {sid} payload to TCP session");
                            // Ignore if TCP side is gone
                            let _ = tx.send(payload.to_vec()).await;
                        } else {
                            eprintln!("WS inbound: no active TCP session");
                        }
                    }
                    Message::Text(_) => { /* ignore; binary-only protocol */ }
                    Message::Ping(p) => {
                        if let Some(tx) = shared.ws_sender().await {
                            let _ = tx.send(Message::Pong(p)).await;
                        }
                    }
                    Message::Pong(_) => {}
                    Message::Close(_) => break,
                }
            }
        })
    };

    let _ = inbound.await;
    let _ = outbound.await;

    // Drop published WS sender on exit
    {
        let mut guard = shared.ws_out_tx.write().await;
        *guard = None;
    }
    println!("🔌 WebSocket closed");
}

/// Accepts a single TCP connection at a time and bridges it over the active WS.
/// If no WS is connected, the TCP connection is rejected after a short read.
async fn run_tcp_acceptor(shared: Shared, bind: &str) {
    let listener = TcpListener::bind(bind).await.expect("bind TCP port");
    println!("🔉 TCP listener ready on {bind}");

    loop {
        match listener.accept().await {
            Ok((stream, peer)) => {
                println!("📥 TCP accepted from {peer}");
                tokio::spawn(handle_tcp_client(shared.clone(), stream, peer));
            }
            Err(e) => {
                eprintln!("TCP accept error: {e}");
            }
        }
    }
}

async fn handle_tcp_client(shared: Shared, mut tcp: TcpStream, peer: SocketAddr) {
    // Prepare a per-session channel for WS→TCP bytes
    let (ws_to_tcp_tx, mut ws_to_tcp_rx) = mpsc::channel::<Vec<u8>>(1024);

    // Register this session's receiver with the WS side
    shared.set_session_in_sender(Some(ws_to_tcp_tx)).await;

    // Ensure we have a WS sender to push TCP→WS
    let ws_sender = match shared.ws_sender().await {
        Some(tx) => tx,
        None => {
            eprintln!("❌ No WS connected; closing TCP {peer}");
            let _ = tcp.shutdown().await;
            // Clear session slot
            shared.set_session_in_sender(None).await;
            return;
        }
    };

    // Pump A: TCP -> WS
    let ws_sender_a = ws_sender.clone();
    //let mut tcp_reader = tcp.try_clone().expect("clone tcp for read");
    let (mut tcp_reader, mut tcp_writer) = tcp.into_split();
    let a = tokio::spawn(async move {
        let mut buf = vec![0u8; 16 * 1024];
        loop {
            match tcp_reader.read(&mut buf).await {
                Ok(0) => break, // EOF
                Ok(n) => {
                    if ws_sender_a.send(Message::Binary(buf[..n].to_vec().into())).await.is_err() {
                        // WS gone
                        break;
                    }
                }
                Err(e) => {
                    eprintln!("TCP read error: {e}");
                    break;
                }
            }
        }
    });

    // Pump B: WS -> TCP
    let b = tokio::spawn(async move {
        while let Some(chunk) = ws_to_tcp_rx.recv().await {
            if let Err(e) = tcp_writer.write_all(&chunk).await {
                eprintln!("TCP write error: {e}");
                break;
            }
        }
        let _ = tcp_writer.shutdown().await;
    });

    let _ = a.await;
    let _ = b.await;

    // Clear session slot
    shared.set_session_in_sender(None).await;
    println!("📤 TCP {peer} closed");
}

// ---- Shared helpers ----
impl Shared {
    async fn ws_sender(&self) -> Option<mpsc::Sender<Message>> {
        self.ws_out_tx.read().await.clone()
    }

    async fn session_in_sender(&self) -> Option<mpsc::Sender<Vec<u8>>> {
        self.session_in_tx.read().await.clone()
    }

    async fn set_session_in_sender(&self, s: Option<mpsc::Sender<Vec<u8>>>) {
        let mut guard = self.session_in_tx.write().await;
        *guard = s;
    }
}
