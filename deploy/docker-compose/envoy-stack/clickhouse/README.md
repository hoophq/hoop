# ClickHouse lanes

> hoop-inspect 0.1.0

An overlay on the stack in the parent directory: one `clickhouse-server`
behind four lanes, one per wire protocol it exposes. The lanes add no rules;
the three database lanes inherit the process's one guardrail and one mask
rule, and the HTTP lane is audit-only.

```
client   ──TLS───────> envoy :8446 ──OPA──> hoop-inspect :18123 ──http──> clickhouse :8123   http codec
chclient ──native────> envoy :9000 ───────> hoop-inspect :19006 ──tcp───> clickhouse :9000   clickhouse codec
client   ──plaintext──> envoy :9004 ───────> hoop-inspect :19004 ──tcp───> clickhouse :9004   mysql codec
client   ──plaintext──> envoy :9005 ───────> hoop-inspect :19005 ──tcp───> clickhouse :9005   postgres codec
```

## Run it

```bash
./run.sh                                                                          # parent stack
docker compose -f docker-compose.yml -f clickhouse/docker-compose.clickhouse.yml up -d --wait
./clickhouse/demo-clickhouse.sh
docker compose -f docker-compose.yml -f clickhouse/docker-compose.clickhouse.yml down -v
```

Ports on the host are Envoy's: `8446`, `9000`, `9004`, `9005`. Inside the
compose network the client dials `envoy:<port>`.

```bash
C="docker compose -f docker-compose.yml -f clickhouse/docker-compose.clickhouse.yml"

# Native protocol: clickhouse-client, LZ4 compression on by default
$C exec -T chclient clickhouse-client -h envoy --port 9000 \
  -u appuser --password apppass -d appdb \
  --query 'SELECT name, email FROM customers'

# MySQL emulation: the MariaDB CLI, --skip-ssl (see ../mysql/README.md)
$C exec -T client env MYSQL_PWD=apppass \
  mariadb -h envoy -P 9004 -u appuser --skip-ssl appdb -e 'SELECT name, email FROM customers'

# PostgreSQL emulation: psql, sslmode=disable as on every pg lane here
$C exec -T client env PGPASSWORD=apppass \
  psql -h envoy -p 9005 -U appuser -d appdb -c 'SELECT name, email FROM customers'

# HTTP: the SQL is the POST body; X-Hoop-User is what OPA reads
$C exec -T client curl -sk https://envoy:8446/ -H 'X-Hoop-User: alice' \
  -H X-ClickHouse-User:appuser -H X-ClickHouse-Key:apppass \
  --data-binary 'SELECT name, email FROM appdb.customers FORMAT JSONEachRow'
```

## Design

ClickHouse has four front doors and all four become explicit lanes:

| Port | Protocol | Codec | Lane | What the relay does |
|---|---|---|---|---|
| 9000 | native | `clickhouse` | `clickhouse-native` | statement text, guardrails, bounded LZ4 block decoding, row masking |
| 9004 | MySQL emulation | `mysql` | `clickhouse-mysql` | statement text, guardrails, row masking |
| 9005 | PostgreSQL emulation | `postgres` | `clickhouse-pg` | statement text, guardrails, row masking |
| 8123 | HTTP | `http` | `clickhouse-http` | fat gate, request line guardrails, audit with the SQL; no masking |

**Prefer :9000.** It is ClickHouse's own protocol. The codec clamps both
hello revisions to 54450 so peers and inspector use one known packet layout,
classifies native Query packets, and returns denials as ClickHouse Exception
code 497. Compressed results are not accumulated: each LZ4 frame's declared
decompressed size is checked before allocation, blocks are capped separately,
and scratch space is reused block by block. The overlay sets 16 MiB per frame
and 64 MiB per block; `clickhouse.max_frame_bytes` and
`clickhouse.max_block_bytes` are the per-listener controls.

**:8446 is an audit lane.** Two documented facts about the relay decide that.
Guardrails scan `Statement.Text`, which on `http` is the request line
(`sidecar/policy/pii.go`); ClickHouse takes the SQL as the POST body, so a
rule never sees it. And `http` masking substitutes bytes under a
`Content-Length` the gate corrects (`sidecar/gate/contentlength.go`);
ClickHouse always answers `Transfer-Encoding: chunked`, so the body passes
unmasked. The lane sets `mask: {rules: []}` so the config says so, rather
than inheriting a rule that loads clean and never fires. What the lane does
give: OPA's fat gate on `X-Hoop-User` (port `8446` is the `clickhouse`
service in `../opa/authz.rego`), an audit record per request carrying the
SQL (`capture_body`), and the request line scanned, which covers
`?param_x=` values ClickHouse binds into `{x:Type}`, and `?query=` on a
GET, which ClickHouse makes read-only.

`capture_body` records both directions, and the relay has no request-only
setting. With no masking on the lane, every result set lands in the audit
trail in the clear, up to `max_body_bytes`. `../sidecar/read-audit.py`
reports that as a `LEAK` on this overlay; it is right, and the demo says so
before printing it.

**:9000 is the native lane.** `clickhouse-client` reaches it through an Envoy
TCP proxy and the sidecar's `clickhouse` codec. The demo proves both response
masking on a compressed result and a denied DELETE surfaced as a native
Exception. Envoy remains a byte router; policy decisions happen only after
the sidecar has decoded the Query packet.

## Configure the native lane

