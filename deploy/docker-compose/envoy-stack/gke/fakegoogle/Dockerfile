# fake-google: tokeninfo + a Connect Gateway stand-in, stdlib only, so the
# build needs no network beyond the base images.
#
# Build via compose: docker compose -f docker-compose.yml \
#   -f gke/docker-compose.gke.yml build fake-google

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /out/fakegoogle .

FROM alpine:3.20
COPY --from=build /out/fakegoogle /usr/local/bin/fakegoogle
# Non-root; compose lowers ip_unprivileged_port_start so it can bind 443,
# the port the sidecar's default tokeninfo URL names.
RUN adduser -D -u 10001 fakegoogle
USER fakegoogle
ENTRYPOINT ["/usr/local/bin/fakegoogle"]
