build-snapshot:
	goreleaser release --rm-dist --snapshot

build-push:
	goreleaser release --rm-dist

release:
	echo "TODO"

.PHONY: build release
