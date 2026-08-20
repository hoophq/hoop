#!/bin/bash
set -euo pipefail

: "${1:? Missing version! $0 <version>}}"
VERSION=$1

# The artifact sections live in release-notes.sh, shared with the GitHub
# release body and the manual release path.
cat - <<EOF
# Changelog

$(git log -1 --pretty=format:%B)

$("$(dirname "$0")/release-notes.sh" "$VERSION")

Helm chart sources: https://github.com/hoophq/helm-chart
EOF