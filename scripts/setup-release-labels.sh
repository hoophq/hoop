#!/bin/bash
# One-time setup: create the major/minor/patch labels used by
# .github/workflows/pr-label-check.yml and .github/workflows/auto-release.yml.
#
# Re-running is safe: existing labels are updated in place.
set -euo pipefail

gh auth status >/dev/null

upsert_label() {
  local name=$1 color=$2 desc=$3
  if gh label list --limit 200 --json name --jq '.[].name' | grep -Fxq "$name"; then
    gh label edit "$name" --color "$color" --description "$desc"
    echo "updated: $name"
  else
    gh label create "$name" --color "$color" --description "$desc"
    echo " created: $name"
  fi
}

upsert_label "major"        "B60205" "Bumps the major version on release (breaking changes)"
upsert_label "minor"        "0E8A16" "Bumps the minor version on release (new features)"
upsert_label "patch"        "FBCA04" "Bumps the patch version on release (bug fixes)"
upsert_label "skip-release" "C5DEF5" "Merges without publishing a release (docs, CI, refactors, etc.)"

echo
echo "Release labels are ready."
