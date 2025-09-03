use crate::rdpproxy::rdpproxy::*;
use crate::tasks::tasks::*;
use anyhow::{Context, Result};
use async_trait::async_trait;
use bytes::BytesMut;
use ironrdp_pdu::pcb::PreconnectionBlob;
use std::net::IpAddr;
use std::time::Duration;
use std::{fmt, io};
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite};
use tokio::net::{TcpListener, TcpSocket, TcpStream};
use tokio::runtime::Runtime;
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

impl<S> Client<S>
where
    S: AsyncWrite + AsyncRead + Unpin + Send + Sync,
{
    pub async fn serve(self) -> anyhow::Result<()> {
        let Self {
            mut client_stream,
            client_addr,
        } = self;
        println!("Serving client from {}", client_addr);

        //let timeout = tokio::time::sleep(tokio::time::Duration::from_secs(10));

        let upstream_addr = ("10.211.55.5", 3389);

        // Connect upstream
        let server_stream = TcpStream::connect(upstream_addr)
            .await
            .with_context(|| format!("connecting to upstream {:?} failed", upstream_addr))?;

        // No PCB: pass an empty BytesMut
        let proxy = RdpProxy::builder()
            .client_stream(client_stream)
            .server_stream(server_stream)
            .client_stream_leftover_bytes(bytes::BytesMut::new())
            .build();

        // Do RDP (no TLS/NLA) handshake, then pipe
        proxy.run().await?;
        //let read_pcb_fut = read_pcb(&mut client_stream);

        //let (pcb, mut leftover_bytes) = tokio::select! {
        //    () = timeout => {
        //        println!("Timeout waiting for preconnection blob from {}", client_addr);
        //        return Ok(())
        //    }
        //    result = read_pcb_fut => {
        //        match result {
        //            Ok(result) => {
        //                println!("Read preconnection blob from {}", client_addr);
        //                result
        //            },
        //            Err(error) => {
        //                eprintln!("Error reading preconnection blob from {}: {}", client_addr, error);
        //                return Ok(())
        //            }
        //        }
        //    }
        //};

        //println!("Thread");
        //let token = pcb
        //    .v2_payload
        //    .as_deref()
        //    .context("V2 payload missing from RDP PCB")?;

        //let source_ip = client_addr.ip();

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

fn decode_pcb(buf: &[u8]) -> Result<Option<(PreconnectionBlob, usize)>, io::Error> {
    let mut cursor = ironrdp_core::ReadCursor::new(buf);

    match ironrdp_core::decode_cursor::<PreconnectionBlob>(&mut cursor) {
        Ok(pcb) => {
            let pdu_size = ironrdp_core::size(&pcb);
            let read_len = cursor.pos();

            // NOTE: sanity check (reporting the wrong number will corrupt the communication)
            if read_len != pdu_size {
                println!(
                    "Warning: inconsistent lengths when reading preconnection blob: read_len={}, pdu_size={}",
                    read_len, pdu_size
                );
            }

            Ok(Some((pcb, read_len)))
        }
        Err(e) if matches!(e.kind, ironrdp_core::DecodeErrorKind::NotEnoughBytes { .. }) => {
            println!("Not enough bytes to decode preconnection blob, waiting for more...");
            Ok(None)
        }
        Err(e) => Err(io::Error::new(io::ErrorKind::InvalidData, e)),
    }
}

/// Returns the decoded preconnection PDU and leftover bytes
pub async fn read_pcb(
    mut stream: impl AsyncRead + AsyncWrite + Unpin,
) -> io::Result<(PreconnectionBlob, BytesMut)> {
    let mut buf = BytesMut::with_capacity(1024);

    loop {
        let n_read = stream.read_buf(&mut buf).await?;
        println!("Read {} bytes from stream", n_read);

        if n_read == 0 {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "not enough bytes to decode preconnection PDU",
            ));
        }

        if let Some((pdu, read_len)) = decode_pcb(&buf)? {
            let leftover_bytes = buf.split_off(read_len);
            return Ok((pdu, leftover_bytes));
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

        let socket_addr = SocketAddr::new(IpAddr::V4(Ipv4Addr::new(127, 0, 0, 1)), 3389);
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

async fn handle_tcp_peer(stream: TcpStream, peer_addr: SocketAddr) -> anyhow::Result<()> {
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
