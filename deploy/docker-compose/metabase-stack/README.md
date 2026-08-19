# Metabase + hoop-inspect

> hoop-inspect 0.1.0

A control on the **warehouse**, demonstrated through Metabase. Masking, policy
and a per-statement audit trail live on the wire, so they apply to every client
that speaks the protocol. Metabase here, and equally dbt, DBeaver, a notebook
or someone's `psql`. No Metabase plugin, no driver, no patched image, no agent
in the container: the integration is a **hostname and a port** in Metabase's
connection form.

```
metabase :3000 ──pgwire──> hoop-inspect :15432 ──pgwire+TLS──> appdb :5432
     │                     policy · masking · audit
     └── H2 app database, inside the container, NOT proxied

client ─────────────────────────────────────────────────────> appdb :5432
     direct, bypassing the proxy: the unmasked ground truth
```

There is no Envoy and no OPA here, unlike [`../envoy-stack`](../envoy-stack).
The sidecar is the whole story: the [`hoopinspect`](../../../hoopinspect)
library running as a process, parsing pgwire, enforcing policy per statement,
writing an audit trail and rewriting result rows on the way back.

## Why not just use Metabase Pro?

Often you should. Metabase Pro has row and column security, connection
impersonation and usage analytics, and **within Metabase** some of it is
structurally stronger than what a proxy can do. This is a different control at
a different layer, not a better version of those features.

**Metabase's controls stop at Metabase's edge.** That is the whole argument.
They are per-user, semantic, and scoped to one tool. A proxy on the wire is
per-connection, syntactic, and scoped to the warehouse. If the requirement is
*"control what Metabase users see,"* buy Pro. If the requirement is *"this PII
does not leave this warehouse,"* Pro does not address it at all. The same
database is reached by dbt, DBeaver, notebooks, Retool and `psql`, and no
Metabase licence covers any of them.

Two narrower arguments sit on top of that:

