//src/main.rs
mod gateway;
mod headers;
mod logio;
mod protocol;
mod proxy;
mod rdpproxy;
mod session;
mod tasks;
mod transport;
mod token;
mod tls;

use anyhow::{Context, Result};
use url::Url;
use uuid::Uuid;

use crate::gateway::gateway::{GatewayService, ListenerUrls};

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

fn main() -> anyhow::Result<()> {
    println!("Starting Gateway Service...");

    let l = ListenerUrls::new(Url::parse("tcp://0.0.0.0:3389")?);
    let mut s = GatewayService::new(Uuid::new_v4(), vec![l]);
    s.start()?;

    let rt = tokio::runtime::Builder::new_current_thread()
        .enable_io()
        .build()
        .context("failed to build the async runtime")?;
    rt.block_on(build_signals_fut())?;

    s.stop();
    Ok(())
}
