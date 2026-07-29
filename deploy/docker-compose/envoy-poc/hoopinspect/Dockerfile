# hoop-inspect sidecar image.
#
# The relay is assembled by the hoopinspect root module, which has zero
# dependencies. The binary lives in the nested pii/alcatraz module because it
# wires in PII detection, so the build needs exactly one module download:
# github.com/hoophq/alcatraz, itself dependency-free. One edge deep, no vendor
# directory, no transitive tree to review.
#
# Build from the repo root:
#   docker build -f deploy/docker-compose/envoy-poc/hoopinspect/Dockerfile \
#     -t hoop-inspect:local hoopinspect/

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
# The nested module declares the alcatraz dependency; the root module is
# pulled in beside it by a replace directive.
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
