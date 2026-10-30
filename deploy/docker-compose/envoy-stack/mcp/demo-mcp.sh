#!/usr/bin/env bash
#
# Walks one MCP session through the mcp lane: initialize, tools/list, an
# allowed call, a call a guardrail rule denies, a call mcp_gateway denies,
# then the session's audit rows.
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
    rm -rf bin
    exit 0
fi

step "build hoop-inspect and the test MCP server for linux"
export GOPRIVATE="${GOPRIVATE:-github.com/hoophq/libhoop,github.com/hoophq/libhoop-ica}"
arch=$(docker version --format '{{.Server.Arch}}')
mkdir -p bin
(cd "$root/sidecar/cmd" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$here/bin/hoop-inspect" .)
(cd "$root/sidecar/mcpgateway" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$here/bin/upstream" ./internal/testupstream/cmd)

step "start the lane"
compose up -d --wait

version=2025-06-18
sid=""

# post sends one JSON-RPC message and prints the response body. The lane may
# answer as JSON or as one SSE event; both come out as JSON.
post() {
    local hdrs=(-H 'content-type: application/json' -H 'accept: application/json, text/event-stream')
    if [[ -n "$sid" ]]; then
        hdrs+=(-H "mcp-session-id: $sid" -H "mcp-protocol-version: $version")
    fi
    compose exec -T client curl -sS -i "${hdrs[@]}" -d "$1" http://127.0.0.1:18901/mcp | tr -d '\r'
}
body() { sed '1,/^$/d' | sed -n 's/^data: //p; /^{/p'; }
show() { compose exec -T client jq -c "$1"; }

step "1. initialize"
resp=$(post '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"'"$version"'","capabilities":{},"clientInfo":{"name":"demo-mcp.sh","version":"1"}}}')
sid=$(printf '%s\n' "$resp" | sed -n 's/^[Mm]cp-[Ss]ession-[Ii]d: //p')
printf '%s\n' "$resp" | body | show '{protocolVersion: .result.protocolVersion, server: .result.serverInfo.name}'
echo "session: $sid"
post '{"jsonrpc":"2.0","method":"notifications/initialized"}' >/dev/null

step "2. tools/list: run_query is gone, mcp_gateway.denied_tools filtered it"
post '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | body | show '[.result.tools[].name]'

step "3. tools/call echo: allowed"
post '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hi"}}}' | body | show '.result'

step "4. tools/call delete_customer: denied by the guardrail rule, as a tool result"
post '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"delete_customer","arguments":{"id":"7"}}}' | body | show '.result'

step "5. tools/call run_query: denied by mcp_gateway, as a JSON-RPC error"
post '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"run_query","arguments":{"sql":"select 1"}}}' | body | show '.error // .result'

step "6. end the session"
compose exec -T client curl -sS -o /dev/null -w '%{http_code}\n' -X DELETE \
    -H "mcp-session-id: $sid" -H "mcp-protocol-version: $version" http://127.0.0.1:18901/mcp

step "audit rows of this session"
compose logs --no-log-prefix hoop-inspect | grep '^{' |
    compose exec -T client jq -c -s --arg sid "$sid" '
        (map(select(.metadata["mcp.session"] == $sid)) | .[0].session_id) as $s
        | .[] | select(.session_id == $s)
        | {kind, what: (.metadata.activity // .metadata["mcp.method"]), tool: (.metadata["mcp.tool"] // .metadata.tool),
           allowed: (if .kind == "statement" or .kind == "violation" then .allowed else null end), rule}
        | with_entries(select(.value != null))'

echo
echo "stop with: ./demo-mcp.sh down"
