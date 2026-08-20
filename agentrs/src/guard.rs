//! The boundary between the RDP transport and the PII guard.
//!
//! Everything in `agentrs` that is not the transport itself talks to the
//! guard only through this module, and the guard lives in a separate crate
//! selected at build time by which `libhoop` the `./libhoop` path resolves to:
//!
//! - OSS (`_libhoop/rdp-guard`, wired by `make libhoop-ensure`) — a
//!   passthrough stub. RDP is proxied normally with no detection and no
//!   redaction, `supports_pii_guard()` is a compile-time `false`, and
//!   `GuardConfig` is an uninhabited type so the guarded branch below cannot
//!   be reached. A session the gateway tried to delegate is refused by
//!   `GuardConfig::resolve` rather than forwarded unguarded.
//! - Enterprise (a checkout of `hoophq/libhoop` at `./libhoop`) — the real
//!   hold-and-release guard.
//!
//! Both crates expose an identical surface; `tests/guard_surface.rs` asserts
//! that and runs under both flavors in CI.

pub use hoop_rdp_guard::*;

/// Refuses to build a release that cannot enforce.
///
/// The failure this prevents is silent: if a release job resolves
/// `./libhoop` to the OSS stub — because the checkout step is missing, or
/// ran after `libhoop-ensure`, or the private repo lacks `rdp-guard/` — then
/// `cargo build --release` succeeds, every test passes, and the published
/// `hoop_rs` simply never enforces anything. Nothing downstream notices,
/// because an agent that reports `supports_pii_guard=false` is a legitimate
/// state the gateway handles by refusing guarded sessions.
///
/// A runtime probe cannot catch it either: `supports_pii_guard()` is false
/// without the OCR and Presidio endpoints, which CI does not have, and the
/// cross-compiled release artifacts cannot be executed on the build host
/// anyway. So assert at compile time, and have the release jobs pass
/// `--features require-enforcement`.
#[cfg(feature = "require-enforcement")]
const _: () = assert!(
    hoop_rdp_guard::ENFORCEMENT_AVAILABLE,
    "this release was built against the OSS guard stub and cannot enforce; \
     check out hoophq/libhoop at ./libhoop before building"
);
