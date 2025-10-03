pub mod conf;
pub mod proxy;
pub mod rdp_proxy;
pub mod run;
pub mod session;
pub mod tls;
pub mod ws;
pub mod x509;

use crate::run::run_agent;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    run_agent().await
}
