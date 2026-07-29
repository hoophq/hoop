# hoop-inspect sidecar image, PII-enabled build.
#
# Two builds exist and they differ in one property worth stating plainly:
#
#   cmd/hoop-inspect           zero dependencies, no module download at all
#   pii/alcatraz/.../hoop-inspect-pii   links github.com/hoophq/alcatraz
#
# This Dockerfile builds the second, because the POC demonstrates masking
# Brazilian CPFs and IBANs, which the eight built-in detectors cannot find.
# Alcatraz is itself dependency-free, so the tree is exactly one edge deep —
# but it is not zero, and a deployment that has to justify a supply chain
# should build the base binary instead by pointing the build stage at
# ./cmd/hoop-inspect and dropping the `go mod download`.
#
# Build from the repo root:
#   docker build -f deploy/docker-compose/envoy-poc/hoopinspect/Dockerfile \
#     -t hoop-inspect:local hoopinspect/

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
# The nested module is where the alcatraz dependency lives; the root module
# stays dependency-free and is pulled in by a replace directive.
WORKDIR /src/pii/alcatraz
RUN go mod download
# CGO off and a static build so the runtime stage can be distroless-thin.
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=poc" \
    -o /out/hoop-inspect ./cmd/hoop-inspect-pii

FROM alpine:3.20
# curl only so the compose healthcheck can hit /healthz.
RUN apk add --no-cache curl ca-certificates
COPY --from=build /out/hoop-inspect /usr/local/bin/hoop-inspect

# Non-root: the process needs no privileges. It binds high ports and reads a
# config file, nothing else.
RUN adduser -D -u 10001 inspect
USER inspect

ENTRYPOINT ["/usr/local/bin/hoop-inspect"]
CMD ["-config", "/etc/hoop-inspect/config.json"]
