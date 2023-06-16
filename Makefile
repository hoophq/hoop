PUBLIC_IMAGE := "hoophq/hoop"
VERSION ?= $(or ${GIT_TAG},${GIT_TAG},unknown)
GITCOMMIT ?= $(shell git rev-parse HEAD)
DIST_FOLDER ?= ./dist

DATE ?= $(shell date -u "+%Y-%m-%dT%H:%M:%SZ")

GOOS ?= linux
GOARCH ?= amd64

LDFLAGS := "-s -w \
-X github.com/runopsio/hoop/common/version.version=${VERSION} \
-X github.com/runopsio/hoop/common/version.gitCommit=${GITCOMMIT} \
-X github.com/runopsio/hoop/common/version.buildDate=${DATE}"

build:
	rm -rf ${DIST_FOLDER}/${GOOS}_${GOARCH} && mkdir -p ${DIST_FOLDER}/${GOOS}_${GOARCH}
	env GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags ${LDFLAGS} -o ${DIST_FOLDER}/${GOOS}_${GOARCH}/hoop client/main.go

done:
	find ${DIST_FOLDER} -type f
	echo "-------"
	find ./build -type f

release: clean
	cd ./build/webapp && npm install && npm run release:hoop-ui
	mv ./build/webapp/resources ./rootfs/app/ui
	goreleaser release
	helm package ./build/helm-chart/chart/agent/ --app-version ${GIT_TAG} --destination ./dist/ --version ${GIT_TAG}
	helm package ./build/helm-chart/chart/gateway/ --app-version ${GIT_TAG} --destination ./dist/ --version ${GIT_TAG}
	echo -n "${GIT_TAG}" > ./latest.txt
	aws s3 cp ./dist/ s3://hoopartifacts/release/${GIT_TAG}/ --exclude "*" --include "*.tgz" --include "*.tar.gz" --recursive
	aws s3 cp ./latest.txt s3://hoopartifacts/release/latest.txt
	aws s3 cp ./dist/checksums.txt s3://hoopartifacts/release/${GIT_TAG}/checksums.txt

publish:
	./scripts/publish-release.sh

clean:
	rm -rf ./rootfs/app/ui

test:
	go test -v github.com/runopsio/hoop/...

.PHONY: publish-snapshot release publish clean test build done
