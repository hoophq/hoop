mod run;
mod certs;
mod conf;
mod listener;
mod logio;
mod proxy;
mod rdp;
mod session;
mod tasks;
mod tls;
mod transport;
mod ws;

use crate::{run::run_agent};

fn main() -> anyhow::Result<()> {
    run_agent()
}
