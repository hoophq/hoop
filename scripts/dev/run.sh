#!/bin/bash

set -eo pipefail

if ! [[ -f .env ]]; then
  echo "missing .env file"
  exit 1
fi

# libhoop used to be selected here: LIBHOOP in .env named a directory or a git
# remote, and a block below symlinked or cloned it into ./libhoop. It is now
# the module github.com/hoophq/libhoop, resolved from the proxy like any other
# dependency.
#
# Check it before building anything. Unresolved, it fails a hundred lines
# later as one "unknown revision" per import, none of which say what to do.
if [[ -z "$(go list -m -f '{{.Dir}}' github.com/hoophq/libhoop 2>/dev/null)" ]]; then
  echo "libhoop does not resolve; the gateway and agent cannot build." >&2
  if [[ -d ./libhoop ]]; then
    echo >&2
    echo "  You have a clone at ./libhoop. Point the workspace at it:" >&2
    echo "      make libhoop-dev" >&2
  else
    echo >&2
    echo "  It is a private module. Either clone it to ./libhoop and run" >&2
    echo "  'make libhoop-dev', or give Go credentials for it:" >&2
    echo "      export GOPRIVATE=github.com/hoophq/libhoop" >&2
    echo "      git config --global url.\"https://<token>@github.com/hoophq/\".insteadOf \"https://github.com/hoophq/\"" >&2
  fi
  exit 1
fi

# HOOPDEV_SLOT runs one stack per git worktree. Slot 0 (default) is the usual
# "hoopdev" container. Slot N is "hoopdev-N", with each host port + N*100.
HOOPDEV_SLOT="${HOOPDEV_SLOT:-0}"
if ! [[ $HOOPDEV_SLOT =~ ^[0-9]$ ]]; then
  echo "HOOPDEV_SLOT must be a number from 0 to 9, got '$HOOPDEV_SLOT'" >&2
  exit 1
fi

function hostport() {
  echo $(($1 + HOOPDEV_SLOT * 100))
}

CONTAINER_NAME=hoopdev
IMAGE_NAME=hoopdev
SLOT_ARGS=()
if [[ $HOOPDEV_SLOT != 0 ]]; then
  CONTAINER_NAME="hoopdev-${HOOPDEV_SLOT}"
  IMAGE_NAME="hoopdev-${HOOPDEV_SLOT}"

  # Clients on the host get API_URL and GRPC_URL from the gateway. A value
  # left at 8009/8010 sends them to the slot 0 stack.
  API_URL_WANT="http://127.0.0.1:$(hostport 8009)"
  GRPC_URL_WANT="grpc://127.0.0.1:$(hostport 8010)"
  API_URL_SET="$(sed -nE 's/^[[:space:]]*API_URL=//p' .env | tail -n 1)"
  GRPC_URL_SET="$(sed -nE 's/^[[:space:]]*GRPC_URL=//p' .env | tail -n 1)"
  if [[ $API_URL_SET != "$API_URL_WANT" || $GRPC_URL_SET != "$GRPC_URL_WANT" ]]; then
    echo "slot $HOOPDEV_SLOT serves the API on $(hostport 8009) and gRPC on $(hostport 8010)." >&2
    echo "  set these in this worktree's .env:" >&2
    echo "      API_URL=$API_URL_WANT" >&2
    echo "      GRPC_URL=$GRPC_URL_WANT" >&2
    exit 1
  fi

  # entrypoint.sh waits on API_URL from inside the container, where the
  # shifted host port does not exist.
  SLOT_ARGS=(-e HOOPDEV_HEALTHZ_URL=http://127.0.0.1:8009/api/healthz)
  echo "--> SLOT $HOOPDEV_SLOT: container $CONTAINER_NAME, API $API_URL_WANT, gRPC $GRPC_URL_WANT"
fi

trap ctrl_c INT

function ctrl_c() {
    docker stop "$CONTAINER_NAME"
    exit 130
}

mkdir -p "$HOME/.hoop/dev"

WEBAPP_BUILD="${WEBAPP_BUILD:-0}"
if [[ $WEBAPP_BUILD == "1" ]]; then
  echo 'run "make build-dev-webapp" to build the webapp'
  exit 1
fi

docker build -t "$IMAGE_NAME" -f ./scripts/dev/Dockerfile .
mkdir -p ./dist/dev/bin
cp ./scripts/dev/entrypoint.sh ./dist/dev/bin/entrypoint.sh

# Build Rust agent for development
HOOP_RS_BUILD="${HOOP_RS_BUILD:-1}"
if [[ $HOOP_RS_BUILD == "1" ]]; then
  echo "Building Rust agent..."
  echo ""
  echo "You need to have Rust installed to build the Rust agent."
  echo "You need to have Cross installed to build the Rust agent for multiple architectures."
  if [[ $HOOPDEV_SLOT == 0 ]]; then
    make build-dev-rust
    cp $HOME/.hoop/bin/hoop_rs ./dist/dev/bin/hoop_rs
  else
    # $HOME/.hoop/bin is shared by all checkouts; a slot writes its own copy.
    make build-dev-rust HOOP_RS_OUT="$PWD/dist/dev/bin/hoop_rs"
  fi
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

if [[ $ALCATRAZ_MODELS_DOWNLOAD == "1" && $HOOPDEV_SLOT != 0 ]]; then
  # Only slot 0 writes the shared cache, so two starts never fill it at once.
  if ! [[ -f $ALCATRAZ_MODELS_DIR/checksums.txt ]]; then
    echo "slot $HOOPDEV_SLOT reads the Alcatraz cache but does not fill it: $ALCATRAZ_MODELS_DIR has no checksums.txt." >&2
    echo "  fill it once from slot 0, or run: ./scripts/dev/alcatraz-models.sh \"$ALCATRAZ_MODELS_DIR\"" >&2
    exit 1
  fi
  echo "--> SLOT $HOOPDEV_SLOT: USING ALCATRAZ MODELS CACHED IN $ALCATRAZ_MODELS_DIR"
elif [[ $ALCATRAZ_MODELS_DOWNLOAD == "1" ]]; then
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
docker stop "$CONTAINER_NAME" &> /dev/null || true
docker rm "$CONTAINER_NAME" &> /dev/null || true

mkdir -p ./dist/dev/spiffe

# Proxy credentials carry the proxy's listen port, so in slot N the proxies
# listen on the slot's ports and publish them unchanged.
docker run --rm --name "$CONTAINER_NAME" \
  -p "$(hostport 2225):22" \
  -p "$(hostport 8009):8009" \
  -p "$(hostport 8010):8010" \
  -p "$(hostport 15432):$(hostport 15432)" \
  -p "$(hostport 12222):$(hostport 12222)" \
  -p "$(hostport 13389):$(hostport 13389)" \
  -p "$(hostport 18888):$(hostport 18888)" \
  --env-file=.env \
  "${SLOT_ARGS[@]}" \
  --cap-add=NET_ADMIN \
  --add-host=host.docker.internal:host-gateway \
  -v ./dist/dev/bin/:/app/bin/ \
  -v ./dist/dev/root/.ssh:/root/.ssh \
  -v ./dist/dev/resources/:/app/ui/ \
  "${ALCATRAZ_MOUNT[@]}" \
  -it "$IMAGE_NAME" /app/bin/entrypoint.sh
