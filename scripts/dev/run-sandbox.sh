#!/bin/bash
# Run the gateway and one agent on this machine with the embedded PGlite
# database: no Docker, no Postgres, no Rust. For cloud sandboxes, where
# `make run-dev` cannot run, and for the support agent's QA and evidence.
#
#   scripts/dev/run-sandbox.sh start [--bin PATH] [--ui]
#                                     build ./client (or use PATH), start, seed;
#                                     --ui also builds and serves the web UI
#   scripts/dev/run-sandbox.sh stop
#   scripts/dev/run-sandbox.sh status
#
# start always begins from an empty database. It seeds a local admin, one
# agent and one bash connection, and writes $DIR/env: `source` it to point
# the hoop CLI and curl at the stack. Logs go to $DIR, not to the terminal.
#
# One stack at a time: the gRPC port is fixed (gateway/transport/server.go).
# To compare origin/main with a branch, build each binary, then start, capture
# and stop one before the other. --ui builds the UI of this checkout.

set -eo pipefail
cd "$(dirname "$0")/../.."

DIR="${HOOP_SANDBOX_DIR:-${TMPDIR:-/tmp}/hoop-sandbox}"
API_URL=http://127.0.0.1:8009
GRPC_URL=grpc://127.0.0.1:8010
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=Sandbox-Passw0rd

die() { echo "run-sandbox: $*" >&2; exit 1; }

alive() { [[ -f "$DIR/$1.pid" ]] && kill -0 "$(cat "$DIR/$1.pid")" 2>/dev/null; }

# wait_for <seconds> <command...>: retry the command once a second.
wait_for() {
  local n=$1; shift
  for ((i = 0; i < n; i++)); do "$@" && return 0; sleep 1; done
  return 1
}

healthy() { [[ "$(curl -s -o /dev/null -w '%{http_code}' "$API_URL/api/healthz")" == 200 ]]; }

