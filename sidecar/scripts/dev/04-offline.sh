#!/usr/bin/env bash
#
# The same assertions, against a fake provider. No GCP, no credential, no
# network, no spend.
#
# This exercises the identical code path: Claude on Vertex is the Anthropic
# Messages API with a different URL and auth header, and analyzer/vertex
# imports the Anthropic request builder and response parser rather than
# carrying its own. So a fake speaking the Messages format on localhost tests
# everything except the OAuth hop, which 00-preflight.sh covers.
#
# Use this in CI, or when changing the relay and re-testing in seconds.
#
# Usage:
#   ./04-offline.sh

set -uo pipefail
cd "$(dirname "$0")"
source ./lib.sh

export WORKDIR="${WORKDIR}-offline"
mkdir -p "$WORKDIR"
export CONFIG="$WORKDIR/config.yaml" RELAY_LOG="$WORKDIR/relay.log"
export RELAY_PID="$WORKDIR/relay.pid" BIN="$WORKDIR/hoop-inspect"
export FAKE_PID="$WORKDIR/fake.pid"
FAKE_PORT="${FAKE_PORT:-58111}"

cleanup() {
    [[ -f "$RELAY_PID" ]] && kill "$(cat "$RELAY_PID")" 2>/dev/null
    [[ -f "$FAKE_PID"  ]] && kill "$(cat "$FAKE_PID")"  2>/dev/null
    rm -f "$RELAY_PID" "$FAKE_PID"
}
trap cleanup EXIT

if [[ "${1:-}" == "down" ]]; then
    cleanup; docker rm -f hoop-offline-pg >/dev/null 2>&1
    ok "torn down"; exit 0
fi

# ------------------------------------------------------------ fake provider
h "Fake Anthropic-format provider"

cat > "$WORKDIR/fake.py" <<'PY'
"""Speaks enough of the Anthropic Messages API to drive the analyzer.

Classifies on the USER message only. The SYSTEM prompt lists "DELETE or UPDATE
with no WHERE" among its high-risk examples, so matching against the whole
request body marks every statement high -- a trap worth naming, because the
first version of this harness did exactly that and every test passed for the
wrong reason.
"""
import json, re, time
from http.server import BaseHTTPRequestHandler, HTTPServer
import sys

PORT = int(sys.argv[1])

class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass

    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("content-length", 0)))
        req = json.loads(raw or b"{}")
        user = " ".join(m.get("content", "") for m in req.get("messages", [])).lower()

        # Match the rule prompts in the config: a destructive or bulk
        # statement with no WHERE clause is high, anything scoped is low.
        # A real model reaches the same verdict from the prompt; this keeps
        # the offline run deterministic.
        destructive = re.search(r"\bdelete\b|\bdrop\b|\bupdate\b|\btruncate\b|wipe|delete_all", user)
        scoped = re.search(r"\bwhere\b", user)
        high = bool(destructive) and not scoped
        tool = "report_high_risk" if high else "report_low_risk"
        title = "unbounded destructive statement" if high else "ordinary traffic"

        # A real provider takes hundreds of milliseconds. Without some
        # latency here a cache hit and a cache miss are indistinguishable,
        # and the cache assertion in 03-verify.sh would pass either way.
        time.sleep(0.4)

        body = json.dumps({"content": [{
            "type": "tool_use", "name": tool,
            "input": {"title": title, "explanation": "fake classifier"}}]}).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

HTTPServer(("127.0.0.1", PORT), H).serve_forever()
PY

python3 "$WORKDIR/fake.py" "$FAKE_PORT" & echo $! > "$FAKE_PID"
for i in $(seq 1 20); do
    curl -sf -X POST "http://127.0.0.1:${FAKE_PORT}/" -d '{}' >/dev/null 2>&1 && break
    [[ $i -eq 20 ]] && die "the fake provider never came up"
    sleep 0.2
done
ok "fake provider on :${FAKE_PORT}"

