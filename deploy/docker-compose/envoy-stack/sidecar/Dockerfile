# hoop-inspect sidecar image.
#
# The relay is assembled by the sidecar root module. Its protocol codecs
# live in github.com/hoophq/libhoop, a PRIVATE module, so the build cannot
# reach them from the proxy without a credential. This stack already builds
# sidecar from the local tree, so it takes libhoop from the local checkout
# the same way, through a second build context.
#
# That means ./libhoop must exist beside the repo. It is gitignored, so a
# clone without it cannot build this image — which is the same constraint as
# building sidecar at all now that the codecs are private.
#
# Build via compose, which supplies both contexts:
#   docker compose build hoop-inspect

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
COPY --from=libhoop . /libhoop
# The nested cmd module declares the plugin dependencies; the root module and
# the other nested modules are pulled in beside it by replace directives.
# libhoop needs one too: the version its consumers pin is not published.
WORKDIR /src/cmd
RUN go mod edit -replace github.com/hoophq/libhoop=/libhoop && go mod tidy
# CGO off and a static build so the runtime stage can be distroless-thin.
# The version matches sidecar/cmd/main.go; bump both together.
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
