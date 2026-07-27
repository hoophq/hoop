use std::sync::Arc;

use futures::stream::SplitSink;
use tokio_tungstenite::WebSocketStream;
use uuid::Uuid;

#[derive(Clone, Debug)]
pub struct SessionInfo {
    pub sid: Uuid,
    pub target_address: String,
    pub username: String,
    pub password: String,
    pub proxy_user: String,
    pub client_address: String,
    /// Agent-side PII guard config, resolved from the SessionStarted
    /// metadata (gateway policy) and agent env (endpoints). None = unguarded.
    pub guard: Option<crate::piigate::config::GuardConfig>,
    pub sender: Arc<
        tokio::sync::Mutex<
            SplitSink<
                WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>,
                tungstenite::Message,
            >,
        >,
    >,
}

// TODO implement session manager to handle cleanup in a better way in shutdowns
