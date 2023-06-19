#!/bin/bash
set -euo pipefail

: "${1:? Missing version! $0 <version>}}"
VERSION=$1

cat - <<EOF
# Changelog

$(git log -1 --pretty=format:%B)

## Assets

- [hoop-darwin-arm64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Darwin_arm64.tar.gz)
- [hoop-darwin-amd64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Darwin_x86_64.tar.gz)
- [hoop-linux-arm64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Linux_arm64.tar.gz)
- [hoop-linux-amd64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Linux_x86_64.tar.gz)
- [hoop-windows-arm64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Windows_arm64.tar.gz)
- [hoop-windows-amd64](https://releases.hoop.dev/release/${VERSION}/hoop_${VERSION}_Windows_x86_64.tar.gz)
- [checksums.txt](https://releases.hoop.dev/release/${VERSION}/checksums.txt)

## Docker Images

- [hoophq/hoop:latest](https://hub.docker.com/repository/docker/hoophq/hoop)
- [hoophq/hoop:${VERSION}](https://hub.docker.com/repository/docker/hoophq/hoop)

### Agent Image | amd64

- [hoophq/hoopdev:latest](https://hub.docker.com/repository/docker/hoophq/hoopdev)
- [hoophq/hoopdev:${VERSION}](https://hub.docker.com/repository/docker/hoophq/hoopdev)

## Helm Chart

https://github.com/hoophq/helm-chart

- [hoop-chart-${VERSION}](https://releases.hoop.dev/release/${VERSION}/hoop-chart-${VERSION}.tgz)
- [hoopagent-chart-${VERSION}](https://releases.hoop.dev/release/${VERSION}/hoopagent-chart-${VERSION}.tgz)

EOF