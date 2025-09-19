use std::sync::Arc;

use futures::stream::SplitSink;
use tokio_tungstenite::WebSocketStream;
use tracing::info;
use uuid::Uuid;

use std::collections::HashMap;
use tokio::sync::RwLock;
use tokio::sync::mpsc::{Receiver, Sender};

use tokio::sync::Mutex;

#[derive(Clone)]
pub struct SessionInfo {
    pub session_id: Uuid,
    pub target_address: String,
    pub username: String,
    pub password: String,
    pub proxy_user: String,
    pub client_address: String,
    pub sender: Arc<
        tokio::sync::Mutex<
            SplitSink<
                WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>,
                tungstenite::Message,
            >,
        >,
    >,
}

#[derive(Clone)]
pub struct SessionManager {
    sessions: Arc<RwLock<HashMap<Uuid, SessionInfo>>>,
    active_proxies: Arc<RwLock<HashMap<Uuid, tokio::task::JoinHandle<()>>>>,
    session_channels: Arc<RwLock<HashMap<Uuid, (Sender<Vec<u8>>, Arc<Mutex<Receiver<Vec<u8>>>>)>>>,
}

impl SessionManager {
    async fn shutdown(&self) {
        info!("Shutting down session manager...");

        // Cancel all active proxy tasks
        let mut proxies = self.active_proxies.write().await;
        for (session_id, handle) in proxies.drain() {
            handle.abort();
            info!("Cancelled proxy task for session {}", session_id);
        }

        // Clear all sessions
        let mut sessions = self.sessions.write().await;
        sessions.clear();

        // Clear all channels
        let mut channels = self.session_channels.write().await;
        channels.clear();

        info!("Session manager shutdown complete");
    }
}
