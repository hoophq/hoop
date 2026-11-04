#!/bin/bash
# Print the release-notes body: every artifact a release publishes, at the
# stable URL it lands on.
#
# One copy, three consumers — the GitHub release body (auto-release.yml), the
# CHANGELOG.txt shipped to S3 (generate-changelog.sh) and the manual release
# path (publish-release.sh). They used to carry three drifting copies of this
# list, which is how the release page ended up advertising one image out of ten.
#
# Everything listed here is published by .github/workflows/release.yml on a tag
# push. Two things it publishes are deliberately absent: hoophq/hoopdev-ng, a
# best-effort clean build that skips whenever its clean base is unpublished (the
# repository does not exist on Docker Hub yet), and the per-commit
# :<sha>-<arch> tags, which are build intermediates.
#
# The ICA GPU image and bundle are published only when the repository variable
# ICA_GPU_ASSETS_URL is set (release.yml gates those steps on it), so they are
# listed only when the same variable reaches this script's environment. Both
# workflow call sites pass it; the manual path inherits the shell's.
#
# Usage: ICA_GPU_ASSETS_URL=... release-notes.sh <version>

set -euo pipefail

: "${1:? Missing version! $0 <version>}"
VERSION=$1

# Docker Hub renders a tag page for a filtered listing, so each link lands on
# the tag it names instead of the repository's front page.
hub() { echo "https://hub.docker.com/r/hoophq/${1}/tags?name=${2}"; }

# The gated GPU artifacts, each with its own leading newline so that an unset
# variable adds nothing — not even a blank line — to the body.
GPU_IMAGE= GPU_BUNDLE=
if [[ -n ${ICA_GPU_ASSETS_URL:-} ]]; then
  GPU_IMAGE="
- [hoophq/hoopsidecar:${VERSION}-ica-gpu]($(hub hoopsidecar "${VERSION}-ica-gpu")) — the ICA engine on TurboOCR and CUDA NER, linux/amd64 only; needs an NVIDIA GPU"
  GPU_BUNDLE="

### Citrix ICA sidecar on a GPU, without Docker

