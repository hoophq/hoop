build-snapshot:
	goreleaser release --rm-dist --snapshot

# manual build/deploy
publish-snapshot:
	rm -rf rootfs/ui
	curl -sL https://hoopartifacts.s3.amazonaws.com/ui/hoop-ui-latest.tar.gz -o hoop-ui-latest.tar.gz
	tar -xf hoop-ui-latest.tar.gz && rm -f hoop-ui-latest.tar.gz
	mv resources rootfs/ui
	goreleaser release --rm-dist --snapshot
	docker tag runops/hoop:v0.0.0-arm64v8 runops/hoop:${VERSION}-arm64v8
	docker tag runops/hoop:v0.0.0-amd64 runops/hoop:${VERSION}-amd64
	docker push runops/hoop:${VERSION}-arm64v8
	docker push runops/hoop:${VERSION}-amd64
	docker manifest rm runops/hoop:${VERSION} || true
	docker manifest create runops/hoop:${VERSION} --amend runops/hoop:${VERSION}-arm64v8 --amend runops/hoop:${VERSION}-amd64
	docker manifest push runops/hoop:${VERSION}

publish:
	goreleaser release --rm-dist

release:
	echo "TODO"

.PHONY: build release
