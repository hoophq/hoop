# ICA GPU sidecar delivery

This is the embedded Citrix ICA engine in `Dockerfile.sidecar`. The existing
`ica`/`ica-assets` targets remain CPU builds. The RDP OCR and agent images are
unrelated. The new `ica-gpu` and `ica-gpu-assets` targets support Linux amd64.
Neither target silently falls back to Tesseract or CPU ONNX Runtime.

## Configure the runtime

Start from `examples/config.gpu.single.yaml` for one fixed VDA, or
`examples/config.gpu.cidr.yaml` for Linux TPROXY. Both use the installed bundle's
own NER model. `listen` is the sidecar's local binding; `upstream` is the VDA.
`source_cidrs` admits the actual connecting TCP peer: for an SSH forward this
can be loopback rather than the Citrix Gateway SNIP. A TCP forward does not
preserve the original destination; use fixed-upstream mode on that hop.
Transparent mode needs TPROXY rules, policy routes and CAP_NET_ADMIN, and uses
`ica.transparent.{source_cidrs,upstream_cidrs,upstream_ports}` instead of a fixed
`upstream`. These samples do not install routes or change the published desktop.
The Azure CPU address `10.20.2.21` belongs to that CPU host; it is not a generic
GPU address. Remove each EDIT marker only after reviewing its deployment value.

GPU paths are top-level `ica.ocr_turbo_{library,det_model,rec_model,rec_dict,cls_model}`.
The classifier is optional. `ica.ner_model` and `ica.ner_ort_library` remain
explicit paths. Backend/device settings live under `ica.options`:

| Setting | Meaning |
| --- | --- |
| `ocr_backend: turboocr` | Native resident GPU framebuffer and OCR engine |
| `ocr_turbo_device: 0` | CUDA device ordinal |
| `ocr_turbo_framebuffer_bytes` | Optional shared source framebuffer budget; 0 keeps the engine default |
| `ocr_turbo_regional_cache: true` | Initial complete scan, then update cached screen text from changed regions |
| `ocr_turbo_full_screen: true` | Scan the entire framebuffer; set regional cache false for this test |
| `pii_matches_only: true` | Mask accepted configured PII/NER matches only; unreadable OCR never independently masks |
| `ner_backend: ort` / `ner_accelerator: cuda` | ONNX Runtime with a CUDA provider; requires an ORT-tagged binary and CUDA-enabled library |
| `ner_gpu_device: 0` | CUDA NER device ordinal |
| `ner_entities` | Select PERSON, LOCATION, ORGANIZATION, NRP; absent/empty inherits the engine's four types |

Match-only requires complete-screen OCR (full-screen or regional cache). Missed
or unreadable sensitive text can remain visible in this policy. OCR is not a
guarantee of complete text recognition. The samples use 0.8 OCR/PII thresholds;
these are example values, not a change to any live lab setting. Structured
`pii_entities` is independent of `ner_entities`; local rules follow the configured
engine policy. The inspector is a separate debug facility and is never enabled
by these samples/images/installers. Normal audit/notes contain metadata only.

Omit CPU-specific workers, tiles, gray preparation, thread limit, line-band and
fresh-reuse settings. TurboOCR relaxes implicit CPU defaults to the native device
profile; explicit incompatible overrides fail validation. CPU defaults stay
unchanged. `-validate` checks configuration and explicit file existence without CUDA/model initialization;
Serve and `/readyz` report actual native/provider initialization failures.
`HostCheck` recognizes the GPU bundle's manifest and asset paths before advertising
ICA capability; it does not prove that CUDA inference is healthy. For a custom
asset root, `HOOP_ICA_GPU_ASSETS` changes that discovery root only; the model
is discovered at its sibling `models/KnightsAnalytics_distilbert-NER`. Set all
runtime paths explicitly in YAML too.

## Prepare one native GPU asset set

The source-build producer and manual GPU workflow are documented in
[Building the downloadable runtime](GPU-RUNTIME.md). They create the native
runtime archive described below, including live device/smoke gates, before the
sidecar image or end-user asset bundle consumes it. A target GPU build is
required; these artifacts are not produced on a CPU-only release runner.

Build the pinned libhoop-ica `native/gpuframe` library and smoke executable against
the matching TurboOCR C ABI, then serialize detection/recognition TensorRT engines
for the intended GPU/runtime. Supply CUDA-enabled ORT, provider libraries, and the
matching CUDA/cuDNN/TensorRT userspace libraries. The host NVIDIA driver is not
bundled. Keep redistribution notices for each included component. The pack tool
checks inventory/integrity; it does not build or certify third-party binaries.

Prepare a directory with regular files (materialize SONAME symlinks):

```text
manifest.json
lib/libhoop-gpuframe.so
lib/libturboocr-device.so
lib/<CUDA, cuDNN, TensorRT and other required shared libraries>
ort/lib/libonnxruntime.so
ort/lib/libonnxruntime_providers_cuda.so
ort/lib/libonnxruntime_providers_shared.so
engines/{det.engine,rec.engine,dict.txt}
bin/hoop-gpuframe-smoke
smoke/fixture.ppm
smoke/expected.txt
licenses/NOTICE
licenses/<component license files>
```

