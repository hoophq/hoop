PUBLIC_IMAGE := "hoophq/hoop"
VERSION ?= $(or ${GIT_TAG},${GIT_TAG},v0)
GITCOMMIT ?= $(shell git rev-parse HEAD)
DIST_FOLDER ?= ./dist

DATE ?= $(shell date -u "+%Y-%m-%dT%H:%M:%SZ")

GOOS ?= linux
GOARCH ?= amd64
# compatible with uname -s
OS := $(shell echo "$(GOOS)" | awk '{print toupper(substr($$0, 1, 1)) tolower(substr($$0, 2))}')
SYMLINK_ARCH := $(if $(filter $(GOARCH),amd64),x86_64,$(if $(filter $(GOARCH),arm64),aarch64,$(ARCH)))

LDFLAGS := "-s -w \
-X github.com/runopsio/hoop/common/version.version=${VERSION} \
-X github.com/runopsio/hoop/common/version.gitCommit=${GITCOMMIT} \
-X github.com/runopsio/hoop/common/version.buildDate=${DATE}"

build:
	rm -rf ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH} && mkdir -p ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}
	env CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags ${LDFLAGS} -o ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}/ client/hoop.go
	cp ./scripts/hoopwrapper ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}/hoopwrapper
	cp ./scripts/hoopstart ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}/hoopstart
	tar -czvf ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${GOARCH}.tar.gz -C ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH} .
	tar -czvf ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${SYMLINK_ARCH}.tar.gz -C ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH} .
	sha256sum ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${GOARCH}.tar.gz > ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${GOARCH}_checksum.txt
	sha256sum ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${SYMLINK_ARCH}.tar.gz > ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${SYMLINK_ARCH}_checksum.txt
	rm -rf ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}

package-helmchart:
	mkdir -p ./dist
	helm package ./build/helm-chart/chart/agent/ --app-version ${VERSION} --destination ${DIST_FOLDER}/ --version ${VERSION}
	helm package ./build/helm-chart/chart/gateway/ --app-version ${VERSION} --destination ${DIST_FOLDER}/ --version ${VERSION}

release:
	./scripts/generate-changelog.sh ${VERSION} > ${DIST_FOLDER}/CHANGELOG.txt
	find ${DIST_FOLDER}/binaries/ -name *_checksum.txt -exec cat '{}' \; > ${DIST_FOLDER}/checksums.txt
	mv ${DIST_FOLDER}/binaries/*.tar.gz ${DIST_FOLDER}/
	echo -n "${VERSION}" > ${DIST_FOLDER}/latest.txt
	aws s3 cp ${DIST_FOLDER}/ s3://hoopartifacts/release/${VERSION}/ --exclude "*" --include "checksums.txt" --include "*.tgz" --include "*.tar.gz" --recursive
	aws s3 cp ${DIST_FOLDER}/latest.txt s3://hoopartifacts/release/latest.txt
	aws s3 cp ./scripts/install-cli.sh s3://hoopartifacts/release/install-cli.sh
	aws s3 cp ${DIST_FOLDER}/CHANGELOG.txt s3://hoopartifacts/release/${VERSION}/CHANGELOG.txt

build-webapp:
	mkdir -p ./dist
	cd ./build/webapp && npm install && npm run release:hoop-ui && mv ./resources ../../dist/webapp-resources

build-nodeapi:
	mkdir -p ./dist
	cd ./build/api && npm install --omit=dev && npm run build && mv ./out ../../dist/api && mv node_modules ../../dist/api/node_modules

build-dev-client:
	go build -ldflags "-s -w -X github.com/runopsio/hoop/common/version.strictTLS=false" -o ${HOME}/.hoop/bin/hoop github.com/runopsio/hoop/client

publish:
	./scripts/publish-release.sh

publish-tools:
	./scripts/publish-tools.sh

run-dev:
	./scripts/dev/run-setup.sh
	./scripts/dev/run.sh

run-dev-postgres:
	./scripts/dev/run-postgres.sh

clean:
	rm -rf ./rootfs/app/ui

test:
	go test -v github.com/runopsio/hoop/...

.PHONY: release publish publish-tools clean test build build-webapp build-nodeapi build-dev-client package-binaries package-helmchart publish-assets run-dev run-dev-postgres
