#!/bin/bash
set -eo pipefail

: "${VERSION:?Variable not set or empty}"
: "${GOARCH:?Variable not set or empty}"

mkdir -p dist/
rm -f ./dist/hoop-$VERSION.darwin-$GOARCH.pkg
rm -rf ./Hoop.app

echo "starting packaging hoop-$VERSION.darwin-$GOARCH.pkg ..."
fyne package --name Hoop --os darwin --icon ./assets/icon.png --appBuild 1 --appVersion $VERSION --executable hoopapp --appID hoop.dev.app --release
pkgbuild --identifier Hoop --info Hoop.app/Contents/Info.plist --version $VERSION --ownership recommended --root Hoop.app --install-location /Applications/Hoop.app ./dist/hoop-$VERSION.darwin-$GOARCH.pkg

rm -rf ./Hoop.app
