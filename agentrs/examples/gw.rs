use agentrs::session::Header;
// this is a test file for a gateway server that accepts TCP RDP connections and bridges them over WebSocket to the agent
// tis will be move and implemented in the ../../gateway/ folder in golang
use axum::{
    Router,
    extract::State,
    extract::ws::{Message, WebSocket, WebSocketUpgrade},
    response::IntoResponse,
    routing::get,
};
use futures::{SinkExt, StreamExt};
use ironrdp_pdu::nego::{ConnectionRequest, NegoRequestData};
use ironrdp_pdu::tpdu::{TpduCode, TpduHeader};
use ironrdp_pdu::x224;
use std::{net::SocketAddr, sync::Arc};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
    sync::{Notify, RwLock, mpsc},
};
use uuid::Uuid;

use ironrdp_core::{Decode, ReadCursor};
use ironrdp_pdu::tpkt::TpktHeader;
use std::io;
use tokio::io::AsyncRead;

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

        target_map.insert(
            "fake2".to_string(),
            (
                "chico".to_string(),
                "090994".to_string(),
                "10.211.55.7".to_string(),
            ),
        );
        Self {
            target_map: target_map,
        }
    }
}

#[derive(Clone)]
struct SessionInfo {
    session_id: Uuid,
    target_address: Option<String>,
    username: Option<String>,
    password: Option<String>,
    client_address: Option<String>,
    pub sender: mpsc::Sender<Vec<u8>>,
    pub credentials_received: Arc<tokio::sync::Notify>,
}

pub async fn read_first_tpkt<S: AsyncRead + Unpin>(client: &mut S) -> io::Result<Vec<u8>> {
    // Read 4-byte TPKT header
    let mut hdr = [0u8; TpktHeader::SIZE];
    client.read_exact(&mut hdr).await?;

    // Not TPKT (e.g., TLS/RDG)? Return what we have so upper layers can branch
    if hdr[0] != TpktHeader::VERSION {
        return Ok(hdr.to_vec());
    }

    // Parse header with your own decoder (endianness checked)
    let mut cur = ReadCursor::new(&hdr);
    let tpkt = TpktHeader::read(&mut cur).map_err(|e| {
        io::Error::new(
            io::ErrorKind::InvalidData,
            format!("TPKT parse error: {e:?}"),
        )
    })?;

    let total_len = tpkt.packet_length();
    if total_len < TpktHeader::SIZE {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid TPKT length",
        ));
    }

    // Read the remaining payload
    let body_len = total_len - TpktHeader::SIZE;
    let mut body = vec![0u8; body_len];
    client.read_exact(&mut body).await?;

    let mut pdu = Vec::with_capacity(total_len);
    pdu.extend_from_slice(&hdr);
    pdu.extend_from_slice(&body);
    Ok(pdu)
}

