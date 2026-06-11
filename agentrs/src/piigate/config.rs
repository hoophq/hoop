//! Resolving the agent-side PII guard configuration for a session.
//!
//! Policy (enable decision, score threshold, denylist, band padding) is sent
//! by the gateway in the SessionStarted metadata. Endpoints (Presidio
//! analyzer, OCR sidecar) come from the agent's own environment — the
//! sidecar and analyzer live in the customer network next to the agent, so
//! their addresses are deliberately NOT carried on the wire or held in
//! gateway state. This mirrors the Go agent's terminal-DLP precedent, where
//! the agent reads MSPRESIDIO_ANALYZER_URL from its environment.

use std::collections::HashMap;

use tracing::warn;

use super::presidio::AnalysisParams;
use super::GatePolicy;

/// Env var for the Presidio analyzer base URL (shared with the Go agent's
/// DLP override convention).
pub const PRESIDIO_ANALYZER_URL_ENV: &str = "MSPRESIDIO_ANALYZER_URL";
/// Env var for the OCR sidecar base URL (shared with the gateway's analyzer).
pub const OCR_SERVER_URL_ENV: &str = "RDP_OCR_SERVER_URL";

/// Fully-resolved guard configuration: gateway policy + agent-local
/// endpoints. Present only when the gateway enabled the guard AND the agent
/// has both endpoints configured.
#[derive(Debug, Clone)]
pub struct GuardConfig {
    pub presidio_url: String,
    pub ocr_url: String,
    pub params: AnalysisParams,
    /// What to do on detection (kill / redact / redact+kill). Sent by the
    /// gateway; defaults to kill (preserve the original behavior) when absent
    /// or unrecognized.
    pub policy: GatePolicy,
}

/// Whether the gateway asked this session to be guarded by the agent.
/// Distinct from "guard could be built": the gateway suppresses its own gate
/// when it delegates, so a requested-but-unbuildable guard is a delegation
/// failure that must fail CLOSED (reject the session), not silently run
/// unguarded.
pub fn guard_requested(metadata: &HashMap<String, String>) -> bool {
    metadata.get("pii_guard").map(String::as_str) == Some("enabled")
}

impl GuardConfig {
    /// Resolves the guard config from the SessionStarted metadata (gateway
    /// policy) and the process environment (endpoints).
    ///
    /// Returns:
    /// - `Ok(None)` when the gateway did not request a guard — the session
    ///   runs transparently (correct: the gateway is enforcing nothing here).
    /// - `Ok(Some(cfg))` when guarding was requested and is buildable.
    /// - `Err(_)` when guarding was REQUESTED but the agent cannot honor it
    ///   (missing Presidio/OCR endpoints). The caller must fail closed and
    ///   reject the session: the gateway already suppressed its own gate on
    ///   the strength of this delegation, so running unguarded would be a
    ///   silent enforcement bypass.
    pub fn resolve(metadata: &HashMap<String, String>, sid: &str) -> anyhow::Result<Option<Self>> {
        if !guard_requested(metadata) {
            return Ok(None);
        }

        let presidio_url = env_url(PRESIDIO_ANALYZER_URL_ENV);
        let ocr_url = env_url(OCR_SERVER_URL_ENV);
        let (Some(presidio_url), Some(ocr_url)) = (presidio_url, ocr_url) else {
            warn!(
                %sid,
                "piigate: gateway delegated the PII guard but {PRESIDIO_ANALYZER_URL_ENV} \
                 and/or {OCR_SERVER_URL_ENV} are not set on the agent; rejecting session \
                 (fail closed — the gateway is not guarding either)"
            );
            anyhow::bail!(
                "PII guard delegated by gateway but agent is missing {PRESIDIO_ANALYZER_URL_ENV} \
                 and/or {OCR_SERVER_URL_ENV}"
            );
        };

        let mut params = AnalysisParams::default();
        if let Some(v) = metadata.get("pii_score_threshold").and_then(|s| s.parse().ok()) {
            params.score_threshold = v;
        }
        if let Some(v) = metadata.get("pii_band_padding").and_then(|s| s.parse().ok()) {
            params.band_padding = v;
        }
        if let Some(list) = metadata.get("pii_entity_denylist") {
            // JSON array (entity names are an external vocabulary, not
            // guaranteed comma-free). A malformed value keeps the default
            // denylist rather than failing the session — the policy is
            // advisory, the enforcement is the gate itself.
            match serde_json::from_str::<Vec<String>>(list) {
                Ok(entities) => params.entity_denylist = entities,
                Err(e) => warn!(%sid, "piigate: ignoring malformed pii_entity_denylist: {e}"),
            }
        }

        let policy = match metadata.get("pii_policy").map(String::as_str) {
            Some("redact") => GatePolicy::Redact,
            Some("redact_and_kill") => GatePolicy::RedactAndKill,
            Some("kill") | None => GatePolicy::Kill,
            Some(other) => {
                warn!(%sid, "piigate: unknown pii_policy {other:?}, defaulting to kill");
                GatePolicy::Kill
            }
        };

        Ok(Some(Self { presidio_url, ocr_url, params, policy }))
    }
}

