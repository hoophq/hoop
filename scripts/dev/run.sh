#!/bin/bash

set -eo pipefail

if ! [[ -f .env ]]; then
  echo "missing .env file"
  exit 1
fi

# libhoop used to be selected here: LIBHOOP in .env named a directory or a git
# remote, and the block below symlinked or cloned it into ./libhoop. It is now
# the module github.com/hoophq/libhoop/v2, resolved from the proxy like any
# other dependency. Export GOPRIVATE=github.com/hoophq/libhoop and have git
# credentials for it; to build against a local clone, run `make libhoop-dev`.

trap ctrl_c INT

function ctrl_c() {
    docker stop hoopdev
    exit 130
}

mkdir -p "$HOME/.hoop/dev"

WEBAPP_BUILD="${WEBAPP_BUILD:-0}"
if [[ $WEBAPP_BUILD == "1" ]]; then
  echo 'run "make build-dev-webapp" to build the webapp'
  exit 1
fi

docker build -t hoopdev -f ./scripts/dev/Dockerfile .
mkdir -p ./dist/dev/bin
cp ./scripts/dev/entrypoint.sh ./dist/dev/bin/entrypoint.sh

# Build Rust agent for development
HOOP_RS_BUILD="${HOOP_RS_BUILD:-1}"
if [[ $HOOP_RS_BUILD == "1" ]]; then
  echo "Building Rust agent..."
  echo ""
  echo "You need to have Rust installed to build the Rust agent."
  echo "You need to have Cross installed to build the Rust agent for multiple architectures."
  make build-dev-rust
  cp $HOME/.hoop/bin/hoop_rs ./dist/dev/bin/hoop_rs
fi


# Alcatraz NER model, for testing DLP_PROVIDER=alcatraz with a masking rule
# that asks for a statistical entity type (PERSON, LOCATION, NRP). The backend
# never fetches at runtime, so the files have to be on disk before the first
# such session: the published hoophq/hoopagent `-alcatraz` tags bake them in,
# and the dev container mounts them from the host instead.
#
# Selecting the provider in .env is the whole setup — the cache fills itself on
# the next run (~250MB once, checksums only after that) and the container gets
# ALCATRAZ_NER_MODEL_PATH pointed at the mount, overriding whatever .env says.
# Force it either way with ALCATRAZ_MODELS_DOWNLOAD=1 or =0; point somewhere
# else with ALCATRAZ_MODELS_DIR. A directory seeded by hand is mounted as-is.
#
# Read-only because the agent only reads it, and alcatraz checks every file
# against the manifest's sha256 on load.
ALCATRAZ_MODELS_DIR="${ALCATRAZ_MODELS_DIR:-$HOME/.hoop/dev/alcatraz-models}"
if [[ -z ${ALCATRAZ_MODELS_DOWNLOAD:-} ]]; then
  if grep -qE '^[[:space:]]*DLP_PROVIDER=alcatraz[[:space:]]*$' .env; then
    ALCATRAZ_MODELS_DOWNLOAD=1
  else
    ALCATRAZ_MODELS_DOWNLOAD=0
  fi
fi

if [[ $ALCATRAZ_MODELS_DOWNLOAD == "1" ]]; then
  echo "--> CACHING ALCATRAZ MODELS IN $ALCATRAZ_MODELS_DIR"
  ./scripts/dev/alcatraz-models.sh "$ALCATRAZ_MODELS_DIR"
fi

ALCATRAZ_MOUNT=()
if [[ -d $ALCATRAZ_MODELS_DIR ]]; then
  echo "--> MOUNTING ALCATRAZ MODELS FROM $ALCATRAZ_MODELS_DIR"
  ALCATRAZ_MOUNT=(
    -v "$ALCATRAZ_MODELS_DIR:/opt/alcatraz/models:ro"
    -e ALCATRAZ_NER_MODEL_PATH=/opt/alcatraz/models
  )
fi

VERSION="${VERSION:-unknown}"
CGO_ENABLED=0 GOOS=linux go build \
  -ldflags "-s -w -X github.com/hoophq/hoop/common/version.version=${VERSION} -X github.com/hoophq/hoop/client/proxy.defaultListenAddrValue=0.0.0.0" \
  -o ./dist/dev/bin/hooplinux github.com/hoophq/hoop/client
docker stop hoopdev &> /dev/null || true
docker rm hoopdev &> /dev/null || true

mkdir -p ./dist/dev/spiffe

docker run --rm --name hoopdev \
  -p 2225:22 \
  -p 8009:8009 \
  -p 8010:8010 \
  -p 15432:15432 \
  -p 12222:12222 \
  -p 13389:13389 \
  -p 18888:18888 \
  --env-file=.env \
  --cap-add=NET_ADMIN \
  --add-host=host.docker.internal:host-gateway \
  -v ./dist/dev/bin/:/app/bin/ \
  -v ./dist/dev/root/.ssh:/root/.ssh \
  -v ./dist/dev/resources/:/app/ui/ \
  "${ALCATRAZ_MOUNT[@]}" \
  -it hoopdev /app/bin/entrypoint.sh
