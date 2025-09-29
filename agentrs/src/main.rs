pub mod conf;
pub mod listener;
pub mod logio;
pub mod proxy;
pub mod rdp_proxy;
pub mod run;
pub mod session;
pub mod tasks;
pub mod tls;
pub mod ws;
pub mod x509;

use crate::run::run_agent;

fn main() -> anyhow::Result<()> {
    run_agent()
}