fn env_url(key: &str) -> Option<String> {
    std::env::var(key)
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn md(pairs: &[(&str, &str)]) -> HashMap<String, String> {
        pairs.iter().map(|(k, v)| (k.to_string(), v.to_string())).collect()
    }

    // Endpoint env vars are process-global. This mutex serializes only the
    // tests in THIS module against each other — it cannot protect against
    // other tests in the same binary touching these keys. These two env vars
    // are read nowhere else under test, so that is sufficient here; do not
    // reuse these keys in other tests without coordinating on this lock.
    use std::sync::Mutex;
    static ENV_LOCK: Mutex<()> = Mutex::new(());

    fn with_endpoints<R>(presidio: Option<&str>, ocr: Option<&str>, f: impl FnOnce() -> R) -> R {
        let _g = ENV_LOCK.lock().unwrap();
        // SAFETY: serialized by ENV_LOCK; no other thread reads env here.
        unsafe {
            match presidio {
                Some(v) => std::env::set_var(PRESIDIO_ANALYZER_URL_ENV, v),
                None => std::env::remove_var(PRESIDIO_ANALYZER_URL_ENV),
            }
            match ocr {
                Some(v) => std::env::set_var(OCR_SERVER_URL_ENV, v),
                None => std::env::remove_var(OCR_SERVER_URL_ENV),
            }
        }
        let r = f();
        unsafe {
            std::env::remove_var(PRESIDIO_ANALYZER_URL_ENV);
            std::env::remove_var(OCR_SERVER_URL_ENV);
        }
        r
    }

    #[test]
    fn none_when_gateway_did_not_request() {
        with_endpoints(Some("http://p"), Some("http://o"), || {
            assert!(GuardConfig::resolve(&md(&[]), "sid").unwrap().is_none());
            assert!(GuardConfig::resolve(&md(&[("pii_guard", "off")]), "sid")
                .unwrap()
                .is_none());
        });
    }

    #[test]
    fn errors_when_requested_but_endpoints_missing() {
        // Fail closed: requested + missing endpoints must be an error so the
        // caller rejects the session (the gateway is not guarding either).
        with_endpoints(None, Some("http://o"), || {
            assert!(GuardConfig::resolve(&md(&[("pii_guard", "enabled")]), "sid").is_err());
        });
        with_endpoints(Some("http://p"), None, || {
            assert!(GuardConfig::resolve(&md(&[("pii_guard", "enabled")]), "sid").is_err());
        });
        with_endpoints(None, None, || {
            assert!(GuardConfig::resolve(&md(&[("pii_guard", "enabled")]), "sid").is_err());
        });
    }

    #[test]
    fn resolves_policy_and_endpoints() {
        with_endpoints(Some("http://presidio:5001/"), Some("http://ocr:8868"), || {
            let cfg = GuardConfig::resolve(
                &md(&[
                    ("pii_guard", "enabled"),
                    ("pii_score_threshold", "0.75"),
                    ("pii_band_padding", "30"),
                    ("pii_entity_denylist", r#"["DATE_TIME","NRP"]"#),
                ]),
                "sid",
            )
            .expect("resolve ok")
            .expect("guard should be present");
            assert_eq!(cfg.presidio_url, "http://presidio:5001/");
            assert_eq!(cfg.ocr_url, "http://ocr:8868");
            assert_eq!(cfg.params.score_threshold, 0.75);
            assert_eq!(cfg.params.band_padding, 30);
            assert_eq!(cfg.params.entity_denylist, vec!["DATE_TIME", "NRP"]);
        });
    }

    #[test]
    fn parses_policy_or_defaults_to_kill() {
        with_endpoints(Some("http://p"), Some("http://o"), || {
            let resolve = |policy: Option<&str>| {
                let mut pairs = vec![("pii_guard", "enabled")];
                if let Some(p) = policy {
                    pairs.push(("pii_policy", p));
                }
                GuardConfig::resolve(&md(&pairs), "sid").unwrap().unwrap().policy
            };
            assert_eq!(resolve(None), GatePolicy::Kill);
            assert_eq!(resolve(Some("kill")), GatePolicy::Kill);
            assert_eq!(resolve(Some("redact")), GatePolicy::Redact);
            assert_eq!(resolve(Some("redact_and_kill")), GatePolicy::RedactAndKill);
            // Unknown value falls back to kill (fail-safe, not fail-open).
            assert_eq!(resolve(Some("bogus")), GatePolicy::Kill);
        });
    }

    #[test]
    fn defaults_when_policy_keys_absent() {
        with_endpoints(Some("http://p"), Some("http://o"), || {
            let cfg = GuardConfig::resolve(&md(&[("pii_guard", "enabled")]), "sid")
                .unwrap()
                .unwrap();
            let d = AnalysisParams::default();
            assert_eq!(cfg.params.score_threshold, d.score_threshold);
            assert_eq!(cfg.params.band_padding, d.band_padding);
            assert_eq!(cfg.params.entity_denylist, d.entity_denylist);
        });
    }
}
