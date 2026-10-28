# Building the downloadable ICA GPU runtime

The runtime can be distributed as a versioned asset, like the CPU native assets.
End users download binaries and models; they do not need the TurboOCR source or
a CUDA compiler. Two artifacts serve different consumers:

| Artifact | Contains / consumer |
| --- | --- |
| `ica-gpu-runtime_<version>_Linux_amd64_sm120.tar.gz` | TurboOCR device library, libhoop framebuffer bridge, CUDA ORT, userspace dependency libraries, OCR engines/dictionary, synthetic smoke fixture, notices and manifest. Input to `Dockerfile.sidecar` GPU targets. |
| `hoop-ica-gpu-assets_<hoop-version>_Linux_amd64.tar.gz` | The runtime above plus the ORT-tagged sidecar executable, NER model, installer and YAML examples. End-user native installation, corresponding to the CPU `hoop-ica-assets` bundle. |

The GPU runtime version is independent of the Hoop release version. Its manifest
records the exact TurboOCR commit, libhoop-ica module version and commit, SDK
versions, GPU model/compute capability, tested driver, minimum configured driver,
glibc version, and successful device/smoke gates. The checksum inventory covers
every packaged file. Engines are profile-specific, so a new GPU/runtime combination
needs its own tested asset version. The NVIDIA driver and host glibc remain host
prerequisites and are never copied from the builder into the runtime.

## Prepare the GPU builder once

Use a trusted Linux amd64 machine with a real target GPU, such as the RTX 5090.
`build-gpu-runtime.py` does not rent/start an instance, install system packages,
fetch models or source, or publish anything. It requires:

- Python 3.9+, Git, CMake 3.24+, a compatible C++ compiler, `ldd` and `patchelf`.
- A working NVIDIA driver and CUDA SDK with `nvcc` and headers.
- TensorRT, cuDNN and **CUDA-enabled** ONNX Runtime SDKs with their shared libraries.
- TurboOCR's C++ build dependencies: OpenCV, Drogon, protobuf/gRPC, c-ares,
  libcurl/libjpeg-turbo, PDFium SDK and the headers required by the pinned source.
  Supply custom dependency prefixes through `CMAKE_PREFIX_PATH` and their shared
  library directories through the builder profile's `runtime_library_dirs`.
- Read-only source inputs: a clean tracked checkout of the reviewed TurboOCR SHA
  (with matching initialized submodules), and libhoop-ica checked out at the exact
  module tag pinned in `sidecar/ica/go.mod`. The builder refuses a different HEAD
  or tracked edits; unrelated untracked files do not affect the pin check.
- Compatible `det.engine`, `rec.engine`, `dict.txt`, optionally `cls.engine`,
  already serialized for this TensorRT/GPU profile. This producer builds the
  native runtime from source; it does not select/train OCR models or regenerate
  TensorRT engines. Smoke tests gate their compatibility with the new runtime.
- A synthetic P6 PPM fixture and `expected.txt` containing a known recognized
  substring, plus component licenses and a `NOTICE` covering redistribution of
  included dependencies and model files. Do not use a captured private desktop.

Copy `gpu-builder.example.json` to the builder's configuration directory and
replace every SDK/model/license path and version placeholder. The example records
a source SHA, not an automatically selected branch. Set it to the reviewed commit
you intend to release. `device` is the GPU ordinal reported by `nvidia-smi`; the
producer isolates that GPU by UUID for CUDA calls. Metadata versions are compared
against installed CUDA/cuDNN/TensorRT headers and the loaded ORT library. These
checks refuse a CPU ORT library without its CUDA/shared providers. The complete
sidecar still tests actual CUDA NER model initialization through `/readyz` when
installed; the runtime producer's live inference tests exercise OCR.

## Build locally on that GPU host

Pass a new work directory for every attempt. Failed logs/builds stay there; the
producer never refreshes an existing output directory or overwrites an archive.
Use a retained build root outside both source checkouts.

```sh
python3 deploy/ica-sidecar/build-gpu-runtime.py \
  --config /opt/ica-gpu-builder/config.json \
  --turboocr-source /path/to/reviewed/TurboOCR \
  --ica-source /path/to/libhoop-ica-at-the-module-tag \
  --work-dir "$GPU_BUILD_ROOT/new-runtime-build" \
  --version 0.1.0-cuda-profile-sm120 --jobs 4
```

The producer performs these steps in order:

1. Check source pins, driver, GPU profile and actual SDK versions.
2. Build `turboocr-device` and `turbo_ocr_tests`, with model fetching disabled,
   native CPU tuning disabled, and the configured CUDA architecture.
3. Run `turbo_ocr_tests '[words_gpu][device]'` on the selected GPU.
4. Build `hoop-gpuframe` and `hoop-gpuframe-smoke` against that device library.
5. Collect ELF dependencies and split CUDA/cuDNN/TensorRT runtime families;
   materialize SONAME symlinks and refuse basename/version collisions. Never
   bundle `libcuda`, NVIDIA driver libraries, or the host C library/loader.
6. Set relative library RPATHs and check that dependencies resolve from the
   staged runtime or the explicitly allowed host OS/driver libraries.
7. Run the relocated framebuffer smoke in both conservative and match-only
   modes against the synthetic fixture. Both must exit successfully and print
   `PASS:`. A failed device test, unresolved dependency or failed smoke creates
   no release archive.
8. Pack the verified runtime plus manifest/notices and write its `.sha256` file.

`--work-dir/logs/` retains configure/build/device/smoke output. Offline unit tests
use synthetic command doubles; passing those tests is not evidence of real GPU
inference. A real GPU build and its smoke logs are required before publication.

## Produce and publish an asset through CI

`.github/workflows/ica-gpu-runtime.yml` is manually dispatched. It requires a
prepared self-hosted runner labelled `self-hosted`, `linux`, `x64`, `ica-gpu`, and
`GH_TOKEN` with read access to both source repositories. It never provisions Vast.
Ordinary CPU runners cannot replace this GPU gate. Dispatch from trusted source.

Inputs are runtime asset `version`, the full `turboocr_revision`, the host-owned
`builder_config` path, and `publish` (default **false**). The supplied SHA must
match the builder profile. A successful build uploads the runtime tarball and
checksum as a downloadable workflow artifact. Logs are retained even on failure.

With `publish: true`, a separate production job verifies the tarball checksum and
uses the same release mirror credentials as the CPU release pipeline. It refuses
an existing version prefix and publishes under:

```text
https://releases.hoop.dev/release/ica-gpu-runtime/<runtime-version>/
  ica-gpu-runtime_<runtime-version>_Linux_amd64_sm120.tar.gz
  ica-gpu-runtime_<runtime-version>_Linux_amd64_sm120.tar.gz.sha256
```

These are URL templates until a real asset is built and published. The job checks
that the public archive URL is reachable and prints the exact URL/digest in its
summary. It does not edit repository variables or update existing deployments.

Set those values as `ICA_GPU_ASSETS_URL` and `ICA_GPU_ASSETS_SHA256` for the normal
Hoop release, or pass them directly to the existing Docker build commands in
[GPU.md](GPU.md). That release builds the `ica-gpu` image and downloadable
`hoop-ica-gpu-assets` bundle from the **same** verified runtime. Native users then
extract the complete bundle and run its installer; no source compilation occurs
on the deployment host.

No asset, public URL, GPU compatibility claim or deployed version is created by
adding these source files alone. Live CUDA build/smoke and publication are separate
explicit actions.
