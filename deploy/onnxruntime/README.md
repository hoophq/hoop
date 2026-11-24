# Shared ONNX Runtime CPU download

`fetch.sh OUTPUT_DIRECTORY amd64|arm64` installs the native CPU libraries and
license notices from Microsoft's official `Microsoft.ML.OnnxRuntime` NuGet
package. It does not install .NET or change the runtime version. Both agent and
sidecar Dockerfiles and the real-model NER CI job use this helper, avoiding a
separate GitHub-release download that can become unavailable.

The package URL and SHA256 are pinned by `manifest.env`. Downloads, including
cached copies, must pass the checksum before extraction. The package is retained
in the output directory for diagnostics/cache reuse; only `lib/`, `LICENSE` and
`ThirdPartyNotices.txt` are copied into the runtime images/bundle. A failed HTTP
request, checksum, unsupported architecture or mismatched `ORT_VERSION` stops the
build. The helper requires POSIX sh, curl, sha256sum and unzip.

The output layout matches the existing SDK layout:

```text
lib/libonnxruntime.so
lib/libonnxruntime.so.1 -> libonnxruntime.so
lib/libonnxruntime.so.1.28.0 -> libonnxruntime.so
lib/libonnxruntime_providers_shared.so
LICENSE
ThirdPartyNotices.txt
VERSION
```

No Python or .NET runtime is required in a published image. The CUDA/GPU runtime
bundle remains a separate build input.

To upgrade, update the package version and verified checksum in `manifest.env`,
the `ORT_VERSION` defaults in both Dockerfiles, and the relay/ICA default library
paths together. Check compatibility with the pinned Go ORT bindings and NER model.
CI reads the manifest through the helper; it has no separate version override.
Run `python3 -B -m unittest discover -s deploy/onnxruntime -p 'test_*.py'` and the
existing real-model NER test. Verify both architecture downloads and image builds.

Official installation source: https://onnxruntime.ai/docs/install/
