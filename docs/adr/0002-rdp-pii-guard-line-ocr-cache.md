# ADR-0002: Line-level OCR cache for the RDP PII guard

- **Status:** Proposed
- **Date:** 2026-06-26
- **Related:** PR #1555 (windowed latency metrics), PR #1557 (OCR sidecar timing), PR #1558 (skip byte-identical repaints)

## Context

The realtime RDP PII guard holds server→client frames, OCRs the changed
screen regions, runs Presidio over the recognized text, and redacts/kills on
detection before pixels reach the client. It runs in two places with identical
semantics: the agent (`agentrs/src/piigate/`, Rust) and the gateway
(`gateway/rdp/`, Go). The agent path is the one used for capable agents (the
GPU node); the gateway path is the kill-only fallback.

### Measured cost (GCP T4 node, verified, stable across trials)

For a representative 24-line, 1900×800 dirty band:

| Component         | Time      | Detail                                            |
|-------------------|-----------|---------------------------------------------------|
| **Total**         | **~390ms**|                                                   |
| ONNX inference    | ~270ms 69%| det ~43ms + 4 rec runs of `(6,3,48,~910)` ~57ms ea|
| Python overhead   | ~122ms 31%| normalize, crops, CTC decode, DB postprocess      |
| Presidio          | ~25ms     | fine                                              |
| composite/redact  | ~2ms      | noise                                             |

Key facts established by profiling:

- **Recognition (rec) on full-width line crops is the floor (~228ms of the
  ~270ms inference).** Cost scales with crop count × crop width. Crops are
  full screen-width text lines (~910px), batched 6 per run, 4 runs.
- det (~43ms), Presidio (~25ms), compositing and redaction are not worth
  optimizing.
- Downscaling the input is **ruled out**: it corrupts small fonts (a guard
  that misses PII is worse than a slow one) and there is no safe fixed scale
  across unknown client resolutions.
- The existing per-window OCR cache (`OcrCache` in `analyze.rs`) is
  **architecturally ineffective**: it keys on a hash of the *entire dirty
  band's* pixels and is replaced every analysis. A dirty band is, by
  construction, a region that just changed, and it spans the full width — so
  any one-pixel change (caret, clock) busts the whole band's hash. Production
  logs showed `ocr_calls ≈ ocr_chunks` (≈ zero reuse).
- PR #1558 already skips OCR for byte-identical repaints (RDP resends
  unchanged tiles). That helps **static** screens. It does **not** help the
  common case where a small part of a screen changes (one typed line, one new
  log row) while most lines are unchanged: today the whole band is re-OCR'd.

### The opportunity

On-screen text is horizontal; edits almost always touch **one line** while the
surrounding lines are unchanged. If OCR results are cached **per text line**
and only lines whose pixels changed are re-OCR'd, rec work drops roughly in
proportion to how little of the band actually changed.

### Measured win (prototype on the real engine, GCP T4 node)

A simulation calling the real RapidOCR det + rec stages the way a line-cache
would (det over the band, rec only the changed lines) on a 24-line 1900×800
band:

| Lines changed (of 24) | rec time | frame total | vs current (~387ms) |
|-----------------------|----------|-------------|---------------------|
| 1                     | 11ms     | ~130ms      | **66% faster**      |
| 2                     | 20ms     | ~139ms      | **64% faster**      |
| 4                     | 43ms     | ~162ms      | **58% faster**      |
| 12 (half)             | 151ms    | ~270ms      | **30% faster**      |
| 24 (full repaint)     | 268ms    | ~387ms      | 0% (never worse)    |

Additionally, **det scales linearly with dirty-band height** (1900×800 →
118ms, 1900×200 → 25ms, 1900×100 → 14ms). Since the dirty band is only the
changed region, a small edit yields BOTH a small/cheap det AND few re-OCR'd
lines. A realistic one-line edit (small band) is therefore
~det 20ms + rec 11ms + presidio 25ms ≈ **~55ms**, versus ~387ms today.

