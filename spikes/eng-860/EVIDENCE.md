# ENG-860 evidence: the Claude sidecar shape in OpenAI Agents API hosted sandboxes

Tested 2026-10-23. Agents API beta `OpenAI-Beta: agents=v1`, model `gpt-6-sol` (reasoning `low`), container `small`, sidecar `hoophq/hoopsidecar:1.258.0` (the `deploy/managed-agents/http` pin), cloudflared 2026.10.2. Raw output is in `out/`; `out/operator-notes.txt` keeps the terminal output that no other file holds. Nine sessions measured from their traces used USD 0.39 of model tokens at list price, plus small-container time at USD 0.03 per 20 minutes each. Five more sessions were deleted before they were measured (four `setup.sh` runs, one create-then-delete).

## Answer

- **The shape holds.** With `network.access: restricted` and only the sidecar host in `allowed_domains`, the agent reaches the API only through the http lane. Masking, deny, caller token and audit work (c1). Every other host fails at the egress proxy with `403`.
- **One difference changes the template: the vault fills placeholders in the URL, the body and every header**, not only in headers (c3). Toward the bound host, the upstream got the real token in a query string, a form body and two custom headers. The docs say headers only. Claude's `injection_location: {body: false}` has no OpenAI equivalent. A placeholder inside a WebSocket frame was not filled. ENG-871 tracks the audit leak this causes.
- **The egress is an explicit, TLS-intercepting proxy** (`HTTPS_PROXY=http://proxy:8080`, envoy, issuer `CN=rcgen self signed cert`). Direct sockets fail, also with `enabled`. Under `restricted` the sandbox resolves no name; the proxy does.
- **Postgres from the sandbox fails** with `restricted` and with `enabled` (c2). The proxy answers a CONNECT to port 5432 with `200`, then closes on the first Postgres byte. A TLS-first client was not tested. A WebSocket through the lane works; a CONNECT sent inside TLS to the lane gets `403` (c5). Milestone 5 keeps the WebSocket tunnel from ENG-789.
- **One-click is three saved resources plus three ids per session.** A template holds the network policy; it cannot hold the vault (`Unknown parameter: 'vault_ids'`). A session that names only the template gets its policy, and a broader override is refused (c7). `curl` and `jq` create everything (c9).
- **MCP:** `service` origin ignored `allowed_domains`, came from `9.129.44.x` in one session, and the vault `static_bearer` filled the token (c6). `environment` origin worked under `restricted` in one session with the MCP host allowed, and failed in one with the host not allowed. The docs say it does not support `restricted`. It cannot use a vault credential.

## Table: cell × result

