use std::time::Duration;


#[derive(Clone)]
pub struct WebSocketConfig {
    pub gateway_url: String,
    pub heartbeat_interval: Duration,
    pub channel_buffer_size: usize,
    pub max_concurrent_sessions: usize,
}

impl Default for WebSocketConfig {
    fn default() -> Self {
        Self {
            gateway_url: "ws://localhost:8080/ws".to_string(),
            heartbeat_interval: Duration::from_secs(30),
            channel_buffer_size: 100,
            max_concurrent_sessions: 1000,
        }
    }
}
