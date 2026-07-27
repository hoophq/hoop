//! Connection-scoped control framing shared between the agent and gateway.
//!
//! Most agent<->gateway frames are addressed to a specific RDP session via the
//! 16-byte sid in the header. A few frames are *connection*-scoped instead —
//! they describe the agent connection itself, not any one session (e.g. the
//! capability advertisement sent right after connect).
//!
//! The wire format requires a non-nil, versioned UUID in every header (the
//! gateway rejects nil/version-0 sids), so connection-scoped frames are
//! addressed with this well-known sentinel sid. Both sides agree it never
//! identifies a real session; the gateway dispatches these frames by message
//! type at the connection level before any session lookup.
//!
//! Keep this value byte-for-byte identical to the gateway constant
//! `broker.ControlSentinelSID`.

use uuid::Uuid;

/// Well-known sentinel sid for connection-scoped control frames. A fixed v4
/// UUID — non-nil and version 4 — so it passes header validation while never
/// colliding with a real (randomly generated) session id.
pub const CONTROL_SENTINEL_SID: Uuid = Uuid::from_bytes([
    0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x4c, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01,
]);

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sentinel_is_non_nil_versioned() {
        // The gateway rejects nil and version-0 sids in DecodeHeader; the
        // sentinel must pass that validation.
        assert!(!CONTROL_SENTINEL_SID.is_nil());
        assert_eq!(CONTROL_SENTINEL_SID.get_version_num(), 4);
    }

    #[test]
    fn sentinel_matches_gateway_constant() {
        // Must stay byte-for-byte identical to broker.ControlSentinelSID.
        assert_eq!(
            CONTROL_SENTINEL_SID.to_string(),
            "00000000-0000-4c00-8000-000000000001"
        );
    }
}
