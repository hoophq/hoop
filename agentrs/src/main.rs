mod sessions;

use tokio::net::TcpStream;
use tokio::time::timeout;
use serde::{Deserialize, Serialize};
use tokio::io::{AsyncReadExt, AsyncWriteExt, Interest};
use bytes::BytesMut;
use std::collections::HashMap;
use futures::{SinkExt, StreamExt};
use tokio::sync::mpsc;
use uuid::Uuid;
use std::time::Duration;
use tokio_tungstenite::{connect_async, tungstenite::Message};

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    // Dev: ws URL (no TLS)
    // TODO: implement TLS support
    let url = std::env::var("GATEWAY_URL")
        .unwrap_or_else(|_| "ws://127.0.0.1:8080/ws".into());
    connect_to_gateway(url).await
}

// TODO: implement a proper session management
struct TcpConn {
    stream: TcpStream,
}

async fn send_framed(
    ws_tx: &mpsc::Sender<Message>,
    sid: Uuid,
    payload: &[u8],
) -> Result<(), mpsc::error::SendError<Message>> {
    let header = sessions::header::Header { sid, len: payload.len() as u32 }.encode();
    let mut framed = Vec::with_capacity(20 + payload.len());
    framed.extend_from_slice(&header);
    framed.extend_from_slice(payload);
    ws_tx.send(Message::Binary(framed.into())).await
}

#[derive(Debug, Deserialize, Serialize)]
#[serde(tag = "conn_type", rename_all = "snake_case")]
pub enum ConnectionDetails {
    Rdp {
        username: String,
        password: String,
        domain: Option<String>,
        port: u16,
    },
}

// hashmap of sessions being session HashMap<Uuid, ConnectionDetails>
#[derive(Debug, Deserialize, Serialize)]
struct SessionJson {
    sid: String,
    // stream: TcpStream,
    conn_details: ConnectionDetails,
    conn_type: String, // e.g. "rdp", "ssh", etc.
}
struct Session {
    sid: Uuid,
    conn_details: ConnectionDetails,
    // stream: TcpStream,
}

