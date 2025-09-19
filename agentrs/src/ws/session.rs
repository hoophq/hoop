use std::sync::Arc;

use futures::stream::SplitSink;
use tokio_tungstenite::WebSocketStream;
use uuid::Uuid;

#[derive(Clone)]
pub struct SessionInfo {
    pub session_id: Uuid,
    pub target_address: String,
    pub username: String,
    pub password: String,
    pub proxy_user: String,
    pub client_address: String,
    pub sender: Arc<tokio::sync::Mutex<SplitSink<WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>, tungstenite::Message>>>,
}
