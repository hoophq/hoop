PUBLIC_IMAGE := "hoophq/hoop"
VERSION ?=

# manual build/deploy
publish-snapshot: clean
	git clone https://github.com/runopsio/webapp.git build/webapp
	cd build/webapp && npm install && npm run release:hoop-ui
	mv resources rootfs/ui
	goreleaser release --rm-dist --snapshot
	docker tag ${PUBLIC_IMAGE}:v0.0.0-arm64v8 ${PUBLIC_IMAGE}:${VERSION}-arm64v8
	docker tag ${PUBLIC_IMAGE}:v0.0.0-amd64 ${PUBLIC_IMAGE}:${VERSION}-amd64
	docker push ${PUBLIC_IMAGE}:${VERSION}-arm64v8
	docker push ${PUBLIC_IMAGE}:${VERSION}-amd64
	docker manifest rm ${PUBLIC_IMAGE}:${VERSION} || true
	docker manifest create ${PUBLIC_IMAGE}:${VERSION} --amend ${PUBLIC_IMAGE}:${VERSION}-arm64v8 --amend ${PUBLIC_IMAGE}:${VERSION}-amd64
	docker manifest push ${PUBLIC_IMAGE}:${VERSION}

release: clean build-chart
	cd ./build/webapp && npm install && npm run release:hoop-ui
	mv ./build/webapp/resources ./rootfs/app/ui
	# goreleaser release
	echo -n "${GIT_TAG}" > ./latest.txt
	aws s3 cp ./dist/ s3://hoopartifacts/release/${GIT_TAG}/ --exclude "*" --include "*.tgz" --include "*.tar.gz" --recursive
	# aws s3 cp ./latest.txt s3://hoopartifacts/release/latest.txt
	# aws s3 cp ./dist/checksums.txt s3://hoopartifacts/release/${GIT_TAG}/checksums.txt

build-chart:
	find ./build/helm-chart -type f
	helm package ./build/helm-chart/chart/agent/ --app-version ${GIT_TAG} --destination ./dist/
	helm package ./build/helm-chart/chart/gateway/ --app-version ${GIT_TAG} --destination ./dist/

publish:
	./scripts/publish-release.sh

clean:
	rm -rf ./rootfs/app/ui

test:
	go test -v github.com/runopsio/hoop/...

.PHONY: publish-snapshot build-chart release publish clean test
