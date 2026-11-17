# ICA raw TCP forwarding without recording

For a connectivity test, merge this block into the existing sidecar config:

```yaml
ica:
  raw_tcp:
    enable: true
```

Keep the existing ICA assets, options, listeners, VDA addresses and source CIDRs.
This works with the CPU and GPU sidecar builds. The inspecting runtime still
initializes at startup, so its normal assets must remain available.
See the [complete example](examples/config.raw-tcp.yaml).

Restart the sidecar to apply the toggle:

```sh
sudo systemctl restart hoopsidecar
```

Accepted TCP connections are forwarded byte for byte in both directions. This
bypasses ICA/CGP decoding, OCR, NER/PII detection and masking. It does not record
traffic or screen content. The ordinary source/destination policy, tenant
admission, connection timeout and shared `ica.max_sessions` limit still apply.
There is no expiry, lifetime session quota or additional one-session limit.
It supports single-VDA, slot and transparent TCP listeners; EDT and mixed-protocol
processes are rejected. It cannot be combined with `ica.diagnostic_recording`.

To restore inspection, set `enable: false` or remove `raw_tcp`, then restart and
launch a fresh connection. Editing the file or sending SIGHUP alone does not
switch existing streams. Stopping the sidecar ends its raw connections promptly.

## Verify the actual mode

While enabled, `/readyz` automatically uses JSON (no extra admin setting needed):

```json
{
  "status": "ready",
  "inspection_enabled": false,
  "graphics_bypassed": true,
  "protocol_validation_enabled": false,
  "raw_tcp": {
    "mode": "raw_tcp",
    "enable": true,
    "ready": true,
    "capture_enabled": false,
    "active": 0,
    "total": 0
  }
}
```

`active` counts open raw connections and `total` is a counter, not a quota.
Capacity readiness remains controlled by `ica.readiness_capacity` and
`ica.readiness_headroom`; when enabled, a full sidecar returns HTTP 503.
Session audit events identify `mode: raw_tcp`, disabled inspection and disabled
capture, without logging payloads. Startup also logs the bypass explicitly.

For an external relay in front of the GPU sidecar, the relay must separately
support and authorize this raw readiness contract. This config changes the
sidecar only; it does not change relay configuration, DNS or Gateway routing.
An inspection-only relay must continue to refuse an uninspected backend.

If a traffic capture is needed instead, use the separate, bounded and explicitly
acknowledged [diagnostic recording mode](DIAGNOSTIC-RECORDING.md).
