# Envoy + OPA + hoop-inspect

> hoop-inspect 0.1.0

A local replica of the common enterprise topology: **Envoy owns TLS and the
network path, OPA owns policy, hoop stitches in behind them.** hoop-inspect is
an ordinary upstream that the client never sees, so there is no UDS, no PROXY
protocol and no MITM trust argument.

```
client ──TLS──> envoy ──ext_authz(gRPC)──> opa      tier 1: fat gate
                  │                                 "can alice reach httpbin?"
                  ▼
                hoop-inspect  :18080 http           tier 2: deep inspection
                              :15432 postgres       "is this DELETE allowed?"
                  │
                  ▼
                httpbin | appdb
```

There is no hoop gateway and no hoop agent in this stack. The sidecar is the
whole tier-2 story: the [`hoopinspect`](../../../hoopinspect) library running
as a process, parsing the wire protocol, enforcing policy per statement,
writing an audit trail and masking responses.

Two lanes, decreasing Envoy visibility:

| Lane | Envoy port | Envoy sees | OPA consulted |
|---|---|---|---|
| HTTPS → httpbin | 8443 | method, path, headers | yes, `ext_authz` |
| postgres → appdb | 5433 | a byte count | no (no pgwire parser) |

## Run it

```bash
./run.sh              # brings the whole stack up
./demo.sh             # walks every lane and prints the audit trail
./run.sh --rebuild    # rebuild the sidecar image, then up
./run.sh down         # tear down, including volumes

./run.sh --review     # + a hoop gateway and the human-approval gate
./demo-review.sh      # walks a statement from refusal to approval to execution
```

Needs `docker`, `curl`, `openssl`, `python3`. No local Go: the sidecar image
compiles in `golang:1.26.5-alpine`, matching the `go 1.26.5` every module in
`hoopinspect/` declares.

`--review` is the exception on both counts. It needs local Go (it
cross-compiles the gateway from this repo) and an `ANTHROPIC_API_KEY`.

