#!/bin/bash
set -eo pipefail

gh auth status

# pull latest tags from remote
LATEST_TAG=$(gh release list -L 1 |awk {'print $1'})

echo "=> fetching tags from remote ..."
git fetch origin
echo ""

echo "=> Here are the last 10 releases from github"
gh release list -L 10

read -rep $'\nWhich version do you like to release?\n=> ' GIT_TAG

# Ask if user wants to fetch PR descriptions
read -rep $'\nFetch detailed PR descriptions from GitHub?\n(y/n) => ' fetch_prs_choice
FETCH_PRS_FLAG=""
case "$fetch_prs_choice" in
  y|Y ) FETCH_PRS_FLAG="--fetch-prs";;
  * ) FETCH_PRS_FLAG="";;
esac

# Ask if user wants AI-powered summarization
read -rep $'\nUse AI-powered changelog summarization? (requires ANTHROPIC_API_KEY)\n(y/n) => ' use_ai_choice
AI_FLAG=""
MODEL_FLAG=""
case "$use_ai_choice" in
  y|Y )
    AI_FLAG="--ai-summary"
    read -rep $'\nWhich Claude model? (default: auto-detect, or specify like "claude-sonnet-4-0-20250514")\n=> ' model_name
    if [[ -n "$model_name" ]]; then
      MODEL_FLAG="--model $model_name"
    fi
    ;;
  * ) AI_FLAG="";;
esac

# Generate raw changelog
RAW_CHANGELOG_FILE="$(mktemp)"
git log $LATEST_TAG..HEAD --pretty=format:"%h %s" --no-merges > $RAW_CHANGELOG_FILE

# Format changelog using the helper script
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FORMATTED_CHANGELOG=$("$SCRIPT_DIR/format-changelog.sh" "$RAW_CHANGELOG_FILE" $FETCH_PRS_FLAG $AI_FLAG $MODEL_FLAG)

# Create final note file
NOTE_FILE="$(mktemp).md"
cat - >$NOTE_FILE <<EOF
$FORMATTED_CHANGELOG
EOF

# Clean up raw changelog
rm -f "$RAW_CHANGELOG_FILE"

# Open in editor for final review/edits
${VISUAL:-${EDITOR:-vi}} $NOTE_FILE


NOTE_CONTENT=$(cat $NOTE_FILE)
cat - >$NOTE_FILE <<EOF
$NOTE_CONTENT

## Assets

- [hoop-darwin-arm64](https://releases.hoop.dev/release/${GIT_TAG}/hoop_${GIT_TAG}_Darwin_arm64.tar.gz)
- [hoop-darwin-amd64](https://releases.hoop.dev/release/${GIT_TAG}/hoop_${GIT_TAG}_Darwin_x86_64.tar.gz)
- [hoop-linux-arm64](https://releases.hoop.dev/release/${GIT_TAG}/hoop_${GIT_TAG}_Linux_arm64.tar.gz)
- [hoop-linux-amd64](https://releases.hoop.dev/release/${GIT_TAG}/hoop_${GIT_TAG}_Linux_x86_64.tar.gz)
- [hoop-windows-arm64](https://releases.hoop.dev/release/${GIT_TAG}/hoop_${GIT_TAG}_Windows_arm64.tar.gz)
- [hoop-windows-amd64](https://releases.hoop.dev/release/${GIT_TAG}/hoop_${GIT_TAG}_Windows_x86_64.tar.gz)
- [checksums.txt](https://releases.hoop.dev/release/${GIT_TAG}/checksums.txt)

## Docker Images

- [hoophq/hoop:latest](https://hub.docker.com/repository/docker/hoophq/hoop)
- [hoophq/hoop:${GIT_TAG}](https://hub.docker.com/repository/docker/hoophq/hoop)

## Helm Chart

- [hoop-chart-${GIT_TAG}](https://releases.hoop.dev/release/${GIT_TAG}/hoop-chart-${GIT_TAG}.tgz)
- [hoopagent-chart-${GIT_TAG}](https://releases.hoop.dev/release/${GIT_TAG}/hoopagent-chart-${GIT_TAG}.tgz)

# Bundles

- [hoop-gateway-bundle-amd64](https://releases.hoop.dev/release/${GIT_TAG}/hoopgateway_${GIT_TAG}-Linux_amd64.tar.gz)
- [hoop-gateway-bundle-arm64](https://releases.hoop.dev/release/${GIT_TAG}/hoopgateway_${GIT_TAG}-Linux_arm64.tar.gz)
- [webapp-bundle](https://releases.hoop.dev/release/${GIT_TAG}/hoop-chart-${GIT_TAG}.tgz)

EOF

cat - <<EOF

RELEASE NOTES
-------------
$(cat $NOTE_FILE)

EOF

ghRelease(){
  gh release create $GIT_TAG -F $NOTE_FILE --title $GIT_TAG
}

read -rep $'=> Do you with to create this release?\n(y/n) => ' choice
case "$choice" in
  y|Y ) ghRelease;;
  n|N ) echo -e "\naborting ..."; exit 0;;
  * ) echo "invalid choice";;
esac
