#!/bin/bash
set -e

gh auth status

echo -e "Check the previous tags of the agent tools image:\n• https://hub.docker.com/r/hoophq/agent-tools/tags"

echo "need to implement, see .github/workflows/release-tools"
exit 1

read -rep $'\nWhich version do you like to release?\n=> ' VERSION

ghRelease(){
  gh workflow run release-tools.yml -f version=${VERSION} -f environment=production --ref main
  gh run list --workflow=release-tools.yml
}

read -rep $'=> Do you with to create this new tag?\n(y/n) => ' choice
case "$choice" in
  y|Y ) ghRelease;;
  n|N ) echo -e "\naborting ..."; exit 0;;
  * ) echo "invalid choice";;
esac
