mod conf;
mod listener;
mod run;
mod session;
mod tls;
mod ws;
pub mod rdp {
    pub mod proxy;
}
pub mod tasks {
    pub mod tasks;
}
pub mod certs {
    pub mod x509;
}
pub mod transport {
    pub mod transport;
}
pub mod proxy {
    pub mod proxy;
}
pub mod logio {
    pub mod logio;
}

use crate::run::run_agent;

fn main() -> anyhow::Result<()> {
    run_agent()
}
