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
COPY ledger.proto reports.proto internal.proto /src/
# One protoc run PER artifact, on purpose: this simulates the multi-team
# shape where each service's CI ships its own descriptor set and nobody
# owns a central re-bundle. The lane configs list ledger.pb and
# reports.pb; internal.pb is shipped only so grpcurl can encode calls the
# lane cannot read (the strict/lenient demo).
RUN mkdir -p /out \
    && protoc -I /src --include_imports --descriptor_set_out=/out/ledger.pb /src/ledger.proto \
    && protoc -I /src --include_imports --descriptor_set_out=/out/reports.pb /src/reports.proto \
    && protoc -I /src --include_imports --descriptor_set_out=/out/internal.pb /src/internal.proto

FROM alpine:3.20
COPY --from=build /out/ledger /usr/local/bin/ledger
COPY --from=descriptors /out/*.pb /srv/

# Non-root, same shape as the sidecar image: binds one high port, reads
# nothing but its own binary.
RUN adduser -D -u 10001 ledger
USER ledger

ENTRYPOINT ["/usr/local/bin/ledger"]
