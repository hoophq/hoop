//! OSS stub for the Hoop RDP PII guard.
//!
//! This is the Rust half of the same OSS/enterprise split `_libhoop` provides
//! for the Go protocols. The enterprise crate lives at `rdp-guard/` inside
//! `hoophq/libhoop`; `make libhoop-map` symlinks `libhoop -> _libhoop`, so an
//! OSS build resolves the `../libhoop/rdp-guard` path dependency to this
//! crate and an enterprise build resolves it to the private one.
//!
//! # Why this stub passes data through instead of failing loud
//!
//! `_libhoop`'s `noopProxy` refuses every protocol with "missing protocol
//! hoop library" because without libhoop those protocols cannot function at
//! all — libhoop *is* the implementation. RDP is different: the transport
//! (framing, TLS, CredSSP, session plumbing) is open source and works on its
//! own. Only detection and redaction are commercial.
//!
//! So an OSS build proxies RDP normally. It does not pin RDP capability sets
//! either, which means an OSS session negotiates the full capability set the
//! server offers — unmodified transport, not degraded transport.
//!
//! # Why that is safe
//!
//! Passthrough is safe because the capability is honest, not because the
//! bytes are copied. Two mechanisms enforce that, and both are compile-time
//! here:
//!
//! 1. [`supports_pii_guard`] is a constant `false`. The agent advertises it
//!    to the gateway in the connect-time capabilities frame, and the gateway
//!    refuses any session that has the PII guard enabled against an agent
//!    that advertised it cannot enforce it.
//! 2. [`GuardConfig`] is an uninhabited type. It has no variants, so no code
//!    path in an OSS build can construct one, `GuardConfig::resolve` can only
//!    ever return `Ok(None)` or an error, and [`Gate`] is unreachable by
//!    construction rather than by convention.
//!
//! Mechanism 1 depends on the gateway behaving correctly. Mechanism 2 does
//! not: if a misconfigured or older gateway delegates the guard anyway,
//! [`GuardConfig::resolve`] refuses the session rather than running it
//! unguarded, matching the enterprise crate's fail-closed delegation-mismatch
//! behavior. A stub that forwarded bytes while advertising `true` would be
//! the DEP-48 violation; this one cannot be.

use std::collections::HashMap;

use tokio::io::AsyncWrite;

/// Mirrors the "missing protocol hoop library" wording the Go stub uses.
pub const MISSING_LIBRARY: &str =
    "missing protocol hoop library for the RDP PII guard, contact your administrator";

/// SessionStarted metadata key through which the gateway delegates guarding.
/// Wire constant, shared with `gateway/broker/protocol_rdp.go`.
pub const PII_GUARD_METADATA_KEY: &str = "pii_guard";

/// Whether this build contains an enforcement engine at all.
///
/// Distinct from [`supports_pii_guard`], which is a runtime question: it
/// additionally requires the OCR and Presidio endpoints to be configured, so
/// it is `false` on an enterprise build with no endpoints just as it is here.
/// That makes it useless for catching the dangerous build mistake — shipping a
/// release binary that was linked against this stub. This constant answers
/// only "was an engine compiled in", so `agentrs` can refuse to build a
/// release without one (`--features require-enforcement`).
pub const ENFORCEMENT_AVAILABLE: bool = false;

/// Whether this build can enforce a delegated PII guard.
///
/// Always `false` here. The agent sends this to the gateway as the
/// `supports_pii_guard` capability, which is what makes the gateway fail
/// closed early and with a clear error instead of letting a guarded session
/// die deeper in the stack.
pub fn supports_pii_guard() -> bool {
    false
}

/// Whether the gateway asked this session to be guarded by the agent.
pub fn guard_requested(metadata: &HashMap<String, String>) -> bool {
    metadata.get(PII_GUARD_METADATA_KEY).map(String::as_str) == Some("enabled")
}

/// A resolved guard configuration.
///
/// Uninhabited in the OSS build: there is no value of this type, so the
/// guarded branch of the proxy is statically dead code.
#[derive(Debug, Clone)]
pub enum GuardConfig {}

