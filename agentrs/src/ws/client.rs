use crate::{
    conf, tls,
    ws::{
        control::{CAPABILITY_FRAME_PROTOCOL, CONTROL_SENTINEL_SID, FRAME_PROTOCOL_V2},
        message::WebSocketMessage,
        message_types::MessageType,
        rdp_message_processor::MessageProcessor,
        types::{ChannelMap, ProxyMap, SessionMap, ViolationAckMap, WsWriter},
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

/// Runs the two connection-owned tasks and joins the loser before returning.
/// Dropping a Tokio JoinHandle detaches its task, so abort without await is not
/// sufficient for reconnect isolation.
async fn run_connection_tasks(
    mut processor_task: tokio::task::JoinHandle<anyhow::Result<()>>,
    mut heartbeat_task: tokio::task::JoinHandle<()>,
) -> anyhow::Result<()> {
    tokio::select! {
        result = &mut processor_task => {
            heartbeat_task.abort();
            let _ = heartbeat_task.await;
            match result {
                Ok(Ok(())) => {
                    info!("> Message processor completed normally");
                    Ok(())
                }
                Ok(Err(error)) => {
                    error!("> Message processor error: {error}");
                    Err(error)
                }
                Err(error) => {
                    error!("> Message processor task panicked: {error}");
                    Err(anyhow::anyhow!("Message processor task panicked: {error}"))
                }
            }
        }
        _ = &mut heartbeat_task => {
            processor_task.abort();
            let _ = processor_task.await;
            debug!("> Heartbeat task completed - connection likely lost");
            Err(anyhow::anyhow!("Heartbeat task completed - connection lost"))
        }
    }
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

    /// Sends the connection-scoped capability advertisement. Carries the
    /// agent's PII guard endpoint readiness and complete Data Masking rule
    /// support. The frame is addressed with the well-known control sentinel
    /// sid, not a session id — the gateway dispatches it by message type at
    /// the connection level.
    async fn send_capabilities(ws_sender: &WsWriter) -> anyhow::Result<()> {
        let metadata = capabilities_metadata();
        let advertised = metadata
            .get(CAPABILITY_SUPPORTS_PII_GUARD)
            .cloned()
            .unwrap_or_default();
        let supports_rules = metadata
            .get(CAPABILITY_SUPPORTS_PII_DATA_MASKING_RULES)
            .cloned()
            .unwrap_or_default();
        let msg = WebSocketMessage::new(MessageType::Capabilities, metadata, Vec::new());
        let framed = msg
            .encode_with_header(CONTROL_SENTINEL_SID)
            .context("encoding capabilities frame")?;
        let mut sender = ws_sender.lock().await;
        sender
            .send(Message::Binary(framed.into()))
            .await
            .context("sending capabilities frame")?;
        info!(
            "> Advertised capabilities to gateway \
             (supports_pii_guard={advertised}, supports_pii_data_masking_rules={supports_rules})"
        );
        Ok(())
    }

    async fn run(self) -> anyhow::Result<()> {
        let (ws_stream, _) = self.connect().await?;
        let (ws_sender, ws_receiver) = ws_stream.split();

        // Clone config manager and sessions for use in the async task
        let ws_sender = Arc::new(Mutex::new(ws_sender));

        // Advertise capabilities as the first frame, before any session can be
        // delegated to us. This lets the gateway decide — at session-creation
        // time — whether it may delegate the PII guard to this agent, and fail
        // closed with a clear error if not, instead of every guarded session
        // dying cryptically inside GuardConfig::resolve. A connection-level
        // failure to send is fatal for this connection (triggers reconnect).
        Self::send_capabilities(&ws_sender).await?;
        let sessions: SessionMap = Arc::new(RwLock::new(HashMap::new()));
        // Store active RDP proxy tasks per session
        let active_proxies: ProxyMap = Arc::new(RwLock::new(HashMap::new()));
        let session_channels: ChannelMap = Arc::new(RwLock::new(HashMap::new()));
        let violation_acks: ViolationAckMap = Arc::new(std::sync::Mutex::new(HashMap::new()));

        let message_processor = MessageProcessor {
            ws_sender: ws_sender.clone(),
            sessions: sessions.clone(),
            active_proxies: active_proxies.clone(),
            session_channels: session_channels.clone(),
            violation_acks: violation_acks.clone(),
            config_manager: self.config_manager.clone(),
        };

        let processor_task =
            tokio::spawn(async move { message_processor.process_messages(ws_receiver).await });

        // Start heartbeat task in case the connection is stuck or deadlocked.
        let heartbeat_task = self.spawn_heartbeat_task(ws_sender.clone());
        let result = run_connection_tasks(processor_task, heartbeat_task).await;
        self.cleanup_resources(sessions, active_proxies, session_channels, violation_acks)
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
        violation_acks: ViolationAckMap,
    ) {
        debug!("> Cleaning up resources...");

        // Match per-session teardown's lock order so reconnect cleanup cannot
        // deadlock a proxy finishing concurrently. Drain ownership under the
        // locks, then release every map before aborting and joining tasks that
        // may themselves run finalization.
        let mut proxies = active_proxies.write().await;
        let mut sessions = sessions.write().await;
        let mut channels = session_channels.write().await;
        let handles: Vec<_> = proxies.drain().collect();
        sessions.clear();
        channels.clear();
        drop(channels);
        drop(sessions);
        drop(proxies);

        for (_, handle) in &handles {
            handle.abort();
        }
        for (sid, handle) in handles {
            let _ = handle.await;
            debug!("> Cancelled and joined proxy task for session {sid}");
        }

        violation_acks
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .clear();
        debug!("> Resource cleanup complete");
    }
}

/// Wire key for the PII-guard capability. Must stay identical to the gateway
/// constant `broker.CapabilitySupportsPIIGuard`.
pub const CAPABILITY_SUPPORTS_PII_GUARD: &str = "supports_pii_guard";
pub const CAPABILITY_SUPPORTS_PII_ENTITY_ALLOWLIST: &str = "supports_pii_entity_allowlist";
pub const CAPABILITY_SUPPORTS_PII_DATA_MASKING_RULES: &str =
    "supports_pii_data_masking_rules";

/// Builds the connection-scoped capability advertisement.
///
/// Split out from `send_capabilities` so the value that actually reaches the
/// gateway is testable without a live socket. That matters more than it
/// looks: an OSS build links the guard stub and MUST advertise `false` here,
/// because that is what makes the gateway refuse a session it would otherwise
/// delegate to an agent that cannot enforce. Advertising `true` from a build
/// with no enforcement engine would be a silent bypass.
fn capabilities_metadata() -> HashMap<String, String> {
    let mut metadata = HashMap::new();
    metadata.insert(
        CAPABILITY_SUPPORTS_PII_GUARD.to_string(),
        crate::guard::supports_pii_guard().to_string(),
    );
    metadata.insert(
        CAPABILITY_SUPPORTS_PII_ENTITY_ALLOWLIST.to_string(),
        crate::guard::ENFORCEMENT_AVAILABLE.to_string(),
    );
    metadata.insert(
        CAPABILITY_SUPPORTS_PII_DATA_MASKING_RULES.to_string(),
        crate::guard::ENFORCEMENT_AVAILABLE.to_string(),
    );
    metadata.insert(
        CAPABILITY_FRAME_PROTOCOL.to_string(),
        FRAME_PROTOCOL_V2.to_string(),
    );
    metadata
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The advertised value must be exactly what the guard reports — never a
    /// hardcoded optimistic default. The gateway's refusal of guarded sessions
    /// against an incapable agent hinges on this one string.
    #[test]
    fn advertises_the_guard_capability_verbatim() {
        let metadata = capabilities_metadata();
        assert_eq!(
            metadata
                .get(CAPABILITY_SUPPORTS_PII_GUARD)
                .map(String::as_str),
            Some(crate::guard::supports_pii_guard().to_string().as_str()),
        );
    }

    #[test]
    fn advertises_entity_allowlist_support_for_guard_build() {
        assert_eq!(
            capabilities_metadata()
                .get(CAPABILITY_SUPPORTS_PII_ENTITY_ALLOWLIST)
                .cloned(),
            Some(crate::guard::ENFORCEMENT_AVAILABLE.to_string()),
        );
    }

    #[test]
    fn advertises_complete_data_masking_rule_support_for_guard_build() {
        assert_eq!(
            capabilities_metadata()
                .get(CAPABILITY_SUPPORTS_PII_DATA_MASKING_RULES)
                .cloned(),
            Some(crate::guard::ENFORCEMENT_AVAILABLE.to_string()),
        );
    }

    #[test]
    fn advertises_typed_frame_protocol() {
        assert_eq!(
            capabilities_metadata()
                .get(CAPABILITY_FRAME_PROTOCOL)
                .map(String::as_str),
            Some(FRAME_PROTOCOL_V2),
        );
    }

    /// The gateway parses this with strconv.ParseBool, so it has to be one of
    /// the forms that accepts — and "true"/"false" are what it stores.
    #[test]
    fn capability_value_is_a_go_parseable_bool() {
        let metadata = capabilities_metadata();
        let value = metadata
            .get(CAPABILITY_SUPPORTS_PII_GUARD)
            .expect("the capability key must always be present");
        assert!(
            value == "true" || value == "false",
            "capability value {value:?} is not a bool the gateway can parse"
        );
    }

    /// An OSS build has no enforcement engine, so it must advertise false.
    /// This is the property that keeps a stub-linked agent from being handed
    /// guarded sessions it would forward in the clear.
    #[test]
    fn a_build_without_an_engine_advertises_false() {
        if crate::guard::ENFORCEMENT_AVAILABLE {
            return; // enterprise build: capability depends on runtime endpoints
        }
        assert_eq!(
            capabilities_metadata()
                .get(CAPABILITY_SUPPORTS_PII_GUARD)
                .map(String::as_str),
            Some("false"),
            "a build linked against the guard stub must never advertise capability"
        );
        assert_eq!(
            capabilities_metadata()
                .get(CAPABILITY_SUPPORTS_PII_ENTITY_ALLOWLIST)
                .map(String::as_str),
            Some("false"),
            "a build linked against the guard stub must not advertise entity-policy support"
        );
        assert_eq!(
            capabilities_metadata()
                .get(CAPABILITY_SUPPORTS_PII_DATA_MASKING_RULES)
                .map(String::as_str),
            Some("false"),
            "a build linked against the guard stub must not advertise complete-policy support"
        );
    }

    struct DropFlag(std::sync::Arc<std::sync::atomic::AtomicBool>);

    impl Drop for DropFlag {
        fn drop(&mut self) {
            self.0.store(true, std::sync::atomic::Ordering::SeqCst);
        }
    }

    #[tokio::test]
    async fn processor_completion_joins_heartbeat_task() {
        let dropped = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
        let guard = DropFlag(dropped.clone());
        let processor = tokio::spawn(async { Ok(()) });
        let heartbeat = tokio::spawn(async move {
            let _guard = guard;
            std::future::pending::<()>().await;
        });

        run_connection_tasks(processor, heartbeat)
            .await
            .expect("processor completion");
        assert!(
            dropped.load(std::sync::atomic::Ordering::SeqCst),
            "heartbeat task was detached"
        );
    }

    #[tokio::test]
    async fn heartbeat_completion_joins_processor_task() {
        let dropped = std::sync::Arc::new(std::sync::atomic::AtomicBool::new(false));
        let guard = DropFlag(dropped.clone());
        let processor = tokio::spawn(async move {
            let _guard = guard;
            std::future::pending::<anyhow::Result<()>>().await
        });
        let heartbeat = tokio::spawn(async {});

        let error = run_connection_tasks(processor, heartbeat)
            .await
            .expect_err("heartbeat completion must reconnect");
        assert!(error.to_string().contains("connection lost"));
        assert!(
            dropped.load(std::sync::atomic::Ordering::SeqCst),
            "message processor task was detached"
        );
    }
}
