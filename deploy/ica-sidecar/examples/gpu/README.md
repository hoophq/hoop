# GPU sidecar on Kubernetes

[values.yaml](values.yaml) is a complete values file for the Helm chart at
`deploy/helm-chart/chart/sidecar`, running `hoophq/hoopsidecar:<version>-ica-gpu`
on a GPU node pool. Its `config:` block is [config.gpu.single.yaml](../config.gpu.single.yaml)
with `admin.listen` on `0.0.0.0:19000`, which the chart requires for its probes.
Replace every `EDIT` value before installing. The image is Linux amd64 only and is
published only once the GPU runtime asset is configured; see [GPU.md](../../GPU.md).

```bash
helm install ica-gpu deploy/helm-chart/chart/sidecar -f deploy/ica-sidecar/examples/gpu/values.yaml
```

## What the values set, and why

| Value | Reason |
| --- | --- |
| `resources.limits.nvidia.com/gpu: 1` | The device plugin injects `libcuda` and `/dev/nvidia*` into the pod; the image bundles no driver. One pod per GPU: TurboOCR and CUDA NER both use device 0. |
| `nodeSelector`, `tolerations` | Pin to the GPU pool. Names are the pool's own. |
| `runtimeClassName: nvidia` (commented) | Only where the NVIDIA runtime is installed but is not the containerd default. Managed pools on AKS, GKE and EKS need nothing. |
| `probe.readinessPath` / `startupPath: /readyz` | `/healthz` is true as soon as the process is up; `/readyz` waits for the CUDA NER provider, TurboOCR and every bound lane. With the default readiness a pod whose CUDA init failed would receive sessions. The 180 × 5s startup budget covers engine load on a cold node. |
| `terminationGracePeriodSeconds: 120` | Longer than `ica.drain_timeout_sec: 60`, so the kubelet does not kill sessions mid-drain. |
| `laneServices.ica` | Publishes the lane port; the chart's own Service is the admin port only. Keep it internal: the lane is plaintext ICA/CGP, and the admin port is unauthenticated. |

The relay runs as uid 10001 and needs read/write on the injected `/dev/nvidia*`
nodes; the NVIDIA container runtime grants that by default, so fixed-upstream
mode needs no `securityContext`. A permission error on those nodes in the pod
log is a node runtime setting, not a chart one. Transparent (TPROXY) mode
additionally needs `CAP_NET_ADMIN` through
`containerSecurityContext.capabilities.add` and node routing rules outside the
chart; see [config.gpu.cidr.yaml](../config.gpu.cidr.yaml) and libhoop-ica's
`docs/transparent-linux.md`.

## Check a pod

```bash
kubectl -n <ns> exec deploy/ica-gpu-hoopsidecar -- /opt/libhoop-ica/current/gpu/bin/hoop-gpuframe-smoke \
  --det /opt/libhoop-ica/current/gpu/engines/det.engine --rec /opt/libhoop-ica/current/gpu/engines/rec.engine \
  --dict /opt/libhoop-ica/current/gpu/engines/dict.txt --image /opt/libhoop-ica/current/gpu/smoke/fixture.ppm \
  --expect "$(kubectl -n <ns> exec deploy/ica-gpu-hoopsidecar -- cat /opt/libhoop-ica/current/gpu/smoke/expected.txt)"
kubectl -n <ns> port-forward deploy/ica-gpu-hoopsidecar 19000:19000
curl -i http://127.0.0.1:19000/readyz
```

`/readyz` returning 503 with the pod otherwise running means the engine refused
to initialise; the pod log names the failing component. There is no CPU
fallback: an `-ica-gpu` pod never serves ICA without a working GPU.
