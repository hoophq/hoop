# The hoop gateway, for the review lane only.
#
# It exists in this stack for ONE reason: the human-approval gate
# (`require_review`) needs a control plane to file reviews with, and a person
# to answer them. Every other lane here runs with no gateway at all, which is
# the stack's whole point — hoop-inspect reads one YAML file and calls nobody.
#
# The binary is cross-compiled on the HOST by ../run.sh rather than built here.
# Building it in-image would mean copying the whole repository into a Docker
# context (webapp, node_modules, the CLJS toolchain) to compile one Go binary,
# and would throw away the host's Go build cache on every run. The tradeoff is
# that this image cannot be built on its own; ./run.sh --review is the entry
# point.
#
# There is no webapp bundle in here, so `/` serves nothing. Reviews are
# answered over the REST API, which is what an autonomous-agent deployment
# would do anyway. Point a real gateway at the same database if you want the
# UI.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates curl tzdata

COPY bin/hoop /usr/local/bin/hoop

# Runs as root: the gateway wants /opt/hoop for its session WAL, and this is a
# throwaway demo container on a compose network with no published data path.
# The sidecar next door is the one that runs unprivileged.
RUN mkdir -p /opt/hoop/sessions

EXPOSE 8009 8010
ENTRYPOINT ["/usr/local/bin/hoop"]
CMD ["start", "gateway"]
