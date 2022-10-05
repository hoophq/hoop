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

release: clean
	cd ./build/webapp && npm install && npm run release:hoop-ui
	mv ./build/webapp/resources ./rootfs/ui
	goreleaser release
	aws s3 cp .dist/ s3://hoopartifacts/release/${GIT_TAG}/ --exclude "*" --include "*.tar.gz" --include "checksums.txt" --recursive

publish:
	./scripts/publish-release.sh

clean:
	rm -rf ./rootfs/ui

test:
	go test -v github.com/runopsio/hoop/...

.PHONY: publish-snapshot publish clean test
