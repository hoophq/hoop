use crate::client::Client;
use crate::conf::{self, ConfigHandleManager};
use crate::tasks::tasks::*;
use anyhow::Context;
use async_trait::async_trait;
use std::net::IpAddr;
use std::sync::Arc;
use std::time::Duration;
use tokio::net::{TcpListener, TcpSocket, TcpStream};
use tokio::runtime::Runtime;

use std::net::Ipv4Addr;
use std::net::SocketAddr;
use tokio::runtime;
use url::Url;
use uuid::Uuid;

async fn build_tasks(listeners: Vec<ListenerUrls>) -> anyhow::Result<Tasks, anyhow::Error> {
    //println!("Initializing tasks...");
    let mut tasks = Tasks::new();

    // println!("Building tasks...");
    //println!("Gateway listener initialized");

    let g = match GatewayListener::init_and_start(ListenerKind::Tcp) {
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
    pub listeners: Vec<ListenerUrls>,
    pub state: GatewayState,
}

pub struct GatewayListener {
    pub kind: ListenerKind,
    pub listener: TcpListener,
    pub config_manager: conf::ConfigHandleManager,
}

impl GatewayListener {
    pub async fn run(self) -> anyhow::Result<()> {
        match self.kind {
            ListenerKind::Tcp => run_tcp_listener(self.listener, self.config_manager).await,
        }
    }

    pub fn init_and_start(kind: ListenerKind) -> anyhow::Result<Self> {
        let config_manager =
            conf::ConfigHandleManager::init().context("Failed to init config manager")?;

        let socket_addr = SocketAddr::new(IpAddr::V4(Ipv4Addr::new(0, 0, 0, 0)), 3389);
        let socket = TcpSocket::new_v4().context("Failed to create TCP socket")?;
        socket
            .bind(socket_addr)
            .context("Failed to bind TCP socker")?;

        let listener = socket.listen(64).context("Failed to bind TCP listener")?;
        let kind = match kind {
            ListenerKind::Tcp => ListenerKind::Tcp,
        };

        Ok(Self {
            kind,
            listener,
            config_manager,
        })
    }
}

async fn handle_tcp_peer(
    stream: TcpStream,
    peer_addr: SocketAddr,
    config: Arc<conf::Conf>,
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
                .config(config)
                .client_addr(peer_addr)
                .client_stream(stream)
                .build()
                .serve()
                .await?;
        }
    }

    Ok(())
}

async fn run_tcp_listener(
    listener: TcpListener,
    config_manager: ConfigHandleManager,
) -> anyhow::Result<()> {
    println!("Running TCP listener...");

    loop {
        match listener
            .accept()
            .await
            .context("failed to accept connection")
        {
            Ok((stream, peer_addr)) => {
                let config = config_manager.conf.clone();
                ChildTask::spawn(async move {
                    if let Err(e) = handle_tcp_peer(stream, peer_addr, config).await {
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
    pub fn new(listeners: Vec<ListenerUrls>) -> Self {
        Self {
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
