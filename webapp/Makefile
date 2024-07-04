GITCOMMIT ?= $(shell git rev-parse HEAD)
DIST_FOLDER ?= ./resources

build:
	echo -n "${GITCOMMIT}" > ${DIST_FOLDER}/commit.txt
	npm install && npm run release:hoop-ui
	tar -czf latest.tar.gz ${DIST_FOLDER}

release: build
	aws s3 cp latest.tar.gz s3://hoopartifacts/webapp/

.PHONY: build release