impl GuardConfig {
    /// Resolves the guard from gateway policy (SessionStarted metadata) and
    /// agent-local endpoints.
    ///
    /// - Guard not requested: `Ok(None)`, and the session is proxied
    ///   transparently.
    /// - Guard requested: `Err`. The gateway suppresses its own gate when it
    ///   delegates, so running transparently here would be a silent
    ///   enforcement bypass. Refuse the session instead.
    pub fn resolve(metadata: &HashMap<String, String>, sid: &str) -> anyhow::Result<Option<Self>> {
        if guard_requested(metadata) {
            anyhow::bail!(
                "piigate: session {sid} was delegated the RDP PII guard but this build cannot \
                 enforce it: {MISSING_LIBRARY}"
            );
        }
        Ok(None)
    }

    /// Unreachable OSS counterpart of the enterprise capability interceptor.
    pub fn pin_server_capability_frame(
        &self,
        _frame: &[u8],
    ) -> anyhow::Result<Option<Vec<u8>>> {
        match *self {}
    }

    /// Unreachable OSS counterpart of the enterprise capability interceptor.
    pub fn pin_client_capability_frame(
        &self,
        _frame: &[u8],
    ) -> anyhow::Result<Option<Vec<u8>>> {
        match *self {}
    }
}

/// A terminal or informational event emitted by the gate.
///
/// Uninhabited in the OSS build.
pub enum GateEvent {}

impl GateEvent {
    /// Whether the session owner must tear the session down.
    pub fn is_terminal(&self) -> bool {
        match *self {}
    }

    /// The violation report to ship upstream, already serialized as JSON.
    ///
    /// Pre-serializing keeps the report type out of the seam entirely: the
    /// transport layer forwards opaque bytes and never links the detection
    /// model.
    pub fn report_json(&self) -> Option<&[u8]> {
        match *self {}
    }

    /// One-line description for the session log.
    pub fn log_message(&self) -> &str {
        match *self {}
    }
}

/// The hold-and-release valve on the server-to-client stream.
///
/// Split from [`GateEvents`] so the ingest path can hold a shared reference
/// while the event loop consumes the stream mutably — both run concurrently
/// under the same `select!`.
///
/// Uninhabited in the OSS build: [`Gate::spawn`] consumes a [`GuardConfig`],
/// which cannot exist here.
pub struct Gate {
    never: GuardConfig,
}

/// The gate's event stream. See [`Gate::spawn`].
///
/// Uninhabited in the OSS build.
pub struct GateEvents {
    never: GuardConfig,
}

impl Gate {
    /// Starts the valve. Cleared bytes are written to `sink`, which the gate
    /// owns and closes.
    pub fn spawn<W>(
        _session_id: String,
        cfg: GuardConfig,
        _sink: W,
    ) -> anyhow::Result<(Self, GateEvents)>
    where
        W: AsyncWrite + Unpin + Send + 'static,
    {
        match cfg {}
    }

    /// Feeds server-to-client bytes into the valve.
    pub fn ingest(&self, _data: &[u8]) {
        match self.never {}
    }

    /// Whether the gate has stopped forwarding.
    pub fn killed(&self) -> bool {
        match self.never {}
    }

    /// Drops held bytes, cancels in-flight analysis, and closes the sink.
    pub async fn close(&self) {
        match self.never {}
    }
}

impl GateEvents {
    /// Yields the next gate event, or `None` once the gate is finished.
    pub async fn next(&mut self) -> Option<GateEvent> {
        match self.never {}
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn metadata(pairs: &[(&str, &str)]) -> HashMap<String, String> {
        pairs
            .iter()
            .map(|(k, v)| ((*k).to_string(), (*v).to_string()))
            .collect()
    }

    #[test]
    fn never_advertises_guard_capability() {
        assert!(!supports_pii_guard());
    }

    #[test]
    fn unguarded_session_resolves_to_no_guard() {
        let m = metadata(&[("pii_guard", "disabled")]);
        assert!(GuardConfig::resolve(&m, "sid-1").unwrap().is_none());
        assert!(GuardConfig::resolve(&metadata(&[]), "sid-2").unwrap().is_none());
    }

    #[test]
    fn delegated_guard_is_refused_not_silently_dropped() {
        let m = metadata(&[("pii_guard", "enabled")]);
        let err = GuardConfig::resolve(&m, "sid-3")
            .expect_err("a delegated guard this build cannot enforce must refuse the session");
        let msg = format!("{err:#}");
        assert!(msg.contains("sid-3"), "error must name the session: {msg}");
        assert!(
            msg.contains(MISSING_LIBRARY),
            "error must explain the missing library: {msg}"
        );
    }
}
