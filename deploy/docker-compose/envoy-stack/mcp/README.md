# mcp: one MCP session through the lane

```
curl ──> hoop-inspect 127.0.0.1:18901 ──> test MCP server 127.0.0.1:18900/mcp
```

A `protocol: mcp` lane in front of the test MCP server from
`sidecar/mcpgateway/internal/testupstream`. No Envoy: the lane binds
loopback only until it authenticates callers, so the three
containers share one network namespace and nothing is published on the
host.

## Run it

```bash
./demo-mcp.sh         # build on the host, start, run the sequence
./demo-mcp.sh down
```

The build needs Go and access to the private libhoop and libhoop-ica
modules, as any sidecar build does. It runs with the envoy stack up or down.

## What it shows

| Step | Result |
|---|---|
| `initialize` | a session id |
| `tools/list` | no `run_query`: `mcp_gateway.denied_tools` removed it |
| `tools/call echo` | allowed |
| `tools/call delete_customer` | denied by the `no-delete-tools` rule, as a tool result with `isError: true` |
| `tools/call run_query` | denied by `mcp_gateway`, as JSON-RPC error `-32010` |
| `DELETE /mcp` | `204`, the session ends |

The audit rows follow: `session_start`, the `initialize` activity, one
statement for the catalog and one per call, the `delete_customer`
violation, the `run_query` refusal as an activity, `session_end`.

Edit a rule in `config-mcp.yaml` while the stack runs. The sidecar applies
it within 10 seconds, or at once on
`docker compose -f docker-compose.mcp.yml kill -s HUP hoop-inspect`, and
ends the open MCP sessions.