async fn connect_to_gateway(url: String) -> anyhow::Result<()> {
    let mut backoff = Duration::from_secs(1);
    let max_backoff = Duration::from_secs(30);
    let mut sessions_memory: HashMap<Uuid, Session> = HashMap::new();

    loop {
        match connect_async(&url).await {
            Ok((ws_stream, _resp)) => {
                println!("🔗 Connected to gateway at {url}");
                backoff = Duration::from_secs(1); // reset backoff on success

                // 1) One place that actually touches the websocket
                let (mut ws_sink, mut ws_src) = ws_stream.split();
                let (ws_tx, mut ws_rx) = mpsc::channel::<Message>(1024);

                // Task: serialize all outbound WS writes
                let _writer = tokio::spawn(async move {
                    while let Some(msg) = ws_rx.recv().await {
                        if let Err(e) = ws_sink.send(msg).await {
                            eprintln!("WS send error: {e}");
                            break;
                        }
                    }
                    let _ = ws_sink.close().await;
                });

                // 2) Session map: persistent TCP by SID
                let mut sessions: HashMap<Uuid, TcpConn> = HashMap::new();

                while let Some(msg) = ws_src.next().await {
                    match msg {
                        Ok(Message::Text(t)) => {
                            // Handle incoming text messages
                            let payload = serde_json::from_str::<SessionJson>(&t);
                            sessions_memory.insert(
                                Uuid::parse_str(&payload.as_ref().unwrap().sid).unwrap(),
                                Session {
                                    sid: Uuid::parse_str(&payload.as_ref().unwrap().sid).unwrap(),
                                    conn_details: payload.unwrap().conn_details,
                                }
                            );
                            // parse json
                            // extract message type
                            // either add a new session or do whatever should be done
                            // respond ok to gateway and then expect packages data
                            // as Binary
                            println!("📨 Received: {t}");
                            // Echo back for testing
                            //ws.send(Message::Text(format!("Echo: {t}").into())).await?;
                        }
                        Ok(Message::Binary(b)) => {

                            if b.len() < 20 {
                                eprintln!("WS inbound: too short: {}", b.len());
                                continue;
                            }
                            let Some((header, header_size)) = sessions::header::Header::decode(&b) else {
                                eprintln!("WS inbound: bad header");
                                continue;
                            };
                            let need = header_size + header.len as usize;
                            if need > b.len() {
                                eprintln!("WS inbound: truncated payload (need {need}, got {})", b.len());
                                continue;
                            }
                            let sid = header.sid;
                            let payload = &b[header_size..need];
                            //println!("📦 Session ID: {sid}, Payload length: {}", payload.len());

                            // get or open session TCP
                            let conn = if let Some(c) = sessions.get_mut(&sid) {
                                println!("Using existing TCP connection for SID {sid}");
                                c
                            } else {
                                // connect once per SID
                                //println!("Opening new TCP connection for SID {sid}");
                                let target = std::env::var("WINDOWS_TARGET").unwrap_or_else(|_| "192.168.0.161:3389".into());
                                match TcpStream::connect(target.clone()).await {
                                    Ok(stream) => {
                                        stream.set_nodelay(true).ok();
                                        sessions.insert(sid, TcpConn { stream });
                                        sessions.get_mut(&sid).unwrap()
                                    }
                                    Err(e) => {
                                        eprintln!("TCP connect to {target} failed: {e}");
                                        // optionally send an empty/err frame back
                                        continue;
                                    }
                                }
                            };
                            //println!("🔌 Connected TCP for SID {sid}");

                            // write payload
                            // if let Err(e) = conn.stream.write_all(payload).await {
                            //     eprintln!("TCP write error (sid {sid}): {e}");
                            //     sessions.remove(&sid);
                            //     continue;
                            // }
                            loop {
                                conn.stream.writable().await?;

                                match conn.stream.try_write(payload) {
                                    Ok(n) => {
                                        println!("Wrote {n} bytes to TCP (sid {sid})");
                                        break;
                                    }
                                    Err(ref e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                                        println!("⚠️  TCP write would block (sid {sid}), waiting...");
                                        tokio::time::sleep(Duration::from_millis(50)).await;
                                        continue; // retry writing
                                    }
                                    Err(e) => {
                                        eprintln!("⚠️  TCP write error (sid {sid}): {e}, closing session");
                                        //sessions.remove(&sid);
                                        continue;
                                    }
                                }
                            }
                            //println!("Flushing {} bytes to TCP (sid {sid})", payload.len());
                            //conn.stream.flush().await.ok();
                            //println!("Flushed {} bytes to TCP (sid {sid})", payload.len());

                            let idle = Duration::from_millis(50);
                            //let mut buf = Vec::new();
                            let mut buf = BytesMut::with_capacity(16 * 1024); // 16 KiB
                            //let mut buf = vec![0u8; 16 * 1024];
                            //println!("⏳ Reading from TCP (sid {sid})...");
                            //conn.stream.read_buf(&mut buf).await?;

                            //////////////////////////////////////////////////////////////////////
                            // THIS WORKS, NOW I NEED TO FIND A WAY TO KEEP READING EVEN THOUGH //
                            // THE WS READ IS 0, BECAUSE IT MIGHT BE WAITING FOR THE DATA.      //
                            // THE 1 MILLISECOND IDLE HACK MADE IT WORK WITH HIGH LATENCY,      //
                            // BUT AT LEAST I COULD MAKE IT WORK.                               //
                            // ///////////////////////////////////////////////////////////////////
                            loop {
                                //println!("⏳ Reading from TCP (sid {sid})...");
                                // TODO: this is a loop, so even tho the value might be 0,
                                // ::::: it doesn't mean the stream is closed, just that
                                // ::::: there's no data right now. We need to keep trying
                                // ::::: until we hit a timeout or something.
                                // ;;;;; we might be able remove the timeout
                                // ;;;;; and just read until EOF, but that would block
                                // ;;;;; the WS read loop or something
                                let ready = conn.stream.ready(Interest::READABLE | Interest::WRITABLE).await?;

                                if ready.is_readable() {
                                    println!("TCP stream is readable");
                                    let mut chunk = [0u8; 1024 * 16]; // 16 KiB chunk
                                    match conn.stream.try_read(&mut chunk) {
                                        Ok(0) => {
                                            println!("⚠️  TCP read returned 0 (sid {sid}), closing session");
                                            sessions.remove(&sid);
                                            break; // EOF
                                        }
                                        Ok(n) => {
                                            println!("Read {n} bytes from TCP (sid {sid})");
                                            buf.extend_from_slice(&chunk[..n]);
                                            if let Err(e) = send_framed(&ws_tx, sid, &buf).await {
                                                eprintln!("WS send channel closed: {e}");
                                                // break; // break the WS read loop; writer task will exit too
                                            }
                                            // reset buf for next read
                                            buf.clear();
                                        }
                                        Err(ref e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                                            println!("⚠️  TCP read would block (sid {sid}), waiting...");
                                            tokio::time::sleep(idle).await;
                                            continue; // retry reading
                                        }
                                        Err(e) => {
                                            println!("⚠️  TCP read error (sid {sid}): {e}, closing session");
                                            break;
                                        }
                                    }
                                }
                            }
                        }
                        Ok(Message::Ping(_p)) => {
                            //ws.send(Message::Pong(p)).await?;
                        }
                        Ok(Message::Pong(_)) => { /* ignore */ }
                        Ok(Message::Close(c)) => {
                            println!("❌ Connection closed: {c:?}");
                            break;
                        }
                        Ok(_) => {}
                        Err(e) => {
                            eprintln!("⚠️  WebSocket error: {e}");
                            break;
                        }
                    }
                }
                println!("🔄 Disconnected, will attempt to reconnect...");
            }
            Err(e) => {
                eprintln!("⚠️  Failed to connect: {e}");
            }
        }
        println!("⏳ Reconnecting in {} seconds...", backoff.as_secs());
        tokio::time::sleep(backoff).await;
        backoff = std::cmp::min(backoff * 2, max_backoff);
    }
}
