# Envoy + OPA + hoop — Citadel-shaped POC

A local replica of the topology Citadel described: **Envoy owns TLS and the
network path, OPA owns policy, hoop stitches in behind them.** No UDS, no PROXY
protocol, no MITM trust argument — hoop is an ordinary HTTP upstream that the
sandbox never sees.

```
client ──TLS──> envoy ──ext_authz(gRPC)──> opa      tier 1: fat gate
                  │                          │      "can alice reach httpbin?"
                  │                    injects alice's
                  │                    hoop proxy token
                  ▼
                hoop gateway  :18888 httpproxy      tier 2: deep inspection
                              :15432 postgres       "is this DELETE allowed?"
                              :12222 ssh            "who ran which command?"
                  │
                  ▼
                hoop agent ──> httpbin | appdb | sshd
```

Three lanes, decreasing Envoy visibility:

| Lane | Envoy port | Envoy sees | OPA consulted |
|---|---|---|---|
| HTTPS → httpproxy | 8443 | method, path, headers | yes, `ext_authz` |
| postgres | 5432 | a byte count | no — no pgwire parser |
| ssh | 2222 | a byte count | no — **no SSH filter exists at all** |

## Run it

```bash
./run.sh            # ~55s: brings the whole stack up
./creds.sh          # mints the postgres + ssh credentials
./demo-inspect.sh   # the hoop-inspect sidecar lanes (postgres, http)
./run.sh down       # tear down, including volumes
```

Needs `docker`, `curl`, `jq`, `openssl`, `python3`.

Two data paths run side by side in the same compose stack:

| Path | Ports | Driven by |
|---|---|---|
| Envoy → **hoop gateway + agent** | 8443, 15432, 12222 | manual / the hoop UI |
| Envoy → **hoop-inspect sidecar** | 8444, 5433 | `./demo-inspect.sh` |

The gateway path is the existing product. The sidecar path is the
`hoopinspect` library running as a process: it parses the wire protocol,
enforces policy per statement, writes an audit trail, and masks responses —
with no gateway, no agent and no database behind it.

If you change the library, rebuild the sidecar image first:

```bash
cd ../../..   # repo root
docker build -f deploy/docker-compose/envoy-poc/hoopinspect/Dockerfile \
  -t hoop-inspect:local hoopinspect/
cd - && docker compose up -d --force-recreate hoop-inspect
```

Sidecar knobs live in `hoopinspect/config.json`; validate a change without
starting anything:

```bash
go run ./cmd/hoop-inspect -validate \
  -config deploy/docker-compose/envoy-poc/hoopinspect/config.json
```

## The tiers

### Tier 1 — Envoy + OPA (this is config)

`envoy/envoy.yaml` runs an `ext_authz` filter against OPA over gRPC.
`opa/authz.rego` answers reachability and, on allow, returns the caller's hoop
proxy token as a request header. `run.sh` mints that token against the hoop API
and bakes it into the Rego.

```bash
curl -k https://localhost:8443/json -H 'X-Citadel-User: alice'   # 200
curl -k https://localhost:8443/json -H 'X-Citadel-User: bob' -i  # 403 from OPA
curl -k https://localhost:8443/json -i                           # 401, no identity
```

The token lands on hoop's `Authorization` header, which
`gateway/proxyproto/httpproxy/httpproxy.go:245` already reads. **hoop needed no
changes to work behind Envoy** — the `X-Forwarded-Proto` / `X-Forwarded-Host`
handling at `:201,563` and `:229,568` was built for exactly this hop.

`failure_mode_allow: false` — a dead OPA closes the gate.

### Tier 2a — postgres (hoop, not config)

Both statements below cross the *same* Envoy TCP listener on the same port:

```sql
SELECT name, email FROM customers;   -- fine
DELETE FROM customers WHERE id = 1;  -- destructive
```

Envoy cannot tell them apart. There is no pgwire parser in its filter chain, so
OPA is never invoked and never sees a statement. hoop parses the protocol,
records the statement text against a named human, and can evaluate content
rules on it.

### Tier 2b — ssh (the strongest version of the argument)

On the HTTP lane Envoy is coarse. On the SSH lane it is **blind**: Envoy ships
no SSH filter at any fidelity. The handshake, channel multiplexing, the exec
payload and every keystroke are one opaque TCP stream. There is no policy hook
to attach, so OPA is not merely bypassed — there is nothing to consult it
about.

```bash
docker compose exec -T client sshpass -p "$(cat .sshtoken)" \
  ssh -o StrictHostKeyChecking=no -p 2222 hoop@envoy 'whoami; ls /'
```

hoop terminates SSH, authenticates the credential
(`gateway/proxyproto/sshproxy/sshproxy.go:243-288`), opens a session bound to
the `appserver` connection, and records both directions
(`audit.go:316-319` — `SSHConnectionWrite` client-side and agent-side).

Note the invocation: **no connection name on the command line.** The
password-auth path binds the target connection to the credential itself and
carries it in the SSH permission extensions (`sshproxy.go:278`). Passing
`… hoop@envoy appserver 'cmd'` prepends a bogus token to your command and the
remote shell reports `appserver: command not found`. (The cert-auth path in
`sshcertproxy/certproxy.go:445` is the one where an exec names the target.)

