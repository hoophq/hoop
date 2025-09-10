use crate::rdpproxy::rdpproxy::*;
use crate::tasks::tasks::*;
use crate::token::Claims;
use anyhow::{Context, Result};
use async_trait::async_trait;
use bytes::BytesMut;
use ironrdp_core::{Decode, ReadCursor};
use ironrdp_pdu::nego::{ConnectionRequest, NegoRequestData};
use ironrdp_pdu::pcb::PreconnectionBlob;
use ironrdp_pdu::tpkt::TpktHeader;

use ironrdp_pdu::tpdu::{TpduCode, TpduHeader};
use ironrdp_pdu::x224;
use std::net::IpAddr;
use std::time::Duration;
use std::{fmt, io};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite};
use tokio::net::{TcpListener, TcpSocket, TcpStream};
use tokio::runtime::Runtime;
use tokio::time::timeout;
use typed_builder::TypedBuilder;

use std::net::Ipv4Addr;
use std::net::SocketAddr;
use tokio::{runtime, sync::mpsc};
use url::Url;
use uuid::Uuid;

#[derive(TypedBuilder)]
pub struct Client<S> {
    client_addr: SocketAddr,
    client_stream: S,
}

async fn build_tasks(listeners: Vec<ListenerUrls>) -> anyhow::Result<Tasks, anyhow::Error> {
    println!("Initializing tasks...");
    let mut tasks = Tasks::new();

    println!("Building tasks...");
    println!("Gateway listener initialized");

    let g =
        match GatewayListener::init_and_start(listeners[0].internal_url.clone(), ListenerKind::Tcp)
        {
            Ok(g) => g,
            Err(e) => {
                return Err(anyhow::anyhow!(
                    "Failed to initialize gateway listener: {}",
                    e
                ));
            }
        };
    tasks.register(g);
    Ok(tasks)
}

/// Read exactly one TPKT PDU from the stream and return the raw bytes.
/// The buffer length will equal the TPKT length field.
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
pub async fn parse_mstsc_cookie_from_x224(first_pdu: &[u8]) -> Option<Claims> {
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

    // Typical wire line: "Cookie: mstshash=10.211.55.5\r\n"
    let token: &str = &cookies.0;
    let parts = Claims::from_token(token).await?;

    Some(parts)
}

impl<S> Client<S>
where
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync + 'static,
{
    pub async fn serve(self) -> anyhow::Result<()> {
        let Self {
            mut client_stream,
            client_addr,
        } = self;
        println!("Serving client from {}", client_addr);
        // 1) Peek the first client PDU (TPKT/X.224)
        let first_pdu = read_first_tpkt(&mut client_stream).await?;
        // println!("First PDU length: {}", first_pdu.len());
        // //
        let claims = parse_mstsc_cookie_from_x224(&first_pdu).await;
        //println!("Claims from token: {:?}", claims);

        let ip = match claims.clone() {
            Some(c) => format!("{}", c.server_addrs),
            None => {
                return Err(anyhow::anyhow!(
                    "Failed to extract server address from client cookie"
                ));
            }
        };

        let strip: &str = &ip.trim();

        let upstream_addr: SocketAddr = format!("{}:{}", strip, 3389)
            .parse()
            .map_err(|e| anyhow::anyhow!("bad upstream addr '{}': {}", strip, e))?;

        println!("Connecting to upstream RDP server at {:?}", upstream_addr);

        // Connect upstream
        let server_stream = timeout(Duration::from_secs(5), TcpStream::connect(upstream_addr))
            .await
            .map_err(|_| anyhow::anyhow!("connect timeout to {}", upstream_addr))??;

        // No PCB: pass an empty BytesMut
        let proxy = RdpProxy::builder()
            .client_stream(client_stream)
            .server_stream(server_stream)
            .claims(claims)
            .client_stream_leftover_bytes(bytes::BytesMut::from(first_pdu.as_slice()))
            .build();

        // Do RDP (no TLS/NLA) handshake, then pipe
        proxy.run().await?;

        Ok(())
    }
}

#[async_trait]
impl Task for GatewayListener {
    type Output = anyhow::Result<()>;

    const NAME: &'static str = "gateway listener";

    async fn run(self, mut shutdown_signal: ShutdownSignal) -> Self::Output {
        tokio::select! {
            result = self.run() => result,
            _ = shutdown_signal.wait() => Ok(()),
        }
    }
}

#[derive(Clone)]
pub struct ListenerUrls {
    pub internal_url: Url,
}

impl ListenerUrls {
    pub fn new(internal_url: Url) -> Self {
        Self { internal_url }
    }
}
#[derive(Clone)]
pub enum ListenerKind {
    Tcp,
}

pub struct GatewayService {
    pub id: Uuid,
    pub listeners: Vec<ListenerUrls>,
    pub state: GatewayState,
}

pub struct GatewayListener {
    pub id: Uuid,
    pub kind: ListenerKind,
    pub ip: String,
    pub port: u16,
    pub listener: TcpListener,
}

impl GatewayListener {
    pub async fn run(self) -> anyhow::Result<()> {
        match self.kind {
            ListenerKind::Tcp => run_tcp_listener(self.listener).await,
        }
    }

