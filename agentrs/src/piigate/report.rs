//! Reporting PII guard violations from the agent back to the gateway.
//!
//! When the gate detects PII (or fails closed on overload), the agent sends
//! a `guardrails_violation` control message to the gateway carrying entity
//! metadata — entity types, confidence scores, and on-screen bounding boxes.
//! No raw pixels and no recognized OCR text are ever sent (the metadata is
//! still sensitive — it discloses which PII categories were found and roughly
//! where — so the transport stays authenticated). The gateway persists this
//! as guardrails info + per-entity detection rows for audit, exactly as the
//! gateway-side gate's persistPIIViolation does.

use serde::Serialize;

use super::presidio::{EntityDetection, SnapshotResult};

/// The reason a session was terminated by the guard.
#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ViolationKind {
    /// PII detected on screen.
    Detection,
    /// Analysis backlog overflow (fail-closed).
    Overload,
}

/// The violation report payload sent to the gateway in the
/// `guardrails_violation` message body (JSON). Field names are stable wire
/// contract — the gateway deserializes this directly.
#[derive(Debug, Clone, Serialize)]
pub struct ViolationReport {
    pub kind: ViolationKind,
    /// Entity types detected (empty for an overload). Sorted, deduplicated.
    pub entity_types: Vec<String>,
    /// Per-entity detections with screen-space bounding boxes (empty for an
    /// overload). Shapes match the gateway's RDPEntityDetection json tags.
    pub detections: Vec<EntityDetection>,
    /// Bytes dropped, for an overload (0 for a detection).
    pub dropped_bytes: usize,
}

impl ViolationReport {
    /// Builds a detection report from a gate snapshot result.
    pub fn detection(res: &SnapshotResult) -> Self {
        let mut entity_types: Vec<String> = res.counts.keys().cloned().collect();
        entity_types.sort();
        Self {
            kind: ViolationKind::Detection,
            entity_types,
            detections: res.detections.clone(),
            dropped_bytes: 0,
        }
    }

    /// Builds an overload report (fail-closed; no entity metadata).
    pub fn overload(dropped_bytes: usize) -> Self {
        Self {
            kind: ViolationKind::Overload,
            entity_types: Vec::new(),
            detections: Vec::new(),
            dropped_bytes,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn detection_report_sorts_entity_types() {
        let mut res = SnapshotResult::default();
        res.counts.insert("PERSON".into(), 1);
        res.counts.insert("EMAIL_ADDRESS".into(), 2);
        res.detections.push(EntityDetection {
            entity_type: "EMAIL_ADDRESS".into(),
            score: 0.95,
            x: 10,
            y: 20,
            width: 100,
            height: 12,
        });
        let report = ViolationReport::detection(&res);
        assert_eq!(report.kind, ViolationKind::Detection);
        assert_eq!(report.entity_types, vec!["EMAIL_ADDRESS", "PERSON"]);
        assert_eq!(report.detections.len(), 1);
        assert_eq!(report.dropped_bytes, 0);
    }

    #[test]
    fn overload_report_has_no_entities() {
        let report = ViolationReport::overload(4096);
        assert_eq!(report.kind, ViolationKind::Overload);
        assert!(report.entity_types.is_empty());
        assert!(report.detections.is_empty());
        assert_eq!(report.dropped_bytes, 4096);
    }

    #[test]
    fn report_serializes_with_stable_field_names() {
        let report = ViolationReport::overload(10);
        let json = serde_json::to_string(&report).unwrap();
        assert!(json.contains(r#""kind":"overload""#));
        assert!(json.contains(r#""dropped_bytes":10"#));
    }
}
