#!/bin/bash
set -e

PUBLIC_REPO=hoophq/hoopcli

gh auth status

echo "=> Here are the last 10 releases from github"
gh release list -L 10

read -rep $'\nWhich version do you like to release?\n=> ' GIT_TAG
NOTE_FILE="$(mktemp).md"
GIT_COMMIT=$(git log -1 --pretty=format:%B)
cat - >$NOTE_FILE <<EOF
# Changelog

$GIT_COMMIT
EOF
${VISUAL:-${EDITOR:-vi}} $NOTE_FILE


NOTE_CONTENT=$(cat $NOTE_FILE)
cat - >$NOTE_FILE <<EOF
$NOTE_CONTENT

## Assets

- [Darwin arm64](https://hoopartifacts.s3.amazonaws.com/release/${GIT_TAG}/hoop_${GIT_TAG}_Darwin_arm64.tar.gz)
- [Darwin amd64](https://hoopartifacts.s3.amazonaws.com/release/${GIT_TAG}/hoop_${GIT_TAG}_Darwin_x86_64.tar.gz)
- [Linux arm64](https://hoopartifacts.s3.amazonaws.com/release/${GIT_TAG}/hoop_${GIT_TAG}_Linux_arm64.tar.gz)
- [Linux amd64](https://hoopartifacts.s3.amazonaws.com/release/${GIT_TAG}/hoop_${GIT_TAG}_Linux_x86_64.tar.gz)
- [Windows arm64](https://hoopartifacts.s3.amazonaws.com/release/${GIT_TAG}/hoop_${GIT_TAG}_Windows_arm64.tar.gz)
- [Windows amd64](https://hoopartifacts.s3.amazonaws.com/release/${GIT_TAG}/hoop_${GIT_TAG}_Windows_x86_64.tar.gz)
- [checksums.txt](https://hoopartifacts.s3.amazonaws.com/release/${GIT_TAG}/checksums.txt)

## Docker Images

- hoophq/hoop:latest
- hoophq/hoop:${GIT_TAG}

EOF

cat - <<EOF

RELEASE NOTES
-------------
$(cat $NOTE_FILE)

EOF

ghRelease(){
  gh release create $GIT_TAG -F $NOTE_FILE --title $GIT_TAG
  gh release create $GIT_TAG -F $NOTE_FILE --title $GIT_TAG --repo $PUBLIC_REPO
}

read -rep $'=> Do you with to create this release?\n(y/n) => ' choice
case "$choice" in
  y|Y ) ghRelease;;
  n|N ) echo -e "\naborting ..."; exit 0;;
  * ) echo "invalid choice";;
esac