//! The written contract for the guard boundary.
//!
//! `agentrs` reaches the RDP PII guard only through `crate::guard`, which is a
//! re-export of the `hoop-rdp-guard` crate. Which crate that is depends on
//! what `./libhoop` resolves to at build time: the OSS passthrough stub in
//! `_libhoop/rdp-guard`, or the enterprise guard in `hoophq/libhoop`. Both
//! must expose the same surface, or swapping one for the other stops being a
//! build-time concern and becomes a compile error in whichever repository the
//! drift was not introduced in.
//!
//! Nothing else checks that: the two crates live in different repositories and
//! no build compiles them together. This file is the contract they both have
//! to satisfy, and CI runs it under both flavors (`rust-test-oss` and
//! `rust-test-enterprise`). Every item is named with its exact type, so
//! argument types, return types, receivers and generic instantiation are all
//! part of the assertion.
//!
//! This is a compile-time test — it deliberately calls nothing. Behavior is
//! tested where it lives: the stub's passthrough-and-refuse semantics in
//! `_libhoop/rdp-guard`, the real hold-and-release pipeline in the enterprise
//! crate. Asserting either here would only pass by accident of the other's
//! environment.
//!
//! When adding to the boundary: add it here too, and to both crates.

use std::collections::HashMap;

use agentrs::guard;
use tokio::io::DuplexStream;

#[allow(unused)]
fn surface() {
    // Capability advertised to the gateway at connect time. The gateway
    // refuses a guarded session against an agent that reports false.
    let _: fn() -> bool = guard::supports_pii_guard;

    // Delegation check over SessionStarted metadata.
    let _: fn(&HashMap<String, String>) -> bool = guard::guard_requested;

    // Wire constant shared with gateway/broker/protocol_rdp.go.
    let _: &str = guard::PII_GUARD_METADATA_KEY;

    // Build-time marker: whether an enforcement engine was linked at all.
    // Distinct from supports_pii_guard(), which is a runtime question about
    // endpoints. Release builds assert this via --features require-enforcement.
    let _: bool = guard::ENFORCEMENT_AVAILABLE;

    // Resolution: Ok(None) = unguarded, Err = fail closed. Never a silent
    // fallback to transparent forwarding.
    let _: fn(&HashMap<String, String>, &str) -> anyhow::Result<Option<guard::GuardConfig>> =
        guard::GuardConfig::resolve;

    // Guarded activation rewrites both halves of capability negotiation. The
    // config receiver makes these operations unreachable in the OSS build.
    let _: fn(&guard::GuardConfig, &[u8]) -> anyhow::Result<Option<Vec<u8>>> =
        guard::GuardConfig::pin_server_capability_frame;
    let _: fn(&guard::GuardConfig, &[u8]) -> anyhow::Result<Option<Vec<u8>>> =
        guard::GuardConfig::pin_client_capability_frame;

    // Valve construction, generic over the sink; instantiated at a concrete
    // AsyncWrite so the bound is asserted too.
    let _: fn(
        String,
        guard::GuardConfig,
        DuplexStream,
    ) -> anyhow::Result<(guard::Gate, guard::GateEvents)> = guard::Gate::spawn::<DuplexStream>;

    // Valve handle: shared-reference methods, so the ingest path and the event
    // loop can run concurrently under one select!.
    let _: fn(&guard::Gate, &[u8]) = guard::Gate::ingest;
    let _: fn(&guard::Gate) -> bool = guard::Gate::killed;

    // Events, already reduced to what the transport needs: neither the
    // detection model nor the policy enum crosses the boundary.
    let _: fn(&guard::GateEvent) -> bool = guard::GateEvent::is_terminal;
    let _: fn(&guard::GateEvent) -> Option<&[u8]> = guard::GateEvent::report_json;
    let _: fn(&guard::GateEvent) -> &str = guard::GateEvent::log_message;
}

/// Pins the async half. Awaiting inside a function that is never called still
/// type-checks the receivers and the future outputs exactly — an async fn
/// returns an anonymous `impl Future`, which cannot be named in a fn-pointer
/// type.
#[allow(unused)]
async fn async_surface(gate: &guard::Gate, events: &mut guard::GateEvents) {
    let _: () = guard::Gate::close(gate).await;
    let _: Option<guard::GateEvent> = guard::GateEvents::next(events).await;
}

/// The transport must never construct a guard the gateway did not ask for.
///
/// This is the one behavior that is identical in both crates and that the
/// transport depends on: absent the `pii_guard` metadata key there is no
/// delegation, so the session is proxied transparently rather than refused.
#[test]
fn an_unrequested_guard_is_not_a_refusal() {
    let resolved = guard::GuardConfig::resolve(&HashMap::new(), "sid-surface")
        .expect("a session with no delegation must resolve cleanly, not error");
    assert!(
        resolved.is_none(),
        "no delegation means transparent forwarding"
    );
}

/// `guard_requested` and `GuardConfig::resolve` must agree about what counts
/// as delegation, in both crates. If they disagree, a delegated session can be
/// forwarded unguarded — the exact bypass the capability handshake exists to
/// prevent.
#[test]
fn delegation_detection_agrees_with_resolution() {
    let metadata: HashMap<String, String> = [(
        guard::PII_GUARD_METADATA_KEY.to_string(),
        "enabled".to_string(),
    )]
    .into_iter()
    .collect();

    assert!(
        guard::guard_requested(&metadata),
        "the wire constant must be the key resolve() keys off"
    );

    // Delegated. Either the guard is built (enterprise, endpoints configured)
    // or the session is refused (stub always; enterprise without endpoints).
    // The one outcome that must never happen is Ok(None) — that would drop the
    // guard silently and proxy the session in the clear.
    match guard::GuardConfig::resolve(&metadata, "sid-surface") {
        Ok(Some(_)) => {}
        Err(_) => {}
        Ok(None) => panic!("a delegated guard resolved to None: the session would run unguarded"),
    }
}
