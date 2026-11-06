# CPU sidecar cell example

[config.yaml](config.yaml) is a complete standalone sidecar config for the cell API published in **libhoop-ica v0.8.0**. It is an example, not an export of the live lab. Use an ICA-enabled Linux sidecar build/image with Tesseract, the OCR worker, the NER model and ONNX Runtime installed. The standard image does not include the required ICA runtime assets.

```text
Workspace -> Citrix Gateway -> VDA-specific ILB frontend
                              -> Ready sidecar pod in Cell A
                              -> real VDA address:2598
```

Every pod in the cell runs the same ordered slot table. The load balancer chooses a pod for each new TCP flow; the destination slot port chooses the VDA. Different users on the same VDA use the same slot and can land on different pods. Existing sessions stay on their chosen pod; there is no session migration or cross-cell fallback.

| Gateway destination (example) | ILB target port on each Cell A pod | VDA name resolved by the sidecar |
| --- | --- | --- |
| `10.240.16.20:2598` | `21001` | `vda-a001.lab.example:2598` |
| `10.240.16.21:2598` | `21002` | `vda-a002.lab.example:2598` |

The ILB rules, Kubernetes Services/selectors, private DNS and Citrix Gateway alias/STA routing are separate infrastructure configuration; this YAML does not create them. Use `externalTrafficPolicy: Local` and preserve the Gateway SNIP so `source_cidrs` can enforce it. Pod DNS must resolve the VDA names to their **real** private addresses allowed by `upstream_cidrs`, while Gateway routing selects the frontend aliases. Keep those real addresses outside `frontend_cidr`. Do not put a fixed `upstream`, `ica.transparent` or EDT on a slots listener.

Replace the example addresses/names and tenant before use. For another cell, use its own ordered VDA inventory, frontend subnet and Service selectors. The count must match the number of explicit names. For numbered fleets, replace `names` with `upstream_pattern: vda-a%03d.lab.example`; slot numbering starts at 1. See the smaller [pattern example](../config.slots.yaml).

## Run and inspect

From the directory containing the edited file, using the ICA assets bundle:

```bash
hoop-ica -validate -config config.yaml
hoop-ica -config config.yaml
```

The ICA-enabled Linux CLI also accepts `hoop start sidecar --config config.yaml`. Validation does not prove VDA reachability or OCR accuracy. With the existing Helm sidecar chart, place the contents of this file under its `config:` key; it is not a Helm values file on its own. Publish both slot ports to the pods, with a separate VDA frontend mapping to each. Set pod `terminationGracePeriodSeconds: 120` for the 60-second drain window.

Configure startup/readiness probes on admin port 19000 at `/readyz`, and liveness at `/healthz`. Keep the admin port private behind NetworkPolicy; it is unauthenticated and must not be exposed through the ICA session load balancer. A probe example is:

```yaml
startupProbe:
  httpGet: {path: /readyz, port: 19000}
  periodSeconds: 5
  timeoutSeconds: 2
  failureThreshold: 180
readinessProbe:
  httpGet: {path: /readyz, port: 19000}
  periodSeconds: 5
  timeoutSeconds: 2
livenessProbe:
  httpGet: {path: /healthz, port: 19000}
  periodSeconds: 5
  timeoutSeconds: 2
```

For local debugging, forward only to loopback:

```bash
kubectl -n <cell-namespace> port-forward deployment/<cell-release> 19000:19000
# In another terminal:
curl -s http://127.0.0.1:19000/stats
curl -i http://127.0.0.1:19000/readyz
```

`/stats` reports active/total/denied connections for each listener. With `max_sessions: 32` and `readiness_headroom: 2`, capacity readiness becomes false at 30 active sessions; the locked admission limit remains 32. Readiness propagation takes time, so headroom is not a strict 30-session admission limit. A cell with no eligible spare must refuse new launches; it must not bypass inspection. Confirm established desktops stay connected through readiness transitions on your actual load balancer before enabling this signal. To retain hard admission without capacity readiness, set `readiness_capacity: false` **and** `readiness_headroom: 0`.

This CPU example explicitly uses the lab's `pii_matches_only: true`, `HEALTHCARE_IDENTIFIERS` and threshold 0.5, with ONNX Runtime NER. Only accepted policy matches create masks; OCR misses may remain visible. These are policy choices, not requirements of cell routing. Omit those overrides to retain the engine's default policy. No OCR text inspector is enabled.
