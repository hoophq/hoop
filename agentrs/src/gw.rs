use axum::{
    Router,
    extract::State,
    extract::ws::{Message, WebSocket, WebSocketUpgrade},
    response::IntoResponse,
    routing::get,
};
use futures::{SinkExt, StreamExt};
use std::{net::SocketAddr, sync::Arc};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
    sync::{RwLock, mpsc},
};
use uuid::{Uuid, uuid};
mod protocol;
mod session;
use session::Header;

#[derive(Clone)]
struct DatabaseMemory {
    pub target_map: std::collections::HashMap<String, (String, String, String)>, // target_address -> (username, password, target)
}

impl DatabaseMemory {
    pub fn new() -> Self {
        let mut target_map = std::collections::HashMap::<String, (String, String, String)>::new();
        target_map.insert(
            "fake".to_string(),
            (
                "chico".to_string(),
                "090994".to_string(),
                "10.211.55.6".to_string(),
            ),
        );
        Self {
            target_map: target_map,
        }
    }
}

#[derive(Clone)]
struct SessionInfo {
    pub session_id: Uuid,
    pub target_address: Option<String>,
    pub username: Option<String>,
    pub password: Option<String>,
    pub client_address: Option<String>,
    pub sender: mpsc::Sender<Vec<u8>>,
}

#[derive(Clone, Default)]
struct Shared {
    /// Gateway → WS (send messages out to the agent WS).
    ws_out_tx: Arc<RwLock<Option<mpsc::Sender<Message>>>>,

    /// WS → TCP sessions (binary payloads from agent to TCP peers).
    /// Maps session ID to the channel for that specific TCP session.
    sessions: Arc<RwLock<std::collections::HashMap<Uuid, SessionInfo>>>,
}

#[tokio::main]
async fn main() {
    let shared = Shared::default();

    let db = DatabaseMemory::new();
    let app = Router::new()
        .route("/ws", get(ws_handler))
        .with_state(shared.clone());

    let http_listener = TcpListener::bind("0.0.0.0:8080").await.expect("bind 8080");
    println!("> WebSocket server listening on :8080 (path /ws)");

    tokio::spawn(run_tcp_acceptor(shared.clone(), "0.0.0.0:3389", db));

    axum::serve(http_listener, app).await.expect("server");
}

async fn ws_handler(State(shared): State<Shared>, ws: WebSocketUpgrade) -> impl IntoResponse {
    println!("> WebSocket connection request");
    ws.on_upgrade(move |socket| handle_socket(shared, socket))
}

