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
NOTE_FILE="$(mktemp).md"
GIT_COMMIT=$(git log $LATEST_TAG..HEAD --pretty=format:"%h %s%n%n%b")
cat - >$NOTE_FILE <<EOF
# Changelog

$GIT_COMMIT
EOF
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

tagLibhoop(){
  # Resolve libhoop path (it may be a symlink)
  LIBHOOP_PATH="libhoop"
  if [ -L "$LIBHOOP_PATH" ]; then
    LIBHOOP_PATH=$(readlink -f "$LIBHOOP_PATH")
  fi

  if [ ! -d "$LIBHOOP_PATH/.git" ]; then
    echo "WARNING: libhoop directory not found or not a git repository at $LIBHOOP_PATH"
    echo "Skipping libhoop tagging"
    return
  fi

  echo "=> Fetching latest state of libhoop from remote..."
  (cd "$LIBHOOP_PATH" && git fetch origin)

  LIBHOOP_BRANCH=$(cd "$LIBHOOP_PATH" && git rev-parse --abbrev-ref HEAD)
  if [ "$LIBHOOP_BRANCH" != "main" ]; then
    echo "ERROR: libhoop is on branch '${LIBHOOP_BRANCH}', but must be on 'main' before tagging."
    echo "Please switch to main: cd ${LIBHOOP_PATH} && git checkout main"
    exit 1
  fi

  LOCAL_SHA=$(cd "$LIBHOOP_PATH" && git rev-parse HEAD)
  REMOTE_SHA=$(cd "$LIBHOOP_PATH" && git rev-parse origin/main)
  if [ "$LOCAL_SHA" != "$REMOTE_SHA" ]; then
    echo "ERROR: libhoop/main is not up to date with origin/main."
    echo "Please pull the latest changes: cd ${LIBHOOP_PATH} && git pull origin main"
    exit 1
  fi

  echo "=> Tagging libhoop with ${GIT_TAG}..."
  (cd "$LIBHOOP_PATH" && git tag "$GIT_TAG" 2>/dev/null || true && git push origin "$GIT_TAG")
  echo "=> libhoop tagged successfully"
}

ghRelease(){
  tagLibhoop
  gh release create $GIT_TAG -F $NOTE_FILE --title $GIT_TAG
}

read -rep $'=> Do you with to create this release?\n(y/n) => ' choice
case "$choice" in
  y|Y ) ghRelease;;
  n|N ) echo -e "\naborting ..."; exit 0;;
  * ) echo "invalid choice";;
esac