`guardrails` and `mask` are policy sections, not fields under `clickhouse`.
Put them at the top level to inherit them on every database listener, as
`config-clickhouse.yaml` does, or under `clickhouse-native` to configure only
the native lane. Listener guardrails run before and concatenate with top-level
rules; a listener `mode` replaces the top-level mode. Listener mask rules
replace the top-level mask rules.

This standalone shape denies destructive native queries and deterministically
redacts two named result columns:

```yaml
pii:
  entities: [EMAIL_ADDRESS, BR_CPF]

listeners:
  - name: clickhouse-native
    protocol: clickhouse
    listen: 0.0.0.0:19006
    upstream: clickhouse:9000

    guardrails:
      mode: enforce
      rules:
        - name: no-destructive-clickhouse
          type: operation
          operations: [delete, drop, truncate]
          message: destructive ClickHouse statements are not permitted

    mask:
      rules:
        - name: customer-identifiers
          columns: [email, taxpayer_id]
          strategy: redact

    clickhouse:
      max_frame_bytes: 16777216
      max_block_bytes: 67108864
```

Column rules mask the whole cell without relying on detection. To scan every
supported string column for sensitive values instead, replace the mask rule:

```yaml
    mask:
      rules:
        - name: detected-identifiers
          entities: [EMAIL_ADDRESS, BR_CPF]
          strategy: redact
```

To reject sensitive values in query text instead of operations, replace the
guardrail rule:

```yaml
    guardrails:
      mode: enforce
      rules:
        - name: no-cpf-in-query
          type: pii
          entities: [BR_CPF]
          message: do not put a taxpayer id in a query
```

Use `mode: observe` to record guardrail matches without denying queries.
The free build accepts one guardrail and one mask rule per resolved lane; one
rule can list several operations, columns, or entities.

## ClickHouse configuration

`server/config.d/protocols.yaml` turns on `mysql_port: 9004` and
`postgresql_port: 9005`; the image ships them commented out.
`server/users.d/appuser.yaml` stores `appuser`'s password as plaintext, on
purpose: the MySQL emulation authenticates with `mysql_native_password`,
whose scramble is SHA1-based, so the server needs the plaintext or a
double-SHA1. A `password_sha256_hex` would work on `:8123` and `:9005` and
refuse every login on `:9004`. `seed.sql` runs from
`/docker-entrypoint-initdb.d` and creates `appdb.customers` as a
`MergeTree`.

## TLS on database lanes

The demo uses plaintext inside its isolated compose network. A native
ClickHouse TLS port uses TLS-on-connect, so `upstream_tls` and
`downstream_tls` work directly on a `clickhouse` lane. MySQL emulation keeps
the restrictions in [`../mysql`](../mysql/README.md). PostgreSQL emulation
uses pgwire's in-band SSLRequest.

## Known gap: the MySQL 8 CLI on :9004

ClickHouse does not advertise `CLIENT_QUERY_ATTRIBUTES`. The official MySQL
8.0.23+ CLI sets the flag in its handshake response regardless, then sends
`COM_QUERY` with no attribute block, because libmysql gates the block on the
server's capabilities and the flag on nothing. The relay's `mysql` codec
latches capabilities from the client's response alone
(`libhoop/v2/codec/mysql/client.go`, `readHandshakeResponse`; the greeting's
flags are read past in `response.go`), so it strips a block that is not
there, records every statement as `sql.incomplete` with no text, and
forwards it allowed. The guardrail has nothing to scan.

The demo's last beat shows it: a SELECT carrying the taxpayer id the lane
refused from the MariaDB CLI returns a row from the MySQL 8 CLI, and the
trail says `the query-attribute block is malformed`. The fix is one line of
intent in the codec: negotiated capabilities are `client & server`. It is
not in this repository. Until it lands, a `mysql` lane in front of any
server that does not offer the flag (ClickHouse, MariaDB, MySQL before
8.0.23) is enforced only for clients that do not set it: the MariaDB CLI,
go-sql-driver, mysql-connector-python.

## What the demo shows

1. **Native masking.** A compressed native result block is rebuilt around the
   redacted email.
2. **Native denial.** The DELETE returns ClickHouse Exception 497 and never
   reaches the server.
3. **Emulation masking and denial.** MySQL and pgwire enforce the same two
   process-level rules.
4. **Gated.** bob gets OPA's 403 on `:8446` before the relay sees a byte.
5. **Audited, not enforced.** alice's HTTP POST returns emails in the clear
   and lands in the trail with its SQL.
6. **The gap.** The MySQL 8 CLI walks a taxpayer id past the emulation lane.

## Files

| Path | What it is |
|---|---|
| `docker-compose.clickhouse.yml` | the overlay: ClickHouse clients and server, config swaps, host ports `8446`, `9000`, `9004`, `9005` |
| `envoy-clickhouse.yaml` | base Envoy config plus the four ClickHouse listeners |
| `config-clickhouse.yaml` | sidecar config: `appdb`, `httpbin` and four ClickHouse lanes |
| `server/config.d/protocols.yaml` | turns on the MySQL and PostgreSQL emulation ports |
| `server/users.d/appuser.yaml` | `appuser`, plaintext password |
| `seed.sql` | the `customers` fixture, ClickHouse dialect |
| `demo-clickhouse.sh` | the walk above |

The audit table is printed by `../sidecar/read-audit.py`. Its leak check
fires on this overlay for the reason given under Design.
