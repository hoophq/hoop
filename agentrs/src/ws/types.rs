use crate::ws::session::SessionInfo;
use futures::stream::SplitSink;
use std::collections::HashMap;
use std::sync::{Arc, Mutex as StdMutex};
use tokio::sync::{Mutex, RwLock, mpsc};
use tokio_tungstenite::WebSocketStream;
use tokio_tungstenite::tungstenite::protocol::Message;
use uuid::Uuid;

//define some nested types for readability
pub type WsWriter = Arc<
    Mutex<
        SplitSink<
            WebSocketStream<tokio_tungstenite::MaybeTlsStream<tokio::net::TcpStream>>,
            Message,
        >,
    >,
>;

pub type SessionMap = Arc<RwLock<HashMap<Uuid, SessionInfo>>>;
pub type ProxyMap = Arc<RwLock<HashMap<Uuid, tokio::task::JoinHandle<()>>>>;
pub type ChannelMap = Arc<RwLock<HashMap<Uuid, mpsc::Sender<Vec<u8>>>>>;
pub type ViolationAckMap = Arc<StdMutex<HashMap<Uuid, tokio::sync::oneshot::Sender<()>>>>;
