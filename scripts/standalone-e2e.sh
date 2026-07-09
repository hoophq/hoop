#!/usr/bin/env bash
#
# End-to-end test for the single-binary standalone mode (DEP-38).
#
# Runs the REAL shipped artifact — `hoop start standalone` as a subprocess —
# against a throwaway $HOME, twice:
#
#   phase 1 (first boot):  embedded PGlite initdb + migrations + org
#                          bootstrap, first-user registration, and the
#                          standalone agent provisioned and CONNECTED.
#   phase 2 (resume boot): same data dir — the cluster resumes, the
#                          registered user can log in (state persisted), and
#                          the agent reconnects with the same stored
#                          credentials.
#
# This is the only layer that exercises the actual binary: go:embed
# artifacts (SQL migrations, PGlite wasm runtime, web UI), CLI wiring, and
# the gateway+agent single-process composition. Integration suites cover the
# same logic in-process; this catches what only breaks in the shipped
# artifact.
#
# Usage: scripts/standalone-e2e.sh   (from the repo root)
#   HOOP_BIN=/path/to/hoop  skips the build and tests that binary instead.
set -euo pipefail

# The script needs bash + these tools; fail fast with a clear message
# instead of failing mid-run on a lean executor image.
for dep in curl python3 go; do
  command -v "$dep" >/dev/null || { echo "FAIL: missing dependency: $dep" >&2; exit 1; }
done

API_URL=${API_URL:-http://127.0.0.1:8009}
WORK=$(mktemp -d)
HOOP_HOME="$WORK/home"
LOG_DIR="$WORK/logs"
mkdir -p "$HOOP_HOME" "$LOG_DIR"
GATEWAY_PID=""

cleanup() {
  # Two-phase: TERM first (gives the embedded database its clean-shutdown
  # checkpoint), escalate to KILL only if the process lingers, then reap.
  if [[ -n "$GATEWAY_PID" ]] && kill -0 "$GATEWAY_PID" 2>/dev/null; then
    kill "$GATEWAY_PID" 2>/dev/null || true
    for _ in $(seq 1 20); do
      kill -0 "$GATEWAY_PID" 2>/dev/null || break
      sleep 0.25
    done
    kill -9 "$GATEWAY_PID" 2>/dev/null || true
    wait "$GATEWAY_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

fail() { # $1=message $2=logfile
  echo "FAIL: $1" >&2
  if [[ -f "${2:-}" ]]; then
    echo "--- last 50 log lines ($2) ---" >&2
    tail -n 50 "$2" >&2
  fi
  exit 1
}

HOOP_BIN=${HOOP_BIN:-}
if [[ -z "$HOOP_BIN" ]]; then
  echo "==> building the hoop binary"
  HOOP_BIN="$WORK/hoop"
  go build -o "$HOOP_BIN" client/hoop.go
fi

start_gateway() { # $1=phase
  HOME="$HOOP_HOME" DO_NOT_TRACK=1 "$HOOP_BIN" start standalone \
    >"$LOG_DIR/$1.log" 2>&1 &
  GATEWAY_PID=$!
}

wait_healthz() { # $1=phase
  local log="$LOG_DIR/$1.log"
  for _ in $(seq 1 240); do
    if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
      GATEWAY_PID=""
      fail "$1: the standalone process exited prematurely" "$log"
    fi
    if curl -fsS -o /dev/null "$API_URL/api/healthz" 2>/dev/null; then
      return 0
    fi
    sleep 0.5
  done
  fail "$1: gateway did not become healthy within 120s" "$log"
}

# auth_token POSTs a localauth payload and prints the JWT from the Token
# response header (the local provider's contract), or nothing on failure.
auth_token() { # $1=path $2=json-body
  curl -fsS -D - -o /dev/null -H 'Content-Type: application/json' \
    -d "$2" "$API_URL/api/$1" 2>/dev/null |
    awk 'tolower($1) == "token:" {print $2}' | tr -d '\r'
}

wait_agent_connected() { # $1=phase $2=token
  local log="$LOG_DIR/$1.log" status
  for _ in $(seq 1 120); do
    status=$(curl -fsS -H "Authorization: Bearer $2" "$API_URL/api/agents" 2>/dev/null |
      python3 -c 'import json,sys; print(next((a.get("status","") for a in json.load(sys.stdin) if a.get("name")=="standalone"), ""))' 2>/dev/null || true)
    if [[ "$status" == "CONNECTED" ]]; then
      return 0
    fi
    sleep 0.5
  done
  fail "$1: the standalone agent did not report CONNECTED within 60s" "$log"
}

stop_gateway() { # $1=phase
  local log="$LOG_DIR/$1.log"
  kill "$GATEWAY_PID" 2>/dev/null || true
  for _ in $(seq 1 60); do
    if ! kill -0 "$GATEWAY_PID" 2>/dev/null; then
      # Reap the child so the PID cannot be recycled before the next phase.
      wait "$GATEWAY_PID" 2>/dev/null || true
      GATEWAY_PID=""
      return 0
    fi
    sleep 0.5
  done
  fail "$1: gateway did not exit within 30s of SIGTERM" "$log"
}

EMAIL="e2e@standalone.test"
PASS="e2e-standalone-pass-123"

echo "==> phase 1: first boot (initdb + migrations + agent provisioning)"
start_gateway first-boot
wait_healthz first-boot
TOKEN=$(auth_token localauth/register \
  "{\"email\":\"$EMAIL\",\"password\":\"$PASS\",\"name\":\"E2E Admin\"}" || true)
[[ -n "$TOKEN" ]] || fail "first-boot: first-user registration returned no token" "$LOG_DIR/first-boot.log"
wait_agent_connected first-boot "$TOKEN"
stop_gateway first-boot
echo "==> phase 1 OK"

echo "==> phase 2: resume boot (existing cluster + persisted credentials)"
start_gateway resume
wait_healthz resume
TOKEN=$(auth_token localauth/login "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" || true)
[[ -n "$TOKEN" ]] || fail "resume: login with the persisted user failed" "$LOG_DIR/resume.log"
wait_agent_connected resume "$TOKEN"
stop_gateway resume
echo "==> phase 2 OK"

echo "PASS: standalone single-binary end-to-end"
