# hoopinspect

Turn raw database wire-protocol bytes into structured statements, and
structured statements into allow/deny verdicts.

**The library is a pure function over bytes.** It opens no socket, terminates
no TLS, routes nothing. You hand it bytes you already have, and whatever holds
the connection keeps holding it.

**The relay owns a socket.** `pii/alcatraz/cmd/hoop-inspect-pii` wraps the
library in a TCP listener that accepts a connection, dials one upstream, and
pumps bytes through the gate in both directions. It runs behind something that
already owns TLS and identity, typically Envoy forwarding plaintext over
loopback or a unix socket.

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
| HTTP request | `ext_authz`: method, path, headers, bounded body | same, plus normalized resource |
| HTTP **response** | ✗, ext_authz decides before the upstream is called | status, headers, body |
| Deny UX | RBAC/ext_authz drops or returns a bare 403 | operator-authored message |

On HTTP, Envoy's `ext_authz` covers request-side authorization well, and the
gaps named above are the narrow ones. Envoy is not blind here.

## Protocols

| Protocol | Messages decoded | Stateful |
|---|---|---|
| `postgres` | `Query` ('Q'), `Parse` ('P'); handshake skipped | no |
| `http` | HTTP/1.x requests and responses | no |

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
    Metadata  map[string]string // protocol-specific, documented per codec
}
```

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

## Limits

Read these before writing a policy against it.

- **`Tables` is best effort.** A lexer produces it, so it hits the same
  ceiling Envoy's own docs acknowledge for `postgres_proxy`. Empty means
  "could not determine", **never** "touches nothing". Use
  `RequireTableMatch: true` on rules protecting something critical, and accept
  the false positives.
- **HTTP response inspection works; SQL response inspection does not.** For
  the database codecs `FromServer` bytes are consumed and yield no statements,
  because the redaction path is unbuilt. The HTTP codec inspects both
  directions.
- **Masking is HTTP-only.** The wire-database protocols length-prefix their
  rows in binary frames, and substituting bytes in place desynchronizes the
  client (`psql` reports "lost synchronization with server"). The gate refuses
  rather than half-masking: corrupt *and* still leaking is the worst outcome.
  Re-framing per codec is the next step. `pii` policy rules work on every
  protocol, because denying a statement changes no bytes.
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
- **Plaintext only.** If the client negotiates TLS to the server, there is
  nothing to parse. Termination is your problem.
- **Statements are not transactions.** The gate evaluates each one
  independently, with no cross-statement session state.

## Testing

```bash
go test ./...           # unit
go test -race ./...     # concurrency
```

Each codec runs a split-read matrix: the tests feed the same message in two
chunks at every possible boundary, then assert that a fragment emits no
statement and that reassembly loses none.
