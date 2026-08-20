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

$("$(dirname "$0")/release-notes.sh" "$GIT_TAG")
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
