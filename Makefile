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
POSTREST_ARCH_SUFFIX := $(if $(filter $(GOARCH),amd64),linux-static-x64.tar.xz,$(if $(filter $(GOARCH),arm64),ubuntu-aarch64.tar.xz,$(ARCH)))

LDFLAGS := "-s -w \
-X github.com/hoophq/hoop/common/version.version=${VERSION} \
-X github.com/hoophq/hoop/common/version.gitCommit=${GITCOMMIT} \
-X github.com/hoophq/hoop/common/version.buildDate=${DATE} \
-X github.com/hoophq/hoop/common/monitoring.honeycombApiKey=${HONEYCOMB_API_KEY} \
-X github.com/hoophq/hoop/common/monitoring.sentryDSN=${SENTRY_DSN} \
-X github.com/hoophq/hoop/gateway/analytics.segmentApiKey=${SEGMENT_API_KEY} \
-X github.com/hoophq/hoop/gateway/analytics.intercomHmacKey=${INTERCOM_HMAC_KEY}"

run-dev:
	./scripts/dev/run.sh

run-dev-postgres:
	./scripts/dev/run-postgres.sh

build-dev-client:
	go build -ldflags "-s -w -X github.com/hoophq/hoop/client/proxy.defaultListenAddrValue=0.0.0.0" -o ${HOME}/.hoop/bin/hoop github.com/hoophq/hoop/client

test: test-oss test-enterprise

test-oss:
	rm libhoop || true
	ln -s _libhoop libhoop
	env CGO_ENABLED=0 go test -json -v github.com/hoophq/hoop/...

test-enterprise:
	rm libhoop || true
	ln -s ../libhoop libhoop
	env CGO_ENABLED=0 go test -json -v github.com/hoophq/hoop/...

generate-openapi-docs:
	cd ./gateway/ && go run github.com/swaggo/swag/cmd/swag@v1.16.3 init -g api/server.go -o api/openapi/autogen --outputTypes go --markdownFiles api/openapi/docs/ --parseDependency

swag-fmt:
	swag fmt

publish:
	./scripts/publish-release.sh

build:
	rm -rf ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH} && mkdir -p ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}
	env CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${GOARCH} go build -ldflags ${LDFLAGS} -o ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}/ client/hoop.go
	tar -czvf ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${GOARCH}.tar.gz -C ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH} .
	tar -czvf ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${SYMLINK_ARCH}.tar.gz -C ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH} .
	sha256sum ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${GOARCH}.tar.gz > ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${GOARCH}_checksum.txt
	sha256sum ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${SYMLINK_ARCH}.tar.gz > ${DIST_FOLDER}/binaries/hoop_${VERSION}_${OS}_${SYMLINK_ARCH}_checksum.txt
	rm -rf ${DIST_FOLDER}/binaries/${GOOS}_${GOARCH}

build-webapp:
	mkdir -p ${DIST_FOLDER}
	cd ./webapp && npm install && npm run release:hoop-ui && cd ../
	cat ./webapp/src/webapp/version.js | awk -F"'" '{printf "%s", $$2}' > ./webapp/resources/version.txt
	tar -czf ${DIST_FOLDER}/webapp.tar.gz -C ./webapp/resources .

extract-webapp:
	mkdir -p ./rootfs/app/ui && tar -xf ${DIST_FOLDER}/webapp.tar.gz -C rootfs/app/ui/

build-helm-chart:
	mkdir -p ${DIST_FOLDER}
	helm package ./deploy/helm-chart/chart/agent/ --app-version ${VERSION} --destination ${DIST_FOLDER}/ --version ${VERSION}
	helm package ./deploy/helm-chart/chart/gateway/ --app-version ${VERSION} --destination ${DIST_FOLDER}/ --version ${VERSION}

