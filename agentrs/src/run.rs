use tracing::info;

use crate::listener::Service;
use anyhow::Context;

#[cfg(unix)]
async fn build_signals_fut() -> anyhow::Result<()> {
    use tokio::signal::unix::{SignalKind, signal};

    let mut terminate_signal =
        signal(SignalKind::terminate()).context("failed to create terminate signal stream")?;
    let mut quit_signal =
        signal(SignalKind::quit()).context("failed to create quit signal stream failed")?;
    let mut interrupt_signal = signal(SignalKind::interrupt())
        .context("failed to create interrupt signal stream failed")?;

    futures::future::select_all(vec![
        Box::pin(terminate_signal.recv()),
        Box::pin(quit_signal.recv()),
        Box::pin(interrupt_signal.recv()),
    ])
    .await;

    Ok(())
}
pub fn run_agent() -> anyhow::Result<()> {
    tracing_subscriber::fmt::init();
    info!("Starting Agent Service...");

    let mut s = Service::new();
    s.start()?;

    let rt = tokio::runtime::Builder::new_current_thread()
        .enable_io()
        .build()
        .context("failed to build the async runtime")?;
    rt.block_on(build_signals_fut())?;

    s.stop();
    Ok(())
}