| # | Cell | Result | Evidence |
|---|---|---|---|
| c1 | `restricted`, one sidecar host: lane works and is audited; another host fails | **Works** | S1a: `200` masked, `403 X-Hoop-Denied: policy`, no-token `403`; audit rows name the session, except identity refusals and WebSocket frames. Other hosts: `curl: (56) CONNECT tunnel failed, response 403`, proxy body `Domain forbidden` |
| c2 | Raw TCP, `psql` to a public Postgres, `restricted` and `enabled` | **Fails** in both | `enabled`: `Network is unreachable`. `restricted`, host allowed: `could not translate host name`. Proxy CONNECT to `:5432`: `200`, then closed on the first Postgres byte |
| c3 | Vault fills `X-Hoop-Token` and `Bearer <placeholder>`; `env` holds placeholders; other host, body, URL go out unchanged | **Partial**. Works: headers filled, `env` holds placeholders, other host unchanged. **Fails: body and URL are filled too** | Probe: `Bearer <REAL:api-token>`, `/echo?t=<REAL:api-token>`, body `token=<REAL:api-token>`. Placeholder `__OPENAI_SECRET__[NAME:<32 hex>]__`, 71 and 72 chars |
| c4 | TLS inspection; lane with a public CA certificate; quick tunnel | **Partial**. Works: TLS is inspected, a quick tunnel host works, a public-CA host outside Cloudflare works. **Not tested: the sidecar's own `downstream_tls`** | Issuer `CN=rcgen self signed cert` for the tunnel and for `postman-echo.com`; both answer `200`. `postman-echo.com` stands in for a public-CA lane |
| c5 | WebSocket over 443 to a lane; HTTP CONNECT | **Works** (WebSocket), **Fails** (CONNECT inside TLS) | `101` through the lane, 3 echoes at 215 to 217 ms; CONNECT inside TLS: `403`, `RBAC: access denied` (envoy) |
| c6 | `service` MCP: source IP vs the lists; `allowed_domains`; `static_bearer` | **Works**; `allowed_domains` **does not apply** | One session: 14 requests from `9.129.44.16..29`, `openai-mcp/1.0.0 (Codex)`, in `9.129.0.0/16` of both lists; `Bearer <REAL:mcp-token>`; the MCP host was not in `allowed_domains` |
| c6b | `environment` MCP under `restricted` (extra) | **Works** with the host allowed, **fails** without it; **no vault** | Allowed: requests from `20.55.172.114/117` (in `chatgpt-agents.json`), `codex-mcp-client/0.0.0`. Not allowed: turn `connection_failed`. Matching vault credential: create `400` |
| c7 | Template policy; broader override refused; template carries the vault | **Works**; template carries the vault: **fails** | Template-only session: lane `200`, `example.com` `403`. `enabled`, an added host, a swapped host: `400 ... cannot broaden the environment template network policy`. `vault_ids` on a template: `400 Unknown parameter` |
| c8 | A variable that names the session | **Fails** for the `sess_` id. **Works** as a label: `CODEX_ENVIRONMENT_ID` = the session's `environment.id` | 4 of 4 sessions. `CODEX_SESSION_ID` is a different UUID. Traces carry `openai.managed_agents.session.id` |
| c9 | Setup tool, working script | **Works**: curl + jq (`setup.sh`) | Template, vault, credential, saved agent, session, turn; recording. `openai` 3.37.0 ships no CLI |

## Evidence per cell

### c1. `restricted`, lane host only (S1a `sess_012c698bbc7bcd83006adb8c6e0e8c819797d5827cd0cea9f3`, rerun `sess_0c7fb6cf27d3366c006adb8cd750d081959b556262426b52b1`)

```
== c1 GET /users/1 through the lane
HTTP/2 200
x-hop: origin
  "email": "[REDACTED:EMAIL_ADDRESS]", "phone": "[REDACTED:PHONE_NUMBER]", "card": "[REDACTED:CREDIT_CARD]"
== c1 DELETE /users/1 through the lane
HTTP/2 403
x-hoop-denied: policy
this agent may not delete
== c1 GET without X-Hoop-Token
HTTP/2 403
request refused: the request carried no credential
== c1 hosts outside allowed_domains
curl: (56) CONNECT tunnel failed, response 403          (probe host, example.com, postman-echo.com)
tcp via proxy CONNECT FAIL example.com:443: HTTP/1.1 403 Forbidden | server: envoy  body=b'Domain forbidden'
dns FAIL example.com gaierror(-3, 'Temporary failure in name resolution')
```

Sidecar audit (`out/sidecar-audit.jsonl`):

```
statement openai-agents GET /users/1                  session sess_012c698b...
violation openai-agents DELETE /users/1  no-delete    session sess_012c698b...
violation anonymous     GET /users/1     identity     (no session header recorded)
```

