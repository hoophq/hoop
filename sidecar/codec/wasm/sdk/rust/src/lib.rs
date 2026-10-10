//! hoop-codec: write a hoop codec plug-in in Rust.
//!
//! A plug-in is one `cdylib` crate built for `wasm32-unknown-unknown`. It
//! implements [`Codec`] for a type, writes a `fn() -> Manifest`, and calls
//! [`export_codec!`] once at the crate root. The macro emits every export
//! of the ABI (`../../abi/ABI.md`); the host instantiates the module per
//! connection (or per lane), feeds it bytes through `decode`, and reads
//! back the statements policy evaluates. `examples/acmewire` is a complete
//! plug-in and the template to copy.
//!
//! The SDK speaks to the relay through [`host`]: the SQL classifier lives
//! in the relay, so a plug-in for a SQL-carrying protocol calls
//! `host::analyze_sql` and copies the result onto its statement.

pub mod abi;
mod codec;
pub mod host;
mod macros;
mod types;
#[cfg(test)]
mod tests;

pub use codec::{describe, Codec, Connections};
pub use types::*;

/// Installs the panic hook `_initialize` runs: the panic message goes to
/// the host log at level error, then the panic becomes a trap (the plug-in
/// builds with `panic = "abort"`). Without it a trap reaches the operator
/// as "unreachable executed", which names nothing. On a host target there
/// is no relay log, so the default hook stays.
pub fn install_panic_hook() {
    #[cfg(target_arch = "wasm32")]
    std::panic::set_hook(Box::new(|info| {
        host::log(host::Level::Error, &format!("plug-in panicked: {info}"));
    }));
}
