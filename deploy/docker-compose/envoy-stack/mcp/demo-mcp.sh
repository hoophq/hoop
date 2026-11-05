#!/usr/bin/env bash
#
# Walks one MCP session through the mcp lane: a request without the caller
# token, initialize, tools/list without the tools an mcp_tool rule hides, an
# allowed call, a call to a hidden tool, a call over the mcp_gateway budget,
# then the session's audit rows, /stats, the metrics and the traces.
#
# Usage:
#   ./demo-mcp.sh         build, start, run the sequence
#   ./demo-mcp.sh down    stop and remove the binaries

set -euo pipefail
cd "$(dirname "$0")"
here=$(pwd)
root=$(cd ../../../.. && pwd)

compose() { docker compose -f docker-compose.mcp.yml "$@"; }
step() { printf '\n\033[1;36m==>\033[0m \033[1m%s\033[0m\n' "$*"; }

if [[ "${1:-}" == "down" ]]; then
    compose down -v --remove-orphans
    rm -rf bin caller-tokens
    exit 0
fi

step "build hoop-inspect and the test MCP server for linux"
export GOPRIVATE="${GOPRIVATE:-github.com/hoophq/libhoop,github.com/hoophq/libhoop-ica}"
arch=$(docker version --format '{{.Server.Arch}}')
mkdir -p bin
(cd "$root/sidecar/cmd" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$here/bin/hoop-inspect" .)
(cd "$root/sidecar/mcpgateway" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$here/bin/upstream" ./internal/testupstream/cmd)

if [[ ! -s caller-tokens ]]; then
    (umask 077 && od -An -tx1 -N24 /dev/urandom | tr -d ' \n' >caller-tokens)
fi
chmod 600 caller-tokens
token=$(cat caller-tokens)

step "start the lane"
compose up -d --wait

version=2025-06-18
sid=""

# post sends one JSON-RPC message and prints the response body. The lane may
# answer as JSON or as one SSE event; both come out as JSON.
post() {
    local hdrs=(-H 'content-type: application/json' -H 'accept: application/json, text/event-stream' -H "x-hoop-token: $token")
    if [[ -n "$sid" ]]; then
        hdrs+=(-H "mcp-session-id: $sid" -H "mcp-protocol-version: $version")
    fi
    compose exec -T client curl -sS -i "${hdrs[@]}" -d "$1" http://127.0.0.1:18901/mcp | tr -d '\r'
}
body() { sed '1,/^$/d' | sed -n 's/^data: //p; /^{/p'; }
show() { compose exec -T client jq -c "$1"; }

step "0. initialize without the caller token: refused before mcpproxy reads it"
compose exec -T client curl -sS -w ' (%{http_code})\n' -H 'content-type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize"}' http://127.0.0.1:18901/mcp

step "1. initialize with the caller token"
resp=$(post '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"'"$version"'","capabilities":{},"clientInfo":{"name":"demo-mcp.sh","version":"1"}}}')
sid=$(printf '%s\n' "$resp" | sed -n 's/^[Mm]cp-[Ss]ession-[Ii]d: //p')
printf '%s\n' "$resp" | body | show '{protocolVersion: .result.protocolVersion, server: .result.serverInfo.name}'
echo "session: $sid"
post '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null

step "2. tools/list: delete_customer and run_query are gone, the mcp_tool rule hid them"
post '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | body | show '[.result.tools[].name]'

step "3. tools/call echo: allowed (call 1 of 3)"
post '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hi"}}}' | body | show '.result'

step "4. tools/call delete_customer: hidden, and still refused by the rule, as a tool result (call 2)"
post '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"delete_customer","arguments":{"id":"7"}}}' | body | show '.result'

step "5. tools/call echo: allowed (call 3)"
post '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"echo","arguments":{"message":"again"}}}' | body | show '.result'

step "6. tools/call echo: over mcp_gateway.max_calls_per_session, as a JSON-RPC error (call 4)"
post '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"echo","arguments":{"message":"once more"}}}' | body | show '.error // .result'

step "7. end the session"
compose exec -T client curl -sS -o /dev/null -w '%{http_code}\n' -X DELETE -H "x-hoop-token: $token" \
    -H "mcp-session-id: $sid" -H "mcp-protocol-version: $version" http://127.0.0.1:18901/mcp

step "audit rows of this session"
compose logs --no-log-prefix hoop-inspect | grep '^{' |
    compose exec -T client jq -c -s --arg sid "$sid" '
        (map(select(.metadata["mcp.session"] == $sid)) | .[0].session_id) as $s
        | .[] | select(.session_id == $s)
        | {kind, principal, what: (.metadata.activity // .metadata["mcp.method"]), tool: (.metadata["mcp.tool"] // .metadata.tool),
           hidden: .metadata["mcp.tools_hidden"],
           allowed: (if .kind == "statement" or .kind == "violation" then .allowed else null end), rule}
        | with_entries(select(.value != null))'

step "/stats: the lane's sessions, and the messages it refused (the rule and the budget)"
compose exec -T client curl -sS http://127.0.0.1:19000/stats | show '.listeners[]'

step "hoop.sidecar.mcp.messages, from the collector's last export"
sleep 6 # one metrics interval
compose logs --no-log-prefix otel-collector | awk '
    /^ResourceMetrics #0/ { if (cur != "") out = cur; cur = "" }
    /^Metric #/ { inm = 0 }
    /-> Name: hoop.sidecar.mcp.messages$/ { inm = 1 }
    inm && /^Data point attributes:/ { line = "" }
    inm && /-> hoop\.(direction|mcp\.[a-z]+): / { sub(/^ *-> /, ""); line = line "  " $0 }
    inm && /^Value: / { cur = cur line "  => " $2 "\n" }
    END { if (cur != "") out = cur; printf "%s", out }'

step "the trace of each tool call: hoop.statement and its policy stage"
compose logs --no-log-prefix otel-collector |
    grep -E '^ +(Trace ID|Parent ID|Name) +: |-> hoop\.(tables|operation|verdict|rule|deny\.source|engine|outcome): ' | head -60

echo
echo "stop with: ./demo-mcp.sh down"