The cache never makes a frame slower than today (a full-screen change misses
every line = current cost). The remaining floor for full-screen-changing
content (video) is inherent to OCR-everything and unavoidable without
downscaling (which is rejected — see Alternatives).

## Decision

Replace the whole-band `OcrCache` with a **persistent, line-level OCR cache**,
where the *changed region* is detected at RDP tile granularity but OCR is always
performed on full-width lines.

### Change detection: RDP tiles; OCR unit: full-width lines

RDP delivers screen changes as **rectangular bitmap tiles** (commonly fixed
squares, e.g. 64×64), not arbitrary regions. This makes "what changed" precise
and cheap: the set of tiles in a frame's bitmap updates *is* the dirty region.

However, OCR **must not** be performed per tile. Screen text runs horizontally
and routinely spans many tiles, so OCRing a tile in isolation shreds any word
that crosses its boundary. Measured on the box, a line containing
`john.doe@example.com` OCR'd per 64px tile yields fragments
(`...ohn.d | be@e) | ample | .com...`) — Presidio would never see the email and
the PII would be **missed**. The same line OCR'd full-width reads perfectly.
Per-tile OCR is therefore a security regression and is rejected.

The reconciliation: **changed tiles select which full-width lines are dirty**;
those lines (and only those) are re-OCR'd, the rest are served from the
line cache. A one-tile edit (a typed character) dirties the 1–2 lines that tile
intersects, so OCR work collapses to those lines.

The mechanism is **geometry-agnostic**: it does not assume any particular tile
size. Each `BitmapPatch` already carries its `(x, y, width, height)` (extracted
in `framing.rs`), and PR #1558's pixel-compare already establishes whether a
patch actually changed anything. The dirty lines are simply the full-width rows
spanned by the changed patches' vertical extents (with the existing band
padding so a partially-touched text line is fully covered). Whether the server
sends 64×64 squares, RemoteFX tiles, or arbitrary rectangles, the rule is the
same.

### Unit: the horizontal text line

The cache granularity is a single text line (a full-width row slice of bounded
height). Lines are the natural stable unit for screen text and align with how
rec already consumes crops (height-48 line crops).

Line segmentation reuses the **detection (det) stage**, which already runs over
the full band and returns line boxes. We do **not** invent a separate line
finder: det defines the lines, rec is what we cache and skip.

### Cache key and value

- **Key:** `(line_y0, line_y1, x0, x1, pixel_hash)` — the line's geometry in
  full-canvas coordinates plus a hash of that line's **exact pixel bytes**.
- **Value:** the recognized `Vec<Word>` for that line, in full-canvas
  coordinates.

The pixel hash over the line's exact bytes is the load-bearing safety
property: **any** pixel change in a line misses the cache and forces a fresh
rec on that line. A changed line can never reuse stale words.

### Flow per analyzed band

1. Run **det** over the dirty band (unchanged — det is cheap and must see the
   whole band to find line boxes correctly).
2. For each detected line box:
   - Hash the line's pixels.
   - **Cache hit** (geometry + hash match) → reuse cached words. No rec call.
   - **Cache miss** → rec that line, store the result.
3. Concatenate all lines' words (cached + freshly rec'd) in reading order →
   the same text Presidio sees today.
4. Run Presidio (unchanged).
5. Publish the new cache snapshot: the lines analyzed this frame, keyed by
   geometry+hash. The cache is **persistent across frames** (not cleared each
   call) but bounded (see below).

### Why this preserves the security invariant

The guarantee is unchanged: *every forwarded pixel was analyzed in its final
on-screen position.* This holds because:

- det still runs over the full dirty band every frame, so no line is ever
  missed at the segmentation stage.
- A line is reused **only** when its pixels are byte-identical to when it was
  rec'd — i.e. it shows exactly the content that was already analyzed. There is
  no new content in a reused line.
- A changed line (any pixel differs) misses and is freshly rec'd, so new PII is
  always read.
- det runs on the full band, so a line that *appears* (new content in
  previously-blank rows) is detected and, having no cache entry, is rec'd.