build-gateway-bundle:
	rm -rf ${DIST_FOLDER}/hoopgateway
	mkdir -p ${DIST_FOLDER}/hoopgateway/opt/hoop/bin
	mkdir -p ${DIST_FOLDER}/hoopgateway/opt/hoop/migrations
	mkdir -p ${DIST_FOLDER}/hoopgateway/opt/hoop/webapp
	curl -sL https://github.com/PostgREST/postgrest/releases/download/v11.2.2/postgrest-v11.2.2-${POSTREST_ARCH_SUFFIX} -o postgrest.tar.xz && \
	tar -xf postgrest.tar.xz -C ${DIST_FOLDER}/hoopgateway/opt/hoop/bin/ && rm -f postgrest.tar.xz && \
	chmod 0755 ${DIST_FOLDER}/hoopgateway/opt/hoop/bin/postgrest && \
	tar -xf ${DIST_FOLDER}/binaries/hoop_${VERSION}_Linux_${GOARCH}.tar.gz -C ${DIST_FOLDER}/hoopgateway/opt/hoop/bin/ && \
	cp rootfs/app/migrations/*.up.sql ${DIST_FOLDER}/hoopgateway/opt/hoop/migrations/ && \
	tar -xf ${DIST_FOLDER}/webapp.tar.gz -C ${DIST_FOLDER}/hoopgateway/opt/hoop/webapp --strip 1 && \
	tar -czf ${DIST_FOLDER}/hoopgateway_${VERSION}-Linux_${GOARCH}.tar.gz -C ${DIST_FOLDER}/ hoopgateway

release: release-aws-cf-templates
	./scripts/generate-changelog.sh ${VERSION} > ${DIST_FOLDER}/CHANGELOG.txt
	find ${DIST_FOLDER}/binaries/ -name *_checksum.txt -exec cat '{}' \; > ${DIST_FOLDER}/checksums.txt
	mv ${DIST_FOLDER}/binaries/*.tar.gz ${DIST_FOLDER}/
	echo -n "${VERSION}" > ${DIST_FOLDER}/latest.txt
	aws s3 cp ${DIST_FOLDER}/ s3://hoopartifacts/release/${VERSION}/ --exclude "*" --exclude webapp.tar.gz --include checksums.txt --include "*.tgz" --include "*.tar.gz" --recursive
	aws s3 cp ${DIST_FOLDER}/latest.txt s3://hoopartifacts/release/latest.txt
	aws s3 cp ./scripts/install-cli.sh s3://hoopartifacts/release/install-cli.sh
	aws s3 cp ${DIST_FOLDER}/CHANGELOG.txt s3://hoopartifacts/release/${VERSION}/CHANGELOG.txt

release-aws-cf-templates:
	sed "s|LATEST_HOOP_VERSION|${VERSION}|g" deploy/aws/hoopdev-platform.template.yaml > ${DIST_FOLDER}/hoopdev-platform.template.yaml
	aws s3 cp --region us-east-1 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-us-east-1/${VERSION}/hoopdev-platform.template.yaml
	aws s3 cp --region us-east-1 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-us-east-1/latest/hoopdev-platform.template.yaml
	aws s3 cp --region us-east-2 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-us-east-2/${VERSION}/hoopdev-platform.template.yaml
	aws s3 cp --region us-east-2 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-us-east-2/latest/hoopdev-platform.template.yaml
	aws s3 cp --region us-west-1 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-us-west-1/${VERSION}/hoopdev-platform.template.yaml
	aws s3 cp --region us-west-1 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-us-west-1/latest/hoopdev-platform.template.yaml
	aws s3 cp --region us-west-2 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-us-west-2/${VERSION}/hoopdev-platform.template.yaml
	aws s3 cp --region us-west-2 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-us-west-2/latest/hoopdev-platform.template.yaml
	aws s3 cp --region eu-west-1 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-eu-west-1/${VERSION}/hoopdev-platform.template.yaml
	aws s3 cp --region eu-west-1 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-eu-west-1/latest/hoopdev-platform.template.yaml
	aws s3 cp --region eu-west-2 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-eu-west-2/${VERSION}/hoopdev-platform.template.yaml
	aws s3 cp --region eu-west-2 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-eu-west-2/latest/hoopdev-platform.template.yaml
	aws s3 cp --region eu-central-1 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-eu-central-1/${VERSION}/hoopdev-platform.template.yaml
	aws s3 cp --region eu-central-1 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-eu-central-1/latest/hoopdev-platform.template.yaml
	aws s3 cp --region ap-southeast-2 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-ap-southeast-2/${VERSION}/hoopdev-platform.template.yaml
	aws s3 cp --region ap-southeast-2 ${DIST_FOLDER}/hoopdev-platform.template.yaml s3://hoopdev-platform-cf-ap-southeast-2/latest/hoopdev-platform.template.yaml

publish-sentry-sourcemaps:
	tar -xvf ${DIST_FOLDER}/webapp.tar.gz
	sentry-cli sourcemaps upload --release=$$(cat ./version.txt) ./public/js/app.js.map --org hoopdev --project webapp

.PHONY: run-dev run-dev-postgres test-enterprise test-oss test generate-openapi-docs build build-dev-client build-webapp build-helm-chart build-gateway-bundle extract-webapp publish release release-aws-cf-templates swag-fmt