/// Extract "mstshash=…" (or msthash=…) from an X.224 ConnectionRequest inside a single TPKT.
pub async fn parse_mstsc_cookie_from_x224(first_pdu: &[u8]) -> Option<String> {
    if first_pdu.len() < TpktHeader::SIZE {
        return None;
    }
    // If not TPKT, bail (likely TLS/RDG)
    if first_pdu[0] != TpktHeader::VERSION {
        return None;
    }

    // Verify we have the whole TPKT
    let mut tpkt_cur = ReadCursor::new(first_pdu);
    let tpkt = TpktHeader::read(&mut tpkt_cur).ok()?;
    if first_pdu.len() != tpkt.packet_length() {
        // You must feed exactly one complete TPKT buffer here.
        return None;
    }

    // Optional: check TPDU header/code before full decode (helps with debugging)
    let payload = &first_pdu[TpktHeader::SIZE..];
    let mut tpdu_cur = ReadCursor::new(payload);
    let tpdu = TpduHeader::read(&mut tpdu_cur, &tpkt).ok()?;

    println!("TPDU code: {:?}", tpdu.code);
    println!("TPDU LI: {}", tpdu.li);
    if tpdu.code != TpduCode::CONNECTION_REQUEST {
        println!("Not a Connection Request");
        return None; // Not a Connection Request
    }

    // Decode X.224<ConnectionRequest> using your generic impls
    let mut cur = ReadCursor::new(first_pdu);
    let x224::X224(ConnectionRequest { nego_data, .. }) =
        <x224::X224<ConnectionRequest> as Decode>::decode(&mut cur).ok()?;
    //println!("Negotiation data: {:?}", nego_data);
    // In this API, the cookie is embedded in nego_data variants.
    // The exact enum variants depend on your IronRDP revision. Common ones:
    //   - NegoRequestData::Cookie(Cookie)             //
    //   - NegoRequestData::RoutingToken(Vec<u8>)      //
    //   //println!("Nego data: {:?}", nego_data);
    let cookies = match nego_data? {
        NegoRequestData::Cookie(c) => c, // Cookie struct
        _ => return None,
        // Add any other variants your enum has; default to None
    };
    //println!("Extracted cookie: {:?}", cookies);

    // Typical wire line: "Cookie: mstshash=user\r\n"

    Some(cookies.0)
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
        .route("/api/ws", get(ws_handler))
        .with_state(shared.clone());

    let http_listener = TcpListener::bind("0.0.0.0:8009").await.expect("bind 8009");
    println!("> WebSocket server listening on :8009 (path /ws)");

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

                        // Try to decode as ACK or credentials response first
                        if let Some(header) = Header::decode(&b) {
                            if header.data_size <= b.len() {
                                let json_data = &b[header.data_size..];
                                if let Ok(response) =
                                    serde_json::from_slice::<serde_json::Value>(json_data)
                                {
                                    // Check if it's an RDP started response
                                    if let Some(message_type) = response.get("message_type") {
                                        if message_type == "rdp_started" {
                                            println!(
                                                "> Received RDP started response for session: {}",
                                                header.sid
                                            );
                                            // Notify that RDP started is ready
                                            {
                                                let mut sessions = shared.sessions.write().await;
                                                if let Some(session) = sessions.get_mut(&header.sid)
                                                {
                                                    session.credentials_received.notify_one();
                                                }
                                            }
                                            continue;
                                        }
                                    }

                                    // Check if it's credentials response
                                }
                            }
                        }

                        // If not a control message, it's RDP data with header - forward to TCP
                        if let Some(header) = Header::decode(&b) {
                            if header.data_size <= b.len() {
                                let rdp_data = &b[header.data_size..];
                                let sessions = shared.sessions.read().await;
                                if let Some(session) = sessions.get(&header.sid) {
                                    println!(
                                        "WS -> TCP: {} bytes for session {}",
                                        rdp_data.len(),
                                        header.sid
                                    );
                                    if let Err(e) = session.sender.send(rdp_data.to_vec()).await {
                                        eprintln!(
                                            "Failed to forward data to TCP session {}: {}",
                                            header.sid, e
                                        );
                                    } else {
                                        println!(
                                            "> Successfully forwarded {} bytes to TCP session {}",
                                            rdp_data.len(),
                                            header.sid
                                        );
                                    }
                                } else {
                                    println!(
                                        "> No TCP session found for session ID: {}",
                                        header.sid
                                    );
                                }
                            }
                        } else {
                            println!("> Received data without valid header, ignoring");
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

    let first_rdp_data = read_first_tpkt(&mut tcp).await.unwrap();
    let extracted_creds = parse_mstsc_cookie_from_x224(&first_rdp_data).await.unwrap();

    println!("> Extracted creds: {}", extracted_creds);

    // Starting a session after reading headers
    let (target_address, username, password) = db
        .target_map
        .get(&extracted_creds)
        .map(|(user, pass, target)| (format!("{}:3389", target), user.clone(), pass.clone()))
        .unwrap();

    // Prepare a per-session channel for WS→TCP bytes
    let (ws_to_tcp_tx, mut ws_to_tcp_rx) = mpsc::channel::<Vec<u8>>(1024);

    //    // Register this session's receiver with the WS side
    {
        let mut sessions = shared.sessions.write().await;
        // Store session info but we dont know yet each target, username, password
        let session_info = SessionInfo {
            session_id,
            target_address: Some(target_address.clone()),
            username: Some(username.clone()),
            password: Some(password.clone()),
            client_address: Some(peer.to_string()),
            sender: ws_to_tcp_tx.clone(),
            credentials_received: Arc::new(Notify::new()),
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
    // After reading initial RDP packets and extracting creds, send session info to agent
    // Step 1: Send Headers Session info for the agent
    use serde_json::json;
    let handshake_info = json!({
        "session_id": session_id.to_string(),
        "client_address": peer.to_string(),
        "username": username,
        "password": password,
        "target_address": target_address,
        "proxy_user": extracted_creds,
        "message_type": "session_started",
        "protocol": "rdp"
    })
    .to_string()
    .into_bytes();

    let handshake_header = Header {
        sid: session_id,
        len: handshake_info.len() as u32,
        data_size: 20,
    };

    let mut handshake_framed = Vec::with_capacity(20 + handshake_info.len());
    handshake_framed.extend_from_slice(&handshake_header.encode());
    handshake_framed.extend_from_slice(&handshake_info);

    println!(
        "> Sending handshake to agent for session {}: {} bytes",
        session_id,
        handshake_info.len()
    );
    println!(
        "> Handshake data (first 50 bytes): {:02x?}",
        &handshake_info[..std::cmp::min(50, handshake_info.len())]
    );
    println!(
        "> Handshake framed data (first 30 bytes): {:02x?}",
        &handshake_framed[..std::cmp::min(30, handshake_framed.len())]
    );

    if let Err(e) = ws_sender
        .send(Message::Binary(handshake_framed.into()))
        .await
    {
        eprintln!("> Failed to send handshake to agent: {}", e);
        let _ = tcp.shutdown().await;
        {
            let mut sessions = shared.sessions.write().await;
            sessions.remove(&session_id);
        }
        return;
    }
    // Step 2: Wait for RDP started response from agent
    if !wait_for_rdp_started(&shared, session_id).await {
        eprintln!(
            "> No RDP started response received from agent; closing TCP {}",
            peer
        );
        let _ = tcp.shutdown().await;
        {
            let mut sessions = shared.sessions.write().await;
            sessions.remove(&session_id);
        }
        return;
    }

    // send rdp data with session headers (including the first packet we already read)
    //
    //    // Pump A: TCP -> WS (send RDP bytes with session header)
    let ws_sender_a = ws_sender.clone();
    let (mut tcp_reader, mut tcp_writer) = tcp.into_split();
    let a = tokio::spawn(async move {
        // First, send the RDP packet we already read
        println!("> Sending first RDP packet: {} bytes", first_rdp_data.len());
        println!(
            "> First RDP data (first 20 bytes): {:02x?}",
            &first_rdp_data[..std::cmp::min(20, first_rdp_data.len())]
        );
        let header = Header {
            sid: session_id,
            len: first_rdp_data.len() as u32,
            data_size: 20,
        };
        let mut framed_data = Vec::with_capacity(20 + first_rdp_data.len());
        framed_data.extend_from_slice(&header.encode());
        framed_data.extend_from_slice(&first_rdp_data);

        println!(
            "> Framed data (first 30 bytes): {:02x?}",
            &framed_data[..std::cmp::min(30, framed_data.len())]
        );

        if ws_sender_a
            .send(Message::Binary(framed_data.into()))
            .await
            .is_err()
        {
            eprintln!("> Failed to send first RDP packet");
            return;
        }
        println!("> Successfully sent first RDP packet to agent");

        // Then continue reading from TCP connection
        let mut buf = vec![0u8; 16 * 1024];
        loop {
            match tcp_reader.read(&mut buf).await {
                Ok(0) => break, // EOF
                Ok(n) => {
                    println!("> TCP -> WS: {} bytes for session {}", n, session_id);

                    // Create header for RDP data
                    let rdp_data = buf[..n].to_vec();
                    println!(
                        "> RDP data (first 20 bytes): {:02x?}",
                        &rdp_data[..std::cmp::min(20, rdp_data.len())]
                    );
                    let header = Header {
                        sid: session_id,
                        len: rdp_data.len() as u32,
                        data_size: 20,
                    };

                    // Frame the RDP data with header
                    let mut framed_data = Vec::with_capacity(20 + rdp_data.len());
                    framed_data.extend_from_slice(&header.encode());
                    framed_data.extend_from_slice(&rdp_data);

                    println!(
                        "> Framed data (first 30 bytes): {:02x?}",
                        &framed_data[..std::cmp::min(30, framed_data.len())]
                    );

                    if ws_sender_a
                        .send(Message::Binary(framed_data.into()))
                        .await
                        .is_err()
                    {
                        // WS gone
                        break;
                    }
                    println!("> Successfully sent RDP data to agent");
                }
                Err(e) => {
                    eprintln!("TCP read error: {e}");
                    break;
                }
            }
        }
    });
    //    // Pump B: WS -> TCP
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
}

// ---- Shared helpers ----
impl Shared {
    async fn ws_sender(&self) -> Option<mpsc::Sender<Message>> {
        self.ws_out_tx.read().await.clone()
    }
}

async fn wait_for_rdp_started(shared: &Shared, session_id: Uuid) -> bool {
    // Get the notify handle for this session
    let notify = {
        let sessions = shared.sessions.read().await;
        match sessions.get(&session_id) {
            Some(session) => Some(session.credentials_received.clone()),
            None => None,
        }
    };

    let notify = match notify {
        Some(notify) => notify,
        None => {
            println!("> Session {} not found", session_id);
            return false;
        }
    };

    // Wait for notification with timeout
    tokio::select! {
        _ = notify.notified() => {
            println!("> Received RDP started response for session {}", session_id);
            true
        }
        _ = tokio::time::sleep(tokio::time::Duration::from_secs(5)) => {
            println!("> Timeout waiting for RDP started response for session {}", session_id);
            false
        }
    }
}
