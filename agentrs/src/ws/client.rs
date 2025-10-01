use crate::{
    conf,
    tasks::*,
    ws::{
        rdp_message_processor::MessageProcessor,
        types::{ChannelMap, ProxyMap, SessionMap, WsWriter},
    },
};
use anyhow::Context;
use async_trait::async_trait;
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
    pub config_manager: conf::ConfigHandleManager,
    pub request: Request,
    pub reconnect_interval: Duration,
}

#[async_trait]
impl Task for WebSocket {
    type Output = anyhow::Result<()>;

    const NAME: &'static str = "agent";

    async fn run(self, mut shutdown_signal: ShutdownSignal) -> Self::Output {
        tokio::select! {
            result = self.run_with_reconnect() => result,
            _ = shutdown_signal.wait() => Ok(()),
        }
    }

    fn get_name(&self) -> &'static str {
        Self::NAME
    }
}

impl WebSocket {
    pub fn new() -> anyhow::Result<Self> {
        let config_manager =
            conf::ConfigHandleManager::init().context("Failed to init config manager")?;

        let gateway_url = std::env::var("GATEWAY_URL")
            .unwrap_or_else(|_| "ws://localhost:8009/api/ws".to_string());
        let mut request = gateway_url.into_client_request().unwrap();

        // Insert a custom header
        let token = config_manager.conf.token.clone().unwrap();
        request
            .headers_mut()
            .insert("HOOP_KEY", HeaderValue::from_str(token.as_str())?);

        Ok(WebSocket {
            request,
            config_manager,
            reconnect_interval: Duration::from_secs(5),
        })
    }

    async fn run_with_reconnect(self) -> anyhow::Result<()> {
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
                    error!("> Connection failed (attempt {}): {}", attempts, e);

                    tokio::time::sleep(self.reconnect_interval).await;
                    continue;
                }
            }
        }
    }

    async fn run(self) -> anyhow::Result<()> {
        let (ws_stream, _) = connect_async(self.request.clone()).await?;

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
