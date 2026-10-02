# The terminating bastion, built from the LOCAL sidecar and libhoop trees.
#
# There is no published-image mode in this stack, and that is not an
# oversight. ADR-0027 is not in a release yet, so no published
# hoophq/hoopsidecar carries the relay code — a released image would bring up
# a stack whose whole subject is missing, and fail in a way that looks like a
# configuration mistake. When it ships, add a Dockerfile.released here the way
# ssh-stack has one.
#
# It needs ../../../libhoop, a PRIVATE module, so this stack is runnable by
# hoop engineers alone until then.
#
# Build via compose, which supplies both contexts:
#   ./run.sh

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
COPY --from=libhoop . /libhoop
WORKDIR /src/cmd
RUN go mod edit -replace github.com/hoophq/libhoop=/libhoop && go mod tidy
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=0.1.0" \
    -o /out/hoop-inspect .

FROM alpine:3.20
# curl for the compose healthcheck, and nothing else. A TERMINATING BASTION
# spawns no process and serves no session of its own, so the tools an end-hop
# image needs — a shell for a user, rsync, openssh-client — would be surface
# with nothing to do. What this container runs commands ON is somewhere else.
RUN apk add --no-cache curl ca-certificates

COPY --from=build /out/hoop-inspect /usr/local/bin/hoop-inspect

# The hash of the two source trees this binary was built from. run.sh hashes
# them again on every run and rebuilds when the image no longer matches, so a
# stale image cannot pass for a current one.
ARG SOURCE_STAMP=unknown
LABEL dev.hoop.source-stamp=$SOURCE_STAMP

# An unprivileged account with no shell, and no accounts for the people who
# log in. There is deliberately no `devuser` here: on an end-hop the login
# name has to resolve to an OS account, and on a bastion it must not — a
# session never runs here, and a host that could run one would be a host
# worth attacking for more than its credentials.
RUN adduser -D -H -u 10001 -s /sbin/nologin hoop \
    && mkdir -p /etc/hoop-inspect \
    && chown hoop:hoop /etc/hoop-inspect

USER hoop
ENTRYPOINT ["/usr/local/bin/hoop-inspect"]
CMD ["--config", "/etc/hoop-inspect/config.yaml"]