# ------------------------------------------------------------------ config
h "Build and configure"
( cd ../../cmd && go build -o "$BIN" . ) || die "go build failed"
printf 'fake-key' > "$WORKDIR/key"; chmod 600 "$WORKDIR/key"

cat > "$CONFIG" <<YAML
log_level: info
admin: {listen: "127.0.0.1:${ADMIN_PORT}"}
audit: {file: "-", memory_buffer: 256, query_sessions: 500}
pii:
  entities: [EMAIL_ADDRESS, US_SSN]
analyzer:
  provider: anthropic
  model: fake-model
  endpoint: http://127.0.0.1:${FAKE_PORT}/
  credentials_file: ${WORKDIR}/key
  timeout_sec: 10
  fail_open: true
  cache: {size: 512, ttl_sec: 300}
  max_calls: 100
policy: {enforce: true}
listeners:
  - name: appdb
    protocol: postgres
    listen: "127.0.0.1:${PG_RELAY_PORT}"
    upstream: "127.0.0.1:${PG_UPSTREAM_PORT}"
    connection: appdb
    policy:
      rules:
        - {name: no-drops, type: operation, operations: [drop, truncate],
           message: schema changes are not permitted on appdb}
        - name: risky-writes
          type: ai_analysis
          trigger: {operations: [update, delete]}
          high: block
          medium: warn
          low: allow
  - name: api
    protocol: http
    listen: "127.0.0.1:${HTTP_RELAY_PORT}"
    upstream: "127.0.0.1:${HTTP_UPSTREAM_PORT}"
    connection: api
    http: {capture_body: true, max_body_bytes: 8192, headers: [Content-Type]}
    policy:
      rules:
        - name: risky-payloads
          type: ai_analysis
          trigger: {resources: ["/anything"]}
          high: block
YAML

"$BIN" -validate -config "$CONFIG" | sed 's/^/  /' || die "validation failed"
ok "config valid"

# --------------------------------------------------------------- upstreams
h "Upstreams"
docker rm -f hoop-offline-pg >/dev/null 2>&1 || true
docker run -d --name hoop-offline-pg \
    -p "127.0.0.1:${PG_UPSTREAM_PORT}:5432" \
    -e POSTGRES_USER=testuser -e POSTGRES_PASSWORD=testpass -e POSTGRES_DB=appdb \
    postgres:17 >/dev/null || die "could not start postgres"
for i in $(seq 1 40); do
    docker exec hoop-offline-pg pg_isready -U testuser -d appdb >/dev/null 2>&1 && break
    [[ $i -eq 40 ]] && die "postgres never became ready"
    sleep 0.5
done
seed_postgres hoop-offline-pg || die "could not seed postgres"
ok "postgres seeded"

docker rm -f hoop-test-http >/dev/null 2>&1 || true
docker run -d --name hoop-test-http \
    -p "127.0.0.1:${HTTP_UPSTREAM_PORT}:80" \
    --entrypoint gunicorn kennethreitz/httpbin \
    -b 0.0.0.0:80 -k gevent --keep-alive 75 httpbin:app >/dev/null \
    || die "could not start httpbin"
ok "httpbin on :${HTTP_UPSTREAM_PORT} (keep-alive 75s)"
note "gunicorn defaults to keep_alive=2s. The relay dials the upstream when it"
note "accepts, then holds the request while it classifies, so a 4s Vertex call"
note "outlives a 2s idle budget and the upstream hangs up first. See the"
note "known limit in README.md."

# ------------------------------------------------------------------- relay
h "Relay"
: > "$RELAY_LOG"
"$BIN" -config "$CONFIG" >> "$RELAY_LOG" 2>&1 & echo $! > "$RELAY_PID"
for i in $(seq 1 40); do
    curl -sf "http://127.0.0.1:${ADMIN_PORT}/healthz" >/dev/null 2>&1 && break
    [[ $i -eq 40 ]] && { cat "$RELAY_LOG"; die "the relay never became healthy"; }
    sleep 0.5
done
ok "relay healthy"

h "Assertions"
./03-verify.sh
rc=$?

docker rm -f hoop-offline-pg >/dev/null 2>&1
exit $rc
