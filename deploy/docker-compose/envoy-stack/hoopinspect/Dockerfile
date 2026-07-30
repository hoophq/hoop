# hoop-inspect sidecar image.
#
# The relay is assembled by the hoopinspect root module, which has zero
# dependencies. The binary lives in the nested cmd module because it links the
# optional plugins, so the build needs exactly two module downloads:
# github.com/hoophq/alcatraz, itself dependency-free, and gopkg.in/yaml.v3.
# One edge deep, no vendor directory, no transitive tree to review.
#
# Build from the repo root:
#   docker build -f deploy/docker-compose/envoy-stack/hoopinspect/Dockerfile \
#     -t hoop-inspect:local hoopinspect/

FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
# The nested cmd module declares the plugin dependencies; the root module and
# the other nested modules are pulled in beside it by replace directives.
WORKDIR /src/cmd
RUN go mod download
# CGO off and a static build so the runtime stage can be distroless-thin.
# The version matches hoopinspect/cmd/main.go; bump both together.
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=0.1.0" \
    -o /out/hoop-inspect .

FROM alpine:3.20
# curl only so the compose healthcheck can hit /healthz.
RUN apk add --no-cache curl ca-certificates
COPY --from=build /out/hoop-inspect /usr/local/bin/hoop-inspect

# Non-root: the process needs no privileges. It binds high ports and reads a
# config file, nothing else.
RUN adduser -D -u 10001 inspect
USER inspect

ENTRYPOINT ["/usr/local/bin/hoop-inspect"]
CMD ["-config", "/etc/hoop-inspect/config.yaml"]
