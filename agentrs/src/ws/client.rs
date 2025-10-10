use crate::{
    conf, tls,
    ws::{
        rdp_message_processor::MessageProcessor,
        types::{ChannelMap, ProxyMap, SessionMap, WsWriter},
    },
};
use anyhow::Context;
use axum::http::HeaderValue;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::RwLock;
use tracing::{debug, error, info};
use tungstenite::{client::IntoClientRequest, handshake::client::Request};

use futures::{SinkExt, StreamExt};
use tokio::sync::Mutex;
use tokio_tungstenite::{connect_async, tungstenite::protocol::Message};

#[derive(Clone)]
pub struct WebSocket {
    pub gateway_url: String,
    pub config_manager: conf::ConfigHandleManager,
    pub request: Request,
    pub reconnect_interval: Duration,
    pub max_attempts: u32,
}

fn build_websocket_url() -> String {
    let gateway_url = std::env::var("HOOP_GATEWAY_URL");
    // if is not set the gateway_url exit the program
    let gateway_url = match gateway_url {
        Ok(url) => url,
        Err(_) => {
            error!("HOOP_GATEWAY_URL environment variable is not set");
            std::process::exit(1);
        }
    };

    let gateway_url = match gateway_url.as_str() {
        url if url.starts_with("ws://") || url.starts_with("wss://") => url.to_string(),
        url if url.starts_with("http://") => {
            format!("ws://{}", url.trim_start_matches("http://"))
        }
        url if url.starts_with("https://") => {
            format!("wss://{}", url.trim_start_matches("https://"))
        }
        url => format!("ws://{}", url), // no scheme, default to ws://
    };

    let gateway_url = gateway_url.trim_end_matches('/');
    gateway_url.to_string()
}

impl WebSocket {
    pub fn new() -> anyhow::Result<Self> {
        let config_manager =
            conf::ConfigHandleManager::init().context("Failed to init config manager")?;

        let gateway_url = build_websocket_url();

        let ws_url = format!("{}/api/ws", gateway_url);
        debug!("WebSocket URL: {}", ws_url);

        let mut request = ws_url.into_client_request().unwrap();

        // Insert a custom header
        let token = config_manager.conf.token.clone().unwrap();
        request.headers_mut().insert(
            "User-Agent",
            HeaderValue::from_static("Hoop-Agent-Rust/0.1"),
        );
        request
            .headers_mut()
            .insert("HOOP_KEY", HeaderValue::from_str(token.as_str())?);

        Ok(WebSocket {
            gateway_url,
            request,
            config_manager,
            reconnect_interval: Duration::from_secs(5),
            max_attempts: 5,
        })
    }
    fn is_localhost(&self) -> bool {
        self.gateway_url.contains("localhost")
            || self.gateway_url.contains("127.0.0.1")
            || self.gateway_url.contains("::1")
            || self.gateway_url.contains("0.0.0.0")
    }
    fn is_tls_enabled(&self) -> bool {
        self.gateway_url.starts_with("wss://")
    }

    pub async fn run_with_reconnect(self) -> anyhow::Result<()> {
        let mut attempts = 0;
        loop {
            match self.clone().run().await {
                Ok(_) => {
                    debug!("> WebSocket connection closed gracefully");
                    return Ok(());
                }
                Err(e) if e.to_string().contains("401 Unauthorized") => {
                    error!("> Unauthorized: Invalid token provided. Please check your HOOP_KEY.");
                    return Err(e);
                }
                Err(e) => {
                    attempts += 1;
                    if attempts >= self.max_attempts {
                        error!(
                            "> Max reconnection attempts ({}) reached. Exiting.",
                            self.max_attempts
                        );
                        return Err(e);
                    }
                    error!("> Connection failed (attempt {}): {}", attempts, e);

                    tokio::time::sleep(self.reconnect_interval).await;
                    continue;
                }
            }
        }
    }

