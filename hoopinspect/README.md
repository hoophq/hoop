# hoopinspect

Turn raw database wire-protocol bytes into structured statements, and
structured statements into allow/deny verdicts.

**The library is a pure function over bytes.** It opens no socket, terminates
no TLS, routes nothing. You hand it bytes you already have, and whatever holds
the connection keeps holding it.

**The relay owns a socket.** The nested `cmd` module builds `hoop-inspect`,
which wraps the library in a TCP listener that accepts a connection, dials one
upstream, and pumps bytes through the gate in both directions. It runs behind
something that already owns TLS and identity, typically Envoy forwarding
plaintext over loopback or a unix socket.

**Zero dependencies.** Standard library only, tests included, no `go.sum`. You
vendor nothing and get no version skew with whatever links it.

```go
insp, _ := hoopinspect.New(hoopinspect.Postgres)
rules, _ := policy.NewRules([]policy.Rule{{
    Name:       "no-destructive",
    Type:       policy.MatchOperation,
    Operations: []hoopinspect.Operation{hoopinspect.OpDelete, hoopinspect.OpDrop},
    Message:    "destructive statements are not permitted on appdb",
}})

stmts, _ := insp.Inspect(hoopinspect.FromClient, packetBytes)
for _, s := range stmts {
    if v := rules.Evaluate(s); v.Denied {
        return errors.New(v.Message) // surface it in the protocol's error frame
    }
}
```

## Run it locally: the POC stack

The fastest way to watch all of this work is the compose stack in
[`deploy/docker-compose/envoy-poc`](../deploy/docker-compose/envoy-poc). Envoy
terminates TLS and calls OPA for reachability, `hoop-inspect` sits behind it as
an ordinary upstream, and a Postgres database and an HTTP service sit behind
that. No hoop gateway, no agent, no control-plane database: the sidecar reads
one YAML file.

Needs `docker`, `curl`, `openssl` and `python3`.

**1. Bring it up.** The first run takes about a minute. It mints a self-signed
cert for Envoy, builds `hoop-inspect:local` from the `hoopinspect` tree, and
starts six containers. From the repo root:

```bash
cd deploy/docker-compose/envoy-poc
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
docker compose logs hoop-inspect | ./hoopinspect/read-audit.py
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
[docs/adr/hoopinspect-flow.md](../docs/adr/hoopinspect-flow.md).

## Running the relay yourself

Two ways, same relay.

**Through the hoop CLI**, which links the same plugins into the binary the
release pipeline already builds. Nothing extra to compile or ship:

```bash
hoop start inspect --config config.yaml --validate   # check the config and exit
hoop start inspect --config config.yaml              # run
```

`--config` also reads `HOOP_INSPECT_CONFIG`, which is the shape a Kubernetes
deployment wants: mount the ConfigMap, set the variable, pass no arguments.

**As a standalone binary**, when a sidecar container should carry the relay and
nothing else. It lives in the nested `cmd` module, which is where the optional
plugins get linked (the YAML front end and alcatraz PII detection) so the root
module stays dependency-free:

```bash
cd cmd
go build -o hoop-inspect .

./hoop-inspect -validate -config config.yaml
./hoop-inspect -config config.yaml
./hoop-inspect -version
```

As a container, with the `hoopinspect` tree as the build context. From the repo
root:

```bash
docker build -f deploy/docker-compose/envoy-poc/hoopinspect/Dockerfile \
  -t hoop-inspect:local hoopinspect/
```

There are no build tags. Every capability is decided by the config file, so an
operator turning on PII detection does not also have to swap the binary. Omit
the `pii` section and detection is off, which makes masking unavailable and a
`pii` policy rule a config error: both are refused at startup rather than
silently skipped.

To embed the relay in your own process, call `sidecar.Setup` to load the config
and build the detector, then `sidecar.Run`. That is all `hoop start inspect`
does; read `client/cmd/startinspect.go` for the whole of it.

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
instead of a port, `upstream_tls` encrypts the connection to the backend (see
[Upstream TLS](#upstream-tls)), `idle_timeout_sec` closes an idle connection
(leave it unset for interactive sessions, since psql idles between
keystrokes), and `max_conns` bounds concurrency.

### 2. Know how a listener inherits

| Field | Merge | Why |
|---|---|---|
| `policy.rules` | concatenate, listener first | Every rule denies and the first match wins, so concatenating cannot change the allow/deny outcome. Order only picks which message the user reads. |
| `policy.opa` | replace | One lane has one decision endpoint. |
| `policy.enforce` | replace | A lane rolling out behind an enforcing default has to be able to say observe-only. |
| `mask` | replace | A rule owns an entity type, and two concatenated lists leave two rules competing for one entity. |

### 3. Validate before you deploy

Nothing needs to be running. Here it is against the POC's own config, from the
`hoopinspect` directory:

```bash
cd cmd && go build -o hoop-inspect . && \
  ./hoop-inspect -validate -config ../../deploy/docker-compose/envoy-poc/hoopinspect/config.yaml
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
reports every problem in one run rather than one per restart. Four things it
refuses outright:

- `mask.enabled` on a protocol whose codec can carry neither masking
  mechanism, naming the lane.
- A `pii` rule naming an entity absent from `pii.entities`, naming the entity.
  Without this check the rule loads, evaluates, and matches nothing, so a
  guardrail looks live while allowing everything it was written to stop.
- A key typo, in YAML or JSON.
- A bad regex in any lane's rules, naming the lane.

### 4. Ask the running process what it resolved

Reading the file cannot tell you which rules a lane ended up with, because
inheritance happens at startup. Debugging a denial that never fired starts
here, again against the POC config:

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

## Overlap with Envoy

Envoy already parses some of this.

