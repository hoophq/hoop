# The ledger image carries two artifacts:
#
#   /usr/local/bin/ledger   the dummy gRPC server (stdlib only, no modules,
#                           so the build needs no network)
#   /srv/ledger.pb          the FileDescriptorSet compiled from ledger.proto
#
# The second one is for the SIDECAR, not for this server: a gRPC lane needs
# a descriptor set before it can decode, capture or mask payloads
# (ADR-0013). Compiling it here keeps ledger.proto the single source of
# truth; the grpc-descriptors init service in ../docker-compose.grpc.yml
# copies it out to the volume hoop-inspect mounts.
#
# Build via compose: docker compose -f docker-compose.yml \
#   -f grpc/docker-compose.grpc.yml build ledger

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/ledger .

FROM alpine:3.20 AS descriptors
RUN apk add --no-cache protobuf
COPY ledger.proto /src/ledger.proto
RUN mkdir -p /out && protoc -I /src \
    --include_imports --descriptor_set_out=/out/ledger.pb /src/ledger.proto

FROM alpine:3.20
COPY --from=build /out/ledger /usr/local/bin/ledger
COPY --from=descriptors /out/ledger.pb /srv/ledger.pb

# Non-root, same shape as the sidecar image: binds one high port, reads
# nothing but its own binary.
RUN adduser -D -u 10001 ledger
USER ledger

ENTRYPOINT ["/usr/local/bin/ledger"]
