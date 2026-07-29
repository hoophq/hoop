# Envoy + OPA + hoop-inspect — POC

A local replica of the common enterprise topology: **Envoy owns TLS and the
network path, OPA owns policy, hoop stitches in behind them.** No UDS, no PROXY
protocol, no MITM trust argument — hoop-inspect is an ordinary upstream that
the client never sees.

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
| postgres → appdb | 5433 | a byte count | no — no pgwire parser |

## Run it

```bash
./run.sh              # brings the whole stack up
./demo.sh             # walks every lane and prints the audit trail
./run.sh --rebuild    # rebuild the sidecar image, then up
./run.sh down         # tear down, including volumes
```

Needs `docker`, `curl`, `openssl`, `python3`.

Ports: `8443` HTTPS, `5433` postgres, `19000` sidecar admin, `9901` Envoy
admin. Inside the compose network the postgres listener is `envoy:5432`; 5433
is only the host-side publication, because a laptop usually has something on
5432 already.

`run.sh` builds `hoop-inspect:local` from `../../../hoopinspect` on first run
and reuses it afterwards. After a library change:

```bash
./run.sh --rebuild
```

The image builds `hoop-inspect-pii`, the relay with
[alcatraz](https://github.com/hoophq/alcatraz) PII detection linked in, so the
demo can mask Brazilian CPFs and IBANs and deny a query that embeds one. The
`pii` section of `hoopinspect/config.yaml` names which of the 45 entity types
are active — five here, and it is required rather than defaulted: turning on
every recognizer rewrites ordinary numeric columns, because `US_SSN` fires on
roughly a third of random nine-digit business ids.

Sidecar knobs live in `hoopinspect/config.yaml`; validate a change without
starting anything:

```bash
cd ../../../hoopinspect/pii/alcatraz && go run ./cmd/hoop-inspect-pii -validate \
  -config ../../../deploy/docker-compose/envoy-poc/hoopinspect/config.yaml
```

## The tiers

### Tier 1 — Envoy + OPA (this is config)

`envoy/envoy.yaml` runs an `ext_authz` filter against OPA over gRPC.
`opa/authz.rego` answers reachability: which users may reach which service.
Nothing else — no statement, no response, no content.

```bash
curl -k https://localhost:8443/json -H 'X-Hoop-User: alice'   # 200
curl -k https://localhost:8443/json -H 'X-Hoop-User: bob' -i  # 403 from OPA
curl -k https://localhost:8443/json -i                           # 401, no identity
```

`failure_mode_allow: false` — a dead OPA closes the gate.

OPA injects no upstream credential here. hoop-inspect authenticates nobody:
Envoy already did, and the sidecar's contract is that it runs behind something
owning identity. The one header the Rego does pass on is
`x-hoop-correlation-id`, so an audit row joins to an Envoy access log line.

### Tier 2a — postgres (hoop-inspect, not config)

Both statements below cross the *same* Envoy TCP listener on the same port:

```sql
SELECT name, email FROM customers;   -- fine
DELETE FROM customers WHERE id = 1;  -- destructive
```

Envoy cannot tell them apart. There is no pgwire parser in its filter chain, so
OPA is never invoked and never sees a statement. hoop-inspect parses the
protocol, records the statement text, and refuses the DELETE with a real
pgwire `ErrorResponse` carrying the operator's message — the developer reads
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
 Ada Lovelace | [REDACTED:EMAIL_ADDRESS] | 123-45-6789 | ******************5432
```

That is not byte substitution. A pgwire `DataRow` length-prefixes every row
and every column, so rewriting bytes in place desynchronizes psql on the next
message; the codec rebuilds the frames around the new values instead
(`hoopinspect/codec/postgres/rewrite.go`). `123-45-6789` survives on purpose —
alcatraz rejects sequential digit runs as test fixtures.

### Tier 2b — the HTTP response

On the HTTP lane Envoy is genuinely capable on the request side, and
pretending otherwise loses a technical review. `ext_authz` sees method, path,
headers and a bounded body slice. What it structurally cannot see is a
**response**: it decides before the upstream is called.

`hoopinspect/config.yaml` carries two rules that make the difference concrete:

- `no-internal-ids` denies `/anything/users/*/orders/*` — one rule for every
  id, because the codec normalizes the path to a stable resource rather than
  matching raw strings.
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

`read-audit.py` also asserts the trail contains no value that masking removed
— recording a masked value in the clear has un-masked it.

## Identity

Every session in this POC records `principal: anonymous`, and that is honest
rather than broken. `session.Identity` carries a Subject, and
`proxy.Config.IdentityFn` is the seam a deployment fills from a verified JWT,
an mTLS peer cert or a credential token
(`hoopinspect/proxy/proxy.go:86-93`). The sidecar exposes it as
`identity_header` on a listener, but the current implementation contributes
only the peer address: extracting a header there would mean buffering ahead of
the relay, and identity for HTTP belongs in the gate where the request is
already parsed (`hoopinspect/sidecar/sidecar.go:650-667`).

So the actor column is wired end to end and unpopulated. Filling it is one
function, not an architecture change — and `X-Hoop-User` is already on
every request that reaches the sidecar.

## Known gaps in this POC

- **Masking needs a codec that can carry it.** `gate.MaskSupported` asks the
  codec rather than listing protocols. HTTP declares its length in a header
  the gate corrects; postgres rebuilds its length-prefixed row frames around
  the new values. A codec offering neither gets its `mask` section refused at
  startup rather than accepted and silently never fired.
- **Two codecs ship: postgres and http.** MySQL, MSSQL and MongoDB were
  removed to keep the surface to what is exercised end to end. Adding one is a
  new `codec/<name>` package and nothing else.
- **`psql` must disable SSL** (`PGSSLMODE=disable`). The sidecar does not
  terminate TLS, and Envoy passes the TCP stream through untouched. In
  production Envoy would terminate on that listener too.
- **OPA runs under `linux/amd64`.** `openpolicyagent/opa:*-envoy` publishes no
  arm64 image. Fine under emulation; drop the `platform:` line on amd64.
- **Identity is a header.** `X-Hoop-User` stands in for a verified JWT
  subject. Real deployments read
  `input.attributes.metadataContext.filterMetadata` populated by Envoy's
  `jwt_authn` filter — same Rego shape, no compose-level IdP.
- **No SSH lane.** hoop's gateway terminates SSH and records both directions;
  hoopinspect has no SSH codec, so that lane is not in this stack. The
  argument it made is unchanged and worth stating: Envoy ships **no SSH filter
  at any fidelity**, so every service reached over SSH is entirely unpoliced
  by the Envoy+OPA layer.

## What this demonstrates

1. hoop deploys behind Envoy as a plain upstream, with **zero Envoy
   extensions** — no ext_proc, no WASM, no custom filter. Compose config and
   one YAML file.
2. Policy reachability stays in OPA. hoop does not ask to own tier 1.
3. The tier-2 gap is real and structural. On postgres, Envoy forwards bytes it
   cannot parse. On HTTP, `ext_authz` is request-side by construction, so the
   response — the place data actually leaves the building — is unreachable to
   it. hoop-inspect reads both, denies on content, masks in both directions,
   and records the statement either way.