api() { curl -sf -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' "$@"; }

agent_connected() {
  api "$API_URL/api/agents" | jq -e '.[] | select(.name == "sandbox-agent" and .status == "CONNECTED")' >/dev/null
}

stop_one() {
  alive "$1" || { rm -f "$DIR/$1.pid"; return 0; }
  local pid; pid=$(cat "$DIR/$1.pid")
  kill "$pid"
  # The next start must not get an answer from this process.
  wait_for 30 bash -c "! kill -0 $pid 2>/dev/null" || kill -9 "$pid"
  rm -f "$DIR/$1.pid"
}

cmd_stop() {
  stop_one agent
  stop_one gateway
  echo "run-sandbox: stopped"
}

cmd_status() {
  alive gateway && healthy && echo "gateway: up ($API_URL)" || echo "gateway: down"
  alive agent && echo "agent: running" || echo "agent: down"
}

build_ui() {
  local meta=webapp/resources/public/data/connections-metadata.json docs="$DIR/documentation"
  if [[ ! -s $meta ]]; then
    # The webapp build reads this file through the GitHub API, which a cloud
    # session cannot call. The repository is public: read it with git.
    [[ -d $docs/.git ]] || git clone -q --depth 1 --filter=blob:none --sparse \
      https://github.com/hoophq/documentation "$docs"
    git -C "$docs" sparse-checkout set store
    mkdir -p "$(dirname "$meta")" && cp "$docs/store/connections.json" "$meta"
  fi
  echo "run-sandbox: building the web UI (log: $DIR/ui.log)"
  # npm install rewrites the lockfiles when its version differs. Undo only
  # that, so a commit after this never carries it.
  local locks=(webapp/package-lock.json webapp_v2/package-lock.json) clean=0
  git diff --quiet -- "${locks[@]}" && clean=1
  SKIP_CONNECTION_METADATA_DOWNLOAD=1 ./scripts/dev/build-webapp.sh >"$DIR/ui.log" 2>&1 &&
    [[ -f dist/dev/resources/public/index.html ]] || {
    tail -20 "$DIR/ui.log" >&2
    die "web UI build failed; a Maven checksum error is a rate limit: see the evidence skill pitfalls"
  }
  [[ $clean == 1 ]] && git checkout -q -- "${locks[@]}"
  export STATIC_UI_PATH="$PWD/dist/dev/resources/public"
}

cmd_start() {
  local bin="" ui=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --bin) bin="$2"; shift 2 ;;
      --ui) ui=1; shift ;;
      *) die "unknown argument: $1" ;;
    esac
  done
  command -v jq >/dev/null || die "jq is required"
  alive gateway || alive agent && die "a stack is already running; run '$0 stop' first"
  curl -s -o /dev/null "$API_URL" && die "port 8009 is in use by another process"

  mkdir -p "$DIR/bin"
  rm -rf "$DIR/pgdata"
  if [[ -z "$bin" ]]; then
    bin="$DIR/bin/hoop"
    echo "run-sandbox: building ./client (log: $DIR/build.log)"
    go build -o "$bin" ./client >"$DIR/build.log" 2>&1 || {
      tail -20 "$DIR/build.log" >&2
      die "build failed; a private module error needs GOPRIVATE=github.com/hoophq/libhoop,github.com/hoophq/libhoop-ica"
    }
  fi
  [[ -x "$bin" ]] || die "not an executable: $bin"
  [[ $ui == 1 ]] && build_ui

  POSTGRES_DB_URI="pglite://$DIR/pgdata" API_URL="$API_URL" GRPC_URL="$GRPC_URL" \
    AUTH_METHOD=local GIN_MODE=release \
    nohup "$bin" start gateway >"$DIR/gateway.log" 2>&1 &
  echo $! >"$DIR/gateway.pid"
  wait_for 120 healthy || { tail -20 "$DIR/gateway.log" >&2; die "gateway did not become healthy"; }

  TOKEN=$(curl -s -D - -o /dev/null -H 'content-type: application/json' \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\",\"name\":\"Sandbox Admin\"}" \
    "$API_URL/api/localauth/register" | sed -nE 's/^[Tt]oken: (.*)\r?$/\1/p' | tr -d '\r')
  [[ -n "$TOKEN" ]] || die "admin registration returned no token"

  local key
  key=$(api -d '{"name":"sandbox-agent","mode":"standard"}' "$API_URL/api/agents" | jq -r '.token')
  [[ -n "$key" && "$key" != null ]] || die "agent creation returned no key"
  HOOP_KEY="$key" nohup "$bin" start agent >"$DIR/agent.log" 2>&1 &
  echo $! >"$DIR/agent.pid"
  wait_for 60 agent_connected || { tail -20 "$DIR/agent.log" >&2; die "agent did not connect"; }

  local agent_id
  agent_id=$(api "$API_URL/api/agents" | jq -r '.[] | select(.name == "sandbox-agent") | .id')
  api -o /dev/null -d "{\"name\":\"bash\",\"type\":\"custom\",\"subtype\":\"\",\"agent_id\":\"$agent_id\",
    \"command\":[\"/bin/bash\"],\"secret\":{},\"access_mode_runbooks\":\"enabled\",
    \"access_mode_exec\":\"enabled\",\"access_mode_connect\":\"enabled\",\"access_schema\":\"disabled\"}" \
    "$API_URL/api/connections" || die "connection creation failed"

  cat >"$DIR/env" <<EOF
export HOOP_APIURL=$API_URL
export HOOP_GRPCURL=$GRPC_URL
export HOOP_TOKEN=$TOKEN
export HOOP_SANDBOX_DIR=$DIR
EOF
  echo "run-sandbox: up. API $API_URL, admin $ADMIN_EMAIL, agent sandbox-agent, connection bash${STATIC_UI_PATH:+, web UI}"
  echo "run-sandbox: source $DIR/env  # then: hoop exec bash -i 'echo ok'"
}

case "$1" in
  start) shift; cmd_start "$@" ;;
  stop) cmd_stop ;;
  status) cmd_status ;;
  *) die "usage: $0 start [--bin PATH] [--ui] | stop | status" ;;
esac
