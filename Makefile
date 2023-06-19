PUBLIC_IMAGE := "hoophq/hoop"
VERSION ?= $(or ${GIT_TAG},${GIT_TAG},v0)
GITCOMMIT ?= $(shell git rev-parse HEAD)
DIST_FOLDER ?= ./dist

DATE ?= $(shell date -u "+%Y-%m-%dT%H:%M:%SZ")

GOOS ?= linux
GOARCH ?= amd64
# compatible with uname -s
OS := $(shell echo "$(GOOS)" | awk '{print toupper(substr($$0, 1, 1)) tolower(substr($$0, 2))}')

LDFLAGS := "-s -w \
-X github.com/runopsio/hoop/common/version.version=${VERSION} \
-X github.com/runopsio/hoop/common/version.gitCommit=${GITCOMMIT} \
-X github.com/runopsio/hoop/common/version.buildDate=${DATE}"

build:
	rm -rf ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH} && mkdir -p ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}
	env CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags ${LDFLAGS} -o ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}/hoop client/main.go
	tar -czvf ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${GOARCH}.tar.gz -C ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH} .
	sha256sum ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${GOARCH}.tar.gz > ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${GOARCH}_checksum.txt
	rm -rf ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}

package-helmchart:
	mkdir -p ./dist
	helm package ./build/helm-chart/chart/agent/ --app-version ${VERSION} --destination ${DIST_FOLDER}/ --version ${VERSION}
	helm package ./build/helm-chart/chart/gateway/ --app-version ${VERSION} --destination ${DIST_FOLDER}/ --version ${VERSION}

release:
	./scripts/brew-recipe.sh ${DIST_FOLDER} ${VERSION} > ${DIST_FOLDER}/hoop.rb
	find ${DIST_FOLDER}/binaries/ -name *_checksum.txt -exec cat '{}'  \; > ${DIST_FOLDER}/checksums.txt
	echo -n "${VERSION}" > ${DIST_FOLDER}/latest.txt
	aws s3 cp ${DIST_FOLDER}/ s3://hoopartifacts/release/${VERSION}/ --exclude "*" --include "checksums.txt" --include "*.tgz" --include "*.tar.gz" --recursive --dryrun
	aws s3 cp ${DIST_FOLDER}/hoop.rb s3://hoopartifacts/release/${VERSION}/hooprb.txt --dryrun
	aws s3 cp ${DIST_FOLDER}/latest.txt s3://hoopartifacts/release/latest.txt --dryrun
	cat ${DIST_FOLDER}/hoop.rb

build-webapp:
	mkdir -p ./dist
	cd ./build/webapp && npm install && npm run release:hoop-ui && mv ./resources ../../dist/webapp-resources

publish:
	./scripts/publish-release.sh

publish-tools:
	./scripts/publish-tools.sh

clean:
	rm -rf ./rootfs/app/ui

test:
	go test -v github.com/runopsio/hoop/...

.PHONY: release publish publish-tools clean test build build-webapp package-binaries package-helmchart publish-assets
