use tracing::info;

use crate::ws::client::WebSocket;

pub async fn run_agent() -> anyhow::Result<()> {
    // this will simplify the async runtime by using the default
    tracing_subscriber::fmt::init();
    info!("Starting Agent Service...");
    let ws = WebSocket::new()?;
    ws.run_with_reconnect().await?;
    Ok(())
}