    pub fn init_and_start(url: Url, kind: ListenerKind) -> anyhow::Result<Self> {
        let ip = match url.host_str() {
            Some(ip) => String::from(ip),
            None => "".to_string(),
        };

        let port = match url.port_or_known_default() {
            Some(port) => port,
            None => 0,
        };

        if ip == "" || port == 0 {
            return Err(anyhow::anyhow!("Invalid URL: {}", url));
        }

        let socket_addr = SocketAddr::new(IpAddr::V4(Ipv4Addr::new(0, 0, 0, 0)), 3389);
        let socket = TcpSocket::new_v4().context("Failed to create TCP socket")?;
        socket
            .bind(socket_addr)
            .context("Failed to bind TCP socker")?;

        let listener = socket.listen(64).context("Failed to bind TCP listener")?;
        let kind = match kind {
            ListenerKind::Tcp => ListenerKind::Tcp,
        };

        println!("Listening on {}:{}", ip, port);
        Ok(Self {
            id: Uuid::new_v4(),
            kind,
            port,
            ip,
            listener,
        })
    }
}

async fn handle_tcp_peer(
    stream: TcpStream,
    peer_addr: SocketAddr,
    //   servera_addr: (&'static str, u16),
) -> anyhow::Result<()> {
    println!("Accepted connection from {}", peer_addr);
    // Handle the TCP connection here
    if let Err(e) = stream.set_nodelay(true) {
        eprint!("Failed to set nodelay: {}", e);
    }

    let mut peeked = [0; 4];

    let n_read = stream
        .peek(&mut peeked)
        .await
        .context("couldn't peek four first bytes")?;

    match &peeked[..n_read] {
        [b'J', b'E', b'T', b'\0'] => anyhow::bail!("not yet supported"),
        [b'J', b'M', b'U', b'X'] => anyhow::bail!("not yet supported"),
        _ => {
            Client::builder()
                .client_addr(peer_addr)
                .client_stream(stream)
                .build()
                .serve()
                .await?;
        }
    }

    Ok(())
}

async fn run_tcp_listener(listener: TcpListener) -> anyhow::Result<()> {
    println!("Running TCP listener...");

    loop {
        match listener
            .accept()
            .await
            .context("failed to accept connection")
        {
            Ok((stream, peer_addr)) => {
                ChildTask::spawn(async move {
                    if let Err(e) = handle_tcp_peer(stream, peer_addr).await {
                        eprintln!("Error handling TCP peer {}: {}", peer_addr, e);
                    }
                })
                .detach();
            }
            Err(e) => eprintln!("Failed to accept connection: {}", e),
        }
    }
}

enum GatewayState {
    Stopped,
    Running {
        shutdown_handle: ShutdownHandle,
        runtime: Runtime,
    },
}
impl GatewayService {
    pub fn new(id: Uuid, listeners: Vec<ListenerUrls>) -> Self {
        Self {
            id,
            listeners,
            state: GatewayState::Stopped,
        }
    }

    pub fn start(&mut self) -> anyhow::Result<()> {
        let runtime = runtime::Builder::new_multi_thread()
            .enable_all()
            .build()
            .expect("failed to create runtime");
        println!("Runtime created");

        let tasks = runtime.block_on(build_tasks(self.listeners.clone()))?;

        println!("Tasks created");

        let mut join_all = futures::future::select_all(
            tasks.inner.into_iter().map(|child| Box::pin(child.join())),
        );

        runtime.spawn(async {
            loop {
                let (result, _, rest) = join_all.await;

                match result {
                    Ok(Ok(())) => println!("A task terminated gracefully"),
                    Ok(Err(error)) => eprintln!("A task failed"),
                    Err(error) => eprintln!("Something went very wrong with a task"),
                }

                if rest.is_empty() {
                    break;
                } else {
                    join_all = futures::future::select_all(rest);
                }
            }
        });

        self.state = GatewayState::Running {
            shutdown_handle: tasks.shutdown_handle,
            runtime,
        };
        Ok(())
    }

    pub fn stop(&mut self) {
        match std::mem::replace(&mut self.state, GatewayState::Stopped) {
            GatewayState::Stopped => {
                println!("Gateway service is already stopped");
            }
            GatewayState::Running {
                shutdown_handle,
                runtime,
            } => {
                println!("Stopping gateway service...");

                // Send shutdown signals to all tasks
                shutdown_handle.signal();

                runtime.block_on(async move {
                    const MAX_COUNT: usize = 3;
                    let mut count = 0;

                    loop {
                        tokio::select! {
                            _ = shutdown_handle.all_closed() => {
                                println!("All tasks have terminated gracefully");
                                break;
                            }
                            _ = tokio::time::sleep(Duration::from_secs(10)) => {
                                count += 1;

                                if count >= MAX_COUNT {
                                    eprintln!("Some tasks are not terminating, forcing shutdown");
                                    break;
                                } else {
                                    eprintln!("Waiting for tasks to terminate... (attempt {}/{})", count, MAX_COUNT);
                                }
                            }
                        }
                    }
                });

                // Wait for 1 more second before forcefully shutting down the runtime
                runtime.shutdown_timeout(Duration::from_secs(1));

                self.state = GatewayState::Stopped;
            }
        }
    }
}
