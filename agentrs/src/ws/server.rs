use crate::{
    conf,
    tasks::tasks::*,
    ws::{
        rdp_message_processor::MessageProcessor,
        types::{ChannelMap, ProxyMap, SessionMap, WsWriter},
    },
};
use anyhow::Context;
use async_trait::async_trait;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::RwLock;

use futures::{SinkExt, StreamExt};
use tokio::sync::Mutex;
use tokio_tungstenite::{connect_async, tungstenite::protocol::Message};

#[derive(Clone)]
pub struct WebSocket {
    pub config_manager: conf::ConfigHandleManager,
    pub gateway_url: String,
    pub reconnect_interval: Duration,
    pub max_reconnection_attempts: usize,
}

#[async_trait]
impl Task for WebSocket {
    type Output = anyhow::Result<()>;

    const NAME: &'static str = "agent listener";

    async fn run(self, mut shutdown_signal: ShutdownSignal) -> Self::Output {
        tokio::select! {
            result = self.run_with_reconnect() => result,
            _ = shutdown_signal.wait() => Ok(()),
        }
    }
}

impl WebSocket {
    pub fn new() -> anyhow::Result<Self> {
        let config_manager =
            conf::ConfigHandleManager::init().context("Failed to init config manager")?;

        let gateway_url =
            std::env::var("GATEWAY_URL").unwrap_or_else(|_| "ws://localhost:8009/api/ws".to_string());
        Ok(WebSocket {
            gateway_url: gateway_url.to_string(),
            config_manager: config_manager,
            reconnect_interval: Duration::from_secs(5),
            max_reconnection_attempts: 10,
        })
    }

    async fn run_with_reconnect(self) -> anyhow::Result<()> {
        let mut attempts = 0;
        loop {
            match self.clone().run().await {
                Ok(_) => {
                    println!("> WebSocket connection closed gracefully");
                    return Ok(());
                }
                Err(e) if e.to_string().contains("connection closed") => {
                    println!("> WebSocket connection closed by server");
                    return Ok(());
                }
                Err(e) if attempts >= self.max_reconnection_attempts => {
                    eprintln!("> Max reconnection attempts reached, giving up: {}", e);
                    return Err(e);
                }
                Err(e) => {
                    attempts += 1;
                    eprintln!(
                        "> Connection failed (attempt {}/{}): {}",
                        attempts, self.max_reconnection_attempts, e
                    );

                    tokio::time::sleep(self.reconnect_interval).await;
                    continue;
                }
            }
        }
    }

    async fn run(self) -> anyhow::Result<()> {
        let (ws_stream, _) = connect_async(self.gateway_url.clone())
            .await
            .expect("Failed to connect to gateway");

        let (ws_sender, ws_receiver) = ws_stream.split();
        println!("> Connected to gateway");

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

        tokio::select! {
            result = processor_task => {
                match result {
                    Ok(Ok(())) => println!("> Message processor completed normally"),
                    Ok(Err(e)) => eprintln!("> Message processor error: {}", e),
                    Err(e) => eprintln!("> Message processor task panicked: {}", e),
                }
            }
            _ = heartbeat_task => {
                println!("> Heartbeat task completed");
            }
        }

        self.cleanup_resources(sessions, active_proxies, session_channels)
            .await;
        Ok(())
    }

    fn spawn_heartbeat_task(&self, ws_sender: WsWriter) -> tokio::task::JoinHandle<()> {
        tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(30));

            loop {
                interval.tick().await;

                let mut sender = ws_sender.lock().await;
                if sender.send(Message::Ping(vec![].into())).await.is_err() {
                    eprintln!("> Failed to send heartbeat ping");
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
        println!("> Cleaning up resources...");

        // Cancel all active proxy tasks
        let mut proxies = active_proxies.write().await;
        for (session_id, handle) in proxies.drain() {
            handle.abort();
            println!("> Cancelled proxy task for session {}", session_id);
        }

        // Clear sessions and channels
        sessions.write().await.clear();
        session_channels.write().await.clear();

        println!("> Resource cleanup complete");
    }
}