**For the approval gate, read [Tier 2d](#tier-2d-a-human-approves-the-statement)
first and run it by hand.** Nine steps against a proxy on your machine and a
gateway you already have — no compose, no script. The two commands above
automate the same thing once you have seen each part answer for itself.

Ports: `8443` HTTPS, `5433` postgres, `19000` sidecar admin, `9901` Envoy
admin. Inside the compose network the postgres listener is `envoy:5432`; 5433
is only the host-side publication, because a laptop usually has something on
5432 already.

Envoy reaches the sidecar over TCP here, which keeps the topology to one
claim: hoop-inspect is an ordinary upstream. To close the last port and have
Envoy reach it over a unix socket instead, add the overlay in
[`uds/`](uds/README.md): same lanes, same policy, no data port open at all.

Two more overlays add an MSSQL lane, and what separates them is who terminates
the client's TLS. [`mssql/`](mssql/README.md) runs SQL Server 2022 over TDS
8.0, an ordinary TLS-on-connect handshake Envoy terminates with no TDS
awareness, and adds a Kerberos client and an AD domain controller.
[`mssql2019/`](mssql2019/README.md) runs TDS 7.4, where the client wraps its
handshake inside `0x12` PRELOGIN packets that Envoy cannot speak, so the relay
takes the connection directly and reads through an encrypted login.

**The running transport.** `/stats` reports the address each lane bound, so
you can read it off the process instead of the config:

```bash
curl -s localhost:19000/stats | python3 -m json.tool
```

| `addr` reads | Transport |
|---|---|
| `[::]:15432` | TCP, the default stack |
| `/run/hoop-inspect/pg.sock` | unix socket, the `uds/` overlay |

The startup log carries the same fact as an explicit field:
`docker compose logs hoop-inspect | grep listening` prints `"network":"tcp"`
or `"network":"unix"` per lane.

For the code path behind these commands, a per-command runbook and a
troubleshooting table, read
[docs/adr/hoopinspect-flow.md](../../../docs/adr/hoopinspect-flow.md).

`run.sh` builds `hoop-inspect:local` from `../../../hoopinspect` on first run
and reuses it afterwards. After a library change:

```bash
./run.sh --rebuild
```

The image builds `hoop-inspect` with
[alcatraz](https://github.com/hoophq/alcatraz) PII detection linked in, so the
demo can mask Brazilian CPFs and IBANs and deny a query that embeds one. The
`pii` section of `hoopinspect/config.yaml` names which of the 45 entity types
are active, five here. The section is required rather than defaulted: turning
on every recognizer rewrites ordinary numeric columns, because `US_SSN` fires
on roughly a third of random nine-digit business ids.

Sidecar knobs live in `hoopinspect/config.yaml`; validate a change without
starting anything:

```bash
cd ../../../hoopinspect/cmd && go run . -validate \
  -config ../../deploy/docker-compose/envoy-stack/hoopinspect/config.yaml
```

That one runs on the host, so it needs Go 1.26.5 or newer. An older toolchain
stops at `requires go >= 1.26.5`; `GOTOOLCHAIN=auto`, the default, fetches the
right one instead.

## The tiers

### Tier 1: Envoy + OPA (this is config)

`envoy/envoy.yaml` runs an `ext_authz` filter against OPA over gRPC.
`opa/authz.rego` answers reachability: which users may reach which service.
Nothing else: no statement, no response, no content.

```bash
curl -k https://localhost:8443/json -H 'X-Hoop-User: alice'   # 200
curl -k https://localhost:8443/json -H 'X-Hoop-User: bob' -i  # 403 from OPA
curl -k https://localhost:8443/json -i                           # 401, no identity
```

With `failure_mode_allow: false`, a dead OPA closes the gate.

OPA injects no upstream credential here. hoop-inspect authenticates nobody:
Envoy already did, and the sidecar's contract is that it runs behind something
owning identity. The one header the Rego does pass on is
`x-hoop-correlation-id`, so an audit row joins to an Envoy access log line.

### Tier 2a: postgres (hoop-inspect, not config)

Both statements below cross the *same* Envoy TCP listener on the same port:

```sql
SELECT name, email FROM customers;   -- fine
DELETE FROM customers WHERE id = 1;  -- destructive
```

Envoy cannot tell them apart. There is no pgwire parser in its filter chain, so
OPA is never invoked and never sees a statement. hoop-inspect parses the
protocol, records the statement text, and refuses the DELETE with a real
pgwire `ErrorResponse` carrying the operator's message; the developer reads
it in psql rather than watching a socket drop.

```bash
docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable \
  psql -h envoy -p 5432 -U appuser -d appdb \
       -c 'DELETE FROM customers WHERE id = 1;'
```

The same lane rewrites the result set on the way back:

```
     name     |          email           |     ssn     |          iban
--------------+--------------------------+-------------+------------------------
 Ada Lovelace | [REDACTED:EMAIL_ADDRESS] | ***-**-6789 | ******************5432
 Grace Hopper | [REDACTED:EMAIL_ADDRESS] | ***-**-4321 | ******************3000
```

The codec rebuilds the frames around the new values rather than substituting
bytes. A pgwire `DataRow` length-prefixes every row and every column, so
rewriting bytes in place desynchronizes psql on the next message
(`hoopinspect/codec/postgres/rewrite.go`).

A **column rule** masks the `ssn` column by position rather than by detection.
The seeded value `123-45-6789` is one alcatraz refuses, because it rejects
sequential digit runs as test fixtures, so the entity rule skips it and
`columns: [ssn]` catches it anyway. A column rule works wherever the protocol
names its values, and it does not care what the value looks like. The same
placeholder posted to the HTTP lane survives unmasked, and `./demo.sh` shows
that contrast.

### Tier 2b: the HTTP response

On the HTTP lane Envoy is capable on the request side, and pretending
otherwise loses a technical review. `ext_authz` sees method, path, headers and
a bounded body slice. It structurally cannot see a **response**: it decides
before the upstream is called.

`hoopinspect/config.yaml` carries two rules that make the difference concrete:

- `no-internal-ids` denies `/anything/users/*/orders/*` with one rule for
  every id, because the codec normalizes the path to a stable resource rather
  than matching raw strings.
- `no-upstream-5xx` suppresses a 5xx on the way back. There is no ext_authz
  shape that does this.

Masking runs here too, by the simpler mechanism: `./demo.sh` posts a body with
an email, an SSN, a card, a CPF and an IBAN, and reads back five rewritten
values with `Content-Length` corrected to match.

### Check the recorded evidence

The audit trail is JSONL on stdout plus a queryable store on the admin
listener:

```bash
curl -s localhost:19000/api/sessions | python3 -m json.tool
curl -s localhost:19000/stats        | python3 -m json.tool
docker compose logs hoop-inspect | ./hoopinspect/read-audit.py
```

`read-audit.py` also asserts the trail contains no value that masking removed;
recording a masked value in the clear has un-masked it.

## Tier 2c: producers report, OPA decides (off in this stack)

Tiers 2a and 2b deny locally. This one splits a decision in half: a
**producer** works out WHAT is in a statement, and Rego decides what that
means. hoop-inspect calls what a producer reports a **finding**, and
`action: defer` on a rule is how you say "report it, do not deny it".

Two producers sit commented out in `hoopinspect/config.yaml`:

- **`pii`**: a `type: pii` rule carrying `action: defer`. No credential, no
  per-statement cost: the detector in the `pii:` section is already running
  for masking. Uncomment the rule and the `opa:` block, nothing else.
- **`ai_analysis`**: sends a statement to a language model and reports the
  risk it comes back with. Needs a credential and spends money per statement,
  so it also needs the `analyzer:` block.

Either way nothing else in the stack changes: the Rego is already loaded and
the OPA container is already running.

The appdb lane demonstrates where the **determination** lives. `high: block`
would put it in this YAML, a second place decisions live that nobody reviews.
`defer` puts it in `opa/authz.rego`, beside the ext_authz rules the same team
already owns:

```yaml
analyzer:
  provider: vertex                     # vertex | anthropic | openai
  model: claude-sonnet-4-5@20250929
  extra: {project: my-gcp-project, region: global}
  # credentials_file omitted -> Application Default Credentials
  send: redacted                       # see the note below — it does NOT withhold values
  cache: {size: 4096, ttl_sec: 900}

listeners:
  - name: appdb
    policy:
      rules:
        - name: sensitive-columns
          type: pii
          action: defer                # report the classes, do not deny
          entities: [US_SSN, CREDIT_CARD, IBAN_CODE, EMAIL_ADDRESS]
        - name: risky-writes
          type: ai_analysis
          high: defer                  # the analyzer classifies; Rego decides
          medium: defer
          low: defer
      opa:
        url: http://opa:8181/v1/data/envoy/authz/inspect
        fail_open: false
        gate: true                     # ask Rego what is worth a model call
```

BR_CPF is missing from that entity list because `no-cpf-in-query` in the
defaults already denies it outright, for free, before OPA is consulted.
Taking the `pii` rule on its own means dropping `gate: true`, because a gate
with no `ai_analysis` rule is a round trip that buys nothing and is refused at
startup.

The lane then consults OPA twice, at the same URL, with `input.phase` saying
which call it is. The **gate** phase runs before the producers and answers
`request: {"ai_analysis": true}` or `false`, so the cost control is a Rego
rule rather than a `trigger:` list. That is why the ai rule above has no
trigger at all. The **decide** phase runs after, carrying what each producer
established in `input.findings`, keyed by source:

```json
{"findings": {
  "pii": {"rule": "sensitive-columns", "status": "ok",
          "values": {"entities": ["US_SSN"], "rules": ["sensitive-columns"]}},
  "ai_analysis": {"rule": "risky-writes", "status": "ok",
                  "values": {"risk_level": "high"}}
}}
```

Read `opa/authz.rego` from the `inspect` rules down. It blocks a high risk
level, blocks a protected entity class the free rules let through, and fails
CLOSED when the ai finding's `status` is not `ok` or `cached`. That last rule
is the one worth copying: an absent `risk_level` means nothing triggered, the
budget was spent, `send: refuse` stopped the transmission, or the provider
failed, and a policy keying only on the level allows all four. A producer that
ran and could NOT answer still appears in `findings` carrying its status,
which is what makes that case expressible.

No finding carries statement content. `pii` reports entity classes and never
the values behind them, a matched pattern's text is never reported, and model
prose never travels, because OPA's decision log copies everything sent to it.

Three things keep the model affordable. The **gate** excludes everything
outside `inspect_sensitive`, so an ORM's traffic against other tables costs
nothing. The **cache** keys on the statement shape, so `WHERE id = 1` and
`WHERE id = 2` are one verdict. The analyzer runs after `no-destructive-sql`,
so anything a free rule already refused never reaches a model.

On the httpbin lane it also needs an `http:` block with `capture_body: true`.
Without a body the model sees `POST /anything` and nothing else, and a request
with no body is skipped rather than classified, so a forgotten flag looks like
an analyzer that does not fire.

Verdicts land in the audit trail as `metadata.risk_level` and roll up per
session. `metadata.ai_status` rides beside it and says what the analyzer did,
which is the key to read when a level is missing. That is the analyzer's own
audit vocabulary and it keeps the specific word (`budget_exhausted`,
`refused`); the finding it publishes to Rego reports the generic `unavailable`
with the word in `reason`:

```bash
curl -s localhost:19000/api/stats | python3 -m json.tool   # by_risk
curl -s localhost:19000/config    | python3 -m json.tool   # ai_rules per lane
```

This evaluator **fails open** by default. It depends on a third-party API, and
refusing every statement during a vendor outage is a larger incident than the
one it guards against. OPA and the local rules still fail closed. Full
reference in [`hoopinspect/README.md`](../../../hoopinspect/README.md) and
[`docs/adr/hoopinspect-flow.md`](../../../docs/adr/hoopinspect-flow.md#risk-analysis-the-ai-session-analyzer).

## Tier 2d: a human approves the statement

Every tier above decides on its own. This one does not: it stops the statement
and asks a person.

The walkthrough below is deliberately manual — nine steps you run yourself,
against a proxy on your machine and a hoop gateway you already have. No
compose profile and no script, because the point is to see each moving part
answer for itself. (`./run.sh --review` and `./demo-review.sh` automate the
same thing once you have seen it work.)

### Nothing is held

```
caller ──sql──> hoop proxy ──> postgres        (data path)
  │                 │
  │                 └──HTTPS──> hoop gateway   (claim / file)
  └──HTTPS poll ──────────────> hoop gateway
                                     ▲
reviewer ────── approves ────────────┘
```

A flagged statement is **refused and the database session ends**:

```
FATAL:  this statement needs human approval before it can run; review it at
        http://127.0.0.1:8009/sessions/<id>. The session is closed: once it is
        approved, reconnect and re-issue the same statement
        (poll statement_hash=6f77928a…).
```

Holding the connection instead would burn a slot against `max_conns`, trip
`idle_timeout_sec`, be killed by driver-side statement timeouts anyway, and
need a cancellation `policy.Evaluator` cannot carry. So the caller polls,
waits, reconnects, and re-issues.

**That makes the caller's pool and backoff settings part of the deployment
contract.** A gated lane drops connections as normal operation, and a pool
that treats a `FATAL` as a host-level failure will look like a hoop outage.

### "The agent" can be a person

The design talks about a sandboxed autonomous agent, because that is the case
that forced it. Nothing in the mechanism requires one.

The thing that polls and retries is **whatever holds the `hpk_` token** — an
LLM agent, a CI job, a migration script, or you with `curl` and `psql`. The
gate never asks who is calling; it asks whether an approved, unconsumed review
exists for this exact statement on this connection. A human running the steps
below is exercising the identical code path an agent would.

Three roles are in play, and they are worth keeping straight:

| Role | Holds | Does |
|---|---|---|
| **caller** | nothing special | issues SQL through the proxy. May be an agent, a script, or a person at a psql prompt |
| **relay** (hoop proxy) | the `hpk_` token, via `token_file` | classifies, files the review, refuses, and later claims the approval |
| **reviewer** | a gateway login in the `reviewers_groups` | answers the review in the webapp or over the API |

The poll in step 7 uses the relay's `hpk_` token, because the review belongs to
that identity. Caller and reviewer being the same person is fine for a
walkthrough and is exactly what you must not ship: the whole control is that
they are different people.

### What you need first

- **A hoop gateway you can reach**, with an admin login. The dev one from
  `make run-dev` on `http://127.0.0.1:8009` is what these steps assume.
- **A postgres to point at.** Any database; the proxy sits in front of it.
- **Three credentials**, obtained in steps 1–2.

Nothing here needs the compose stack in this directory.

---

### Step 1 — get a model credential

The gate is an `ai_analysis` action, so something has to classify the statement
before there is anything to review.

These steps use Google's OpenAI-compatible endpoint, which the `openai`
provider talks to unchanged — it is a bearer token against a chat-completions
URL, which is what that provider already sends.

1. Open [Google AI Studio](https://aistudio.google.com/apikey) → **Create API
   key**, and enable the *Generative Language API* on the project it belongs
   to.
2. Write the key to a file and lock it down. The relay **refuses a credential
   file that group or other can read**, and reports the mode when it does:

```bash
mkdir -p ~/.hoop/proxy
printf '%s' 'AIza…' > ~/.hoop/proxy/analyzer-key
chmod 600 ~/.hoop/proxy/analyzer-key
```

> Vertex AI works too, with `provider: vertex`, `extra: {project, region}` and
> Application Default Credentials — but it serves **Claude** models on the path
> the provider builds, not Gemini. See `hoopinspect/README.md` for that route.

### Step 2 — get the relay's `hpk_` token

This is the credential the proxy authenticates to the gateway with. It is an
identity in its own right: the reviews it files are owned by it, and a
reviewer reading the queue sees which environment asked.

Create an AI agent on the gateway — in the webapp under **AI Agents**, or over
the API:

```bash
API=http://127.0.0.1:8009/api
ADMIN_TOKEN=…      # see below

curl -sS -X POST "$API/ai-agents" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"pghoop-relay","groups":["admin"]}' | python3 -m json.tool
```

The response carries `"key": "hpk_…"`. **It is shown exactly once.** Save it
with the same permissions as above:

```bash
printf '%s' 'hpk_…' > ~/.hoop/proxy/api-key
chmod 600 ~/.hoop/proxy/api-key
```

<details>
<summary>Getting <code>ADMIN_TOKEN</code></summary>

Log in to the webapp and copy the bearer token from your browser's devtools,
or on a local-auth gateway ask for one — it comes back in a `Token` **header**,
not in the body:

```bash
curl -sS -D - -o /dev/null -X POST "$API/localauth/login" \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"…"}' | grep -i '^token:'
```
</details>

The `groups` you give it matter: they decide which connections it can reach.
`admin` reaches everything, which is right for a walkthrough and wrong for a
real sandbox — scope it to a group that only reaches the connection it fronts.

### Step 3 — tell the gateway what it is reviewing

Two objects, both on the gateway, both one-time.

**A connection** whose name matches the lane's `connection:`. An approval is
scoped to it, so one for `pghoop` cannot authorize the same SQL against
`payments-db`. It needs an agent to hang off, and any existing one will do —
no hoop session is ever opened against it, because the proxy is the data path.

```bash
AGENT_ID=$(curl -sS -H "Authorization: Bearer $ADMIN_TOKEN" "$API/agents" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)[0]["id"])')

curl -sS -X POST "$API/connections" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"pghoop\",\"type\":\"database\",\"subtype\":\"postgres\",
       \"agent_id\":\"$AGENT_ID\",\"command\":[],\"secret\":{},
       \"access_mode_runbooks\":\"disabled\",\"access_mode_exec\":\"disabled\",
       \"access_mode_connect\":\"disabled\",\"access_schema\":\"disabled\"}"
```

**An access request rule** naming who reviews. Without it the gateway answers
`422` — there is nobody to ask, and inventing a reviewer would be worse than
refusing:

```bash
curl -sS -X POST "$API/access-requests/rules" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"pghoop-statement-review","access_type":"command",
       "connection_names":["pghoop"],"approval_required_groups":[],
       "reviewers_groups":["admin"],"force_approval_groups":["admin"],
       "all_groups_must_approve":false,"min_approvals":1}'
```

This is the org's existing review apparatus — groups, `min_approvals`,
force-approval, Slack, the webapp. No second approval system was built for
this.

### Step 4 — write the proxy config

One lane, one gate. Save as `~/.hoop/proxy/config.yaml`:

```yaml
log_level: info

admin:
  listen: 127.0.0.1:19000        # /healthz, /config, /stats, audit query API

audit:
  file: "-"                      # stdout
  memory_buffer: 256

# The in-process detector. Two entities is enough here, and it is what the
# analyzer's `send:` mode below keys on.
pii:
  entities: [EMAIL_ADDRESS, US_SSN]

analyzer:
  provider: openai
  endpoint: https://generativelanguage.googleapis.com/v1beta/openai/chat/completions
  model: gemini-2.5-flash
  credentials_file: /Users/you/.hoop/proxy/analyzer-key
  timeout_sec: 20
  # FALSE, inverting the analyzer's usual default, and the one setting worth
  # arguing about. The gate fires on the analyzer's FINDING, so a
  # classification that never happened is not a flag — fail open and every
  # statement you wrote require_review for runs unreviewed for as long as the
  # provider is down.
  fail_open: false
  # WARNING: despite the name, this does not withhold values. It appends a
  # note naming the entity classes found and sends the statement unchanged.
  # Use `send: refuse` if a statement carrying a detected entity must not
  # reach the provider at all.
  send: redacted
  cache: {size: 4096, ttl_sec: 900}
  max_calls: 200

review:
  api_url: http://127.0.0.1:8009        # gateway ROOT, not /api
  # A PATH, never the material.
  token_file: /Users/you/.hoop/proxy/api-key
  timeout_sec: 5
  require_marker: false
  # A refusal is remembered this long, so a caller polling in a tight loop
  # does not turn one denial into a stream of gateway calls. Refusals only —
  # an APPROVED answer is never cached, because a cached approval is a
  # revocation that cannot be honored.
  poll_cache_ttl_sec: 5

policy:
  enforce: true

listeners:
  - name: pghoop
    protocol: postgres
    listen: 127.0.0.1:15432
    upstream: 127.0.0.1:5432           # your database
    connection: pghoop                 # REQUIRED: what an approval is scoped to
    policy:
      rules:
        # Ahead of the gate on purpose. A statement a free local rule already
        # refuses never reaches a model and never troubles a person: DROP and
        # TRUNCATE are not "ask a human", they are "no".
        - name: no-destructive-ddl
          type: operation
          operations: [drop, truncate]
          message: destructive DDL is not permitted on pghoop, approved or not

        - name: risky-writes
          type: ai_analysis
          # `unknown` is not optional on a gated lane. DO, CALL and EXECUTE
          # decide their effect at runtime, so the lexer reports `unknown`
          # rather than guessing. Name only [delete, update] and a caller walks
          # past the gate in two statements: PREPARE ... AS DELETE is gated,
          # and the EXECUTE that actually deletes the row is not.
          trigger: {operations: [delete, update, unknown]}
          high: require_review
          medium: require_review
          low: warn                    # still recorded, with its level
          prompt: |
            You are classifying statements against a production customer
            database. Treat any statement that deletes or modifies customer
            rows as at least medium risk. A statement scoped to a single row
            by primary key is medium; anything unscoped is high.
```

Check it before running anything. This parses the config, resolves each lane
and reads both credential files — including their permissions:

```bash
hoop start proxy --config ~/.hoop/proxy/config.yaml --validate
```

```
config OK: 1 listener(s)
  pghoop           postgres  enforcing 1 rule(s) + 1 ai rule(s) + human review
```

`+ human review` is the gate. If it is missing, the lane is not gated —
check that a risk level maps to `require_review` and that the `review:` block
is present.

### Step 5 — start it

```bash
hoop start proxy --config ~/.hoop/proxy/config.yaml
```

Then confirm what the lane actually resolved to, which is not the same
question as what you wrote in the file:

```bash
curl -s localhost:19000/config | python3 -m json.tool
```

```json
"lanes": [{ "name": "pghoop", "listen": "127.0.0.1:15432",
            "connection": "pghoop", "ai_rules": ["risky-writes"],
            "review": "gateway=127.0.0.1:8009 require_marker=false pending_ttl=5s" }]
```

### Step 6 — issue a statement and watch it refused

Connect **through the proxy** (port 15432), not to your database directly:

```bash
psql -h 127.0.0.1 -p 15432 -U youruser yourdb \
  -c '-- hoopdev:correlation_id=task-1
DELETE FROM customers WHERE 1 = 0;'
```

```
FATAL:  this statement needs human approval before it can run; review it at
        http://127.0.0.1:8009/sessions/<id>. The session is closed: once it is
        approved, reconnect and re-issue the same statement
        (poll statement_hash=6f77928a…).
server closed the connection unexpectedly
```

`WHERE 1 = 0` deletes nothing, so you can run this walkthrough against a real
table safely.

Three things to notice. The severity is `FATAL` and the session really is
gone. The message names where a human answers it. And it hands you the
`statement_hash`, so you never have to reproduce the canonicalization
yourself:

```bash
H=6f77928a…      # copy it from the message
```

> **Use `-c`.** psql's own lexer discards a comment that precedes the first
> token of a statement, so a marker sent through a heredoc or `-f file.sql`
> never reaches the wire and `require_marker: true` refuses it. A driver sends
> what you give it, so this is a psql problem rather than an agent one.

### Step 7 — poll, as the caller

This is the step an autonomous agent loops on. Run it yourself with the
relay's token and you are doing exactly what it does:

```bash
SANDBOX=$(cat ~/.hoop/proxy/api-key)
curl -sS -H "Authorization: Bearer $SANDBOX" \
  "$API/relay/reviews?connection=pghoop&statement_hash=$H" | python3 -m json.tool
```

```json
{"review_id": "…", "session_id": "…", "status": "PENDING",
 "url": "http://127.0.0.1:8009/sessions/…"}
```

Read-only: run it twice and it still says `PENDING`. Only the relay's claim
may settle a review, which is why polling can never consume an approval.

Retry the statement now and the count of pending reviews does not move — the
create path dedupes on the marker, so a caller polling and retrying does not
fill the queue with copies of one question.

### Step 8 — approve it, as a reviewer

Open the URL from step 6 in the webapp, or answer it over the API:

```bash
RID=$(curl -sS -H "Authorization: Bearer $SANDBOX" \
  "$API/relay/reviews?connection=pghoop&statement_hash=$H" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["review_id"])')

curl -sS -X PUT "$API/reviews/$RID" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"status":"APPROVED"}'
```

Poll again and it reads `APPROVED`.

### Step 9 — re-issue: it runs, exactly once

```bash
psql -h 127.0.0.1 -p 15432 -U youruser yourdb \
  -c '-- hoopdev:correlation_id=task-1
DELETE FROM customers WHERE 1 = 0;'
```

```
DELETE 0
```

A **new connection**, because the old one is gone. The claim runs before
classification, so this cost one gateway round trip and **no model call** —
visible in the trail as `ai_status: skipped`:

```bash
curl -s localhost:19000/events | python3 -c '
import json,sys
for e in json.load(sys.stdin)["events"]:
    md = e.get("metadata") or {}
    if "review_status" in md or "ai_status" in md:
        print("%-5s review=%-11s ai=%-8s %s" % (
            "ALLOW" if e.get("allowed") else "DENY",
            md.get("review_status","-"), md.get("ai_status","-"),
            (e.get("statement") or "")[:44].replace("\n"," ")))'
```

Run it a third time and it is refused again. A human approved **one execution
of that exact statement**, not standing permission — and an approval for
`WHERE 1 = 0` never covers `WHERE 2 = 0`, which is the whole reason the key is
an exact hash rather than a statement shape.

The gate's own counters sit beside the lane's:

```bash
curl -s localhost:19000/stats | python3 -m json.tool
```

`approvals` is how many statements it let through, `filed` how many questions
reached a person, `refusals` how many it would not even file, and `errors`
should be `0` — a non-zero `errors` with everything else working means the
gateway is intermittent, and every one of those was a refused statement.

### What it does not cover

A statement whose parameter values the relay cannot read is **refused, not
gated**: a postgres `Parse` (what every driver sends), an mssql
`sp_executesql`, a truncated HTTP body. One approval would otherwise cover
every later binding. `psql -c` sends a simple Query, which is why the steps
above work from the command line and a driver on the same lane would not.

### When a step does not do what it says

| Symptom | Cause |
|---|---|
| `--validate` fails on a credential file | Mode is not `0600`, or the path is wrong. The error reports the mode it found: `… ./api-key is 0644, want 0600 or stricter` |
| `has a rule that gates on "require_review" but the config has no "review" section` | Step 4's `review:` block is missing |
| `the config has a "review" section but no rule gates on "require_review"` | The mirror image: the block is there but no risk level maps to `require_review`, so the credential would be read and never used |
| `gates on "require_review" but sets no "connection"` | The lane needs `connection:` — an approval is scoped to one |
| No `+ human review` in `--validate`, but it passed | The lane genuinely has no gate. All three mismatches above are refusals, so a clean validate without that suffix means no rule asked for review |
| `gateway returned 404: connection not found` | The lane's `connection:` is not a connection the gateway has, or the `hpk_` token's groups cannot reach it (step 3) |
| `gateway returned 422` | That connection has no access request rule — nobody to review it (step 3) |
| `risk analysis unavailable; denying` | The analyzer could not answer. With `fail_open: false` that refuses, which is correct for a gated lane. The provider's own reason is in the relay log |
| `this lane requires a hoopdev:correlation_id marker` when you sent one | psql stripped it. Use `-c` |
| Nothing is gated at all | The trigger did not match. Compare the audit line's `operation` against the rule's `trigger` |

Reference: [ADR-0005](../../../docs/adr/0005-hoopinspect-review-gate.md) and
[`hoopinspect/README.md`](../../../hoopinspect/README.md#holding-a-statement-for-a-human-require_review).

### Doing all of that in one command

Once you have seen it work, the compose profile in this directory does the
same setup automatically — gateway, database, connection, rule and token — and
walks the flow:

```bash
export ANTHROPIC_API_KEY=sk-ant-…    # the automated path uses Anthropic
./run.sh --review
./demo-review.sh
```
## Identity

Every session in this stack records `principal: anonymous`, and that is honest
rather than broken. `session.Identity` carries a Subject, and
`proxy.Config.IdentityFn` is the seam a deployment fills from a verified JWT,
an mTLS peer cert or a credential token
(`hoopinspect/proxy/proxy.go:86-93`). The sidecar exposes it as
`identity_header` on a listener, but the current implementation contributes
only the peer address: extracting a header there would mean buffering ahead of
the relay, and identity for HTTP belongs in the gate where the request is
already parsed (`hoopinspect/sidecar/sidecar.go:650-667`).

So the actor column is wired end to end and unpopulated. Filling it takes one
function rather than an architecture change, and `X-Hoop-User` already rides
on every request that reaches the sidecar.

## Known gaps

- **Masking needs a codec that can carry it.** `gate.MaskSupported` asks the
  codec rather than listing protocols. HTTP declares its length in a header
  the gate corrects; postgres rebuilds its length-prefixed row frames around
  the new values. A codec offering neither gets its `mask` section refused at
  startup rather than accepted and silently never fired.
- **Three codecs ship: postgres, http and mssql.** MySQL and MongoDB were
  removed to keep the surface to what is exercised end to end. This base stack
  runs the first two; the overlays in [`mssql/`](mssql/README.md) and
  [`mssql2019/`](mssql2019/README.md) run the third. Adding one is a new
  `codec/<name>` package and nothing else.
- **`psql` must disable SSL** (`PGSSLMODE=disable`) on the CLIENT leg. Nothing
  terminates TLS between psql and the sidecar: Envoy passes that TCP stream
  through untouched. In production Envoy would terminate on that listener too.
  The SIDECAR-to-`appdb` leg is a separate hop and it IS encrypted (TLSv1.3,
  verified against the cert `./run.sh` mints). Check it from a client session:
  `SELECT ssl, version FROM pg_stat_ssl WHERE pid = pg_backend_pid();`
  Masking and policy are unaffected, because the sidecar is the TLS client on
  that hop and decrypts what it reads.
- **OPA runs under `linux/amd64`.** `openpolicyagent/opa:*-envoy` publishes no
  arm64 image. Fine under emulation; drop the `platform:` line on amd64.
- **Identity is a header.** `X-Hoop-User` stands in for a verified JWT
  subject. Real deployments read
  `input.attributes.metadataContext.filterMetadata` populated by Envoy's
  `jwt_authn` filter (same Rego shape, no compose-level IdP).
- **No SSH lane.** hoop's gateway terminates SSH and records both directions;
  hoopinspect has no SSH codec, so that lane is not in this stack. The point
  stands: Envoy ships **no SSH filter at any fidelity**, so every service
  reached over SSH is unpoliced by the Envoy+OPA layer.

## The claims this stack backs

1. hoop deploys behind Envoy as a plain upstream, with **zero Envoy
   extensions**: no ext_proc, no WASM, no custom filter. Compose config and
   one YAML file.
2. Policy reachability stays in OPA. hoop does not ask to own tier 1.
3. The tier-2 gap is real and structural. On postgres, Envoy forwards bytes it
   cannot parse. On HTTP, `ext_authz` is request-side by construction, so the
   response (the place data leaves the building) is unreachable to it.
   hoop-inspect reads both, denies on content, masks in both directions, and
   records the statement either way.