- Hash collision (two different line images, same 64-bit hash) is the only
  theoretical reuse-of-stale risk. Mitigations: the geometry is part of the
  key (so only same-position, same-size lines can collide), the hash covers
  every byte, and the failure mode is a single-frame miss that self-corrects on
  the next changed frame (it does not persistently blind the guard). This is
  the same residual risk the existing cache already accepts, now scoped to a
  line instead of a band.

### Cache lifetime and bounds

- **Persistent across frames** (the whole point — unchanged lines stay cached).
- **Bounded** by entry count and total bytes; LRU eviction. A screen has
  O(tens) of lines, so the working set is small. Sizing TBD during
  implementation, with a hard cap to prevent unbounded growth on pathological
  streams.
- Keyed in full-canvas coordinates so scrolling (lines moving vertically)
  misses and re-OCRs — correct, since a scrolled line is at a new position and
  its surrounding context changed. (A future optimization could detect vertical
  shifts; out of scope here.)

### Parity

The gateway (Go) gate must implement the identical scheme. Both gates enforce
the same guarantee and are tested with the same adversarial leak suite. If the
implementations risk diverging, the agent path is authoritative (it is the one
used for capable agents); the gateway path may land in a follow-up as long as
the gateway gate's existing (whole-band) behavior remains correct in the
interim.

## Consequences

### Positive

- Rec work drops in proportion to the fraction of lines that changed. For
  typical interactive use (one line edited per frame), the ~228ms rec cost
  collapses toward one line's cost.
- det, Presidio, and the security model are unchanged.
- Complements PR #1558: #1558 skips frames where *nothing* changed; this skips
  the *unchanged lines* within frames where something did.

### Negative / risks

- **Complexity on a security-critical path.** Line segmentation, word
  ownership across line boundaries, and coordinate bookkeeping must be exactly
  right. Mitigated by: reusing det for segmentation, a comprehensive leak-test
  suite (identical-line reuse, changed-line re-OCR, appearing/disappearing
  lines, scrolling, hash-collision simulation), and review.
- **Worst case unchanged.** A full-screen change (video, full repaint, scroll)
  misses every line and costs the same as today. Acceptable: the cache never
  makes things slower (a miss is exactly today's path), it only makes the
  common case faster.
- **Does not touch the ~122ms Python overhead** or the inherent det cost. A
  native OCR server (separate decision) addresses those; this ADR addresses the
  rec floor for partially-changed screens.

## Alternatives considered

- **Tile-grid cache** (fixed e.g. 256px tiles): simpler geometry but words
  spanning tile boundaries need stitching, and a full-width line spans many
  tiles so a one-char edit still re-OCRs a whole tile row. Worse fit for
  horizontal text than line granularity.
- **Fix the existing whole-band cache** (persist it, narrow the bands): minimal
  change but still busts on any sub-band change; does not deliver the win.
- **Downscaling**: rejected (accuracy + unknown client resolution).
- **Native OCR server first**: a complementary, separate decision. It removes
  the ~122ms Python overhead and GIL serialization (~390→~270ms) but not the
  rec floor for partially-changed screens. The two are orthogonal; this ADR can
  land independently.

## Implementation notes (non-binding)

- Build on the existing `Word`/`OcrChunk`/`own_words` machinery in
  `agentrs/src/piigate/analyze.rs` and `bands.rs`.
- The current `BandAnalyzer::analyze` already splits bands into chunks and runs
  OCR per chunk; the change is to (a) segment by det line boxes, (b) consult a
  persistent line cache before rec, (c) stop clearing the cache each call.
- Keep the `OcrExtract` diagnostics (server_ms, bytes) and the
  `record_ocr_detail` / `paints_*` metrics so the cache hit rate is observable
  in the same windowed latency log.
- Add a `lines_total` / `lines_recognized` (cache-miss) metric pair mirroring
  the existing `paints_total` / `paints_changed`, so the line-cache hit rate is
  visible in production immediately.