    async fn connect_locall_with_custom_tls(
        &self,
    ) -> anyhow::Result<(
        tokio_tungstenite::WebSocketStream<
            tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>,
        >,
        tungstenite::handshake::client::Response,
    )> {
        let url = url::Url::parse(&self.request.uri().to_string())?;

        let connector = match url.scheme() {
            "ws" => Some(tokio_tungstenite::Connector::Plain),
            "wss" => {
                // Create a TLS connector that accepts any certificate
                // This is to inside localhost we do not need to validade the certificate
                // if TLS is enable locally and the user is running make run-dev
                // inside the docker it is try to validate the authorithy

                let config = tokio_rustls::rustls::client::ClientConfig::builder()
                    .dangerous()
                    .with_custom_certificate_verifier(Arc::new(
                        tls::danger::NoCertificateVerification,
                    ))
                    .with_no_client_auth();

                Some(tokio_tungstenite::Connector::Rustls(Arc::new(config)))
            }
            other => {
                error!("Scheme {} is not supported! Use either ws or wss", other);
                return Err(anyhow::anyhow!("Unsupported scheme: {}", other));
            }
        };

        let (ws_stream, response) = tokio_tungstenite::connect_async_tls_with_config(
            self.request.clone(),
            None,
            false,
            connector,
        )
        .await?;

        Ok((ws_stream, response))
    }
    async fn connect(
        &self,
    ) -> anyhow::Result<(
        tokio_tungstenite::WebSocketStream<
            tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>,
        >,
        tungstenite::handshake::client::Response,
    )> {
        let connection_timeout = Duration::from_secs(30); // 30 second timeout
        let is_localhost = self.is_localhost();
        let tls_enabled = self.is_tls_enabled();

        if is_localhost || !tls_enabled {
            let (ws_stream, response) =
                tokio::time::timeout(connection_timeout, self.connect_locall_with_custom_tls())
                    .await
                    .context("WebSocket connection timeout")??;
            return Ok((ws_stream, response));
        }

        let (ws_stream, response) =
            tokio::time::timeout(connection_timeout, connect_async(self.request.clone()))
                .await
                .context("WebSocket connection timeout")??;
        return Ok((ws_stream, response));
    }

    async fn run(self) -> anyhow::Result<()> {
        let (ws_stream, _) = self.connect().await?;
        let (ws_sender, ws_receiver) = ws_stream.split();

        // Clone config manager and sessions for use in the async task
        let ws_sender = Arc::new(Mutex::new(ws_sender));
        let sessions: SessionMap = Arc::new(RwLock::new(HashMap::new()));
        // Store active RDP proxy tasks per session
        let active_proxies: ProxyMap = Arc::new(RwLock::new(HashMap::new()));
        let session_channels: ChannelMap = Arc::new(RwLock::new(HashMap::new()));

        let message_processor = MessageProcessor {
            ws_sender: ws_sender.clone(),
            sessions: sessions.clone(),
            active_proxies: active_proxies.clone(),
            session_channels: session_channels.clone(),
            config_manager: self.config_manager.clone(),
        };

        let processor_task =
            tokio::spawn(async move { message_processor.process_messages(ws_receiver).await });

        // Start heartbeat task in case connection stucked or deadlock
        let heartbeat_task = self.spawn_heartbeat_task(ws_sender.clone());

        let result = tokio::select! {
            result = processor_task => {
                match result {
                    Ok(Ok(())) => {
                        info!("> Message processor completed normally");
                        // This is a graceful closure, return Ok to exit reconnection loop
                        Ok(())
                    }
                    Ok(Err(e)) => {
                        error!("> Message processor error: {}", e);
                        // This is a connection error, return it to trigger reconnection
                        Err(anyhow::anyhow!(e))
                    }
                    Err(e) => {
                        error!("> Message processor task panicked: {}", e);
                        // Task panic indicates connection issues, return error to trigger reconnection
                        Err(anyhow::anyhow!("Message processor task panicked: {}", e))
                    }
                }
            }
            _ = heartbeat_task => {
                debug!("> Heartbeat task completed - connection likely lost");
                // Heartbeat task completion indicates connection loss, return error to trigger reconnection
                Err(anyhow::anyhow!("Heartbeat task completed - connection lost"))
            }
        };
        self.cleanup_resources(sessions, active_proxies, session_channels)
            .await;
        result
    }

    fn spawn_heartbeat_task(&self, ws_sender: WsWriter) -> tokio::task::JoinHandle<()> {
        tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(30));

            loop {
                interval.tick().await;

                let mut sender = ws_sender.lock().await;
                if sender.send(Message::Ping(vec![].into())).await.is_err() {
                    error!("> Failed to send heartbeat ping");
                    break;
                }
            }
        })
    }

    async fn cleanup_resources(
        &self,
        sessions: SessionMap,
        active_proxies: ProxyMap,
        session_channels: ChannelMap,
    ) {
        debug!("> Cleaning up resources...");

        // Cancel all active proxy tasks
        let mut proxies = active_proxies.write().await;
        for (session_id, handle) in proxies.drain() {
            handle.abort();
            debug!("> Cancelled proxy task for session {}", session_id);
        }

        // Clear sessions and channels
        sessions.write().await.clear();
        session_channels.write().await.clear();

        debug!("> Resource cleanup complete");
    }
}
