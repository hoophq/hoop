#!/bin/bash
set -e

gh auth status

echo -e "\n=> Here are the last 5 releases from github"
gh release list -L 5

# latest release
LATEST_VERSION=$(gh release list -L 1 |awk {'print $1'})
read -rep $'\nWhich version do you like to deploy?\n['$LATEST_VERSION'] => ' GIT_TAG
GIT_TAG="${GIT_TAG:=$LATEST_VERSION}"

ghRunWorkflow(){
  echo "running workflow for $GIT_TAG ..."
  gh workflow run deploy.yml -f version=$GIT_TAG --repo hoophq/hoop
  echo "=> deployment all app started, redirecting to workflow in 5 seconds ..."
  sleep 5 # give some time to github to update the workflow status
  gh workflow view deploy.yml -w
}

echo -e "\n=> Release Information $APP_NAME/$GIT_TAG"
gh release view $GIT_TAG --json author,name,createdAt,publishedAt,url,targetCommitish

echo -e "\n"
echo -e "=> Do you want to deploy all apps with the version=$GIT_TAG ?"
read -rep $'(y/n) => ' choice
case "$choice" in
  y|Y ) ghRunWorkflow;;
  n|N ) echo -e "\naborting ..."; exit 0;;
  * ) echo "invalid choice";;
esac
