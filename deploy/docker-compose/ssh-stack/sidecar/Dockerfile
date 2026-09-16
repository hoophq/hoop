# hoop-inspect with an SSH lane, plus the things an END-HOP session needs.
#
# One image serves both sidecars in this stack. The bastion uses none of the
# session machinery below — it admits no session capability — but giving it a
# second image to prove that would be a second thing to keep in step.
#
# The build stage matches envoy-stack/sidecar/Dockerfile: sidecar's codecs
# live in github.com/hoophq/libhoop, a PRIVATE module, so the build takes it
# from the local checkout through a second context. ./libhoop must exist
# beside the repo.
#
# Build via compose, which supplies both contexts:
#   docker compose build endhost

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
# curl for the compose healthcheck. rsync and coreutils because an end-hop
# SPAWNS what the client asks for: `rsync` over ssh runs `rsync --server` on
# this side, and the walkthrough greps and cats real files.
# curl for the compose healthcheck. The rest because an end-hop SPAWNS what
# the client asks for, and each client runs a DIFFERENT program on this side:
#   rsync           -> `rsync --server`, an exec
#   scp -O          -> `scp -f`, an exec (openssh-client carries the binary)
#   scp (default)   -> the sftp subsystem, served inside the sidecar itself
RUN apk add --no-cache curl ca-certificates rsync coreutils openssh-client

COPY --from=build /out/hoop-inspect /usr/local/bin/hoop-inspect

# The hash of the two source trees this binary was built from. run.sh hashes
# them again on every run and rebuilds when the image no longer matches, so a
# stale image cannot pass for a current one. A stamp on the image rather than
# in a file beside it, because a file can outlive the image it describes.
ARG SOURCE_STAMP=unknown
LABEL dev.hoop.source-stamp=$SOURCE_STAMP

# The account every session on the end-hop becomes.
#
# The sidecar RUNS AS this account rather than as root, and that is load
# bearing rather than hygiene. File transfer is served inside the sidecar
# process — there is no child to hand a credential to — so the subsystem is
# refused unless the process already IS the account (ADR-0015). Running as
# root here would leave `sftp` denied and `ssh` working, which is a confusing
# way to learn this rule.
RUN adduser -D -u 10001 -s /bin/sh devuser \
    && mkdir -p /home/devuser/upload \
    && chown devuser:devuser /home/devuser/upload

# A SECOND account, for the multi-user lane. The certificate names three
# principals — devuser, analyst and ghost — and only two of them exist here.
# `ghost` is deliberately absent: a name the CA vouches for is not by itself
# a name this host can become, and the session has to be refused rather than
# run as somebody else.
RUN adduser -D -u 10004 -s /bin/sh analyst

WORKDIR /home/devuser
USER devuser

ENTRYPOINT ["/usr/local/bin/hoop-inspect"]
CMD ["-config", "/etc/hoop-inspect/config.yaml"]
