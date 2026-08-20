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
# Usage: release-notes.sh <version>

set -euo pipefail

: "${1:? Missing version! $0 <version>}"
VERSION=$1

# Docker Hub renders a tag page for a filtered listing, so each link lands on
# the tag it names instead of the repository's front page.
hub() { echo "https://hub.docker.com/r/hoophq/${1}/tags?name=${2}"; }

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

- [hoophq/hoopdev:${VERSION}-alcatraz]($(hub hoopdev "${VERSION}-alcatraz")) — the full agent plus the model
- [hoophq/hoopagent:${VERSION}-minimal-alcatraz]($(hub hoopagent "${VERSION}-minimal-alcatraz")) — minimal plus the model
- [hoophq/hoopagent:${VERSION}-distroless-alcatraz]($(hub hoopagent "${VERSION}-distroless-alcatraz")) — distroless plus the model

### Agent with the OCR engine

For the realtime RDP PII guard.

- [hoophq/hoop-agent-ocr:${VERSION}]($(hub hoop-agent-ocr "${VERSION}")) · [:latest]($(hub hoop-agent-ocr latest)) — CPU, linux/amd64 + linux/arm64
- [hoophq/hoop-agent-ocr:${VERSION}-gpu]($(hub hoop-agent-ocr "${VERSION}-gpu")) — CUDA, linux/amd64 only

## Helm Chart

- [hoop-chart-${VERSION}](https://releases.hoop.dev/release/${VERSION}/hoop-chart-${VERSION}.tgz)
- [hoopagent-chart-${VERSION}](https://releases.hoop.dev/release/${VERSION}/hoopagent-chart-${VERSION}.tgz)

## Bundles

- [hoop-gateway-bundle-amd64](https://releases.hoop.dev/release/${VERSION}/hoopgateway_${VERSION}-Linux_amd64.tar.gz)
- [hoop-gateway-bundle-arm64](https://releases.hoop.dev/release/${VERSION}/hoopgateway_${VERSION}-Linux_arm64.tar.gz)
EOF
