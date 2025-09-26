use crate::ws::session::SessionInfo;
use futures::stream::SplitSink;
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::RwLock;
use tokio::sync::mpsc::{Receiver, Sender};
use tokio_tungstenite::WebSocketStream;
use uuid::Uuid;

use tokio::sync::Mutex;
use tokio_tungstenite::tungstenite::protocol::Message;

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
pub type ChannelMap = Arc<RwLock<HashMap<Uuid, (Sender<Vec<u8>>, Arc<Mutex<Receiver<Vec<u8>>>>)>>>;