async fn handle_socket(shared: Shared, socket: WebSocket) {
    println!("> WebSocket upgraded");
    let (tx_out, mut rx_out) = mpsc::channel::<Message>(1024);

    {
        let mut guard = shared.ws_out_tx.write().await;
        *guard = Some(tx_out.clone());
    }

    let (mut ws_tx, mut ws_rx) = socket.split();

    // Task 1: pump outbound messages (Gateway→WS)
    // first connection
    let outbound = tokio::spawn(async move {
        while let Some(msg) = rx_out.recv().await {
            match &msg {
                Message::Binary(data) => {
                    println!("> WS outbound: sending {} bytes to agent", data.len());
                }
                other => {
                    println!("> WS outbound: sending {:?} to agent", other);
                }
            }
            if let Err(e) = ws_tx.send(msg).await {
                eprintln!("WS send error: {e}");
                break;
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
                        println!("> WS inbound: received {} bytes from agent", b.len());
                        println!(
                            "> WS inbound data (first 20 bytes): {:02x?}",
                            &b[..std::cmp::min(20, b.len())]
                        );

                        // Try to find a session to forward this data to
                        // For now, we'll forward to the first available session
                        // In a real implementation, you'd need to match by session ID
                        let sessions = shared.sessions.read().await;
                        //TODO need to get the session ID from the header

                        if let Some((session_id, session)) = sessions.iter().next() {
                            println!("WS -> TCP: {} bytes for session {}", b.len(), session_id);
                            if let Err(e) = session.sender.send(b.to_vec()).await {
                                eprintln!(
                                    "Failed to forward data to TCP session {}: {}",
                                    session_id, e
                                );
                            } else {
                                println!(
                                    "> Successfully forwarded {} bytes to TCP session {}",
                                    b.len(),
                                    session_id
                                );
                            }
                        } else {
                            println!(">  No TCP sessions available to forward data to");
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
    println!("> WebSocket closed");
}

/// Accepts a single TCP connection at a time and bridges it over the active WS.
/// If no WS is connected, the TCP connection is rejected after a short read.
async fn run_tcp_acceptor(shared: Shared, bind: &str, db: DatabaseMemory) {
    let listener = TcpListener::bind(bind).await.expect("bind TCP port");
    println!("> TCP listener ready on {bind}");

    loop {
        match listener.accept().await {
            Ok((stream, peer)) => {
                println!("> TCP accepted from {peer}");
                tokio::spawn(handle_tcp_client(shared.clone(), stream, peer, db.clone()));
            }
            Err(e) => {
                eprintln!("TCP accept error: {e}");
            }
        }
    }
}

async fn handle_tcp_client(
    shared: Shared,
    mut tcp: TcpStream,
    peer: SocketAddr,
    db: DatabaseMemory,
) {
    // Generate a unique session ID for this TCP connection
    let session_id = uuid::Uuid::new_v4();
    println!(
        "> Generated session ID: {} for TCP connection from {}",
        session_id, peer
    );

    // Prepare a per-session channel for WS→TCP bytes
    let (ws_to_tcp_tx, mut ws_to_tcp_rx) = mpsc::channel::<Vec<u8>>(1024);

    // Register this session's receiver with the WS side
    {
        let mut sessions = shared.sessions.write().await;
        // Store session info but we dont know yet each target, username, password
        let session_info = SessionInfo {
            session_id,
            target_address: None,
            username: None,
            password: None,
            client_address: Some(peer.to_string()),
            sender: ws_to_tcp_tx.clone(),
        };

        sessions.insert(session_id, session_info);
    }

    // Ensure we have a WS sender to push TCP→WS
    let ws_sender = match shared.ws_sender().await {
        Some(tx) => tx,
        None => {
            eprintln!("> No WS connected; closing TCP {peer}");
            let _ = tcp.shutdown().await;
            // Clear session slot
            {
                let mut sessions = shared.sessions.write().await;
                sessions.remove(&session_id);
            }
            return;
        }
    };

    // Send session info to agent with connection details
    let connection_info = create_connection_info(session_id, peer);
    let header = Header {
        sid: session_id,
        len: connection_info.len() as u32,
    };

    let mut framed = Vec::with_capacity(20 + connection_info.len());
    framed.extend_from_slice(&header.encode());
    framed.extend_from_slice(&connection_info);

    println!(
        "> Sending connection info to agent for session {}: {} bytes",
        session_id,
        connection_info.len()
    );
    if let Err(e) = ws_sender.send(Message::Binary(framed.into())).await {
        eprintln!("> Failed to send connection info to agent: {}", e);
        let _ = tcp.shutdown().await;
        {
            let mut sessions = shared.sessions.write().await;
            sessions.remove(&session_id);
        }
        return;
    }

    // Pump A: TCP -> WS (send raw RDP bytes without headers)
    let ws_sender_a = ws_sender.clone();
    let (mut tcp_reader, mut tcp_writer) = tcp.into_split();
    let a = tokio::spawn(async move {
        let mut buf = vec![0u8; 16 * 1024];
        loop {
            match tcp_reader.read(&mut buf).await {
                Ok(0) => break, // EOF
                Ok(n) => {
                    println!("> TCP -> WS: {} bytes for session {}", n, session_id);

                    // Send raw RDP bytes directly without headers
                    if ws_sender_a
                        .send(Message::Binary(buf[..n].to_vec().into()))
                        .await
                        .is_err()
                    {
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
            println!(
                "WS -> TCP: {} bytes for session {}",
                chunk.len(),
                session_id
            );
            println!(
                "WS -> TCP data (first 20 bytes): {:02x?}",
                &chunk[..std::cmp::min(20, chunk.len())]
            );
            if let Err(e) = tcp_writer.write_all(&chunk).await {
                eprintln!("TCP write error: {e}");
                break;
            } else {
                println!(
                    "Successfully wrote {} bytes to TCP client for session {}",
                    chunk.len(),
                    session_id
                );
            }
        }
        let _ = tcp_writer.shutdown().await;
    });

    let _ = a.await;
    let _ = b.await;

    // Clear session slot
    {
        let mut sessions = shared.sessions.write().await;
        sessions.remove(&session_id);
    }
    println!("TCP session {} closed", session_id);
}

fn create_connection_info(session_id: uuid::Uuid, peer: SocketAddr) -> Vec<u8> {
    // Create a JSON structure with connection info
    use serde_json::json;

    let info = json!({
        "session_id": session_id.to_string(),
        "client_address": peer.to_string(),
        "timestamp": chrono::Utc::now().to_rfc3339(),
        "protocol": "rdp"
    });

    info.to_string().into_bytes()
}

// ---- Shared helpers ----
impl Shared {
    async fn ws_sender(&self) -> Option<mpsc::Sender<Message>> {
        self.ws_out_tx.read().await.clone()
    }
}