Use a synthetic smoke fixture with known text; `expected.txt` contains that text.
The input manifest has `format: 1`, `arch: "amd64"` and non-empty strings for
`version`, `libhoop_ica` (the exact module pin in `sidecar/ica/go.mod`),
`turboocr_revision`, `cuda`, `cudnn`, `tensorrt`, `ort`, `compute_capability`,
`gpu_model`, and `min_driver`. Record actual build versions, not guessed values.
The packer adds a SHA256 inventory for every asset; it never changes the input.
It rejects symlinks, traversal paths, missing required files, duplicate archive
entries and checksum mismatches. Keep output outside the input directory.

```sh
python3 deploy/ica-sidecar/gpu-assets.py pack --root "$GPU_ASSET_DIR" --output "$GPU_ASSET_TARBALL"
python3 deploy/ica-sidecar/gpu-assets.py verify "$GPU_ASSET_TARBALL" --engine "$(awk '/hoophq\/libhoop-ica /{print $2}' sidecar/ica/go.mod)"
sha256sum "$GPU_ASSET_TARBALL"
```

Upload that reviewed asset archive to an HTTPS artifact location. Its immutable
URL and SHA256 are explicit build inputs; do not put credentials into the URL.
TensorRT engine portability is limited by hardware/runtime compatibility; see
[NVIDIA's engine compatibility documentation](https://docs.nvidia.com/deeplearning/tensorrt/latest/inference-library/engine-compatibility.html).
Match ORT's CUDA/cuDNN requirements using the
[CUDA provider matrix](https://onnxruntime.ai/docs/execution-providers/CUDA-ExecutionProvider.html).

## Build image and downloadable native bundle

Both targets compile the same sidecar binary with CGO and `-tags ORT` and consume
the same verified GPU archive. They use the repository's checksum-verified NER
model. The native bundle includes the model too; no old lab installation is
required. Pass the existing private module/release token as a BuildKit secret.

```sh
docker buildx build -f Dockerfile.sidecar --platform linux/amd64 --target ica-gpu \
  --secret id=gh_token,env=GH_TOKEN --build-arg VERSION="$VERSION" \
  --build-arg ICA_GPU_ASSETS_URL="$GPU_ASSETS_URL" \
  --build-arg ICA_GPU_ASSETS_SHA256="$GPU_ASSETS_SHA256" \
  --load -t hoopsidecar:ica-gpu .
docker buildx build -f Dockerfile.sidecar --platform linux/amd64 --target ica-gpu-assets \
  --secret id=gh_token,env=GH_TOKEN --build-arg VERSION="$VERSION" \
  --build-arg ICA_GPU_ASSETS_URL="$GPU_ASSETS_URL" \
  --build-arg ICA_GPU_ASSETS_SHA256="$GPU_ASSETS_SHA256" \
  --output type=local,dest=ica-gpu-export .
```

The export contains `binaries/hoop-ica-gpu-assets_<version>_Linux_amd64.tar.gz`
and its checksum file. Verify the published outer checksum before extraction.
The tag release workflow builds/publishes the GPU image and includes the native
bundle in its release artifacts only when repository variables
`ICA_GPU_ASSETS_URL` and `ICA_GPU_ASSETS_SHA256` are both configured. With neither,
existing CPU releases behave as before; one without the other fails the release
job. A PR that changes the bundle inputs gets the same preview as the CPU bundle:
`hoophq/hoopsidecar:<PR>.0.0-g<sha>-ica-gpu` and the native bundle at
`releases.hoop.dev/release/<PR>.0.0-g<sha>/`, skipped while the variables are
unset. Updating that asset set is an explicit release input, not an automatic engine
regeneration. No GPU artifacts are published by this source change itself.

On a GPU host with NVIDIA Container Toolkit, first run the smoke test:

```sh
docker run --rm --gpus all --entrypoint /opt/libhoop-ica/current/gpu/bin/hoop-gpuframe-smoke \
  hoopsidecar:ica-gpu --det /opt/libhoop-ica/current/gpu/engines/det.engine \
  --rec /opt/libhoop-ica/current/gpu/engines/rec.engine \
  --dict /opt/libhoop-ica/current/gpu/engines/dict.txt \
  --image /opt/libhoop-ica/current/gpu/smoke/fixture.ppm --expect 'KNOWN TEXT'
# Use the actual synthetic text from expected.txt above.
docker run --rm --gpus all --network host \
  -v "$PWD/config.yaml:/etc/hoop-inspect/config.yaml:ro" hoopsidecar:ica-gpu
```

For native Ubuntu 24.04 installation, extract the downloadable bundle, edit a
GPU example, then run `sudo ./install.sh --config /path/config.yaml --start`.
Add `--cidr` only for a real TPROXY deployment. The GPU installer checks manifest
hashes, native dependencies, the GPU smoke test as the service user and offline
configuration against staged asset paths before switching to a new versioned release. It does not overwrite
or prune existing release directories. It installs a `hoop-ica` systemd service,
with loader paths for the bundle's userspace libraries. `--start` also waits for
`/readyz`, which includes CUDA NER loading; no CPU fallback occurs on failure.
Keep the model and library paths in the sample or adjust them consistently.
GPU device permissions must allow the `hoop` user/container UID 10001 to access
the driver devices; native installation joins existing video/render groups.
