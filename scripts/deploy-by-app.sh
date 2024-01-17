#!/bin/bash
set -e

gh auth status

declare -a apps=("appdemo" "clari" "ebanx" "enjoei" "rdstation", "unico")
echo -e "=> Here are the available apps to deploy"
for app in "${apps[@]}"; do
  echo $app
done

read -rep $'\nWhich app do you like to deploy?\n=> ' APP_NAME

if ! [[ ${apps[@]} =~ $APP_NAME ]]; then
  echo -e "app $APP_NAME not found"
  exit 1
fi

echo -e "\n=> Here are the last 5 releases from github"
gh release list -L 5

# latest release
LATEST_VERSION=$(gh release list -L 1 |awk {'print $1'})
read -rep $'\nWhich version do you like to deploy?\n['$LATEST_VERSION'] => ' GIT_TAG
GIT_TAG="${GIT_TAG:=$LATEST_VERSION}"

ghRunWorkflow(){
  echo "running workflow for $APP_NAME/$GIT_TAG ..."
  gh workflow run deploy-by-app.yml -f version=$GIT_TAG -f app=$APP_NAME --repo hoophq/hoop
  echo "=> deployment for $APP_NAME/$GIT_TAG started, redirecting to workflow in 5 seconds ..."
  sleep 5 # give some time to github to update the workflow status
  gh workflow view deploy-by-app.yml -w
}

echo -e "\n=> Release Information $APP_NAME/$GIT_TAG"
gh release view $GIT_TAG --json author,name,createdAt,publishedAt,url,targetCommitish

echo -e "\n"
echo -e "=> Do you want to deploy the app=$APP_NAME with the version=$GIT_TAG ?"
read -rep $'(y/n) => ' choice
case "$choice" in
  y|Y ) ghRunWorkflow;;
  n|N ) echo -e "\naborting ..."; exit 0;;
  * ) echo "invalid choice";;
esac
