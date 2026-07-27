//! Dirty-band accumulation for incremental framebuffer analysis.
//!
//! Port of `gateway/rdp/analyzer/region.go` (DirtyBands / YBand /
//! splitBands). The semantics are identical by design: the Go gateway gate
//! and this agent-side gate must enforce the same batch-splitting rule.

/// A horizontal band of framebuffer rows `[y0, y1)`.
///
/// Bands always span the full canvas width: on-screen text is horizontal, so
/// a full-width band contains complete text lines and is a contiguous
/// (zero-copy) slice of an RGBA framebuffer.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct YBand {
    pub y0: usize,
    pub y1: usize,
}

impl YBand {
    pub fn height(&self) -> usize {
        self.y1 - self.y0
    }
}

/// Default vertical padding (in pixels) applied around dirty rectangles. It
/// must cover at least one text line height so a partially repainted line is
/// always OCR'd in full.
pub const DEFAULT_BAND_PADDING: usize = 24;

/// Accumulates the vertical extents of dirty rectangles between two analysis
/// snapshots. Each rect is expanded by a vertical padding (so text lines
/// touching the dirty area are fully covered) and merged with adjacent or
/// overlapping bands.
///
/// The type does no locking: it is owned by the gate's analysis task.
#[derive(Debug)]
pub struct DirtyBands {
    canvas_height: usize,
    pad: usize,
    bands: Vec<YBand>, // sorted by y0, non-overlapping, non-adjacent
}

impl DirtyBands {
    /// Creates an accumulator for a canvas of the given height. `pad` is the
    /// vertical padding applied around each dirty rect; 0 falls back to
    /// [`DEFAULT_BAND_PADDING`]. Use the same value as the analysis chunk
    /// overlap so band accumulation and chunking follow one padding policy.
    pub fn new(canvas_height: usize, pad: usize) -> Self {
        let pad = if pad == 0 { DEFAULT_BAND_PADDING } else { pad };
        Self {
            canvas_height,
            pad,
            bands: Vec::new(),
        }
    }

    /// Pads and clamps a rect's vertical extent. Returns None when the
    /// padded extent is empty after clamping. Geometry originates from
    /// wire-format u16s, but checked arithmetic keeps this total even if an
    /// upstream type widens or a caller passes a hostile value — a pathologic
    /// extent saturates to the canvas rather than panicking in debug or
    /// wrapping in release.
    fn padded(&self, y: usize, height: usize) -> Option<YBand> {
        if height == 0 {
            return None;
        }
        let y0 = y.saturating_sub(self.pad);
        let y1 = y
            .checked_add(height)
            .and_then(|v| v.checked_add(self.pad))
            .unwrap_or(usize::MAX)
            .min(self.canvas_height);
        if y0 >= y1 {
            return None;
        }
        Some(YBand { y0, y1 })
    }

    /// Records a dirty rectangle's vertical extent (x is ignored: bands span
    /// the full width). The extent is padded and merged into the band list.
    pub fn add_rect(&mut self, y: usize, height: usize) {
        let Some(band) = self.padded(y, height) else {
            return;
        };

        // Insert keeping the list sorted, then merge overlapping/adjacent.
        let idx = self
            .bands
            .iter()
            .position(|b| band.y0 < b.y0)
            .unwrap_or(self.bands.len());
        self.bands.insert(idx, band);

        let mut merged: Vec<YBand> = Vec::with_capacity(self.bands.len());
        for &b in &self.bands {
            match merged.last_mut() {
                Some(last) if b.y0 <= last.y1 => {
                    if b.y1 > last.y1 {
                        last.y1 = b.y1;
                    }
                }
                _ => merged.push(b),
            }
        }
        self.bands = merged;
    }

    /// Reports whether a rect's vertical extent, padded with the same policy
    /// as [`add_rect`](Self::add_rect), overlaps any accumulated band. This
    /// is the conflict test used to split hold-and-release batches so no
    /// intermediate screen state escapes analysis.
    pub fn intersects(&self, y: usize, height: usize) -> bool {
        let Some(band) = self.padded(y, height) else {
            return false;
        };
        self.bands
            .iter()
            .any(|b| band.y0 < b.y1 && b.y0 < band.y1)
    }

    pub fn is_empty(&self) -> bool {
        self.bands.is_empty()
    }

    /// Returns the current bands and clears the accumulator — the
    /// snapshot-time counterpart to analyzing the framebuffer.
    pub fn take_and_reset(&mut self) -> Vec<YBand> {
        std::mem::take(&mut self.bands)
    }
}

/// A band slice prepared for one OCR invocation. The window is what gets
/// OCR'd; ownership decides which detected words are kept, so words in the
/// overlap between adjacent chunks are attributed to exactly one chunk.
#[derive(Debug, Clone, Copy)]
pub struct OcrChunk {
    /// Rows passed to OCR (ownership expanded by pad, clamped to the band).
    pub win: YBand,
    /// Rows this chunk owns: keep words whose vertical center falls here.
    pub own: YBand,
}

impl OcrChunk {
    /// Reports whether the chunk owns a word whose coordinates are already
    /// in full-screen space.
    pub fn owns(&self, top: usize, height: usize) -> bool {
        let center = top + height / 2;
        center >= self.own.y0 && center < self.own.y1
    }
}

