#!/usr/bin/env bash
#
# Brings up the stack:
#
#   1. build the hoop-inspect sidecar image from the local sidecar tree
#   2. start the emulators and wait until they answer
#   3. bootstrap /descriptors: gRPC server reflection first, buf fallback
#   4. start hoop-inspect
#
# Step 3 is the reason this script exists. The spanner lane pins a
# FileDescriptorSet (runtime lanes never touch reflection, ADR-0013), and the
# sidecar refuses to start without it, so the descriptors must exist BEFORE
# `compose up hoop-inspect`. The script tries reflection (-grpc-discover)
# first, over the same address the lane will dial; the Cloud Spanner emulator
# currently serves NO reflection, so the usual path is the fallback: buf
# builds the set from the public googleapis tree, filtered to the Spanner and
# BigQuery Storage APIs. Needs the grpc2 branch: `protocol: spanner` and
# discovery on spanner lanes land there.
#
# No TLS anywhere: both emulators speak h2c and authenticate nobody.
# Compare envoy-stack/run.sh, where certs are half the script.
#
# Usage:
#   ./run.sh              bring everything up
#   ./run.sh --rebuild    force a sidecar image rebuild first
#   ./run.sh down         tear down including volumes

set -euo pipefail
cd "$(dirname "$0")"

c_ok()   { printf '\033[32m  ok\033[0m  %s\n' "$*"; }
c_step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
warn()   { printf '\033[33mwarn\033[0m %s\n' "$*"; }
die()    { printf '\033[31mfail\033[0m %s\n' "$*" >&2; exit 1; }

if [[ "${1:-}" == "down" ]]; then
    docker compose down -v --remove-orphans
    exit 0
fi

REBUILD=""
[[ "${1:-}" == "--rebuild" ]] && REBUILD=1

need() { command -v "$1" >/dev/null || die "missing required tool: $1"; }
need docker; need curl

# ------------------------------------------------------------ 1. build sidecar
# The image is built from ../../../sidecar (libhoop from ../../../libhoop via
# a second build context), so a library change is one rebuild away from
# running. The tag is gcloud-specific on purpose: envoy-stack's cached
# hoop-inspect:local may predate the spanner protocol.
c_step "hoop-inspect image"
if [[ -n "$REBUILD" ]] || ! docker image inspect hoop-inspect:gcloud >/dev/null 2>&1; then
    docker compose build hoop-inspect
    c_ok "built hoop-inspect:gcloud from ../../../sidecar"
else
    c_ok "reusing hoop-inspect:gcloud (./run.sh --rebuild to rebuild)"
fi

# -------------------------------------------------------------- 2. emulators
# The emulator images are shell-less, so they carry no compose healthcheck;
# --wait therefore gates only on the client (which has one). The real
# readiness wait runs FROM the client, because the emulator ports are not
# published to the host. That is deliberate: the data plane is only
# reachable through the sidecar.
c_step "Starting emulators"
docker compose up -d --wait spanner bigquery client

probe() { # probe <label> <curl args...>: retry until the target answers HTTP
    local label=$1; shift
    for _ in $(seq 1 30); do
        if docker compose exec -T client curl -s -o /dev/null --max-time 2 "$@"; then
            c_ok "$label answers"
            return 0
        fi
        sleep 1
    done
    die "$label did not answer within 30s"
}
# Any HTTP answer proves the server is up; -f would also demand a 2xx,
# which the bigquery emulator's bare / does not return.
probe "spanner REST gateway (:9020)" -f http://spanner:9020/v1/projects/demo/instances
probe "bigquery emulator (:9050)"       http://bigquery:9050/

# ------------------------------------------------------------ 3. descriptors
# Both runs use config-discover.yaml: the same lanes as config.yaml minus the
# grpc: blocks, so the not-yet-fetched descriptor file cannot block the very
# run that fetches it.
#
# --user 0 because the named volume arrives root-owned and the image runs as
# uid 10001; the chmod afterwards is because -grpc-discover-out writes 0600,
# which as root would leave a file the sidecar's uid (and grpcurl's) cannot
# read.
c_step "Descriptor bootstrap"
# Reflection first: it dials the address the lane dials, and if the
# emulator ever grows reflection this stack starts exercising the
# -grpc-discover path end to end without a change here. Today it answers
# Unimplemented, so the buf fallback below is the path that runs: the
# googleapis GitHub tree, filtered to the two API families this stack
# fronts (imports are included in the image automatically). Network on
# first run, cached by docker afterwards. master because googleapis tags
# no releases and a validation stack can tolerate an unpinned tree.
SPANNER_FROM_BUF=""
if docker compose run --rm -T --user 0 hoop-inspect \
    -grpc-discover spanner -grpc-discover-out /descriptors/spanner.pb \
    -config /etc/hoop-inspect/config-discover.yaml; then
  c_ok "wrote /descriptors/spanner.pb over gRPC server reflection"
else
  warn "spanner emulator serves no gRPC reflection; building the set with buf instead"
  docker compose --profile tools run --rm -T buf \
      build 'https://github.com/googleapis/googleapis/archive/refs/heads/master.tar.gz#strip_components=1' \
      --path google/spanner --path google/bigquery/storage \
      -o '/descriptors/spanner.pb#format=binpb'
  SPANNER_FROM_BUF=1
  c_ok "wrote /descriptors/spanner.pb from the googleapis tree (Spanner + BigQuery Storage)"
fi

# Tolerated failure: the bqstorage lane is method-only (no grpc: block), so
# it runs and enforces without a descriptor set. The protoset is only a
# convenience for ./demo.sh's grpcurl calls, which skip the bq beats with a
# notice when it is absent. When the buf fallback produced spanner.pb it
# already carries the BigQuery Storage protos (the --path pair above), so a
# copy stands in for the reflection the goccy emulator does not serve.
if docker compose run --rm -T --user 0 hoop-inspect \
    -grpc-discover bqstorage -grpc-discover-out /descriptors/bqstorage.pb \
    -config /etc/hoop-inspect/config-discover.yaml; then
    c_ok "wrote /descriptors/bqstorage.pb over gRPC server reflection"
elif [[ -n "$SPANNER_FROM_BUF" ]]; then
    docker compose run --rm -T --user 0 --entrypoint /bin/sh hoop-inspect \
        -c 'cp /descriptors/spanner.pb /descriptors/bqstorage.pb'
    c_ok "bqstorage protoset copied from the buf artifact"
else
    warn "bqstorage discovery failed and spanner.pb came from reflection (no bq protos);"
    warn "the lane is method-only and runs without it; demo.sh will skip its beats"
fi

docker compose run --rm -T --user 0 --entrypoint /bin/sh hoop-inspect \
    -c 'chmod 444 /descriptors/*.pb 2>/dev/null || true'

# ------------------------------------------------------------- 4. the sidecar
c_step "Starting hoop-inspect"
docker compose up -d --wait hoop-inspect
curl -sf http://localhost:19001/healthz >/dev/null || die "sidecar admin :19001 not healthy"
c_ok "spanner lane :29010, bqstorage lane :29060, admin :19001"

cat <<'EOF'

ready

  Walk the lanes:   ./demo.sh
  Admin API:        curl -s localhost:19001/stats
                    curl -s 'localhost:19001/api/sessions?limit=5'
  A call, by hand:  docker compose run --rm grpcurl -plaintext \
                      -protoset /descriptors/spanner.pb \
                      -d '{"database":"projects/demo/instances/demo-instance/databases/demodb"}' \
                      hoop-inspect:29010 google.spanner.v1.Spanner/CreateSession
  Tear down:        ./run.sh down
EOF
