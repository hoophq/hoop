# hoop-inspect sidecar image.
#
# Built from the hoopinspect module only — it has zero dependencies, so the
# build needs no module download and no vendor directory. That property is
# the point: this image can be reproduced and audited without a supply-chain
# review.
#
# Build from the repo root:
#   docker build -f deploy/docker-compose/envoy-poc/hoopinspect/Dockerfile \
#     -t hoop-inspect:local hoopinspect/

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
# CGO off and a static build so the runtime stage can be distroless-thin.
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=poc" \
    -o /out/hoop-inspect ./cmd/hoop-inspect

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
