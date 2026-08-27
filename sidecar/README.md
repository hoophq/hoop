# sidecar

> **0.1.0**: the API is settling. Expect the config schema to hold and the Go
> interfaces to move.

Turn raw database wire-protocol bytes into structured statements, and
structured statements into allow/deny verdicts.

**The library is a pure function over bytes.** It opens no socket, terminates
no TLS, routes nothing. You hand it bytes you already have, and whatever holds
the connection keeps holding it.

**The relay owns a socket.** The nested `cmd` module builds `hoop-inspect`,
which wraps the library in a listener that accepts a connection, dials one
upstream, and pumps bytes through the gate in both directions. A TCP port or a
unix socket, your choice per lane (see [Transport](#transport)). It runs behind
something that already owns TLS and identity, typically Envoy forwarding
plaintext over loopback or a unix socket.

**One dependency.** `github.com/hoophq/libhoop`, which carries the protocol
decoders and defines the wire types this module aliases. Everything else is
standard library, tests included. libhoop is private, so building this module
needs `GOPRIVATE=github.com/hoophq/libhoop` and credentials for it.

The root shipped zero dependencies until the decoders moved to libhoop. They
produce the `Statement` a policy evaluates, so this module cannot describe its
own inputs without naming their types.

**Go 1.26.5.** Every module here (the root and the six nested ones) declares
`go 1.26.5`, so an older toolchain refuses the build instead of miscompiling
it. The repo's `go.work` and the sidecar image pin the same version.

```go
import (
    "github.com/hoophq/hoop/sidecar/inspect"
    "github.com/hoophq/hoop/sidecar/policy"
)

insp, _ := inspect.New(inspect.Postgres)
rules, _ := policy.NewRules([]policy.Rule{{
    Name:       "no-destructive",
    Type:       policy.MatchOperation,
    Operations: []inspect.Operation{inspect.OpDelete, inspect.OpDrop},
    Message:    "destructive statements are not permitted on appdb",
}})

stmts, _ := insp.Inspect(inspect.FromClient, packetBytes)
for _, s := range stmts {
    if v := rules.Evaluate(s); v.Denied {
        return errors.New(v.Message) // surface it in the protocol's error frame
    }
}
```

## Run it locally: the Envoy stack

The fastest way to watch all of this work is the compose stack in
[`deploy/docker-compose/envoy-stack`](../deploy/docker-compose/envoy-stack). Envoy
terminates TLS and calls OPA for reachability, `hoop-inspect` sits behind it as
an ordinary upstream, and a Postgres database and an HTTP service sit behind
that. No hoop gateway, no agent, no control-plane database: the sidecar reads
one YAML file.

Needs `docker`, `curl`, `openssl` and `python3`.

**1. Bring it up.** The first run takes about a minute. It mints a self-signed
cert for Envoy, builds `hoop-inspect:local` from the `sidecar` tree, and
starts six containers. From the repo root:

```bash
cd deploy/docker-compose/envoy-stack
./run.sh
```

**2. Check the sidecar is healthy**, and read what each lane resolved:

```bash
curl -s localhost:19000/healthz                        # ok
curl -s localhost:19000/config | python3 -m json.tool
```

**3. Walk the lanes.** `./demo.sh` runs every one of these in order and prints
the audit trail at the end. The individual pieces:

```bash
# Tier 1: OPA answers reachability and nothing else.
curl -sk https://localhost:8443/json -H 'X-Hoop-User: alice' -o /dev/null -w '%{http_code}\n'  # 200
curl -sk https://localhost:8443/json -H 'X-Hoop-User: bob'   -o /dev/null -w '%{http_code}\n'  # 403

# Tier 2, postgres. Envoy forwarded these bytes as opaque TCP and consulted
# nobody, so the sidecar is the only thing that can judge them.
PG="docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable \
  psql -h envoy -p 5432 -U appuser -d appdb"
$PG -c 'DELETE FROM customers WHERE id = 1;'      # refused in a real pgwire ErrorResponse
$PG -c 'SELECT name, email, ssn FROM customers;'  # the result set comes back masked

# Tier 2, http: a denial on the RESPONSE, which no ext_authz config can express.
curl -sk https://localhost:8443/status/503 -H 'X-Hoop-User: alice' -w '\n%{http_code}\n'  # 403
```

**4. Read the evidence** the sidecar recorded:

```bash
curl -s localhost:19000/stats                  | python3 -m json.tool
curl -s 'localhost:19000/api/sessions?limit=1' | python3 -m json.tool
docker compose logs hoop-inspect | ./sidecar/read-audit.py
```

**5. Rebuild after changing this library**, and tear down when you are done:

```bash
./run.sh --rebuild
./run.sh down          # includes volumes
```

| Port | Serves |
|---|---|
| 8443 | Envoy HTTPS, to the `httpbin` lane |
| 5433 | Envoy TCP, to the `appdb` lane |
| 19000 | sidecar admin: `/healthz`, `/stats`, `/config`, `/events`, `/api/*` |
| 9901 | Envoy admin |

That Postgres listener is `envoy:5432` inside the compose network and `5433` on
the host, because a laptop usually has something on 5432 already. It is why the
`psql` line above runs from the `client` container.

`PGSSLMODE=disable` is required on the CLIENT: nothing terminates TLS between
psql and the sidecar on that lane, so a client negotiating it would leave no
plaintext to parse.

The hop from the sidecar to `appdb` IS encrypted, and separately so. See
[Upstream TLS](#upstream-tls).

For the code path behind each command, a per-command runbook and a
troubleshooting table, read
[docs/adr/0005-sidecar-flow.md](../docs/adr/0005-sidecar-flow.md).

## Running the relay yourself

Two ways to run the same relay.

**Through the hoop CLI**, which links the same plugins into the binary the
release pipeline already builds. Nothing extra to compile or ship:

```bash
hoop start sidecar --config config.yaml --validate   # check the config and exit
hoop start sidecar --config config.yaml              # run
```

The command was named `inspect`. On 1.149.0 and newer that name still works as
a deprecated alias and prints a notice naming the new one. **Below 1.149.0 only
`inspect` exists**, so a pinned older image needs the old spelling.

`--config` also reads `HOOP_SIDECAR_CONFIG` (or the older
`HOOP_INSPECT_CONFIG`, still honoured), which is the shape a Kubernetes
deployment wants: mount the ConfigMap, set the variable, pass no arguments.

That binary is already in the images you pull: `hoophq/hoopdev` (agent) and
`hoophq/hoop` (gateway), from **1.126.0** onward as `inspect` and **1.149.0**
onward as `sidecar`. Running the relay out of one
without building anything (`docker exec` into a live container, extending the
image and swapping `CMD`, or adding a second container to the agent pod) is
[QUICKSTART-AGENT-IMAGE.md](./QUICKSTART-AGENT-IMAGE.md).

**As a standalone binary**, when a sidecar container should carry the relay and
nothing else. It lives in the nested `cmd` module, which is where the optional
plugins get linked (the YAML front end and alcatraz PII detection) so they stay
out of the root's dependency set:

```bash
cd cmd
go build -o hoop-inspect .

./hoop-inspect -validate -config config.yaml
./hoop-inspect -config config.yaml
./hoop-inspect -version
```

Go 1.26.5 or newer builds it. Below that the module's own `go` directive stops
the build with `requires go >= 1.26.5`, and `GOTOOLCHAIN=auto` (the default)
fetches the right one rather than failing.

As a container, with the `sidecar` tree as the build context. From the repo
root:

```bash
docker build -f deploy/docker-compose/envoy-stack/sidecar/Dockerfile \
  -t hoop-inspect:local sidecar/
```

There are no build tags. The config file decides every capability, so an
operator turning on PII detection does not also have to swap the binary. Omit
the `pii` section and detection is off, which makes masking unavailable and a
`pii` policy rule a config error: both are refused at startup rather than
silently skipped.

To embed the relay in your own process, call `daemon.Setup` to load the config
and build the detector, then `daemon.Run`. That is all `hoop start sidecar`
does; read `client/cmd/startsidecar.go` for the whole of it.

## Configuring it: config.yaml

One file is the whole configuration. The process reads it at startup, resolves
every listener, and then tells you what it resolved. YAML and JSON both work
and the extension picks the parser: `.yaml` and `.yml` go through the nested
`config/yaml` module, anything else is read as JSON. Decoding is strict, so a
mistyped key fails the startup instead of silently disabling a control.

### 1. Write the file

Top-level `policy` and `mask` are DEFAULTS. Each listener is one upstream, and
inherits those defaults unless it overrides them.

```yaml
log_level: info

admin:
  listen: 127.0.0.1:19000   # /healthz /stats /config /events /api/*

audit:
  file: "-"                 # stdout as JSON lines; a path appends to that file
  async_queue_size: 1024    # a slow sink must not block a user's query
  memory_buffer: 256        # last N events, readable at GET /events
  query_sessions: 500       # backs GET /api/sessions
  fail_closed: false        # true refuses a statement whose audit write failed

pii:                        # omit the section to disable detection entirely
  entities: [EMAIL_ADDRESS, US_SSN, CREDIT_CARD, BR_CPF, IBAN_CODE]

policy:                     # inherited by every listener
  enforce: true             # false is observe-only: inspect and audit, deny nothing
  rules:
    - name: no-cpf-in-query
      type: pii
      entities: [BR_CPF]
      message: do not put a taxpayer id in a query; it lands in the database's own logs

mask:                       # inherited by every listener
  enabled: true
  rules:
    - {name: emails, entity: EMAIL_ADDRESS, strategy: redact}
    - {name: ssn, entity: US_SSN, strategy: partial, keep_last: 4}

listeners:
  - name: appdb
    protocol: postgres
    listen: 0.0.0.0:15432
    upstream: appdb:5432
    connection: appdb       # the name audit rows and policy key on
    policy:
      rules:
        - name: no-destructive-sql
          type: operation   # classifier-derived, so SELECT 'DROP TABLE x' is a select
          operations: [drop, delete, truncate]
          message: destructive statements are not permitted on appdb
    mask:
      rules:                # REPLACES the default list rather than extending it
        - {name: ssn-column, columns: [ssn], strategy: partial, keep_last: 4}
        - {name: emails, entity: EMAIL_ADDRESS, strategy: redact}

  - name: httpbin
    protocol: http
    listen: 0.0.0.0:18080
    upstream: httpbin:8080
    connection: httpbin
    policy:
      opa:
        url: http://opa:8181/v1/data/hoop/inspect
        fail_open: false
      rules:
        - name: no-admin-api
          type: http_resource
          resources: ["/admin/**"]
          message: the admin API is not reachable through this proxy
        - name: no-upstream-5xx
          type: http_status   # response-side, so ext_authz cannot ask it
          statuses: ["5xx"]
          message: upstream failure suppressed by policy
```

Rule types and what each one matches are in [Policy](#policy); masking
strategies and the entity-versus-column choice are in
[Masking and PII](#masking-and-pii).

Other listener fields worth knowing: `network: unix` binds a filesystem socket
instead of a port (see [Transport](#transport)), `upstream_tls` encrypts the
connection to the backend (see [Upstream TLS](#upstream-tls)),
`idle_timeout_sec` closes an idle connection (leave it unset for interactive
sessions, since psql idles between keystrokes), and `max_conns` bounds
concurrency.

### 2. Know how a listener inherits

| Field | Merge | Why |
|---|---|---|
| `policy.rules` | concatenate, listener first | Every rule denies and the first match wins, so concatenating cannot change the allow/deny outcome. Order only picks which message the user reads. |
| `policy.opa` | replace | One lane has one decision endpoint. |
| `policy.enforce` | replace | A lane rolling out behind an enforcing default has to be able to say observe-only. |
| `mask` | replace | A rule owns an entity type, and two concatenated lists leave two rules competing for one entity. |

### 3. Validate before you deploy

Nothing needs to be running. Run it against the Envoy stack's own config, from
the `sidecar` directory:

```bash
cd cmd && go build -o hoop-inspect . && \
  ./hoop-inspect -validate -config ../../deploy/docker-compose/envoy-stack/sidecar/config.yaml
```

```
config OK: 2 listener(s)
  appdb            postgres  enforcing 2 rule(s) + masking
  httpbin          http      enforcing 4 rule(s) + masking
```

Each line is the RESOLVED lane, so the counts include what it inherited. A lane
with an `opa` block reads `+ opa`, and one with `enforce: false` reads
`observe-only`.

Validation builds every lane, so it catches what a syntax check cannot, and it
reports every problem in one run rather than one per restart. It refuses these
outright:

- `mask.enabled` on a protocol whose codec can carry neither masking
  mechanism, naming the lane.
- A `pii` rule naming an entity absent from `pii.entities`, naming the entity.
  Without this check the rule loads, evaluates, and matches nothing, so a
  guardrail looks live while allowing everything it was written to stop.
- A key typo, in YAML or JSON.
- A bad regex in any lane's rules, naming the lane.
- An `ai_analysis` rule with no `analyzer` section, no trigger, or no action
  for any risk level. All three would load and classify nothing.
- An `ai_analysis` rule on a lane whose protocol has no content builder,
  naming the protocol. That rule classifies nothing and says nothing while
  doing it: the analyzer returns before it has a status, so there is no
  finding and no annotation to notice.
- An `analyzer.provider` the binary does not link, naming what it does link.
- A credential file readable by group or other, naming its mode.
- An `http` block on a non-HTTP lane, or `authorization` in its header
  allowlist.

### 4. Ask the running process what it resolved

Reading the file cannot tell you which rules a lane ended up with, because
inheritance happens at startup. Debugging a denial that never fired starts
here, again against the stack config:

```bash
curl -s localhost:19000/config | python3 -m json.tool
```

```json
{"lanes": [
  {"name": "appdb", "protocol": "postgres", "enforcing": true,
   "rules": ["no-destructive-sql", "no-cpf-in-query"], "masking": true},
  {"name": "httpbin", "protocol": "http", "enforcing": true,
   "rules": ["no-admin-api", "no-internal-ids", "no-upstream-5xx",
             "no-cpf-in-query"], "masking": true}
]}
```

Both lanes inherited `no-cpf-in-query`, and neither inherited the other's
rules. Rule names only: a `pattern_regex` can encode business logic, and this
endpoint already sits beside a read interface to the audit trail.

### Sharing a rule block between lanes

This is the reason to prefer YAML. Anchors let several listeners reference one
block, and a top-level key starting `x-` is dropped before validation, so an
anchor does not need a matching config field.

```yaml
x-readonly: &readonly
  - name: no-writes
    type: operation
    operations: [insert, update, delete, drop, truncate]
    message: this credential is read-only

policy:
  enforce: true   # without this every lane below is observe-only

listeners:
  - {name: replica-a, protocol: postgres, listen: 0.0.0.0:15432, upstream: a:5432, policy: {rules: *readonly}}
  - {name: replica-b, protocol: postgres, listen: 0.0.0.0:15433, upstream: b:5432, policy: {rules: *readonly}}
```

`enforce` defaults to false so a misconfigured rule cannot take production down
on first deploy. A lane running observe-only says so in the startup log and in
`/config`.

### Analyzing statements with a model

An `ai_analysis` rule sends a statement to a language model and denies on the
risk it reports. It is the only rule type that leaves the process, costs money
and can be slow, so three things bound it: a trigger decides what is worth
asking about, a cache collapses repeated statement shapes onto one verdict,
and the rule runs LAST in the chain, after the free local rules and OPA.

It runs wherever a content builder renders the statement for a model:
`postgres` and `mssql` send the statement text with the operation, tables and
database the codec derived; `http` sends the method, normalized resource and
body. A lane whose protocol has no builder is refused at startup rather than
left classifying nothing, which is what a relay-only protocol would otherwise
get: no statements to render means no verdict, silently.

```yaml
pii:
  entities: [EMAIL_ADDRESS, US_SSN, BR_CPF]

analyzer:                      # one provider serves every lane
  provider: vertex             # vertex | anthropic | openai
  model: claude-sonnet-4-5@20250929
  extra: {project: my-gcp-project, region: global}
  # credentials_file omitted -> Application Default Credentials.
  # Under GKE Workload Identity there is then no credential on disk at all.
  timeout_sec: 10
  fail_open: true              # the default, and see below
  send: redacted               # raw | redacted | refuse
  max_input_bytes: 8192
  cache: {size: 4096, ttl_sec: 900}
  max_calls: 500

listeners:
  - name: appdb
    protocol: postgres
    listen: 0.0.0.0:15432
    upstream: appdb:5432
    policy:
      rules:
        - name: risky-writes
          type: ai_analysis
          trigger: {operations: [delete, update]}
          high: block
          medium: warn
          low: allow
          message: refused by risk analysis

  - name: api
    protocol: http
    listen: 0.0.0.0:18080
    upstream: httpbin:8080
    http:                      # REQUIRED for HTTP analysis, see below
      capture_body: true
      max_body_bytes: 8192
      headers: [Content-Type]
    policy:
      rules:
        - name: risky-payloads
          type: ai_analysis
          trigger: {resources: ["/anything", "/users/*/orders"]}
          high: block
```

**An HTTP lane needs `capture_body: true`.** The codec exposes nothing by
default, so without it the analyzer sees `POST /anything` and no body, which
tells a model nothing. A request with no body is skipped rather than
classified, so an unset flag shows up as an analyzer that never fires.
`authorization`, `cookie` and `proxy-authorization` cannot be allowlisted;
headers never reach the model regardless.

**Only requests are classified.** By the time a response comes back a write
has already happened, and read-side exposure is masking's job.

**`fail_open` defaults to true here**, the opposite of every other evaluator.
A classifier that denies whenever its provider has an outage takes the
database down with it. Set it false where the classification is a compliance
requirement, and accept that a provider outage then stops traffic.

**`send: redacted` uses the in-process detector** to name entities instead of
transmitting their values. A relay whose job is keeping taxpayer ids out of a
database's query log should not post them to a model vendor. `send: refuse`
denies locally instead of transmitting, and both need a `pii` section.

**Handing the decision to OPA.** `high: block` is a level→action table with
three rows, and it sees only the level. Set the action to `defer` where the
real determination also needs the user, the hour or a break-glass list: the
analyzer classifies and records the level, and the block/allow choice moves
into Rego.

```yaml
policy:
  opa:
    url: http://opa:8181/v1/data/hoop/inspect/result
    fail_open: false
    gate: true
  rules:
    - name: risky-writes
      type: ai_analysis
      # no trigger: with the gate on, Rego decides what is worth classifying
      high: defer
      medium: defer
      low: defer
```

The level then travels to OPA as `input.findings.ai_analysis`, one entry in a
map every producer on the lane reports into. `defer` is not analyzer-only:
any rule type takes `action: defer` and reports the same way (see
[Reporting instead of denying](#reporting-instead-of-denying)), so one
statement can hand Rego a risk level and a set of PII entity classes at once.

`defer` reorders the chain. A decision that reads the risk level has to run
after the thing that fills it, so OPA moves from before the analyzer to after
it, and a statement Rego would have refused for free now costs a model call
first. `gate: true` buys that back by consulting OPA on both sides: once
before the analyzer to answer "is this worth a model call", once after to
answer what the level means. Both calls hit the same URL and carry
`input.phase`, so a policy that ignores the field answers both identically,
and turning the gate on costs one round trip rather than a rewrite.

Four configs are refused at startup. `defer` on a lane with no
`policy.opa.url` defers to a decision that does not exist, which allows
everything; `gate: true` on a lane with no `ai_analysis` rule is a round trip
that buys nothing. An `ai_analysis` rule with no `trigger` is also refused,
except under `gate: true`, where an empty trigger is how you say Rego decides.
So is `action: defer` on an `ai_analysis` rule: that type defers per level
through `high`/`medium`/`low`, and a rule saying both would leave two answers
to one question. [Policy](#policy) covers what the two phases send and what
the gate may answer.

**Writing your own prompt.** Risk depends on what you are protecting, so the
risk guidance is replaceable at two levels.

`analyzer.prompt` is **process-wide**: it applies to every `ai_analysis` rule
on every lane, database and HTTP alike. Put deployment-wide facts there and
nothing protocol-specific, because the same words reach the model judging a
SQL statement and the model judging a JSON body. A rule's own `prompt:` is
where protocol- and lane-specific wording belongs, and it wins:

```yaml
analyzer:
  prompt: |
    You are classifying traffic to a regulated production environment
    holding customer financial records. Anything that reads or modifies
    customer or payment data is at least medium risk.

listeners:
  - name: appdb
    policy:
      rules:
        - name: risky-writes
          type: ai_analysis
          trigger: {operations: [update]}
          high: block
          prompt: |
            You are classifying SQL against the customer ledger. An UPDATE
            with no WHERE clause is always high risk, and so is any schema
            change.

  - name: api
    policy:
      rules:
        - name: risky-payloads
          type: ai_analysis
          trigger: {resources: ["/orders/**"]}
          high: block
          prompt: |
            You are classifying HTTP request bodies to the orders API. A
            payload that cancels or refunds in bulk, or that edits another
            tenant's records, is high risk.
```

Setting only `analyzer.prompt` is fine: the built-in guidance it replaces
covers both protocols, with separate high-risk examples for SQL and for HTTP,
for exactly this reason. Overriding it with database-only wording is the easy
mistake: an HTTP lane then classifies JSON bodies against advice about `DROP`
and `TRUNCATE`.

A prompt replaces the **guidance** only. Two instructions are appended after
whatever you write and cannot be removed:

- Report the verdict by calling exactly one of the three risk tools. The tool
  choice makes the risk level an enum rather than a parsing problem: the level
  is which tool the model chose, so there is no free text to misread.
- Never quote a literal value from the statement in the title or explanation.
  That is a security property: the verdict reaches an audit record, and a
  title repeating the identifier it objected to has published that
  identifier.

Changing a prompt invalidates cached verdicts for the rules it applies to, so
a reworded prompt takes effect on the next statement rather than after the
cache TTL. `/config` reports `custom_prompt: true` and never the text, for the
same reason it reports rule names and not their `pattern_regex`.

**Costs.** An ORM issues one statement shape thousands of times per session.
The cache keys on the shape, with literals stripped and HTTP resources
normalized, so `WHERE id = 1` and `WHERE id = 2` are one verdict. Watch the
hit rate at `/stats` before enabling a blocking action:

```bash
curl -s localhost:19000/config | python3 -m json.tool   # what each lane sends
```

Verdicts land in the audit trail as `metadata.risk_level`, which rolls up to a
session's highest risk in `GET /api/sessions` and `GET /api/stats`. Beside it,
`metadata.ai_status` records what the analyzer did: `ok`, `cached`, `skipped`,
`budget_exhausted`, `refused` or `error`. It merges most-degraded-wins across
rules, so a second `ai_analysis` rule that succeeded cannot hide the first
one's outage. `metadata.ai_rule` names the rule that produced the level and
merges as a unit with `risk_level`, so the name always belongs to the level
shown.

`ai_status` and `ai_rule` are the analyzer's OWN audit vocabulary, not the
finding it publishes to policy. The trail keeps the specific word, because an
operator tuning `max_calls` and one tuning `send: refuse` are chasing
different things. The finding maps both onto the generic `unavailable` and
puts the word in `reason`, so a Rego author never has to learn this package's
statuses to write a policy.

**Credentials never appear in the config.** `credentials_file` is a path; the
file must not be readable by group or other; the material is held in a type
that refuses to print through `%v`, `%#v`, JSON or `slog`; and `/config`
reports the endpoint host only. An endpoint URL carrying userinfo or a query
string is refused at startup, because that view sits beside a read interface
to the audit trail.

**Vertex** authenticates with a GCP OAuth2 bearer minted from a service
account and refreshed automatically, so `-validate` mints one token to prove
the credential, the `roles/aiplatform.user` binding and the host clock before
anything serves traffic. Prefer Workload Identity and omit `credentials_file`.

## Overlap with Envoy

Envoy already parses some of this.

`envoy.filters.network.postgres_proxy` parses SQL from Postgres `Query` and
`Parse` messages, emits `statements_select/insert/update/delete` counters, and
produces dynamic metadata as `table.db → [operations]` that the RBAC network
filter can act on. Within Postgres, at table-and-verb granularity, that is a
real capability.

The boundary:

| | Envoy | sidecar |
|---|---|---|
| Postgres SQL parse | `postgres_proxy`, best effort | full statement text |
| Postgres granularity | `table.db` + operation verb | statement, every effect, and relations split read/write |
| Postgres **response** | ✗ | result columns, row count, and masking by re-framing |
| HTTP request | `ext_authz`: method, path, headers, bounded body | same, plus normalized resource |
| HTTP **response** | ✗, ext_authz decides before the upstream is called | status, headers, body |
| Deny UX | RBAC/ext_authz drops or returns a bare 403 | operator-authored message |

On HTTP, Envoy's `ext_authz` covers request-side authorization well, and the
gaps named above are the narrow ones; Envoy is not blind here.

## Protocols

| Protocol | Request messages | Response messages | Stateful |
|---|---|---|---|
| `postgres` | `Query` ('Q'), `Parse` ('P'); handshake skipped | `RowDescription` ('T'), `DataRow` ('D'), and the three terminators that end a result set | yes |
| `mssql` | `SQLBatch` (0x01) and `RPCRequest` (0x03), reassembled across packets; login forwarded untouched | `COLMETADATA` (0x81), `ROW` (0xD1), `NBCROW` (0xD2) for masking; login replies scanned for a routing redirect | yes |
| `http` | HTTP/1.x requests | HTTP/1.x responses | no |

The Postgres codec is stateful because one `RowDescription` describes every
`DataRow` after it, and those land in different TCP reads. That is why the
registry hands out a factory rather than an instance: two connections sharing
one codec would corrupt each other's reassembly, and one tenant's SQL would
surface in another tenant's audit trail. Give every connection its own.

### MSSQL, and the Kerberos login

TDS gives the SSPI exchange its own packet type, and that fact alone lets
integrated authentication cross the relay with **no Kerberos code in this
library**. LOGIN7 (`0x10`) and each SSPI continuation (`0x11`) carry no SQL, so
the relay forwards them verbatim. Inspection begins at the first SQLBatch
(`0x01`) or RPC (`0x03`). The protocol's own message typing draws that
boundary, so no heuristic guesses where the ciphertext stops.

A relay could do no more here. The SSPI blob is a service ticket bound to the
server's SPN: relayable, and beyond minting, reading or editing.
`mssql.DetectSSPI` reports that a login is integrated, which serves the audit
trail and a clear error message. It cannot report who: under integrated auth
the username field sits empty, because the name lives inside the ticket.

**The codec passes the encrypted login through and keeps reading.** PRELOGIN's
`ENCRYPT_OFF` says "encryption off" and means "encrypt the login only": MS-TDS
3.2.5.3 puts the first LOGIN7 packet inside TLS and leaves every other packet
in the clear. A SQL Server with TLS administratively disabled still negotiates
it, minting a self-signed certificate at startup for the credentials, and
go-mssqldb defaults to it, so every Go client meets this on an ordinary 7.x
lane.

The handshake rides inside `0x12` packets, which the codec already forwards.
The bytes after it break the parse: the client inverts the nesting and writes
TLS records to the socket raw, with no TDS header. A decoder that walks them
as packets loses its place, and the relay stops seeing the session while the
socket keeps carrying it: no statements, no policy, no masking, no audit
trail.

So the codec walks that region by TLS's own record framing, finishes on the
byte where plaintext resumes, and inspects everything after it. Statements
from such a session carry `mssql.login_encrypted`, so an operator reading the
trail sees the window nobody could observe. The pass-through opens once,
during the login, and stays bounded; the codec answers ciphertext before the
login, after it, or past the bound with `ErrStreamUnsafe`, because nobody can
inspect a session that never returns to plaintext.

**The codec refuses one thing outright.** A login response carrying a routing
ENVCHANGE tells the driver to reconnect elsewhere, and drivers obey without
telling the user. Forward it and the client lands on a socket the relay does
not hold, where the session continues with no policy, no masking and no audit,
leaving no trace that it stopped being watched. The codec returns
`ErrStreamUnsafe`, the gate turns that into a denial regardless of policy, and
the connection ends with a message naming the redirect target. No rule enables
this behaviour and none can switch it off.

**MSSQL masks its responses**, by the same re-framing mechanism Postgres uses
and against a harder framing. TDS nests two: an 8-byte packet header wrapping
a token stream, where one `ROW` spans packets and one packet holds several
tokens. Changing a value changes the token's length, which changes how the
tokens repack, so the codec strips the headers, rewrites the token stream, and
lays fresh packets over the result. Patching bytes in place cannot work,
because a longer value has nowhere to go.

A column type it cannot measure (SQL_VARIANT, XML, UDT) stops the rewriting
for that connection. Guessing a length would desynchronize the client, which
turns a privacy gap into a lost session. Statements and policy carry on; only
masking steps aside. Whatever was already rewritten is kept and emitted, so a
value masked earlier in a response stays masked when an unmeasurable token
turns up later in the same one.

A worked deployment, with Envoy terminating TDS 8.0, a Kerberos client and an
AD domain controller, lives in
[`deploy/docker-compose/envoy-stack/mssql`](../deploy/docker-compose/envoy-stack/mssql).

MySQL and MongoDB codecs are **not shipped**. The `Codec` interface and the
shared SQL classifier are protocol-agnostic, so adding one takes a new
`codec/<name>` package and no other change.

The decoders ship in `github.com/hoophq/libhoop`, a separate private module
that imports nothing from here. The packages under `sidecar/codec/` are
the seam: they register each decoder through `Register` and hand it the SQL
classifier. Import those, not libhoop directly. Import only what you
need: a listener that speaks Postgres imports `codec/postgres` and never links
the HTTP machinery.

```go
import _ "github.com/hoophq/hoop/sidecar/codec/postgres" // postgres only
import _ "github.com/hoophq/hoop/sidecar/codec/all"      // postgres + mssql + http
```

## The Statement

```go
type Statement struct {
    Protocol  Protocol          // postgres | http
    Direction Direction         // client | server
    Text      string            // verbatim SQL, or the request line for HTTP
    Operation Operation         // the most consequential effect, not the leading verb
    Effects   []Operation       // every operation performed anywhere in the statement
    Relations []Relation        // {name, access}, write dominating read
    Tables    []string          // Relations flattened, or the HTTP resource
    Database  string            // when the protocol states it
    HTTP      *HTTPDetail       // http only; nil for the wire-database codecs
    Result    *ResultDetail     // response side: columns and row count, never values
    Metadata  map[string]string // protocol-specific, documented per codec
}
```

`Result` is what makes a response-side SQL rule possible: `SELECT *` does not
name the column it returned, so "this query came back with a column named ssn"
is a question no request-side rule can answer. It carries the column names and
a row count, never the rows.

For SQL one scan of the statement text fills four fields, and they are not
four views of the same fact:

- **`Operation` is the most consequential EFFECT**, not the leading verb.
  `WITH x AS (DELETE FROM customers RETURNING *) SELECT count(*) FROM x` is a
  `delete`, because a policy asking "may this run" is asking about the effect
  and not about the spelling.
- **`Effects` carries the full set**, every operation performed anywhere in
  the statement. Read it to tell a statement that only deletes from one that
  deletes and selects.
- **The vocabulary is closed**, and wider verbs fold into it. The scanner
  models `MERGE`, `COPY` and `EXPLAIN`; `Operation` and `Effects` never report
  them, because `MatchOperation` and the AI trigger compare for equality and a
  config naming `update` cannot name `merge`. A `MERGE` is an `update`, its
  `WHEN MATCHED THEN DELETE` branch still adds `delete`; `COPY ... FROM` is an
  `insert` and `COPY ... TO` a `select`; a plain `EXPLAIN` is a `select`,
  because it plans and does not execute. Nothing folds onto `other`, which
  ranks below every real verb and would hide a bulk load.
- **`Relations` carries `{name, access}`**, deduplicated and lowercased, with
  write dominating read for a relation that is both.
  `INSERT INTO staging SELECT * FROM customers` writes `staging` and reads
  `customers`.
- **`Tables` is `Relations` with the access dropped**, kept so rules written
  before the split keep matching. Empty means "could not tell", never
  "touches nothing".
- **`Operation == unknown` with `metadata["sql.incomplete"]` is the
  fail-closed signal.** The scanner met a statement whose effect is decided
  at runtime and says so instead of guessing; the metadata value is the
  reason. A rule naming `unknown` refuses those statements, and one that does
  not name it accepts that risk explicitly.

Comments and string literals are stripped before any of it, so
`SELECT 'DROP TABLE customers'` is a `select`. Prefer `MatchOperation` to
`MatchDenyWords` for verbs.

### Why this is not a parser

A SQL grammar is large, dialect-specific and a permanent maintenance burden.
The question a policy asks is much smaller: which relations does this
statement write, and which does it read. Verbs and the relation names beside
them are token-local, so the scanner answers that question without building
an AST.

It does need a stack. One integer of parenthesis depth says "somewhere inside
parentheses" but not "inside a CTE body", which is why a data-modifying CTE
used to read as a plain select. A stack of LABELLED regions costs one byte
per nesting level, and that one bit of context per level is what made
`WITH x AS (DELETE FROM t) SELECT` readable.

It models no expressions at all: no precedence table, no operator handling
past "these bytes end a token". Expression grammar is where most of a real
parser's cost lives, and none of it answers the question above.

`Dialect` exists because one byte means different things per engine. `[`
opens a quoted identifier in T-SQL and is an array subscript in PostgreSQL:
treating it as an identifier everywhere mangles `SELECT tags[1] FROM t`,
treating it as an operator everywhere loses `[dbo].[customers]`. Only the
lexical rules differ; the analysis after them is shared.

Those choices buy this, measured through the real Postgres path:

| statement | before | after |
|---|---|---|
| `UPDATE audit SET n=E'O\'Brien'; DELETE FROM customers` | `update`, tables `[audit]`, the DELETE invisible | `delete`, audit write + customers write |
| `WITH a AS (DELETE FROM customers RETURNING *) SELECT count(*) FROM a` | `select` | `delete`, customers write |
| `WITH x AS (SELECT $$a)b$$) DELETE FROM customers` | `select` | `delete` |
| `WITH set AS (SELECT 1) SELECT * FROM set` | `set` | `select` |
| `/* outer /* inner */ DELETE FROM customers */ SELECT 1` | `delete`, tables `[customers]` | `select`, no relations |
| `MERGE INTO customers USING s ... WHEN MATCHED THEN DELETE` | `unknown` | `delete`, customers write + s read |
| `COPY customers FROM STDIN` | `other` | `insert`, customers write |
| `COPY (DELETE FROM customers RETURNING *) TO STDOUT` | `other` | `delete` |
| `EXPLAIN ANALYZE DELETE FROM customers` | `other` | `delete` |
| `EXPLAIN DELETE FROM customers` | `other` | `select`, customers READ, because it plans and does not execute |
| `INSERT INTO staging SELECT * FROM customers` | `insert`, tables `[staging customers]` | staging write, customers read |
| `DO $$ BEGIN DELETE FROM customers; END $$` | `unknown` by accident | `unknown`, reason "anonymous code block; body is interpreted at runtime" |
| `CALL purge()` | `call` | `unknown`, reason "stored procedure; body is in the catalog" |

### The ceiling

Three shapes are out of reach for any amount of parsing, this scanner's or
PostgreSQL's own:

- **`DO $$ ... $$`**, whose body is a string interpreted at runtime.
- **`CALL proc()` and `EXECUTE p`**, whose body lives in the catalog or whose
  text was supplied somewhere else entirely.
- **A function call in a SELECT list.** `SELECT purge()` is the same problem
  wearing a read's clothes.

The first two set `Complete=false`, surface as `Operation == unknown`, and
carry the reason in `metadata["sql.incomplete"]`. The third deliberately does
not. Marking every `SELECT count(*)` incomplete would raise the flag on most
traffic, and a flag that fires everywhere is one operators learn to ignore,
which costs more than the blind spot itself. It stays a known, permanent
blind spot, and the control for it is the database's own grant on the
function.

`CREATE FUNCTION` is a complete `create` rather than an unknown: defining a
function performs exactly one effect and the body is data at that moment. The
unanalyzable event is the INVOCATION, already covered above.

Because the first two are permanent, so is the posture. Fail closed on
`unknown`:

```yaml
rules:
  - name: unreadable-statement
    type: operation
    operations: [unknown, other]
    message: this statement cannot be classified, so it is refused
```

MSSQL runs the scanner permanently. No credible Go T-SQL parser exists, so
there is no later version of this where the T-SQL path swaps to a grammar.

### Checked against a real parser

`lexer/conformance/` is a nested module holding a test-only oracle. It runs
PostgreSQL's own parser and the scanner over the same statements and fails
when they disagree, because a hand-written scanner nobody checks against a
real grammar is a pile of assertions about SQL, and SQL does not care what we
assert. The parser is a wasm build, so the suite runs under `CGO_ENABLED=0`
rather than only where a C toolchain is configured.

It is never a runtime dependency. It has its own `go.mod`, nothing the root
ships imports it, and `go test ./...` at the root does not reach it. That is
what keeps its dependencies out of the root, test dependencies included:

```bash
(cd lexer/conformance && go test ./...)
```

## HTTP

Two entry points, because HTTP arrives in two shapes:

```go
// Inside an HTTP pipeline (libhoop's ReverseProxy, an ext_proc server):
// no re-parsing, no second copy of the body.
insp := http.New(http.Options{CaptureBody: true})
stmt := insp.InspectRequest(r, bufferedBody)   // in inspectHandler
stmt := insp.InspectResponse(resp, req, body)  // in modifyResponse

// Holding a socket instead:
i, _ := inspect.New(inspect.HTTP)
stmts, _ := i.Inspect(inspect.FromClient, packetBytes)
```

**Normalized resources.** `/users/12345/orders/98765` becomes
`/users/*/orders/*`, so one rule replaces a regex per endpoint. A short slug
like `/users/alice` survives intact: merging it with `/users/settings` would
silently widen every rule written against either. The normalizer errs toward
keeping segments, so a policy comes out too narrow rather than too broad.

**Data exposure is opt-in.** Bodies and headers are NOT captured by default.
A policy engine's decision log is a copy of everything you send it, and
`Options.Headers` is an allowlist with no "capture all" switch.

## Policy

Two evaluators, meant to be layered via `policy.Chain{local, opa}` so a
statement the local rules already forbid costs no network round-trip.
Anything that defers changes the order: OPA moves after the producers whose
findings it reads, its call carries `phase: decide`, and `opa.gate: true`
adds a second call before them. Both phases are below.

**Local rules.** SQL: `deny_words_list`, `pattern_match` (RE2), `operation`,
`table`. HTTP: `http_resource`, `http_status`. Cross-protocol: `pii` (see
[Masking and PII](#masking-and-pii)). One ordered set can mix them, so a
deployment fronting a database and an API needs one evaluator:

```go
policy.NewRules([]policy.Rule{
    {Name: "no-drop", Type: policy.MatchOperation,
     Operations: []inspect.Operation{inspect.OpDrop}},

    policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.
        WithResources("/admin/**"),

    policy.Rule{Name: "no-5xx-leak", Type: policy.MatchHTTPStatus}.
        WithStatuses("5xx").
        WithMessage("upstream failure suppressed by policy"),
})
```

An HTTP rule never matches a SQL statement and vice versa, so a mixed set
cannot deny the wrong protocol.

**A `table` rule keys on the access.** `access: write` means "nothing writes
to customers" and stops firing on
`INSERT INTO staging SELECT * FROM customers`, which only reads it. The flat
`Tables` list could not express that difference, so the rule had to be
written as "nothing mentions customers" and operators widened it until it
protected nothing. An unset `access` matches either, which is what every rule
written before the split meant, so deployed rules are unaffected.

```yaml
rules:
  - name: no-writes-to-customers
    type: table
    tables: [customers]
    access: write               # unset would also match a plain SELECT
    require_table_match: true   # deny when the relations could not be determined
```

**OPA.** Posts to an OPA Data API endpoint. sidecar does not own policy;
it owns the *input document*:

```json
{"input": {
  "protocol": "postgres",
  "direction": "client",
  "operation": "delete",
  "statement": "DELETE FROM customers WHERE cpf = '111'",
  "tables": ["customers"],
  "effects": ["delete"],
  "relations": [{"name": "customers", "access": "write"}],
  "context": {"principal": "alice@example.com"},
  "phase": "decide",
  "findings": {
    "pii": {"rule": "no-cpf", "status": "ok",
            "values": {"entities": ["BR_CPF", "EMAIL_ADDRESS"],
                       "rules": ["no-cpf", "pii-wide"]}},
    "deny_words_list": {"rule": "no-destructive", "status": "ok",
                        "values": {"words": ["DELETE"],
                                   "rules": ["no-destructive"]}},
    "ai_analysis": {"rule": "risky-writes", "status": "ok",
                    "values": {"risk_level": "high"}}
  }
}}
```

A strict superset of what `ext_authz` and `postgres_proxy` metadata provide,
in one shape for both protocols. Accepts `{"allow": bool}`,
`{"denied": bool}`, or a bare boolean, with an optional `message` and `rule`.

`effects` and `relations` are what `operation` and `tables` could not
express, and a new rule belongs on them:

```rego
result := {"denied": true, "rule": "no-writes-to-customers"} if {
	some r in input.relations
	r.access == "write"
	r.name == "customers"
}
```

`operation` stays the worst single effect and `tables` stays the flattened
names, so a rule written before the split keeps firing.

`phase` and `findings` appear only on a lane that consults OPA after a
producer (see [Analyzing statements with a
model](#analyzing-statements-with-a-model) and [Reporting instead of
denying](#reporting-instead-of-denying)). A single-call lane sends neither, so
a policy written before any of this existed sees a byte-identical document.
`phase` is `gate` on the call before the producers and `decide` on the call
after them.

`findings` rides the decide phase only, and only where something reported. It
is a map keyed by **source**, the producer that wrote the entry: local rules
key by rule TYPE (`pii`, `deny_words_list`, `operation`, ...), the analyzer
keys as `ai_analysis`. Every entry has one shape:

- `status` is always set, and is one of `ok`, `cached`, `skipped`,
  `unavailable`, `error`. Only `ok` and `cached` mean the source answered.
- `reason` narrows a non-ok status where the producer has more to say. The
  analyzer's `unavailable` carries `budget_exhausted` or `refused`.
- `values` is the producer's own: `risk_level` under `ai_analysis`,
  `entities` under `pii`, `words` under `deny_words_list`. Read a key only
  under a source you know writes it.
- `rule` names the first configured rule that produced the entry.

**A source that ran and could not answer still appears**, carrying a status
and no values, and that is the whole reason `status` exists. An absent
`risk_level` on its own means four things at once: nothing triggered, the call
budget was spent, `send: refuse` stopped the transmission, or the provider
failed. Keying on the value alone lets
`findings.ai_analysis.values.risk_level == "low"` pass a statement nobody
classified. An absent SOURCE is a different fact: no producer of that kind is
configured on the lane at all. Write the degraded case out:

```rego
package hoop.inspect

ai := input.findings.ai_analysis

answered if ai.status in {"ok", "cached"}

breakglass if input.context.principal in data.breakglass

# An undefined findings.pii yields no bindings, so this is the empty set on a
# lane with no deferring pii rule rather than an undefined rule.
pii_hits := {e | some e in input.findings.pii.values.entities; e in {"BR_CPF", "US_SSN"}}

# A single-call lane sends no phase at all, and an undefined input.phase makes
# every rule reading it undefined, which fail_open: false reads as a denial of
# everything. Default it to the phase that decides.
phase := object.get(input, "phase", "decide")

result := decide if phase == "decide"

# One else-chain, because two complete rules producing different objects for
# one statement is an eval error rather than a precedence order.
decide := {"denied": true, "rule": "ai-unavailable", "message": msg} if {
	# Fail closed on a level nobody produced. A value-only contract cannot
	# express this case at all. Naming ai.status is the presence check
	# too: with no analyzer on the lane this branch is undefined, which is
	# the difference between "could not answer" and "never ran".
	not answered
	msg := sprintf("risk analysis is %v", [ai.status])
} else := {"denied": true, "rule": "ai-high-risk", "message": "blocked by risk analysis"} if {
	# The determination that used to be `high: block` in sidecar's config.
	ai.values.risk_level == "high"
	not breakglass
} else := {"denied": true, "rule": "pii-in-query", "message": msg} if {
	# A pii rule carrying `action: defer` reports entity classes instead of
	# denying, so one break-glass list covers both producers.
	count(pii_hits) > 0
	not breakglass
	msg := sprintf("%v may not appear in a query here", [concat(", ", sort(pii_hits))])
} else := {"allow": true}

# Gate phase: spend a model call only on the tables worth one.
result := {"allow": true, "request": {"ai_analysis": true}} if {
	phase == "gate"
	input.tables[_] in {"customers", "payments"}
}
```

`input.context` is whatever the caller attached. The relay fills it from the
session: `principal`, `session_id`, `connection`, and `subject`, `email`,
`groups`, `peer_addr`, `upstream`, `correlation_id` where the identity carries
them. A library caller setting `OPAClient.Context` chooses its own keys.

**Statement content never travels in a finding.** The analyzer's title and
explanation are the model's own words about a statement it was shown, `pii`
reports entity classes and not the values behind them, and a `pattern_match`
rule reports that it matched and never the matched text. OPA's decision log is
a copy of everything you send it, so only the closed vocabulary above goes.

**The gate answers `request`** beside its allow/deny: a map from source to
bool. `true` runs a producer its own configuration would have skipped,
overriding an `ai_analysis` rule's `trigger`; `false` vetoes one that
configuration would have run; an absent key means no opinion, leaving that
source in charge of itself. It is read on the gate phase only, so a policy
that returns it on the decide phase is ignored rather than half-honored.

**Both fail closed.** An unreachable OPA, a 500, or an undefined decision
denies. Set `FailOpen` to invert that where availability outranks enforcement.
The gate phase is the exception: an undefined gate decision allows and
requests nothing even under `fail_open: false`. A gate is an optimization over
a policy someone already wrote, so making its absence deny would mean turning
the gate on silently blocks every statement until its author writes a second
rule they never asked for. The decide phase keeps the normal reading.

### Reporting instead of denying

`action: defer` on a local rule splits matching from determining. The rule
still matches; instead of denying it records what it saw as a finding and
evaluation continues, so the decision belongs to whoever reads
`input.findings`, normally the OPA call on the decide phase.

```yaml
rules:
  - name: no-cpf
    type: pii
    action: defer         # report BR_CPF, let Rego decide who may send one
    entities: [BR_CPF]
  - name: no-drop
    type: operation       # no action: still denies, and first match wins
    operations: [drop]
```

First-match-wins applies to DENIALS only. A deferring rule never ends
evaluation, so one statement can report several findings and still be denied
by a hard rule further down. `defer` is the only value `action` takes, and
anything else is refused at startup rather than read as "deny": a rule whose
action was mistyped would otherwise enforce the opposite of what it says. So
is `defer` on a lane with no `policy.opa.url`, which defers to a decision that
does not exist and therefore allows everything.

**Findings key by rule TYPE, not by rule name.** A policy asks what the PII
scanner found, not what the rule named `no-cpf` found, so every deferring
`pii` rule folds into `findings.pii`. `values.rules` is the union of the names
that matched and list values union too, so two `pii` rules matching different
entity classes report the union of both. Overwriting would let a second rule
matching one class hide the first rule's three. `rule` names the first that
matched.

**Only `pii` tells a policy something new.** `operation`, `table`,
`http_resource` and `http_status` match on fields Rego already reads off
`input.operation`, `input.relations` and `input.http`, so their finding carries
`values.rules` and nothing else: the match itself is the whole message.
`deny_words_list` adds `values.words`, because which of several configured
words fired is not recoverable from `input.statement` without reimplementing
the matcher. `pii` adds `values.entities`, and it is the only type
contributing a fact the input document does not carry at all: running a
detector over the statement is not something Rego does.

**A matched pattern's text is never reported.** `pattern_match` says that it
matched and stops there. The text is content lifted out of the statement, and
OPA's decision log is a copy of everything sent to it, so a rule written to
catch a leaked key would file that key in the policy engine's log.

## Masking and PII

Masking covers the response side, where Envoy has no equivalent for any
protocol: Envoy consults `ext_authz` before calling the upstream, so it never
sees the row that comes back.

Requests are never rewritten. Changing the statement the upstream executes is a
correctness change wearing a privacy label, so a value the client put in a
`WHERE` clause is a matter for a `pii` policy rule instead.

Two mechanisms carry it, and the gate picks per protocol by asking the codec
rather than by consulting a list of protocol names:

- **Substitution**, where the payload length is declared in a header the gate
  can find and correct. HTTP, whose `Content-Length` is retagged after the
  rewrite. Leave it stale and the client reads the old count and stops
  mid-document, which reads as a corrupt upstream rather than a masking bug.
- **Re-framing**, where every row and column carries its own length prefix.
  Postgres, whose codec rebuilds each changed `DataRow` around the new values.
  Substituting bytes there desynchronizes the client, and `psql` reports "lost
  synchronization with server".

A codec offering neither gets its `mask` section refused at startup, because
accepting a masking config that can never fire is the failure that ends with an
unmasked SSN in a screenshot.

Detection and rewriting both come from
[alcatraz](https://github.com/hoophq/alcatraz): 45 entity types across 12
countries, 25 of them checksum-verified with Luhn on cards, ISO 7064 mod-97 on
IBAN, Verhoeff on Aadhaar, mod-11 on the Brazilian schemes. It lives in the
nested `pii/alcatraz` module, so the root library never links it.

```go
det, _ := alcatraz.NewDetector(alcatraz.Options{
    Entities: []string{entities.USSSN, entities.CreditCard, entities.BRCPF},
})
m, _ := alcatraz.NewMasker(det, []alcatraz.Rule{
    {Entity: entities.USSSN,      Strategy: alcatraz.StrategyRedact},
    {Entity: entities.CreditCard, Strategy: alcatraz.StrategyPartial, KeepLast: 4},
})
out, res := m.Mask(responseBytes)   // res names WHAT was masked, never values

p, _ := policy.NewRulesWithScanner(rules, det)   // the same det, request side
```

Four strategies: `redact`, `mask`, `partial`, `hash`.

**Credentials too.** Alcatraz is a PII engine and carries no recognizer for a
secret, so `pii/alcatraz/secrets.go` registers three into the same engine:
`AWS_ACCESS_KEY`, `JWT` (decodes the header rather than matching its shape)
and `PRIVATE_KEY` (including a PEM block a size limit cut short). You name
them in config like any other entity.

### The guardrail half

One `Detector` drives both paths. The `pii` policy rule answers a different
question than masking: a national ID in a `WHERE` clause lands in the
database's own query log, slow-query log and `EXPLAIN` output, and response
masking never undoes that.

```json
{"name": "no-cpf-in-query", "type": "pii", "entities": ["BR_CPF"],
 "message": "do not put a taxpayer id in a query"}
```

### Name the entity types you want

There is no all-entities default, on purpose. Enabling all 45 recognizers
rewrites ordinary numeric columns:

```
{"order_id":457555462,"customer_id":123456781}
  -> both masked as US_SSN
```

Nine digits in a legal range *is* a valid SSN as far as any detector can tell:
SSNs carry no checksum, so nothing rejects them. Measured over random
nine-digit business ids, `US_SSN` fires on about a third. `alcatraz.Noisy`
records the offenders with their rates, and `AllEntities()` is there once you
have read it.

The same validation cuts the other way, which will bite you in a demo:
alcatraz **declines** `123-45-6789` and `987-65-4321`, rejecting sequential
and descending runs as test fixtures. If you must mask placeholder SSNs, add
a rule for that shape rather than widening the detector.

A `pii` policy rule records the offending statement in the audit trail, raw
literal included. Set `audit.redact_statements` where that is the wrong trade:
the denial keeps working, and the record keeps a stable fingerprint instead of
the text.

**Masking requires the plugin.** The gate refuses `mask.enabled` with no
detection wired in at startup rather than passing traffic through unmasked.

## Transport

Each lane binds a TCP port or a unix socket. One field decides it, per
listener, and **TCP is the default**: omit `network` and you get a port.

```yaml
listeners:
  - name: appdb-tcp
    listen: 0.0.0.0:15432          # network omitted -> tcp

  - name: appdb-uds
    network: unix                   # the only line that changes it
    listen: /run/hoop-inspect/pg.sock
```

`network` accepts `tcp` or `unix`; anything else is refused at startup, naming
the lane. Lanes in one process can differ, so a deployment can move one lane to
a socket without touching the other.

Nothing above the transport changes. Policy, masking, audit and `upstream_tls`
behave identically, because the gate reads a `net.Conn` and never asks what
kind it is.

### Why pick a socket

A TCP listener on 15432 is reachable by anything that can route to the host. A
NetworkPolicy narrows that; it does not remove it. A unix socket opens no port
at all, so reachability becomes a filesystem question, which is the point of a
sidecar that shares a namespace with exactly one workload.

The cost is coordination: both processes need the same directory, and their
uids have to agree. That is cheap in a pod spec and awkward on a laptop, which
is why the compose stack defaults to TCP and keeps the socket variant in an
overlay.

### Confirming which one is running

Three places say it, and they agree because they read the same resolved config.

**The startup log**, one line per lane:

```json
{"msg":"hoop-inspect listening","listener":"appdb","network":"unix",
 "listen":"/run/hoop-inspect/pg.sock","protocol":"postgres"}
```

**`GET /stats`**, whose `addr` is the address the listener bound:

```bash
curl -s localhost:19000/stats | python3 -m json.tool
```

```json
{"listeners": [
  {"name": "appdb",   "addr": "/run/hoop-inspect/pg.sock",   "active": 0, "total": 9},
  {"name": "httpbin", "addr": "/run/hoop-inspect/http.sock", "active": 0, "total": 7}
]}
```

A path means unix. A `host:port` means TCP. The address is post-bind, so it
reflects what the listener got rather than what the config asked for.

**The filesystem**, for a socket lane:

```bash
ls -l /run/hoop-inspect/
# srwxrwxr-x 1 10001 envoy 0 pg.sock      the leading s is a socket
```

And the negative check, which is the one worth running, because it proves the
port is gone rather than merely unused. Ask the relay's own namespace what it
bound:

```bash
netstat -ltn        # or: ss -ltn
```

On a socket-only deployment the admin port is the only line left; the data
lanes are absent entirely. From a peer, `nc -z -w2 <host> 15432` says the same
thing from the outside.

Do not reach for `(echo > /dev/tcp/host/port)`: that is a bash builtin, and
under `sh` (BusyBox, dash) it fails with no such device and reports every port
as closed, including open ones. It looks like a passing check and proves
nothing.

### Two permission traps

Both cost real time, and neither produces a useful error on its own.

**Creating the socket.** The relay needs write permission on the directory. A
volume that mounts root-owned against a non-root image gives:

```
listen unix /run/hoop-inspect/pg.sock: bind: permission denied
```

**Connecting to it.** `connect()` on a unix socket requires **write**
permission on the socket file, not read. Go creates a listening socket at
`0777 &^ umask`, and the usual 022 clears exactly the group-write bit a peer
needs. The peer then fails with nothing useful in either log: under Envoy it
surfaces only as `flags=UF` and an `upstream_cx_connect_fail` counter, while
the cluster still reports healthy because the endpoint resolved.

Run the relay with the peer's gid and `umask 0002` so its sockets come out
group-writable. `deploy/docker-compose/envoy-stack/uds/` does exactly this and
is worth reading before you write your own.

### Stale sockets after an unclean exit

Go unlinks the socket when the listener closes, so an orderly shutdown leaves
nothing behind. A SIGKILL, an OOM kill or `docker kill` skips that and the file
outlives the process.

The relay reclaims it: at startup it dials the path, and a socket nothing
answers on gets unlinked with a warning. One that DOES answer is left alone and
the bind fails, naming the conflict, because two relays sharing a socket would
split a client's connections between them at random.

## Downstream TLS, and the GSS refusal

The relay terminates no client TLS by default: whatever fronts it owns that
leg. Postgres is the exception, because pgwire leaves no one else able to.

### The pgwire problem

pgwire negotiates TLS **in-band**. The client sends an 8-byte `SSLRequest`,
waits for a one-byte `S`/`N`, then handshakes. A plain TLS listener in front
sees a sentinel where it expects a ClientHello, and fails. Envoy's
`postgres_proxy` filter handles it, at a price: the filter ships contrib-only,
Envoy marks it work-in-progress and documents it as "not hardened", and it
gives up for the rest of the connection once a client asks for GSS encryption.

Compare MSSQL. TDS 8.0 is TLS-on-connect, so an ordinary
`DownstreamTlsContext` terminates it with no protocol awareness, and that lane
needs none of this.

```yaml
listeners:
  - name: appdb
    protocol: postgres            # the only protocol that accepts this
    downstream_tls:
      cert_file: /etc/hoop-inspect/certs/relay.crt
      key_file:  /etc/hoop-inspect/certs/relay.key
```

The sidecar refuses this on any other protocol at startup, and loads the
keypair there too. Finding a bad path on the first client connection would cost
one failed login per restart and leave the startup log silent.

### GSS encryption draws a refusal

Each postgres lane refuses it, configured or not. That refusal separates a lane
that enforces something from one that appears to.

`GSSENCRequest` asks to wrap the session in GSSAPI. Accept it and each later
byte becomes ciphertext: no statements, no masking, no audit trail, and **no
error** saying inspection stopped. libpq defaults `gssencmode=prefer`, so a
developer holding a Kerberos ticket asks for this before anything else, ahead
of TLS.

The relay answers `N`, and the client loses no capability. It falls back and
**keeps its Kerberos authentication**, carried as ordinary tagged messages that
the codec forwards untouched. Kerberos works; the wrapper alone gets declined.

Use `N`, not `E`. Both refuse, and pgjdbc closes and REOPENS the TCP connection
on `E`, which doubles each login in the audit trail.

The codec carries the same refusal as a backstop. Should a GSS request reach it
anyway, because something else fronts the relay and let it through, the codec
returns `ErrStreamUnsafe` and the lane fails closed.

| Client | Behaviour |
|---|---|
| psql / libpq | defaults to `prefer`, so it asks; gets `N`, falls back, Kerberos intact |
| DBeaver / pgjdbc | defaults to `allow`, which skips the request |
| either, `gssencmode=require` | fails with a clear error instead of bypassing inspection |

## Upstream TLS

The hop from the relay to the backend can be encrypted, and it does not cost
you inspection.

```yaml
listeners:
  - name: appdb
    protocol: postgres
    listen: 0.0.0.0:15432
    upstream: appdb:5432
    upstream_tls:
      ca_file: /etc/hoop-inspect/certs/appdb.crt   # omit to use the host trust store
      server_name: appdb                           # defaults to the upstream host
      # cert_file / key_file    for mTLS
      # insecure_skip_verify    logs a warning; do not ship it
```

**Masking and policy are unaffected.** The relay is the TLS *client* on that
hop, so it decrypts on read and the gate inspects plaintext exactly as it does
without TLS. Encryption protects the bytes crossing the network, not the bytes
the relay was built to read. A relay that could not read them would have
nothing to mask.

Do not confuse this with the client's leg. Nothing terminates downstream TLS
here: if the CLIENT negotiates TLS end-to-end, there is no plaintext at this
point in the path and inspection is impossible. That is the limit below, and
it is a different hop.

**Postgres negotiates in-band.** A TLS-on-connect dial fails against it: the
server expects an 8-byte `SSLRequest` and a one-byte `S`/`N` reply before any
handshake, and sending a ClientHello instead gets you `received direct SSL
connection request` in the server log and a closed connection. The relay
speaks that exchange, so `upstream_tls` on a `postgres` lane works the way the
field name implies.

**A refusal is an error, never a downgrade.** If the server answers `N`, the
connection fails with a message naming the likely cause. An operator who
configured `upstream_tls` asked for an encrypted hop; sending credentials in
the clear because the server declined is the outcome they were preventing.

**Channel binding is dropped from the server's offer.** With TLS terminating
at the relay, `SCRAM-SHA-256-PLUS` cannot work: the server binds to its
session with the relay, and the client has a different connection. Worse,
libpq refuses a `-PLUS` mechanism offered over a link it knows is unencrypted,
so relaying the offer fails the connection outright. The relay removes that
one mechanism, leaving plain `SCRAM-SHA-256`, which authenticates the same
password against the same verifier. If you need channel binding end to end,
you need a path with no inspection in it.

## Limits

Read these before writing a policy against it.

- **An empty relation list means "could not tell".** `Relations` and `Tables`
  come from a scanner, and a statement it could not follow reports nothing
  rather than everything. Empty is **never** "touches nothing". Use
  `require_table_match: true` on rules protecting something critical, and
  accept the false positives.
- **`unknown` is an answer, and it has to deny.** `DO`, `CALL` and `EXECUTE`
  decide their effect at runtime, so `Operation` is `unknown` with the reason
  in `metadata["sql.incomplete"]`. A function call in a SELECT list is the
  same blind spot and deliberately does not raise the flag. See
  [The ceiling](#the-ceiling).
- **A response batch can be truncated.** Both shipped codecs inspect and mask
  in each direction, but the Postgres codec stops decoding columns past 1000
  rows in one result set, to keep the relay's memory out of the query's hands.
  It keeps counting and marks the batch `Truncated`. A policy MUST read that as
  inconclusive, never as proof a value is absent.
- **A response statement carries no verb.** For the database codecs a
  `FromServer` statement reports `OpUnknown`, because the operation belongs to
  the request the audit trail already recorded. Key a response-side SQL rule on
  `Result`, not on `Operation`.
- **PII detection is neither sound nor complete.** A checksum-verified
  identifier is solid; everything else is a pattern. Detecting a name column
  takes NER, which this module does not wire, and a caller can split a value
  across two responses. Masking raises the cost of accidental exposure. It
  does not replace withholding access to the table.
- **HTTP/1.x only for stream decoding.** HTTP/2 and HTTP/3 framing belongs to
  whatever terminated the connection; by then it has a `*http.Request`, so
  use `InspectRequest`.
- **Path normalization is conservative.** Numeric, UUID, hex and long opaque
  segments collapse; short slugs do not. A policy comes out too narrow rather
  than too broad.
- **Plaintext DOWNSTREAM only.** If the client negotiates TLS end-to-end past
  the relay, there is nothing to parse; terminating that leg is your problem,
  and Envoy is the usual answer. The UPSTREAM leg may be TLS: the relay
  originates it and still inspects. See [Upstream TLS](#upstream-tls).
- **Statements are not transactions.** The gate evaluates each one
  independently, with no cross-statement session state.

## Testing

```bash
go test ./...           # unit
go test -race ./...     # concurrency
```

That covers the root module only (`inspect/`, `lexer/`, `codec/`, `policy/`,
`gate/`, `proxy/`, `daemon/` and the rest). Each nested module has its own
`go.mod`, so `./...` does not reach it:

```bash
(cd pii/alcatraz && go test ./...)
(cd store/sqlite && go test ./...)
(cd lexer/conformance && go test ./...)   # differential, against PostgreSQL's parser
```

Each codec runs a split-read matrix: the tests feed the same message in two
chunks at every possible boundary, then assert that a fragment emits no
statement and that reassembly loses none.
