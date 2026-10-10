//! The imports of module `hoop`: the only way a codec reaches the relay.
//!
//! Every function here is a thin, safe wrapper over one import. A module
//! imports only what it calls: a plug-in that never touches
//! `analyze_sql` has no `analyze_sql` import, and the host does not ask it
//! for a `sql_dialect`.
//!
//! On a non-wasm target there is no host. The wrappers then panic, so a
//! unit test that strays into a host call fails with a message naming the
//! import instead of linking against nothing.

use crate::abi;
use crate::types::SqlAnalysis;

/// A log level the host understands.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(u32)]
pub enum Level {
    Debug = 0,
    Info = 1,
    Warn = 2,
    Error = 3,
}

#[cfg(target_arch = "wasm32")]
mod imports {
    #[link(wasm_import_module = "hoop")]
    extern "C" {
        pub fn analyze_sql(ptr: u32, len: u32) -> u64;
        pub fn split_sql(ptr: u32, len: u32) -> u64;
        pub fn mask(cptr: u32, clen: u32, vptr: u32, vlen: u32) -> u64;
        pub fn log(level: u32, ptr: u32, len: u32);
    }
}

#[cfg(not(target_arch = "wasm32"))]
mod imports {
    // The imports that return data have no answer off the relay and fail
    // loudly; the wrapper below handles `log`.
    pub unsafe fn analyze_sql(_: u32, _: u32) -> u64 {
        unavailable("analyze_sql")
    }
    pub unsafe fn split_sql(_: u32, _: u32) -> u64 {
        unavailable("split_sql")
    }
    pub unsafe fn mask(_: u32, _: u32, _: u32, _: u32) -> u64 {
        unavailable("mask")
    }
    fn unavailable(name: &str) -> ! {
        panic!("hoop-codec: host import hoop.{name} is only available inside the relay (target wasm32)")
    }
}

/// Classifies SQL text with the host lexer in the manifest's `sql_dialect`.
///
/// The classifier stays in the relay: it is the most safety-critical code
/// in the system, and one auditable copy serves every plug-in. A codec
/// copies the result onto its statement with `Statement::with_analysis`.
pub fn analyze_sql(sql: &str) -> SqlAnalysis {
    let raw = unsafe { abi::take(imports::analyze_sql(sql.as_ptr() as usize as u32, sql.len() as u32)) };
    // The host wrote this; a payload it cannot encode is a host bug, and a
    // trap is the ABI's way to report one.
    serde_json::from_slice(&raw).unwrap_or_else(|err| panic!("hoop-codec: analyze_sql returned malformed JSON: {err}"))
}

/// Splits a multi-statement text with the host lexer, which knows that a
/// semicolon inside a string literal or a dollar-quoted body ends nothing.
pub fn split_sql(sql: &str) -> Vec<String> {
    let raw = unsafe { abi::take(imports::split_sql(sql.as_ptr() as usize as u32, sql.len() as u32)) };
    serde_json::from_slice(&raw).unwrap_or_else(|err| panic!("hoop-codec: split_sql returned malformed JSON: {err}"))
}

/// Hands one result-set cell to the lane's masker and returns the value to
/// forward, which may differ in length. `column` is empty when the
/// protocol does not name one.
///
/// Legal only on the stack of `rewrite` or `flush`; the host traps a call
/// from anywhere else, so `Codec::rewrite` receives it as a closure.
pub(crate) fn mask(column: &str, value: &[u8]) -> Vec<u8> {
    unsafe {
        abi::take(imports::mask(
            column.as_ptr() as usize as u32,
            column.len() as u32,
            value.as_ptr() as usize as u32,
            value.len() as u32,
        ))
    }
}

/// Writes one line to the relay log, tagged with the lane and connection.
/// Off the relay it goes to stderr, so a host-side unit test can reach a
/// path that logs (a refused `open`) without a wasm runtime.
pub fn log(level: Level, message: &str) {
    #[cfg(target_arch = "wasm32")]
    unsafe {
        imports::log(level as u32, message.as_ptr() as usize as u32, message.len() as u32)
    }
    #[cfg(not(target_arch = "wasm32"))]
    eprintln!("hoop[{}] {message}", level as u32);
}
