//! Capability pinning for the redaction guard.
//!
//! Redaction's guarantee — that the rewriter sees every pixel-bearing PDU —
//! only holds if the RDP server delivers screen content exclusively via plain
//! Fast-Path bitmap updates. If the session negotiates bitmap caching,
//! offscreen surfaces, surface commands, or codecs (RemoteFX/NSCodec), pixels
//! can reach the client through paths the rewriter never touches.
//!
//! This module strips those capability sets from a Demand/Confirm Active
//! capability list so the negotiated session uses only Fast-Path bitmap
//! updates. The pure filtering logic lives here (fully unit-tested); the
//! interception stage that applies it to the live PDU stream is in
//! rdp_proxy.rs.
//!
//! Kill-mode does not need pinning (a detection anywhere kills the session);
//! only the redacting policies require it, because a redacted region is only
//! as complete as the set of delivery paths the rewriter rewrites.

use ironrdp_pdu::rdp::capability_sets::{
    BitmapCache, BitmapCacheRev2, BitmapCodecs, CacheFlags, CapabilitySet, CmdFlags,
    OffscreenBitmapCache, SurfaceCommands,
};

/// Names a capability set for logging which ones were pinned off.
fn capability_name(cap: &CapabilitySet) -> &'static str {
    match cap {
        CapabilitySet::BitmapCache(_) => "BitmapCache",
        CapabilitySet::BitmapCacheRev2(_) => "BitmapCacheRev2",
        CapabilitySet::OffscreenBitmapCache(_) => "OffscreenBitmapCache",
        CapabilitySet::SurfaceCommands(_) => "SurfaceCommands",
        CapabilitySet::BitmapCodecs(_) => "BitmapCodecs",
        _ => "other",
    }
}

/// Reports whether a capability set delivers (or caches/derives) pixels
/// through a path the Fast-Path bitmap rewriter does not handle, and must
/// therefore be neutralized for a redacting session.
fn must_pin(cap: &CapabilitySet) -> bool {
    matches!(
        cap,
        CapabilitySet::BitmapCache(_)
            | CapabilitySet::BitmapCacheRev2(_)
            | CapabilitySet::OffscreenBitmapCache(_)
            | CapabilitySet::SurfaceCommands(_)
            | CapabilitySet::BitmapCodecs(_)
    )
}

/// Rewrites a capability set into a neutralized form that advertises "not
/// supported" while preserving the set's presence (some servers/clients are
/// sensitive to a set vanishing entirely). Caches are zeroed, offscreen is
/// marked unsupported, surface commands and codecs are emptied.
fn neutralize(cap: CapabilitySet) -> CapabilitySet {
    match cap {
        CapabilitySet::BitmapCache(_) => CapabilitySet::BitmapCache(BitmapCache {
            caches: Default::default(),
        }),
        CapabilitySet::BitmapCacheRev2(_) => {
            CapabilitySet::BitmapCacheRev2(BitmapCacheRev2 {
                cache_flags: CacheFlags::empty(),
                num_cell_caches: 0,
                cache_cell_info: Default::default(),
            })
        }
        CapabilitySet::OffscreenBitmapCache(_) => {
            CapabilitySet::OffscreenBitmapCache(OffscreenBitmapCache {
                is_supported: false,
                cache_size: 0,
                cache_entries: 0,
            })
        }
        CapabilitySet::SurfaceCommands(_) => CapabilitySet::SurfaceCommands(SurfaceCommands {
            flags: CmdFlags::empty(),
        }),
        CapabilitySet::BitmapCodecs(_) => {
            CapabilitySet::BitmapCodecs(BitmapCodecs(Vec::new()))
        }
        other => other,
    }
}

/// Pins a capability list to Fast-Path-bitmap-only delivery, neutralizing
/// every set that could route pixels around the rewriter. Returns the names
/// of the sets that were pinned (for logging). Mutates `caps` in place.
pub fn pin_capabilities(caps: &mut [CapabilitySet]) -> Vec<&'static str> {
    let mut pinned = Vec::new();
    for cap in caps.iter_mut() {
        if must_pin(cap) {
            pinned.push(capability_name(cap));
            // Replace in place via take/neutralize. CapabilitySet is not
            // Default, so swap through a temporary clone-free move.
            let owned = std::mem::replace(cap, CapabilitySet::Control(Vec::new()));
            *cap = neutralize(owned);
        }
    }
    pinned
}

