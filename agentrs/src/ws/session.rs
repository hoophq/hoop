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

// TODO implement session manager to handle cleanup on shutdown later