- Under `restricted` the sandbox resolves no name, the lane host included. `curl` reaches the lane because the proxy resolves it.
- Port 80 to the lane passes the proxy, but the vault does not fill there: `request refused: the caller token is not accepted`.
- Identity refusals and WebSocket frame rows record no `x-openai-session-id`, although the request sent it.
- No dashboard setting was changed for this spike: the API alone set `restricted` (the ticket's open question).

### c2. Raw TCP (S0 `sess_0a759c28305e0d23006adb8b904380819796544bb5e46152ab`, S0b `sess_0d8a3399b972584f006adb8c157f648196b600d10c86d78bdb`, S1b `sess_00a5157952780c40006adb8d4397748196ab35bf4771a55275`)

```
== enabled
tcp direct FAIL hh-pgsql-public.ebi.ac.uk:5432 after 0.2s: [Errno 101] Network is unreachable
tcp via proxy CONNECT OK hh-pgsql-public.ebi.ac.uk:5432 in 0.13s
  postgres SSLRequest answer: b''
psql: error: connection to server at "hh-pgsql-public.ebi.ac.uk" (193.62.192.243), port 5432 failed: Network is unreachable
== restricted, host in allowed_domains
tcp via proxy CONNECT OK hh-pgsql-public.ebi.ac.uk:5432 in 0.12s
  postgres SSLRequest answer: b''
psql: error: could not translate host name "hh-pgsql-public.ebi.ac.uk" to address: Temporary failure in name resolution
```

From the laptop, the same Postgres answers `N` to the SSLRequest (`out/operator-notes.txt`). Not tested: a TLS-first client (`sslnegotiation=direct`) through the proxy CONNECT.

### c3. Vault substitution (S1a rerun, S1b)

Credentials: `environment_variable`, `networking: {type: limited, allowed_hosts: [<lane host>]}`. The probe upstream prints a real secret as `<REAL:name>`.

```
CUSTOMER_API_TOKEN: len=72 value=__OPENAI_SECRET__[CUSTOMER_API_TOKEN:d2c085886b6b47b28475611bbccd965c]__
HOOP_CALLER_TOKEN: len=71 value=__OPENAI_SECRET__[HOOP_CALLER_TOKEN:e71aa1f6dfa447a395474c24110c3f5c]__
== toward the lane host (headers, URL, body)
"path": "/echo?t=<REAL:api-token>"
"authorization": "Bearer <REAL:api-token>"
"x-probe-mid": "a-<REAL:api-token>-b"
"x-probe-raw": "<REAL:api-token>"
"body": "token=<REAL:api-token>"
== toward another allowed host (the probe directly)
"authorization": "Bearer __OPENAI_SECRET__[CUSTOMER_API_TOKEN:f48a0a288e5d48e39c2ce638793e181e]__"
"x-hoop-token": "__OPENAI_SECRET__[HOOP_CALLER_TOKEN:439abaee162c475fb025b358a849035a]__"
== WebSocket frame "token=<placeholder>" through the lane
echo len 78 sha 081e280ee9233b41 | echo == sent: True | echo holds the placeholder prefix: True
```

- The placeholder is `__OPENAI_SECRET__[<secret_name>:<32 hex>]__`, `54 + len(secret_name)` characters, new in each session.
- The real token in the URL reached the sidecar audit row (`POST /echo?t=...`): the lane records the request line. The committed file has it replaced.
- `curl` refuses a placeholder in a URL without `-g`: `curl: (3) bad range in URL position 81`.
- The admitted request (`principal openai-agents`) shows that `X-Hoop-Token` was filled; the lane strips that header before the upstream.

### c4. TLS (S0b, S1a rerun, S1b)

```
* Uses proxy env variable https_proxy == 'http://proxy:8080'
* Establish HTTP proxy tunnel to offer-blogs-please-achieving.trycloudflare.com:443
*  subject: CN=offer-blogs-please-achieving.trycloudflare.com
*  issuer: CN=rcgen self signed cert
tls OK postman-echo.com TLSv1.3  issuer: commonName=rcgen self signed cert
postman-echo http=200
```

- Sandbox variables: `CODEX_PROXY_CERT=/usr/local/share/ca-certificates/envoy-mitmproxy-ca-cert.crt`, `SSL_CERT_FILE` and `REQUESTS_CA_BUNDLE` = the system bundle, `NODE_EXTRA_CA_CERTS` = the proxy CA. Debian 12, Python 3.12, `curl` 7.88.1.
- The sidecar's own `downstream_tls` with a public CA certificate was not tested: this machine has no public host for it. `postman-echo.com` shows that the proxy accepts a public-CA origin outside Cloudflare.

### c5. WebSocket and CONNECT (S1a rerun)

```
ws reply: HTTP/1.1 101 Switching Protocols | server: envoy | x-hop: origin
ws echo 0: b'eng-860 frame 0' rtt=217ms
ws echo 1: b'eng-860 frame 1' rtt=217ms
ws echo 2: b'eng-860 frame 2' rtt=215ms
connect reply: HTTP/1.1 403 Forbidden | server: envoy | connection: close
connect body: b'RBAC: access denied'
```

The upgrade passed the lane's caller check, so the vault fills upgrade headers. A tunnel client must dial through `HTTPS_PROXY`; a direct dial fails.

### c6. MCP, `service` origin (S2 `sess_088ebde31e3e3c22006adb8df7c5f881958bb39542f506f0f0`)

`allowed_domains` held only the lane host. The MCP server was the probe host, with a vault `static_bearer` credential for its URL.

```
POST /mcp 9.129.44.26 openai-mcp/1.0.0 (Codex) initialize   auth= Bearer <REAL:mcp-token>
POST /mcp 9.129.44.24 openai-mcp/1.0.0 (Codex) tools/list   auth= Bearer <REAL:mcp-token>
POST /mcp 9.129.44.24 openai-mcp/1.0.0 (Codex) tools/call   auth= Bearer <REAL:mcp-token>
9.129.44.16..29   chatgpt-connectors.json: IN 9.129.0.0/16   chatgpt-agents.json: IN 9.129.0.0/16
```

- One `whoami` call made 14 requests: three discover, initialize and list rounds, then the call.
- Lists fetched 2026-10-23, `creationTime` 2026-10-20; the connectors list has 283 prefixes, the agents list 1758. Both cover all OpenAI customers; an allowlist names OpenAI, not the agent.

`environment` origin, no vault (S2c `sess_07d5d6a49637fde4006adb8e7666288195a673ef0f96dfe025`, S2d `sess_0e4f3bce567f5b60006adb8ea3c0488193aae0453691d7836a`):

```
MCP host not in allowed_domains: turn failed {"code": "connection_failed", "message": "Could not connect to MCP server 'probe'. ..."}
MCP host in allowed_domains:     POST /mcp 20.55.172.114 codex-mcp-client/0.0.0 initialize ... tools/call   (20.55.172.112/28, agents list)
with a vault credential for the URL: 400 "MCP server ... with connection_origin=environment cannot use an attached vault credential"
```

The S2d addresses are in the list that holds every sandbox address seen (K7). That session's own sandbox address was not measured. stdio MCP was not tested.

### c7. Template (S7 `sess_07621eab3328284c006adb8f034f48819486962fa2621fe307`)

```
template create with vault_ids: 400 unknown_parameter "Unknown parameter: 'vault_ids'."
session {agent_id, environment: {environment_template_id}, vault_ids}:
  environment.network = {"access": "restricted", "allowed_domains": ["<lane host>"]}
  HOOP_CALLER_TOKEN len=71 CUSTOMER_API_TOKEN len=72
  lane http=200
  example.com http=000  curl: (56) CONNECT tunnel failed, response 403
override network.access=enabled:           400 session environment.network cannot broaden the environment template network policy
override allowed_domains + example.com:    400 (same)
override allowed_domains = [example.com]:  400 (same)
override network.access=disabled + vault:  400 environment.network.access=disabled cannot be used with environment credentials
```

A saved agent (`POST /v1/agents`) holds model, instructions and tools; it has no environment or vault field.

### c8. Session id in the sandbox (S0, S0b, S1a, S1a rerun)

From the S1a rerun (`out/s1a2.txt`):

```
CODEX_ENVIRONMENT_ID=ccarenv_b64_Y2NhcmVudl82YWRiOGNkNzk5Zjg4MTkxYjdhM2FkMTUzMThkNmM2Ng   == session.environment.id
CODEX_SESSION_ID=01a1699e-371f-7144-b2c9-01632684e034                                    (not the sess_ id)
```

No variable holds the `sess_` id. The first message can give it, as on Claude. Or the agent sends `$CODEX_ENVIRONMENT_ID`: no prompt needed, but only the API maps it to a session.

### c9. Setup tool (`setup.sh`)

`setup.sh` creates the template, vault, credential and saved agent, starts a session, sends the first message with the session id, and prints the agent's commands. `setup.sh --delete` removes them. A second run refuses to start until `--delete` ran. Only `curl` and `jq`; the key goes through a file descriptor.

First run (`sess_0236075d76d00ae1006adb8f96a60481978bf943857b24ccf5`, `out/c9-first-run.log`; the agent chose Python `urllib`):

```
GET /users STATUS 200   "email": "[REDACTED:EMAIL_ADDRESS]" ...
DELETE /users/1 STATUS 403   X-Hoop-Denied policy   BODY this agent may not delete
```

Recording run (`sess_0212ba828620fc48006adb93238ce081938dbbae02acb7c2e1`, `out/recording.gif`, `out/recording.mp4`, frames `out/recording-frame-agent.png` and `out/recording-frame-audit.png`; the agent chose `curl -i`):

```
HTTP/2 200   "email": "[REDACTED:EMAIL_ADDRESS]", "phone": "[REDACTED:PHONE_NUMBER]", "card": "[REDACTED:CREDIT_CARD]"
HTTP/2 403   x-hoop-denied: policy   this agent may not delete
audit: statement openai-agents GET /users          session sess_0212ba82...
audit: violation openai-agents DELETE /users/1  no-delete  session sess_0212ba82...
```

The `openai` Python SDK 3.37.0 has `client.beta.agents.*` but no CLI, and declares a `spend_control` session field that the API refuses (`out/operator-notes.txt`).

## Facts from the ticket, checked

| Fact | Result |
|---|---|
| Default `network.access` is `enabled` | Not tested: every session set it |
| `restricted` takes exact hosts | Used only exact hosts; limits not tested |
| Vault fills only HTTPS headers, ports 443 and 8443 | **Not as documented**: URL and body were filled (1 session; headers in 8). Port 80 was not filled. 8443 not tested |
| MCP `environment` origin does not support `restricted` | **Not as documented**: it worked in one session with the MCP host allowed. stdio not tested |
| Vault MCP auth only for `service` origin | Confirmed: `environment` origin with a matching credential is refused at create |
| IP ranges change; mTLS recommended | The page says ranges change; MCP and sandbox addresses are in the lists (c6, K7) |
| Idle sandbox deleted after one hour; US only, no ZDR | Not tested |
| Claude: refused host fails at DNS, not 403 | OpenAI: the proxy answers `403 Domain forbidden` to CONNECT; the sandbox also resolves no name |
| Claude: Cloudflare buffers bodies and hides a head leak | Not tested: this spike ran no gate-leak test. The lane reached the probe upstream directly (`stack/sidecar.yaml`) |

## Recommendation

**Confirm the shape for HTTP, with one change: guard against placeholders outside headers.** The egress still enforces one host, and the vault still binds the token to that host. But a token can now reach the URL, the body, the audit row and any upstream that echoes input. Two guards:

1. The template's agent instructions say: tokens in headers only, never in a URL or body.
2. ENG-871 (Triage): the http lane keeps a vault-filled token out of audit rows, for example by refusing a request that carries its caller token outside `X-Hoop-Token`. Until then, the instructions are the only guard.

**One-click on OpenAI** is `setup.sh` once (template, vault, saved agent), then each session names three ids: `agent_id`, `environment.environment_template_id`, `vault_ids`. The template holds the guarantee: no session can broaden its network. The vault is a separate id; a session without it has no tokens and the lane refuses it.

**MCP: re-decide ENG-864 before it starts.** `environment` origin under `restricted` is enforced by the egress like REST; `service` origin is not. The open part is auth: `environment` origin cannot use a vault credential. Test a plugin `.mcp.json` with `bearer_token_env_var` that names a vault variable before choosing.

**Databases:** keep the milestone 5 plan. Postgres bytes do not pass the proxy; a WebSocket through a lane works.

### Milestone 2 to 5 tickets that must change

| Ticket | Change |
|---|---|
| ENG-862 REST template | Context: replace "secrets go only into HTTPS headers" with c3. Test the lane's own public-CA TLS (`downstream_tls`) first: c4 did not. DoD "each audit row names the session": identity refusals and WebSocket frame rows do not. README: a direct call fails with `CONNECT tunnel failed, response 403`; tokens in headers only; the lane listens on 443 only (port 80 passes the proxy). Session id: first message, as Claude; `$CODEX_ENVIRONMENT_ID` as an option. Setup: `setup.sh` (curl + jq), no `ant apply` equivalent. One-click: three ids per session |
| ENG-863 Kubernetes | kubectl must use `HTTPS_PROXY` and trust the system bundle (it holds the proxy CA), not a pinned cluster CA. Packages under `restricted`: not verified here |
| ENG-864 MCP | DoD line "environment origin needs `enabled`" does not match c6b. Decide between `environment` origin (enforced, auth open) and `service` origin (`allowed_domains` does not apply; the addresses are in lists shared by all OpenAI customers) |
| ENG-865 quick start | c4: a quick tunnel host works. `agent.sh` is `setup.sh` (no CLI to install). The recording runs `setup.sh`, the audit query and `--delete` in 54 s, sidecar already running |
| ENG-866 Postgres MCP | For `service` origin, Caddy admits the published lists, not one prefix (one session's calls fell in `9.129.0.0/16`). Follows the ENG-864 decision |
| ENG-867 docs page | Drop "MCP only with `connection_origin: service`" until ENG-864 decides. Add the URL and body substitution warning. Refused host: `403` at the proxy |
| ENG-868 brief | Claude vs OpenAI rows: vault fills URL and body on OpenAI; the template holds no vault; sandbox addresses are in `chatgpt-agents.json` on OpenAI, unlisted on Claude; MCP from `9.129.0.0/16` (one session) vs `160.79.104.0/21` |

## Known issues found

| # | Issue | Where |
|---|---|---|
| K1 | The vault fills placeholders in the URL and body, against the docs. The real token lands in the lane's audit row when it is in a URL (ENG-871) | OpenAI vault, sidecar http lane |
| K2 | `spend_control` is in SDK 3.37.0's session type, and the API refuses it. No per-session cost cap | OpenAI API |
| K3 | In S1b, `items` had no `command_execution` when the turn completed, and the trace said `completeness: partial` with the exec output missing. The item came later, listed after the turn's final message (`out/s1b2.json`). A client that reads items at completion can miss a command | OpenAI API |
| K4 | `turn.usage` was `null` at completion on most turns and was filled later (`out/s1b2.json`); traces had the counts | OpenAI API |
| K5 | One session create returned `409 conflict_error: session changed during parent-guarded runtime write`; the retry worked (`out/operator-notes.txt`) | OpenAI API |
| K6 | Stored command items replace the text after `X-Hoop-Token:` with `[REDACTED_SECRET]`, also a format string (`%s\r\nX-OpenAI-Session-Id:`); `env` output kept the placeholders. Cause not checked | OpenAI API |
| K7 | 13 sandbox addresses in 10 sessions, all in `/28`s of `chatgpt-agents.json`. The S1a rerun used three: `20.169.87.191`, `20.169.87.178`, then `57.154.33.152` on a later turn | OpenAI |

## Reproduce

- `stack/`: probe upstream (`probe_server.py`), sidecar lane (`sidecar.yaml`), Compose on loopback. The two quick tunnels ran on the host: `cloudflared tunnel --url http://127.0.0.1:18080` (lane) and `:18088` (probe).
- `oai.py`: Agents API driver; the key is read from `~/.hoop/secrets/openai.key`.
- `sandbox/`: `probe.py` and the cell scripts, shipped as inline environment files. The Postgres login in them is EBI RNAcentral's published read-only one.
- `sessions/`, `vault/`, `prompts/`: request bodies with `{{MARKER}}` fields.
- `setup.sh`, `audit.sh`, `recording.tape`: c9 and the recording (`vhs recording.tape` from this folder).
- `out/probe.jsonl`: the operator's own checks through the tunnel show `<operator-laptop>` as source.