### Check the recorded evidence

```bash
docker compose exec -T db psql -U postgres -d hoopdb -x -c \
  "SELECT user_email, connection, connection_subtype, verb, created_at
     FROM private.sessions
    WHERE connection IN ('appdb','appserver')
    ORDER BY created_at DESC LIMIT 3;"
```

The full SQL text is in `private.blobs.blob_stream` as base64 pgwire frames.
Decode a row with `base64 -d` to read the statement; the hoop web UI at
:8009 renders the same data.

## Per-user identity

Each user gets their own hoop credential
(`POST /connections/:name/credentials`), so the session row carries a real
subject. This is the shape Matt asked for — no shared service account at the
hoop layer.

**Caveat, and it matters:** hoop still authenticates to the *upstream database*
with the connection's stored credentials (`agent/controller/postgres.go:69-70`
sends `connenv.user` / `connenv.pass`). Per-user identity is preserved in
hoop's audit trail, not in `pg_stat_activity`. Pass-through credentials for
Postgres — relaying the client's SCRAM instead of rewriting the startup packet
— is unbuilt work, not a config flag.

## Known gaps in this POC

- **Guardrails are configured but do not fire — image/schema skew.** `run.sh`
  attaches a `deny_words_list` rule for `DELETE`/`DROP`/`TRUNCATE`, the rule is
  stored, and the connection references it. It still does not block, and the
  cause is verifiable: the pinned image (`1314.0.0-58eb81a`, built 2026-03-09)
  applies schema migration **64**, but repo HEAD is at **105**, and
  `GetConnectionGuardRailRulesByConnectionAndAttribute`
  (`gateway/models/connections.go:1247`) joins
  `private.guardrail_rules_attributes` — a table migration 68 introduces and
  this database does not have. The rule fetch errors, the agent receives no
  rules, and enforcement silently no-ops.

  Confirm it yourself:
  ```bash
  docker compose exec -T db psql -U postgres -d hoopdb -c '\dt private.*guardrail*'
  docker compose exec -T db psql -U postgres -d hoopdb -tAc 'select version from schema_migrations;'
  ```

  Adding MSPresidio does not help — the local guardrails engine
  (`libhoop/redactor/client.go:167`) never gets rules to materialize. The fix
  is an image built from current HEAD, which needs the private `libhoop` **and**
  the `hoophq/mcpproxy` sibling repo (`agent/go.mod:13` has
  `replace github.com/hoophq/mcpproxy => ../../mcpproxy`, outside the Docker
  build context — so `Dockerfile.localbuild` cannot build this tree as-is).

  **This does not weaken the tier-2 argument.** The audit trail captures the
  full SQL text either way. Recording is the evidence Envoy structurally
  cannot produce; blocking is the same parse plus a verdict. The hoop-inspect
  sidecar path (`./demo-inspect.sh`) does block, on the same statements.
- **SSH payload blob is empty — same image skew.** The SSH *session* is fully
  recorded: `private.sessions` carries subtype `ssh`, the real user, and the
  connection, and the gateway logs the whole channel lifecycle
  (`sshproxy.go:189,215,553`). The `blob_input_id` row exists but holds `[""]`
  on this image. The `/api/feature-flags` endpoint returns 404 here, so
  `experimental.ssh_guardrails` / `experimental.ssh_input_guardrails`
  (`agent/controller/ssh.go:24-25`) cannot be switched on either. The gateway
  lifecycle log (`sshproxy.go:189,215,553`) is the evidence that survives.
- **Credentials need an explicit TTL.** Gateway builds before the no-expiry
  sentinel (`connection_credentials.go:42`) treat an omitted
  `access_duration_seconds` as "expires now". Both scripts pass 24h.
- **`psql` must disable SSL** (`PGSSLMODE=disable`). hoop's postgres proxy
  isn't terminating TLS here, and Envoy passes the TCP stream through
  untouched. In production Envoy would terminate on that listener too.
- **OPA runs under `linux/amd64`.** `openpolicyagent/opa:*-envoy` publishes no
  arm64 image. Fine under emulation; drop the `platform:` line on amd64.
- **Identity is a header.** `X-Citadel-User` stands in for a verified JWT
  subject. Real deployments read
  `input.attributes.metadataContext.filterMetadata` populated by Envoy's
  `jwt_authn` filter — same Rego shape, no compose-level IdP.
- **SSH auth here is password-based.** hoop also supports SSH certificate auth
  against trusted CAs with cert→user mapping
  (`sshcertproxy/certproxy.go`, configured via `ssh_server_config.trusted_cas`
  + `user_mapping`). That is the shape a shop with an SSH CA would actually
  deploy; this POC uses the password path because it needs no CA.

## What this demonstrates for the Citadel conversation

1. hoop already deploys behind Envoy with **zero hoop changes**, across three
   protocols. The UDS + PROXY-protocol idea is unnecessary for this topology.
2. Policy stays in OPA. hoop does not ask to own tier 1.
3. The tier-2 gap is real and structural. On postgres, Envoy forwards bytes it
   cannot parse. On SSH it is worse: **no SSH filter exists in Envoy at all**,
   so there is no seam to attach a policy hook to. Every "~100 services" that
   is reached over SSH is, today, entirely unpoliced by the Envoy+OPA layer.
