//! Resolving the agent-side PII guard configuration for a session.
//!
//! The enable decision, complete connection-scoped Data Masking rule payload,
//! and band padding are sent by the gateway in SessionStarted metadata. Endpoints
//! (Presidio analyzer, OCR sidecar) come from the agent's own environment —
//! the sidecar and analyzer live in the customer network next to the agent,
//! so their addresses are deliberately NOT carried on the wire or held in
//! gateway state. This mirrors the Go agent's terminal-DLP precedent, where
//! the agent reads MSPRESIDIO_ANALYZER_URL from its environment.

use std::collections::{BTreeSet, HashMap};

use serde::Deserialize;
use tracing::warn;

use super::presidio::{AdHocRecognizer, AdHocRecognizerPattern, AnalysisParams};
use super::GatePolicy;

#[derive(Debug, Deserialize)]
struct DataMaskingRule {
    #[serde(default)]
    supported_entity_types: Vec<SupportedEntityTypesEntry>,
    #[serde(default)]
    custom_entity_types: Vec<CustomEntityTypesEntry>,
    score_threshold: Option<f64>,
}

#[derive(Debug, Deserialize)]
struct SupportedEntityTypesEntry {
    name: String,
    #[serde(default)]
    entity_types: Vec<String>,
}

#[derive(Debug, Deserialize)]
struct CustomEntityTypesEntry {
    name: String,
    regex: Option<String>,
    #[serde(default)]
    deny_list: Vec<String>,
    score: f64,
}

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

/// Whether this agent is *capable* of honoring a delegated PII guard right
/// now: it has both the Presidio analyzer and OCR sidecar endpoints
/// configured. This is the single source of truth for capability — the same
/// condition `resolve` requires to build a guard — and is advertised to the
/// gateway at connect time so the gateway can fail closed with a clear,
/// early error instead of letting every guarded session die cryptically in
/// `resolve`.
pub fn supports_pii_guard() -> bool {
    env_url(PRESIDIO_ANALYZER_URL_ENV).is_some() && env_url(OCR_SERVER_URL_ENV).is_some()
}

fn is_canonical_entity_type(name: &str) -> bool {
    !name.is_empty()
        && name
            .bytes()
            .all(|c| c.is_ascii_uppercase() || c.is_ascii_digit() || c == b'_')
}