What the \`-ica-gpu\` image carries, for a Linux amd64 host with an NVIDIA driver:
\`hoop-ica\`, the TurboOCR and CUDA ONNX Runtime libraries, TensorRT engines, the
NER model, an installer and example configs. See deploy/ica-sidecar/GPU.md.

- [hoop-ica-gpu-assets-amd64](https://releases.hoop.dev/release/${VERSION}/hoop-ica-gpu-assets_${VERSION}_Linux_amd64.tar.gz)"
fi

cat - <<EOF
## Assets

- [hoop-darwin-arm64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Darwin_arm64.tar.gz)
- [hoop-darwin-amd64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Darwin_x86_64.tar.gz)
- [hoop-linux-arm64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Linux_arm64.tar.gz)
- [hoop-linux-amd64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Linux_x86_64.tar.gz)
- [hoop-windows-arm64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Windows_arm64.tar.gz)
- [hoop-windows-amd64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Windows_x86_64.tar.gz)
- [checksums.txt](https://releases.hoop.dev/release/${VERSION}/checksums.txt)

## Docker Images

### Gateway

- [hoophq/hoop:${VERSION}]($(hub hoop "${VERSION}")) · [:latest]($(hub hoop latest)) — gateway, linux/amd64 + linux/arm64
- [hoophq/hoop-ng:${VERSION}]($(hub hoop-ng "${VERSION}")) · [:latest]($(hub hoop-ng latest)) — same image from a repository with no AGPL/SSPL component in its history

### Agent

- [hoophq/hoopdev:${VERSION}]($(hub hoopdev "${VERSION}")) · [:latest]($(hub hoopdev latest)) — full agent: bundled database clients and RDP tooling
- [hoophq/hoopagent:${VERSION}-minimal]($(hub hoopagent "${VERSION}-minimal")) — Ubuntu 24.04 plus the agent binary, nothing else
- [hoophq/hoopagent:${VERSION}-distroless]($(hub hoopagent "${VERSION}-distroless")) — distroless static plus the agent binary, no shell

### Agent with the Alcatraz NER model

For \`DLP_PROVIDER=alcatraz\` masking rules that ask for PERSON, LOCATION or NRP.
The model ships in the image and is never fetched at runtime.

- [hoophq/hoopagent:${VERSION}-minimal-alcatraz]($(hub hoopagent "${VERSION}-minimal-alcatraz")) — minimal plus the model
- [hoophq/hoopagent:${VERSION}-distroless-alcatraz]($(hub hoopagent "${VERSION}-distroless-alcatraz")) — distroless plus the model

### Agent with the OCR engine

For the realtime RDP PII guard.

- [hoophq/hoop-agent-ocr:${VERSION}]($(hub hoop-agent-ocr "${VERSION}")) · [:latest]($(hub hoop-agent-ocr latest)) — CPU, linux/amd64 + linux/arm64
- [hoophq/hoop-agent-ocr:${VERSION}-gpu]($(hub hoop-agent-ocr "${VERSION}-gpu")) — CUDA, linux/amd64 only

### Inspection sidecar

The relay, \`hoop start sidecar\`, as its own image; deploy/helm-chart/chart/sidecar
defaults to \`:latest\`.

- [hoophq/hoopsidecar:${VERSION}]($(hub hoopsidecar "${VERSION}")) · [:latest]($(hub hoopsidecar latest)) — Ubuntu 24.04 plus the binary, linux/amd64 + linux/arm64
- [hoophq/hoopsidecar:${VERSION}-distroless]($(hub hoopsidecar "${VERSION}-distroless")) — distroless cc plus the binary, no shell
- [hoophq/hoopsidecar:${VERSION}-alcatraz]($(hub hoopsidecar "${VERSION}-alcatraz")) — the unsuffixed flavour plus the Alcatraz NER model and ONNX Runtime, for a \`pii.ner\` block
- [hoophq/hoopsidecar:${VERSION}-distroless-alcatraz]($(hub hoopsidecar "${VERSION}-distroless-alcatraz")) — distroless plus the same
- [hoophq/hoopsidecar:${VERSION}-ica]($(hub hoopsidecar "${VERSION}-ica")) — \`hoop-inspect\` with the Citrix ICA engine, tesseract, ONNX Runtime and the NER model${GPU_IMAGE}

### Control plane

- [hoophq/hoopcontrolplane:${VERSION}]($(hub hoopcontrolplane "${VERSION}")) · [:latest]($(hub hoopcontrolplane latest)) — Ubuntu 24.04 plus the binary with the web UI embedded, linux/amd64 + linux/arm64
- [hoophq/hoopcontrolplane:${VERSION}-distroless]($(hub hoopcontrolplane "${VERSION}-distroless")) — distroless static plus the binary, no shell

## Helm Chart

- [hoop-chart-${VERSION}](https://releases.hoop.dev/release/${VERSION}/hoop-chart-${VERSION}.tgz)
- [hoopagent-chart-${VERSION}](https://releases.hoop.dev/release/${VERSION}/hoopagent-chart-${VERSION}.tgz)
- [hoopsidecar-chart-${VERSION}](https://releases.hoop.dev/release/${VERSION}/hoopsidecar-chart-${VERSION}.tgz)
- [hoopcontrolplane-chart-${VERSION}](https://releases.hoop.dev/release/${VERSION}/hoopcontrolplane-chart-${VERSION}.tgz)
- [hoop-ng-chart-${VERSION}](https://releases.hoop.dev/release/${VERSION}/hoop-ng-chart-${VERSION}.tgz) — the gateway chart defaulting to hoophq/hoop-ng
- [hoopagent-ng-chart-${VERSION}](https://releases.hoop.dev/release/${VERSION}/hoopagent-ng-chart-${VERSION}.tgz) — the agent chart defaulting to hoophq/hoopdev-ng

The same charts are pushed to \`oci://ghcr.io/hoophq/helm-charts/\`.

## Bundles

- [hoop-gateway-bundle-amd64](https://releases.hoop.dev/release/${VERSION}/hoopgateway_${VERSION}-Linux_amd64.tar.gz)
- [hoop-gateway-bundle-arm64](https://releases.hoop.dev/release/${VERSION}/hoopgateway_${VERSION}-Linux_arm64.tar.gz)

### Citrix ICA sidecar without Docker

What the \`-ica\` image carries, for a Linux host: \`hoop-ica\` (the sidecar with the
ICA engine and ONNX Runtime), the OCR worker, an installer and example configs. The
installer fetches the NER model from releases.hoop.dev/alcatraz/current and runs the
sidecar under systemd; the Linux \`hoop\` CLI of this release serves the same config
with \`hoop start sidecar\` once the assets are installed.

- [hoop-ica-assets-amd64](https://releases.hoop.dev/release/${VERSION}/hoop-ica-assets_${VERSION}_Linux_amd64.tar.gz)
- [hoop-ica-assets-arm64](https://releases.hoop.dev/release/${VERSION}/hoop-ica-assets_${VERSION}_Linux_arm64.tar.gz)${GPU_BUNDLE}
EOF
