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
whole tier-2 story: the [`sidecar`](../../../sidecar) library running
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
```

Needs `docker`, `curl`, `openssl`, `python3`. No local Go: the sidecar image
compiles in `golang:1.26.5-alpine`, matching the `go 1.26.5` every module in
`sidecar/` declares.

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
[docs/adr/0005-sidecar-flow.md](../../../docs/adr/0005-sidecar-flow.md).

`run.sh` builds `hoop-inspect:local` from `../../../sidecar` on first run
and reuses it afterwards. After a library change:

```bash
./run.sh --rebuild
```

The image builds `hoop-inspect` with
[alcatraz](https://github.com/hoophq/alcatraz) PII detection linked in, so the
demo can mask Brazilian CPFs and IBANs and deny a query that embeds one. The
`pii` section of `sidecar/config.yaml` names which of the 45 entity types
are active, five here. The section is required rather than defaulted: turning
on every recognizer rewrites ordinary numeric columns, because `US_SSN` fires
on roughly a third of random nine-digit business ids.

Sidecar knobs live in `sidecar/config.yaml`; validate a change without
starting anything:

```bash
cd ../../../sidecar/cmd && go run . -validate \
  -config ../../deploy/docker-compose/envoy-stack/sidecar/config.yaml
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
rewriting bytes in place desynchronizes psql on the next message. That
reframing lives in the codec itself, in the private
`github.com/hoophq/libhoop/v2/codec/postgres`; `sidecar/codec/postgres`
registers it with the inspector.

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

`sidecar/config.yaml` carries two rules that make the difference concrete:

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
docker compose logs hoop-inspect | ./sidecar/read-audit.py
```

`read-audit.py` also asserts the trail contains no value that masking removed;
recording a masked value in the clear has un-masked it.

## Tier 2c: producers report, OPA decides (off in this stack)

Tiers 2a and 2b deny locally. This one splits a decision in half: a
**producer** works out WHAT is in a statement, and Rego decides what that
means. hoop-inspect calls what a producer reports a **finding**, and
`action: defer` on a rule is how you say "report it, do not deny it".

Two producers sit commented out in `sidecar/config.yaml`:

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
  send: redacted                       # the pii detector above withholds values
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
reference in [`sidecar/README.md`](../../../sidecar/README.md) and
[`docs/adr/0005-sidecar-flow.md`](../../../docs/adr/0005-sidecar-flow.md#risk-analysis-the-ai-session-analyzer).

## Identity

Every session in this stack records `principal: anonymous`, and that is honest
rather than broken. `session.Identity` carries a Subject, and
`proxy.Config.IdentityFn` is the seam a deployment fills from a verified JWT,
an mTLS peer cert or a credential token
(`sidecar/proxy/proxy.go:86-93`). The sidecar exposes it as
`identity_header` on a listener, but the current implementation contributes
only the peer address: extracting a header there would mean buffering ahead of
the relay, and identity for HTTP belongs in the gate where the request is
already parsed (`sidecar/daemon/daemon.go:650-667`).

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
  sidecar has no SSH codec, so that lane is not in this stack. The point
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
