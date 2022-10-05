# manual build/deploy
publish-snapshot: clean
	git clone https://github.com/runopsio/webapp.git build/webapp && cd build/webapp
	npm install && npm run release:hoop-ui
	mv resources rootfs/ui
	goreleaser release --rm-dist --snapshot
	docker tag runops/hoop:v0.0.0-arm64v8 runops/hoop:${VERSION}-arm64v8
	docker tag runops/hoop:v0.0.0-amd64 runops/hoop:${VERSION}-amd64
	docker push runops/hoop:${VERSION}-arm64v8
	docker push runops/hoop:${VERSION}-amd64
	docker manifest rm runops/hoop:${VERSION} || true
	docker manifest create runops/hoop:${VERSION} --amend runops/hoop:${VERSION}-arm64v8 --amend runops/hoop:${VERSION}-amd64
	docker manifest push runops/hoop:${VERSION}

publish: clean
	cd ./build/webapp && npm install && npm run release:hoop-ui
	mv ./build/webapp/resources ./rootfs/ui
	goreleaser release --rm-dist

clean:
	rm -rf ./rootfs/ui

test:
	go test -v github.com/runopsio/hoop/...

.PHONY: publish-snapshot publish clean test