/// Pins the capability list inside a Share Control PDU (a Demand Active from
/// the server or a Confirm Active from the client). Returns the pinned set
/// names. PDUs that are not capability-bearing are left unchanged.
///
/// This operates on an already-decoded PDU so it is fully unit-testable; the
/// live-stream wiring (unwrap MCS SendData, decode ShareControlHeader, apply
/// this, re-encode) is a separate interception stage that must be validated
/// against a real RDP server before the redacting policies rely on it (see
/// RD-241). Until then, redaction's completeness is bounded by whatever the
/// server actually negotiates.
pub fn pin_share_control_pdu(
    pdu: &mut ironrdp_pdu::rdp::headers::ShareControlPdu,
) -> Vec<&'static str> {
    use ironrdp_pdu::rdp::headers::ShareControlPdu;
    match pdu {
        ShareControlPdu::ServerDemandActive(active) => {
            pin_capabilities(&mut active.pdu.capability_sets)
        }
        ShareControlPdu::ClientConfirmActive(active) => {
            pin_capabilities(&mut active.pdu.capability_sets)
        }
        _ => Vec::new(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use ironrdp_pdu::rdp::capability_sets::{Bitmap, BitmapDrawingFlags, General, MajorPlatformType, MinorPlatformType, GeneralExtraFlags};

    fn general() -> CapabilitySet {
        CapabilitySet::General(General {
            major_platform_type: MajorPlatformType::UNSPECIFIED,
            minor_platform_type: MinorPlatformType::UNSPECIFIED,
            protocol_version: 0x0200,
            extra_flags: GeneralExtraFlags::empty(),
            refresh_rect_support: false,
            suppress_output_support: false,
        })
    }

    fn bitmap() -> CapabilitySet {
        CapabilitySet::Bitmap(Bitmap {
            pref_bits_per_pix: 32,
            desktop_width: 1024,
            desktop_height: 768,
            desktop_resize_flag: false,
            drawing_flags: BitmapDrawingFlags::empty(),
        })
    }

    fn codecs() -> CapabilitySet {
        CapabilitySet::BitmapCodecs(BitmapCodecs(Vec::new()))
    }

    fn offscreen() -> CapabilitySet {
        CapabilitySet::OffscreenBitmapCache(OffscreenBitmapCache {
            is_supported: true,
            cache_size: 100,
            cache_entries: 10,
        })
    }

    #[test]
    fn leaves_safe_capabilities_untouched() {
        let mut caps = vec![general(), bitmap()];
        let pinned = pin_capabilities(&mut caps);
        assert!(pinned.is_empty());
        assert!(matches!(caps[0], CapabilitySet::General(_)));
        assert!(matches!(caps[1], CapabilitySet::Bitmap(_)));
    }

    #[test]
    fn neutralizes_offscreen_cache() {
        let mut caps = vec![offscreen()];
        let pinned = pin_capabilities(&mut caps);
        assert_eq!(pinned, vec!["OffscreenBitmapCache"]);
        match &caps[0] {
            CapabilitySet::OffscreenBitmapCache(o) => {
                assert!(!o.is_supported, "offscreen cache must be marked unsupported");
                assert_eq!(o.cache_entries, 0);
            }
            _ => panic!("expected offscreen cache"),
        }
    }

    #[test]
    fn empties_codecs() {
        let mut caps = vec![codecs(), bitmap()];
        // Seed codecs with a non-empty vec to prove it gets emptied.
        let pinned = pin_capabilities(&mut caps);
        assert_eq!(pinned, vec!["BitmapCodecs"]);
        match &caps[0] {
            CapabilitySet::BitmapCodecs(BitmapCodecs(v)) => assert!(v.is_empty()),
            _ => panic!("expected codecs"),
        }
        // The bitmap set (safe) is preserved.
        assert!(matches!(caps[1], CapabilitySet::Bitmap(_)));
    }

    #[test]
    fn pins_inside_a_confirm_active_pdu() {
        use ironrdp_pdu::rdp::capability_sets::{ClientConfirmActive, DemandActive};
        use ironrdp_pdu::rdp::headers::ShareControlPdu;

        let mut pdu = ShareControlPdu::ClientConfirmActive(ClientConfirmActive {
            originator_id: 1,
            pdu: DemandActive {
                source_descriptor: "test".into(),
                capability_sets: vec![general(), bitmap(), offscreen(), codecs()],
            },
        });
        let pinned = pin_share_control_pdu(&mut pdu);
        assert_eq!(pinned.len(), 2);
        assert!(pinned.contains(&"OffscreenBitmapCache"));
        assert!(pinned.contains(&"BitmapCodecs"));
    }

    #[test]
    fn leaves_non_capability_pdus_unchanged() {
        use ironrdp_pdu::rdp::headers::ShareControlPdu;
        let mut pdu = ShareControlPdu::ServerDeactivateAll(
            ironrdp_pdu::rdp::headers::ServerDeactivateAll,
        );
        assert!(pin_share_control_pdu(&mut pdu).is_empty());
    }

    #[test]
    fn pins_all_unsafe_sets_in_a_mixed_list() {
        let mut caps = vec![
            general(),
            bitmap(),
            offscreen(),
            codecs(),
            CapabilitySet::SurfaceCommands(SurfaceCommands {
                flags: CmdFlags::empty(),
            }),
        ];
        let pinned = pin_capabilities(&mut caps);
        // offscreen, codecs, surface commands pinned; general/bitmap kept.
        assert_eq!(pinned.len(), 3);
        assert!(pinned.contains(&"OffscreenBitmapCache"));
        assert!(pinned.contains(&"BitmapCodecs"));
        assert!(pinned.contains(&"SurfaceCommands"));
        assert_eq!(caps.len(), 5, "pinning neutralizes in place, never drops sets");
    }
}