/// Maximum height of a single OCR invocation. Bands taller than this (e.g. a
/// full-screen repaint) are split into chunks OCR'd in parallel, bounding the
/// worst-case OCR latency by the chunk cost instead of the full canvas cost.
pub const MAX_CHUNK_ROWS: usize = 256;

/// Slices bands taller than `max_rows` into chunks with `pad` rows of overlap
/// on each side, so a text line straddling a chunk boundary is fully visible
/// to the chunk that owns its center.
pub fn split_bands(bands: &[YBand], max_rows: usize, pad: usize) -> Vec<OcrChunk> {
    let mut chunks = Vec::new();
    for &band in bands {
        if band.height() <= max_rows + pad {
            chunks.push(OcrChunk {
                win: band,
                own: band,
            });
            continue;
        }
        let mut y = band.y0;
        while y < band.y1 {
            let own = YBand {
                y0: y,
                y1: (y + max_rows).min(band.y1),
            };
            let win = YBand {
                y0: own.y0.saturating_sub(pad).max(band.y0),
                y1: (own.y1 + pad).min(band.y1),
            };
            chunks.push(OcrChunk { win, own });
            y += max_rows;
        }
    }
    chunks
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn add_rect_pads_and_merges() {
        let mut d = DirtyBands::new(1000, 24);
        d.add_rect(100, 10); // [76, 134)
        d.add_rect(120, 10); // [96, 154) — overlaps, merges
        assert_eq!(d.take_and_reset(), vec![YBand { y0: 76, y1: 154 }]);
    }

    #[test]
    fn add_rect_keeps_disjoint_bands_sorted() {
        let mut d = DirtyBands::new(1000, 24);
        d.add_rect(500, 8);
        d.add_rect(100, 8);
        assert_eq!(
            d.take_and_reset(),
            vec![YBand { y0: 76, y1: 132 }, YBand { y0: 476, y1: 532 }]
        );
    }

    #[test]
    fn add_rect_clamps_to_canvas() {
        let mut d = DirtyBands::new(100, 24);
        d.add_rect(0, 8); // y0 saturates at 0
        d.add_rect(90, 50); // y1 clamps to 100
        let bands = d.take_and_reset();
        assert_eq!(bands.first().unwrap().y0, 0);
        assert_eq!(bands.last().unwrap().y1, 100);
    }

    #[test]
    fn add_rect_ignores_empty() {
        let mut d = DirtyBands::new(100, 24);
        d.add_rect(50, 0);
        assert!(d.is_empty());
        // Fully beyond the canvas: padded extent clamps to empty.
        let mut d = DirtyBands::new(100, 0);
        d.add_rect(200, 10);
        assert!(d.is_empty());
    }

    #[test]
    fn intersects_uses_same_padding_policy() {
        let mut d = DirtyBands::new(1000, 24);
        d.add_rect(100, 10); // [76, 134)
        // Padded probe [276-24, 286+24) = [252, 310): no overlap.
        assert!(!d.intersects(276, 10));
        // Probe at 150: padded [126, 198) overlaps [76, 134).
        assert!(d.intersects(150, 10));
        // Same-line repaint trivially intersects.
        assert!(d.intersects(100, 10));
        // Empty probe never intersects.
        assert!(!d.intersects(100, 0));
    }

    #[test]
    fn intersects_empty_accumulator() {
        let d = DirtyBands::new(1000, 24);
        assert!(!d.intersects(100, 10));
    }

    #[test]
    fn split_bands_short_band_is_one_chunk() {
        let bands = [YBand { y0: 10, y1: 200 }];
        let chunks = split_bands(&bands, 256, 24);
        assert_eq!(chunks.len(), 1);
        assert_eq!(chunks[0].win, bands[0]);
        assert_eq!(chunks[0].own, bands[0]);
    }

    #[test]
    fn split_bands_tall_band_overlapping_windows() {
        let bands = [YBand { y0: 0, y1: 600 }];
        let chunks = split_bands(&bands, 256, 24);
        assert_eq!(chunks.len(), 3);
        // Ownership tiles the band exactly.
        assert_eq!(chunks[0].own, YBand { y0: 0, y1: 256 });
        assert_eq!(chunks[1].own, YBand { y0: 256, y1: 512 });
        assert_eq!(chunks[2].own, YBand { y0: 512, y1: 600 });
        // Windows overlap by pad, clamped to the band.
        assert_eq!(chunks[0].win, YBand { y0: 0, y1: 280 });
        assert_eq!(chunks[1].win, YBand { y0: 232, y1: 536 });
        assert_eq!(chunks[2].win, YBand { y0: 488, y1: 600 });
    }

    #[test]
    fn chunk_ownership_by_center() {
        let c = OcrChunk {
            win: YBand { y0: 232, y1: 536 },
            own: YBand { y0: 256, y1: 512 },
        };
        assert!(c.owns(250, 20)); // center 260 in [256,512)
        assert!(!c.owns(240, 20)); // center 250 below own
        assert!(!c.owns(510, 20)); // center 520 above own
    }
}
