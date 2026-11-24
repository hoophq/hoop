# Temporary ICA diagnostic recording

The sidecar embeds libhoop-ica **v0.11.0** and can record an explicitly authorized
TCP reproduction before ICA decoding. The client uses the same Gateway/DNS alias,
sidecar listener and VDA destination. Recording bypasses ICA decoding, OCR, NER,
PII matching and masking; both directional streams can contain credentials,
tickets, clipboard data and screen contents. Use a controlled session containing
synthetic data and an approved private support handoff.

Recording is **off by default**. Add this block to the existing CPU or GPU config,
preserving its model/assets, policy, listener/source and VDA routing settings:

```yaml
admin:
  listen: 127.0.0.1:19000
  ica_readiness_json: true

ica:
  diagnostic_recording:
    acknowledge_unredacted: true
    until: "REPLACE-WITH-ABSOLUTE-RFC3339-EXPIRY"
    output_dir: /var/lib/hoop/ica-diagnostics/reproduction-001
    max_bytes: 536870912
    max_sessions: 3
    session_timeout_sec: 900
```

Replace the expiry with a timestamp at most one hour in the future; do not reuse
an example date. It is absolute, not a restartable duration. `max_sessions` in this
block is the total number of recorded admissions, not concurrency: the recorder
always permits only one raw connection at a time across all ICA lanes. These
settings are separate from `ica.max_sessions`, which retains the normal process
capacity bound. Zero/omitted limits use the engine's defaults (512 MiB total stream
bytes, three admissions, 15 minutes per session). The supported maxima are 1 GiB,
three admissions and 900 seconds. There is also a 16 MiB aggregate metadata bound
and a 1 GiB free-disk reserve.

The output directory must be NEW. Its existing parent must be private (0700),
without symlink components, and writable by the sidecar service account. Capture
files are 0600 and per-session directories are 0700. Provide a private writable
volume for containers with a read-only filesystem; no mount is added automatically.
Do not point recordings at the metadata-only `ica.failure_diagnostic_dir`.

## Enable, reproduce, disable

1. Close old ICA viewers and pause new launches. Save the normal configuration.
2. Add the block above and `admin.ica_readiness_json: true`, validate the config,
   then cold-restart the sidecar. Validation/Build creates no capture files and
   loads no model. At Serve, the normal CPU/GPU inspection runtime loads first,
   then one recorder is created and shared across the ICA lanes.
3. Check `/readyz` on `admin.listen`. A raw-ready response explicitly reports
   `inspection_enabled: false`, `graphics_bypassed: true`,
   `protocol_validation_enabled: false` and nested `diagnostic_recording` metadata.
   Busy/draining/failed or inconsistent states return 503. No OCR text is included.
4. Open a fresh Windows Workspace session from its beginning, reproduce using
   synthetic content, then close that viewer. TCP monitoring probes also consume
   admissions; coordinate probes so they do not exhaust the bounded window first.
5. To stop early, remove `ica.diagnostic_recording` and cold-restart with the
   saved normal configuration. Diagnostic shutdown cancels raw connections
   immediately instead of waiting through the normal inspected-desktop drain grace.
   A running window cannot be enabled, extended,
   replaced or removed through overlapping runtime reloads. Stop/restart all ICA
   lanes together; there is no automatic fallback from a failed inspected session.
6. Verify the normal inspected readiness flags and synthetic email masking before
   returning the lane to normal use. Transfer the private finalized capture through
   the agreed secure support channel. Nothing is uploaded or deleted automatically.

Expiry or a recording limit closes the current raw session and returns **fresh**
connections to the already loaded inspection runtime on the same listener. A
restart with an expired block records nothing and does not touch old output files.
Reusing an existing output directory before expiry is refused rather than resetting
limits or overwriting evidence. Recording initially supports Linux and macOS hosts;
Windows clients can connect normally. EDT/UDP and mixed non-ICA processes are refused
when recording is configured.

## CPU Cell B and GPU Cell A

CPU Cell B's direct sidecar route needs only the updated sidecar/config. No DNS,
NetScaler or VDA changes are required.

GPU Cell A also needs the updated **separate AKS relay image** from libhoop-ica and
its explicit static authorization. The sidecar bump does not update that relay.
Keep the SSH and VDA mappings and temporarily set these relay overrides:

```yaml
relay:
  max_sessions: 1
  readiness_headroom: 0
  diagnostic_recording:
    acknowledge_unredacted: true
    until: "THE-EXACT-SAME-ABSOLUTE-EXPIRY-AS-THE-SIDECAR"
```

The relay requires an exact matching sidecar expiry and actual healthy capture
metadata. Cached raw readiness and both forwarding legs expire independently at
that deadline. A normal relay without this authorization continues to reject raw
readiness. Apply the sidecar and relay changes as one coordinated maintenance
restart with launches paused, then restore the relay's original settings after
capture. See the [relay runbook](https://github.com/hoophq/libhoop-ica/blob/v0.11.0/deploy/aks/external-gpu/DIAGNOSTIC-RECORDING.md).

## Capture format

The private root contains `recording.json` (absolute expiry) and random
`session-*` directories containing `c2s.bin`, `s2c.bin`, `events.jsonl` and
`manifest.json`. Events contain offsets, byte counts and monotonic timing; TCP
read boundaries are not ICA packet boundaries. Manifests include completion,
termination reason and stream SHA256 hashes. A host crash can leave incomplete
files because finalization, rather than each chunk, fsyncs them.

Use libhoop-ica's `verify_capture.py` before offline replay. Start replay at the
beginning of both streams; a resumed connection may lack the startup negotiation.
The sidecar's usual logs/audit remain content-free. Raw captures must stay out of
Git, CI and ordinary logs; no capture export endpoint is added by this feature.