fn apply_data_masking_rules(
    params: &mut AnalysisParams,
    raw_rules: &str,
) -> anyhow::Result<()> {
    let rules: Vec<DataMaskingRule> =
        serde_json::from_str(raw_rules).map_err(|e| anyhow::anyhow!("invalid JSON: {e}"))?;
    if rules.is_empty() {
        anyhow::bail!("rule list is empty");
    }

    let mut entity_allowlist = BTreeSet::new();
    let mut ad_hoc_recognizers = Vec::new();
    let mut score_threshold: Option<f64> = None;
    for rule in rules {
        let mut rule_has_entities = false;

        for group in &rule.supported_entity_types {
            if group.entity_types.is_empty() {
                anyhow::bail!("supported entity group {:?} is empty", group.name);
            }
            for entity in &group.entity_types {
                if !is_canonical_entity_type(entity) {
                    anyhow::bail!("invalid entity type {entity:?}");
                }
                entity_allowlist.insert(entity.clone());
                rule_has_entities = true;
            }
        }

        for custom in rule.custom_entity_types {
            if !is_canonical_entity_type(&custom.name) {
                anyhow::bail!("invalid custom entity name {:?}", custom.name);
            }
            let regex = custom.regex.unwrap_or_default();
            if regex.is_empty() && custom.deny_list.is_empty() {
                anyhow::bail!(
                    "custom entity {:?} requires regex or deny_list",
                    custom.name
                );
            }
            let score = custom.score;
            if !(0.0..=1.0).contains(&score) || !score.is_finite() {
                anyhow::bail!("custom entity {:?} has invalid score {score}", custom.name);
            }
            entity_allowlist.insert(custom.name.clone());
            rule_has_entities = true;
            let patterns = if regex.is_empty() {
                Vec::new()
            } else {
                vec![AdHocRecognizerPattern {
                    name: custom.name.clone(),
                    regex,
                    score,
                }]
            };
            ad_hoc_recognizers.push(AdHocRecognizer {
                name: custom.name.clone(),
                supported_language: "en".into(),
                supported_entity: custom.name,
                deny_list: custom.deny_list,
                patterns,
            });
        }
        if rule_has_entities {
            let score = rule.score_threshold.unwrap_or(0.5);
            if !(0.0..=1.0).contains(&score) || !score.is_finite() {
                anyhow::bail!("invalid score threshold {score}");
            }
            score_threshold = Some(score_threshold.map_or(score, |current| current.min(score)));
        }
    }
    if entity_allowlist.is_empty() {
        anyhow::bail!("rules contain no entity types");
    }

    params.entity_allowlist = entity_allowlist.into_iter().collect();
    params.ad_hoc_recognizers = ad_hoc_recognizers;
    if let Some(score) = score_threshold {
        params.score_threshold = score;
    }
    Ok(())
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
        if let Some(rules) = metadata.get("data_masking_entity_data") {
            apply_data_masking_rules(&mut params, rules)
                .map_err(|e| anyhow::anyhow!("invalid data_masking_entity_data: {e}"))?;
        } else if let Some(list) = metadata.get("pii_entity_allowlist") {
            // Compatibility with gateways predating complete Data Masking
            // policy metadata. The full rule payload takes precedence.
            match serde_json::from_str::<Vec<String>>(list) {
                Ok(entities) => params.entity_allowlist = entities,
                Err(e) => warn!(%sid, "piigate: ignoring malformed pii_entity_allowlist: {e}"),
            }
        } else if let Some(list) = metadata.get("pii_entity_denylist") {
            // Compatibility with gateways predating connection-scoped
            // allowlists.
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
                    ("pii_entity_allowlist", r#"["DATE_TIME","PERSON"]"#),
                ]),
                "sid",
            )
            .expect("resolve ok")
            .expect("guard should be present");
            assert_eq!(cfg.presidio_url, "http://presidio:5001/");
            assert_eq!(cfg.ocr_url, "http://ocr:8868");
            assert_eq!(cfg.params.score_threshold, 0.75);
            assert_eq!(cfg.params.band_padding, 30);
            assert_eq!(cfg.params.entity_allowlist, vec!["DATE_TIME", "PERSON"]);
            assert!(cfg.params.entity_denylist.is_empty());
        });
    }

    #[test]
    fn resolves_legacy_gateway_denylist_when_allowlist_is_absent() {
        with_endpoints(Some("http://p"), Some("http://o"), || {
            let cfg = GuardConfig::resolve(
                &md(&[
                    ("pii_guard", "enabled"),
                    ("pii_entity_denylist", r#"["DATE_TIME","NRP"]"#),
                ]),
                "sid",
            )
            .unwrap()
            .unwrap();
            assert!(cfg.params.entity_allowlist.is_empty());
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
            assert_eq!(cfg.params.entity_allowlist, d.entity_allowlist);
        });
    }

    #[test]
    fn supports_pii_guard_requires_both_endpoints() {
        // Capability advertised to the gateway must be exactly the condition
        // `resolve` needs to build a guard: BOTH endpoints present.
        with_endpoints(Some("http://p"), Some("http://o"), || {
            assert!(supports_pii_guard(), "both endpoints -> capable");
        });
        with_endpoints(Some("http://p"), None, || {
            assert!(!supports_pii_guard(), "missing OCR -> incapable");
        });
        with_endpoints(None, Some("http://o"), || {
            assert!(!supports_pii_guard(), "missing Presidio -> incapable");
        });
        with_endpoints(None, None, || {
            assert!(!supports_pii_guard(), "neither -> incapable");
        });
    }

    #[test]
    fn supports_pii_guard_matches_resolve_buildability() {
        // The capability flag must never disagree with whether resolve can
        // actually build a guard, or the gateway and agent would make
        // contradictory decisions.
        for (p, o) in [
            (Some("http://p"), Some("http://o")),
            (Some("http://p"), None),
            (None, Some("http://o")),
            (None, None),
        ] {
            with_endpoints(p, o, || {
                let capable = supports_pii_guard();
                let resolve_builds =
                    GuardConfig::resolve(&md(&[("pii_guard", "enabled")]), "sid").is_ok();
                assert_eq!(
                    capable, resolve_builds,
                    "capability ({capable}) must match resolve buildability ({resolve_builds}) for p={p:?} o={o:?}"
                );
            });
        }
    }
    #[test]
    fn resolves_complete_data_masking_rules() {
        with_endpoints(Some("http://p"), Some("http://o"), || {
            let cfg = GuardConfig::resolve(
                &md(&[
                    ("pii_guard", "enabled"),
                    ("pii_score_threshold", "0.9"),
                    (
                        "data_masking_entity_data",
                        r#"[
                            {
                                "supported_entity_types": [
                                    {"name": "CONTACT_INFORMATION", "entity_types": ["PERSON", "DATE_TIME"]}
                                ],
                                "custom_entity_types": [
                                    {"name": "EMPLOYEE_ID", "regex": "EMP-[0-9]+", "score": 0.0},
                                    {"name": "VIP_NAME", "deny_list": ["Alice Example"], "score": 0.7}
                                ],
                                "score_threshold": 0.4
                            }
                        ]"#,
                    ),
                ]),
                "sid",
            )
            .unwrap()
            .unwrap();

            assert_eq!(cfg.params.score_threshold, 0.4);
            assert_eq!(
                cfg.params.entity_allowlist,
                vec!["DATE_TIME", "EMPLOYEE_ID", "PERSON", "VIP_NAME"]
            );
            assert_eq!(cfg.params.ad_hoc_recognizers.len(), 2);
            assert_eq!(
                cfg.params.ad_hoc_recognizers[0].patterns,
                vec![AdHocRecognizerPattern {
                    name: "EMPLOYEE_ID".into(),
                    regex: "EMP-[0-9]+".into(),
                    score: 0.0,
                }]
            );
            assert_eq!(
                cfg.params.ad_hoc_recognizers[1].deny_list,
                vec!["Alice Example"]
            );
        });
    }

    #[test]
    fn complete_data_masking_rules_take_precedence_over_legacy_keys() {
        with_endpoints(Some("http://p"), Some("http://o"), || {
            let cfg = GuardConfig::resolve(
                &md(&[
                    ("pii_guard", "enabled"),
                    ("pii_score_threshold", "0.9"),
                    ("pii_entity_allowlist", r#"["US_SSN"]"#),
                    (
                        "data_masking_entity_data",
                        r#"[{"supported_entity_types":[{"name":"TIME_DATA","entity_types":["DATE_TIME"]}],"score_threshold":0}]"#,
                    ),
                ]),
                "sid",
            )
            .unwrap()
            .unwrap();

            assert_eq!(cfg.params.score_threshold, 0.0);
            assert_eq!(cfg.params.entity_allowlist, vec!["DATE_TIME"]);
        });
    }

    #[test]
    fn invalid_complete_data_masking_rules_fail_closed() {
        with_endpoints(Some("http://p"), Some("http://o"), || {
            for rules in [
                "not-json",
                "[]",
                r#"[{"supported_entity_types":[{"name":"UNKNOWN"}]}]"#,
                r#"[{"custom_entity_types":[{"name":"EMPLOYEE_ID","score":0.8}]}]"#,
                r#"[{"custom_entity_types":[{"name":"employee-id","regex":"EMP-[0-9]+","score":0.8}]}]"#,
                r#"[{"custom_entity_types":[{"name":"EMPLOYEE_ID","regex":"EMP-[0-9]+","score":1.1}]}]"#,
                r#"[{"custom_entity_types":[{"name":"EMPLOYEE_ID","regex":"EMP-[0-9]+"}]}]"#,
                r#"[{"custom_entity_types":[{"name":"EMPLOYEE_ID","regex":"EMP-[0-9]+","score":null}]}]"#,
            ] {
                let err = GuardConfig::resolve(
                    &md(&[
                        ("pii_guard", "enabled"),
                        ("data_masking_entity_data", rules),
                    ]),
                    "sid",
                )
                .unwrap_err();
                assert!(
                    err.to_string().contains("invalid data_masking_entity_data"),
                    "unexpected error for {rules}: {err}"
                );
            }
        });
    }

    #[test]
    fn threshold_matches_gateway_rule_combination() {
        let mut params = AnalysisParams::default();
        apply_data_masking_rules(
            &mut params,
            r#"[
                {"score_threshold":0.1},
                {"supported_entity_types":[{"name":"TIME_DATA","entity_types":["DATE_TIME"]}]},
                {"supported_entity_types":[{"name":"CONTACT_INFORMATION","entity_types":["PERSON"]}],"score_threshold":0.9}
            ]"#,
        )
        .unwrap();

        assert_eq!(params.score_threshold, 0.5);
        assert_eq!(params.entity_allowlist, vec!["DATE_TIME", "PERSON"]);
    }

}