`envoy.filters.network.postgres_proxy` parses SQL from Postgres `Query` and
`Parse` messages, emits `statements_select/insert/update/delete` counters, and
produces dynamic metadata as `table.db → [operations]` that the RBAC network
filter can act on. Within Postgres, at table-and-verb granularity, that is a
real capability.

The boundary:

| | Envoy | hoopinspect |
|---|---|---|
| Postgres SQL parse | `postgres_proxy`, best effort | full statement text |
| Postgres granularity | `table.db` + operation verb | statement, operation, tables |
| Postgres **response** | ✗ | result columns, row count, and masking by re-framing |
| HTTP request | `ext_authz`: method, path, headers, bounded body | same, plus normalized resource |
| HTTP **response** | ✗, ext_authz decides before the upstream is called | status, headers, body |
| Deny UX | RBAC/ext_authz drops or returns a bare 403 | operator-authored message |

On HTTP, Envoy's `ext_authz` covers request-side authorization well, and the
gaps named above are the narrow ones. Envoy is not blind here.

## Protocols

| Protocol | Request messages | Response messages | Stateful |
|---|---|---|---|
| `postgres` | `Query` ('Q'), `Parse` ('P'); handshake skipped | `RowDescription` ('T'), `DataRow` ('D'), and the three terminators that end a result set | yes |
| `http` | HTTP/1.x requests | HTTP/1.x responses | no |

The Postgres codec is stateful because one `RowDescription` describes every
`DataRow` after it, and those land in different TCP reads. That is why the
registry hands out a factory rather than an instance: two connections sharing
one codec would corrupt each other's reassembly, and one tenant's SQL would
surface in another tenant's audit trail. Give every connection its own.

MySQL, MSSQL and MongoDB codecs are **not shipped**. The `Codec` interface
and the shared SQL classifier are protocol-agnostic, so adding one means a new
`codec/<name>` package and nothing else. We removed the earlier
implementations to keep the surface to what is exercised end to end.

Import only what you need. A listener that speaks Postgres imports
`codec/postgres` and never links the HTTP machinery:

```go
import _ "github.com/hoophq/hoopinspect/codec/postgres" // postgres only
import _ "github.com/hoophq/hoopinspect/codec/all"      // postgres + http
```

## The Statement

```go
type Statement struct {
    Protocol  Protocol          // postgres | http
    Direction Direction         // client | server
    Text      string            // verbatim SQL, or the request line for HTTP
    Operation Operation         // select | delete | get | post | ...
    Tables    []string          // SQL relations, or the HTTP resource
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

For SQL, `Operation` comes from a lexer that strips comments and string
literals first, so `SELECT 'DROP TABLE customers'` classifies as `select`.
Prefer `MatchOperation` to `MatchDenyWords` for verbs.

## HTTP

Two entry points, because HTTP arrives in two shapes:

```go
// Inside an HTTP pipeline (libhoop's ReverseProxy, an ext_proc server):
// no re-parsing, no second copy of the body.
insp := http.New(http.Options{CaptureBody: true})
stmt := insp.InspectRequest(r, bufferedBody)   // in inspectHandler
stmt := insp.InspectResponse(resp, req, body)  // in modifyResponse

// Holding a socket instead:
i, _ := hoopinspect.New(hoopinspect.HTTP)
stmts, _ := i.Inspect(hoopinspect.FromClient, packetBytes)
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

**Local rules.** SQL: `deny_words_list`, `pattern_match` (RE2), `operation`,
`table`. HTTP: `http_resource`, `http_status`. Cross-protocol: `pii` (see
[Masking and PII](#masking-and-pii)). One ordered set can mix them, so a
deployment fronting a database and an API needs one evaluator:

```go
policy.NewRules([]policy.Rule{
    {Name: "no-drop", Type: policy.MatchOperation,
     Operations: []hoopinspect.Operation{hoopinspect.OpDrop}},

    policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.
        WithResources("/admin/**"),

    policy.Rule{Name: "no-5xx-leak", Type: policy.MatchHTTPStatus}.
        WithStatuses("5xx").
        WithMessage("upstream failure suppressed by policy"),
})
```

An HTTP rule never matches a SQL statement and vice versa, so a mixed set
cannot deny the wrong protocol.

**OPA.** Posts to an OPA Data API endpoint. hoopinspect does not own policy;
it owns the *input document*:

```json
{"input": {
  "protocol": "http",
  "direction": "client",
  "operation": "delete",
  "tables": ["/users/*"],
  "http": {
    "method": "DELETE",
    "path": "/users/42",
    "resource": "/users/*"
  },
  "context": {"user": "alice"}
}}
```

A strict superset of what `ext_authz` and `postgres_proxy` metadata provide,
in one shape for both protocols. Accepts `{"allow": bool}`,
`{"denied": bool}`, or a bare boolean, with an optional `message` and `rule`.

**Both fail closed.** An unreachable OPA, a 500, or an undefined decision
denies. Set `FailOpen` to invert that where availability outranks enforcement.

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
nested `pii/alcatraz` module, so the root library stays dependency-free.

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

- **`Tables` is best effort.** A lexer produces it, so it hits the same
  ceiling Envoy's own docs acknowledge for `postgres_proxy`. Empty means
  "could not determine", **never** "touches nothing". Use
  `RequireTableMatch: true` on rules protecting something critical, and accept
  the false positives.
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

That covers the root module only. Each nested module has its own `go.mod`, so
`./...` does not reach it:

```bash
(cd pii/alcatraz && go test ./...)
(cd store/sqlite && go test ./...)
```

Each codec runs a split-read matrix: the tests feed the same message in two
chunks at every possible boundary, then assert that a fragment emits no
statement and that reassembly loses none.