- **Pro makes you choose between native SQL and column security.** This is
  Metabase's own documentation on [row and column
  security](https://www.metabase.com/docs/latest/permissions/row-and-column-security),
  renamed from "data sandboxing" in Metabase 56, not an inference from
  behaviour:

  > "Row and column security permissions don't apply to the results of SQL
  > questions."
  >
  > "Since Metabase can't parse SQL queries, the results of SQL questions will
  > always use original tables."
  >
  > "You can't set up native query permissions for groups with row and column
  > security."

  So analysts get the SQL editor or they get masking, never both. Impersonation
  is Pro's answer for the native editor, and it delegates to warehouse
  `GRANT`s: a role can revoke a column, it cannot redact one, and it needs a
  database role per human. Sandboxing native queries is an open request
  ([metabase#23858](https://github.com/metabase/metabase/issues/23858)) and is
  unresolved. The sidecar covers the query builder, the native editor and the
  CSV download with one rule, for exactly the reason Metabase gives for not
  covering them: it never parses the question, it reads the response.
- **On OSS there is no masking at any configuration.** The same page: *"Row and
  column security is only available on Pro and Enterprise plans."* Every
  redacted cell in this stack came from the sidecar. This is the weakest of the
  three arguments and it evaporates the day the customer buys Pro; lead with
  scope instead.

### Where Metabase is better

| | Metabase Pro | this sidecar |
|---|---|---|
| Per-user policy | knows the human | sees one shared DB credential |
| Row-level security | filter by user attribute | no equivalent |
| Alias resistance | rewrites the query, knows the column | keys on the name on the wire, see [Known gaps](#known-gaps) |
| Operational cost | none, it is already running | one more hop and failure domain |

Read-only is missing from that table because the sidecar barely wins it: if
you can create a read-only database user, do that instead. A `GRANT` is
simpler and more robust than a policy rule. The lane adds an operator-authored
message in place of `permission denied`, the attempt recorded in the same
trail as everything else, and enforcement when a DBA team owns the warehouse
and will not provision a per-tool user.

## Run it

```bash
./run.sh              # cert → build sidecar → up → provision Metabase
./demo.sh             # five beats, every query issued by Metabase itself
./run.sh --rebuild    # rebuild the sidecar image, then up
./run.sh down         # tear down, including volumes and the minted cert
```

Needs `docker`, `curl`, `openssl`, `python3`. No local Go: the sidecar image
compiles in `golang:1.26.5-alpine` from the same Dockerfile `../envoy-stack`
uses, so building `hoop-inspect:local` in either stack serves the other.

Ports: `3000` Metabase, `19000` sidecar admin, `15432` the postgres lane.
First boot takes a minute or so, because Metabase migrates an empty H2
application database before `/api/health` answers, and `docker compose up
--wait` sits there for it.

```
Metabase   http://localhost:3000   demo@hoop.dev / hoopdemo1
```

`./run.sh` provisions that login and registers the database by driving
`POST /api/setup`. The declarative config file would be tidier and is
Pro/Enterprise-only; writing rows into Metabase's application database
directly would be a hack that breaks on the next schema migration. The setup
API is the supported path on OSS, which is why `metabase/provision.py` exists
and why the image tag is pinned rather than `:latest`.

Sidecar knobs live in [`hoopinspect/config.yaml`](hoopinspect/config.yaml).
Validate a change without starting anything:

```bash
cd ../../../hoopinspect/cmd && go run . -validate \
  -config ../../deploy/docker-compose/metabase-stack/hoopinspect/config.yaml
```

## What `./demo.sh` shows

Every query below is issued **by Metabase**, through the same API its browser
UI calls (`metabase/query.py`). Driving the sidecar with `psql` would be
shorter and would prove the wrong thing: the claim under test is that
redaction survives Metabase's JDBC driver, its result pipeline and its
download endpoint, and only Metabase can test that.

**1. Ground truth.** `psql` straight to `appdb` with the sidecar bypassed.
Masked output means nothing next to nothing.

**2. Query builder.** A table click, no SQL typed. This is the one path Pro's
column security also covers, on Pro, and not on the container running here.

**3. Native SQL editor.** The path column security answers by being switched
off. Here the editor stays on, the analyst writes whatever they like, and the
response comes back rewritten:

```
  id  name          email                     ssn          cpf                 iban
  1   Ada Lovelace  [REDACTED:EMAIL_ADDRESS]  ***-**-6789  [REDACTED:BR_CPF]   ******************5432
```

`ssn` carries **both** a column rule and an entity rule, because the two kinds
fail in opposite directions and neither is sufficient alone.

A **column rule** keys on the output column name in `RowDescription` and does
not care what the value looks like, which is what catches the two SSNs
[alcatraz](https://github.com/hoophq/alcatraz) rejects as obvious test
fixtures (`123-45-6789`, `987-65-4321`). An **entity rule** keys on the value,
so it survives an alias, a join, or a CTE that surfaces the column under a
name no config predicted. Measured against this stack:

| Query | masked |
|---|---|
| `SELECT ssn FROM customers` | 3/3 |
| `SELECT upper(ssn) AS ssn FROM customers` | 3/3 |
| `SELECT ssn AS whatever FROM customers` | 1/3 |
| `SELECT substring(email,1,6) FROM customers` | 0/3 |

[Known gaps](#known-gaps) explains the last two rows. `cpf`, `iban` and the
emails are entity-only, because the same class of value turns up under names
nobody enumerates in advance.

**4. CSV export, 5,000 rows.** A download is where masking usually fails: the
rows are already inside the BI tool and whatever it writes to disk is out of
reach. Here they were masked before Metabase ever held them, and the demo
asserts it rather than showing a screenshot:

```
  PASS 5000 rows exported
  PASS 5000 rows redacted, masking did not stop at row 1000
  PASS 0 addresses survived the export
```

The middle assertion exists because of a constant a careful reader will find:
`maxDecodedRows = 1000` in `codec/postgres/response.go`. It bounds how many
rows are decoded into structured values **for audit**. Masking runs through
`Codec.Rewrite`, a separate path with no row limit; past a 4 MB buffer it
flushes what it holds *masked* and keeps going, so exceeding the buffer
changes the batching granularity and nothing else.

**5. Guardrail.** The lane is read-only by policy, not by `GRANT`, so the
analyst gets a sentence an operator wrote instead of a permission error:

```
  Metabase reports: FATAL: this Metabase connection is read-only; writes are not permitted
```

Classified by effect rather than leading verb. The demo also sends
`WITH gone AS (DELETE FROM customers RETURNING *) SELECT count(*) FROM gone`,
which is a delete and is refused, and then reads the row count directly from
`appdb` to show nothing moved.

The severity is `FATAL` and the SQLSTATE is `42501` because a denial closes
the connection; `ERROR` would leave the client waiting for a `ReadyForQuery`
that never comes, and a hang is a worse answer than a sentence
(`proxy/deny.go`). For a pooled client that means a refused query costs one
pooled connection, which c3p0 replaces on the next checkout.

**6. Audit.** Metabase was not configured to log anything. A first boot, a
full schema sync and a complete `./demo.sh` produce, on this lane:

```
  70 statements recorded (31 set, 26 select, 5 show, 5 begin, 2 delete, 1 rollback)
  68 allowed, 2 denied, 26 result sets
  10034 values masked across 23 result sets (EMAIL_ADDRESS, BR_CPF, IBAN_CODE, column:ssn, US_SSN)
  verified: no masked value appears in the audit trail
```

That last line is an assertion too. Recording, in the clear, a value that
masking removed would have un-masked it.

Those counts are from the **first** `./demo.sh` after a fresh `./run.sh`. The
summary reads a 180-second log window, wide enough to catch the schema sync,
so a second run inside three minutes is summarized together with the first.

```bash
curl -s localhost:19000/api/sessions | python3 -m json.tool
curl -s localhost:19000/stats        | python3 -m json.tool
docker compose logs hoop-inspect | ./hoopinspect/read-audit.py
```

## Identity

hoop-inspect authenticates the **database** credential, which is one shared
`appuser` for the whole of Metabase. `principal` on every session says so.

Metabase closes that gap for free. It prefixes every query it runs with a
comment naming the human:

```sql
-- Metabase:: userID: 1 queryType: native queryHash: 351c93b5…
SELECT id, name, email, ssn FROM customers
```

`hoopinspect/read-audit.py` lifts it out and prints it beside each denial, so
"who ran this" is answerable from the wire without Metabase being configured
to log anything and without an agent inside it. It is an attribution signal,
not an authentication one. Metabase writes that comment, so it is exactly as
trustworthy as Metabase is. Binding a verified subject instead is
`proxy.Config.IdentityFn`, the same seam `../envoy-stack` documents.

## Pointing a real Metabase at the sidecar

The demo provisions itself, but the manual path is four fields.

**The connection form.** Admin → Databases → Add. Everything is the
warehouse's except the first two:

| Field | Value |
|---|---|
| Host | the sidecar's hostname |
| Port | the lane's `listen` port |
| Database name, Username, Password | the warehouse's, unchanged |
| Use a secure connection (SSL) | **off**, unless the lane has `downstream_tls` |

Metabase probes SSL before it falls back, so a plaintext lane logs
`Failed to connect to Database: The server does not support SSL.` once and
then connects. It is noise, and the way to remove it is to terminate TLS at
the sidecar. See `downstream_tls` in
[`hoopinspect/README.md`](../../../hoopinspect/README.md). Postgres is the
only protocol that accepts it, and it is refused at startup on any other.

**Never proxy Metabase's application database.** The `MB_DB_*` connection
carries session tokens, saved questions and dashboard definitions. Routing it
through a policy engine that has no opinion about them buys nothing, and a
read-only rule bricks the product on first write. This stack keeps Metabase's
own state in H2 inside the container so the boundary is impossible to blur by
accident.

**Size `max_conns` against the pool, not against the demo.** Metabase holds a
c3p0 pool per database, sized by
`MB_JDBC_DATA_WAREHOUSE_MAX_CONNECTION_POOL_SIZE`, and a scheduled sync runs
concurrently with whatever dashboards are loading. A lane at its cap
**refuses rather than queues** (`proxy/proxy.go`,
`TestMaxConnsRefusesRatherThanQueues`), and Metabase surfaces that refusal as
a table that silently never finished syncing. Set the lane at or above the
pool. This stack sets 64 as headroom; it actually opens 6 connections in
total across a first boot and a full `./demo.sh`, which you can read off
`/stats`.

**Leave `idle_timeout_sec` unset for BI.** c3p0 keeps connections open and
idle between dashboard loads. Closing them underneath it turns the next query
into a reconnect, or into an error the user sees.

**Decide about `unknown` and `other` by watching a sync.** hoopinspect's
guidance is to fail closed on unclassifiable statements, because `DO`, `CALL`
and `EXECUTE` decide their effect at runtime. The catalogue above shows zero
of either across setup, sync and this demo, but that covers this demo, not
every Metabase feature. Models, pulses, actions, CSV uploads (which write to
the warehouse and a read-only lane will refuse) and a pgjdbc configured with
autosave (`SAVEPOINT` classifies as `other`) were not exercised. The rule is
written out and commented in `hoopinspect/config.yaml`; turn it on against
your own traffic rather than on trust.

**Metabase Cloud is the case this fits best.** Metabase's
[community drivers](https://www.metabase.com/docs/latest/developers-guide/community-drivers)
page is unambiguous: *"Community drivers are not supported on Metabase
Cloud."* The usual "install a plugin" integration is therefore unavailable
there by policy, which is exactly why a wire-level sidecar works: to Metabase
it is a Postgres server, not a plugin. Cloud connects out from Metabase's own
infrastructure, so the sidecar has to be reachable from it, which in practice
means `downstream_tls` on the lane and a firewall allowing the egress IPs
Metabase publishes for Cloud.

## Known gaps

- **A rule can be renamed out of, or shaped out of.** The two mask rule types
  cover each other, and the overlap is not total:

  - A **column rule** is defeated by renaming the output column.
    `SELECT ssn AS whatever FROM customers` does not match `columns: [ssn]`,
    because the name the rule keys on never reaches the wire.
  - An **entity rule** is defeated by anything that stops the value looking
    like itself: `substring(email, 1, 6)` returns a fragment no detector
    recognises, and alcatraz deliberately rejects fixture-shaped values like
    `123-45-6789`.

  So `SELECT ssn AS whatever FROM customers` returns 1 of 3 rows masked here:
  the one real-shaped SSN, caught by the entity rule; the two fixtures, missed
  by both. Enumerate the columns you know about *and* run the detector, and
  accept that an analyst with a native SQL editor can reshape a value out of
  both. If that matters more than the editor does, deny the operation instead
  of masking the response: policy runs on the request, where there is nothing
  to reshape.

- **Bind parameters are invisible to request-side rules.** The postgres codec
  reads `'Q'` Query and `'P'` Parse; it does not decode `'B'` Bind. A query
  builder filter arrives as `WHERE "customers"."ssn" = $1` with the value in
  a later message, so a rule that denies on statement content never sees it.
  Verified against this stack: filtering `ssn` for `123-45-6789` in the query
  builder records the `$1` shape and the literal appears nowhere in the audit
  trail.

  Two consequences, opposite in sign. **Response masking is unaffected**,
  because that same filtered query came back `***-**-6789`: masking works on
  the result set and does not care how the request was framed. But a
  `type: pii` rule that refuses a national id typed into the SQL editor will
  not refuse the same id entered into a query-builder filter box.

- **The query builder caps itself at 2,000 rows.** Metabase appends
  `LIMIT 2000` to builder queries, which is why `./demo.sh` uses a native CSV
  export to test past the 1,000-row decode limit. Nothing to do with the
  sidecar; worth knowing before you conclude masking stopped early.

- **Metabase to sidecar is plaintext here.** On a compose network, which is
  the shape a sidecar deployment actually has. The sidecar to `appdb` hop *is*
  encrypted and verified against the cert `./run.sh` mints; check it from a
  session with
  `SELECT ssl, version FROM pg_stat_ssl WHERE pid = pg_backend_pid();`.
  Inspection is unaffected either way, because the sidecar is the TLS client
  on that hop and decrypts what it reads.

- **One shared credential.** See [Identity](#identity). The per-human signal
  is Metabase's own comment, which is attribution rather than authentication.

- **Postgres only, in this stack.** hoopinspect ships postgres, http and
  mssql codecs. Metabase's other warehouses (BigQuery, Snowflake, Redshift,
  MySQL) need a codec for their wire protocol; that is a new `codec/<name>`
  package and nothing else, but it does not exist today.

- **The Metabase image is pinned** (`v0.63.13`). `provision.py` drives a real
  API that carries no stability guarantee. Bump it deliberately and re-run
  `./run.sh` to confirm provisioning still works.

## The claims this stack backs

1. **The control is on the warehouse, not in the tool.** Metabase is the
   client under test here; the same lane, unchanged, applies to every other
   client that speaks pgwire. That is the claim that does not depend on which
   BI vendor is in the diagram, or on which edition of it is licensed.
2. **Zero Metabase integration surface.** No plugin, no driver, no patched
   image, no container inside Metabase's pod. A hostname and a port, which is
   also why this works on Metabase Cloud, where community drivers are banned
   by policy.
3. **It covers the path Pro's column security does not.** The native SQL
   editor stays enabled and still returns masked rows. Metabase's own docs
   say row and column security "don't apply to the results of SQL questions."
4. **Masking survives the export.** 5,000 rows out through Metabase's own
   download endpoint, asserted redacted, past the row limit a reader would
   reasonably suspect. CSV, JSON and XLSX all verified.
5. **Per-statement audit of a tool nobody instrumented.** Including
   Metabase's own schema sync, keyed to the human by the comment Metabase
   already writes, and independent of the tool being audited.

It does **not** back "this replaces Metabase Pro." See
[Where Metabase is better](#where-metabase-is-better).
