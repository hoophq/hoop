# hoopinspect

Turn raw database wire-protocol bytes into structured statements, and
structured statements into allow/deny verdicts.

**Not a proxy.** It never opens a socket, terminates TLS, or routes anything.
It is a pure function over bytes you already have, so whatever holds the
connection — Envoy, a sidecar, an agent — keeps holding it.

**Zero dependencies.** No `go.sum`. Standard library only, tests included.
Compiles to `GOOS=wasip1 GOARCH=wasm` for an Envoy network filter.

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

## Where this sits relative to Envoy

Envoy already parses some of this, and pretending otherwise is a bad way to
lose a technical review.

`envoy.filters.network.postgres_proxy` parses SQL from Postgres `Query` and
`Parse` messages, emits `statements_select/insert/update/delete` counters, and
produces dynamic metadata as `table.db → [operations]` that the RBAC network
filter can act on. Within Postgres, at table-and-verb granularity, that is a
real capability.

Here is the honest boundary:

| | Envoy | hoopinspect |
|---|---|---|
| Postgres SQL parse | `postgres_proxy`, best effort | full statement text |
| Postgres granularity | `table.db` + operation verb | statement, operation, tables |
| HTTP request | `ext_authz`: method, path, headers, bounded body | same, plus normalized resource |
| HTTP **response** | ✗ — ext_authz decides before the upstream is called | status, headers, body |
| **GraphQL** | ✗ — every call is `POST /graphql` | operation type, root fields, depth |
| Deny UX | RBAC/ext_authz drops or returns a bare 403 | operator-authored message |

On HTTP, Envoy's `ext_authz` is genuinely capable for request-side
authorization — the gaps there are narrower and named above. This is not
"Envoy is blind".

## Protocols

| Protocol | Messages decoded | Stateful |
|---|---|---|
| `postgres` | `Query` ('Q'), `Parse` ('P'); handshake skipped | no |
| `http` | HTTP/1.x requests and responses; GraphQL bodies | no |

MySQL, MSSQL and MongoDB codecs are **not shipped**. The `Codec` interface
and the shared SQL classifier are protocol-agnostic, so adding one is a new
`codec/<name>` package and nothing else; the earlier implementations were
removed to keep the surface to what is exercised end to end.

Import only what you need. A WASM filter that speaks Postgres imports
`codec/postgres` and never links the HTTP and GraphQL machinery:

```go
import _ "github.com/hoophq/hoopinspect/codec/postgres" // ~54 KB of wasm
import _ "github.com/hoophq/hoopinspect/codec/all"      // all four
```

## The Statement

```go
type Statement struct {
    Protocol  Protocol          // postgres | http
    Direction Direction         // client | server
    Text      string            // verbatim SQL, or the request line for HTTP
    Operation Operation         // select | delete | get | mutation | ...
    Tables    []string          // SQL relations, or HTTP resource / GraphQL root fields
    Database  string            // when the protocol states it
    HTTP      *HTTPDetail       // http only; nil for the wire-database codecs
    Metadata  map[string]string // protocol-specific, documented per codec
}
```

For SQL, `Operation` comes from a lexer that strips comments and string
literals first, so `SELECT 'DROP TABLE customers'` classifies as `select`.
That is why `MatchOperation` should be preferred to `MatchDenyWords` for
verbs.

## HTTP and GraphQL

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
like `/users/alice` is deliberately NOT collapsed — merging it with
`/users/settings` would silently widen every rule written against either. The
normalizer errs toward keeping segments, so a policy can only be too narrow,
never accidentally too broad.

**GraphQL is the point.** Every GraphQL call is `POST /graphql`, so a
method-and-path policy allows all operations or none:

```
query    { user(id: 1) { name } }   // a read
mutation { deleteUser(id: 1) }      // destroys a record
```

Identical at the ext_authz layer. The codec resolves operation type,
operation name, root fields and selection depth from the body. Aliases
resolve to the real field, so `{ harmless: deleteUser(id: 1) }` still reports
`deleteUser`, and string literals are dropped so `search(term: "deleteUser")`
does not.

**Data exposure is opt-in.** Bodies and headers are NOT captured by default.
A policy engine's decision log is a copy of everything you send it, and
`Options.Headers` is an allowlist with no "capture all" switch.

## Policy

Two evaluators, meant to be layered via `policy.Chain{local, opa}` so an
obviously-forbidden statement never costs a network round-trip.

**Local rules** — SQL: `deny_words_list`, `pattern_match` (RE2),
`operation`, `table`. HTTP: `http_resource`, `http_status`,
`graphql_operation`, `graphql_field`, `graphql_depth`. One ordered set can
mix both, so a deployment fronting a database and an API needs one evaluator:

```go
policy.NewRules([]policy.Rule{
    {Name: "no-drop", Type: policy.MatchOperation,
     Operations: []hoopinspect.Operation{hoopinspect.OpDrop}},

    policy.Rule{Name: "no-admin", Type: policy.MatchHTTPResource}.
        WithResources("/admin/**"),

    policy.Rule{Name: "read-only-graphql", Type: policy.MatchGraphQLOperation}.
        WithGraphQLOperations(hoopinspect.OpMutation).
        WithMessage("this credential may query but not mutate"),

    policy.Rule{Name: "depth-limit", Type: policy.MatchGraphQLDepth}.
        WithMaxDepth(10),
})
```

An HTTP rule never matches a SQL statement and vice versa, so a mixed set
cannot accidentally deny the wrong protocol.

**OPA** — posts to an OPA Data API endpoint. hoopinspect does not own policy;
it owns the *input document*:

```json
{"input": {
  "protocol": "http",
  "direction": "client",
  "operation": "mutation",
  "tables": ["deleteuser"],
  "http": {
    "method": "POST",
    "path": "/graphql",
    "resource": "/graphql",
    "graphql": {
      "operation_type": "mutation",
      "operation_name": "Nuke",
      "root_fields": ["deleteUser"],
      "depth": 3
    }
  },
  "context": {"user": "alice"}
}}
```

A strict superset of what `ext_authz` and `postgres_proxy` metadata provide,
in one shape for five protocols. Accepts `{"allow": bool}`,
`{"denied": bool}`, or a bare boolean, with an optional `message` and `rule`.

**Both fail closed.** An unreachable OPA, a 500, or an undefined decision
denies. Set `FailOpen` to invert where availability outranks enforcement.
There is no silent middle ground.

## Limits

Read these before writing a policy against it.

- **`Tables` is best effort.** A lexer, not a SQL grammar — the same ceiling
  Envoy's own docs acknowledge for `postgres_proxy`. Empty means "could not
  determine", **never** "touches nothing". Use `RequireTableMatch: true` on
  rules protecting something critical, and accept the false positives.
- **HTTP response inspection works; SQL response inspection does not.** For
  the database codecs `FromServer` bytes are consumed and yield no
  statements — the redaction path is unbuilt. The HTTP codec inspects both
  directions.
- **HTTP/1.x only for stream decoding.** HTTP/2 and HTTP/3 framing belongs to
  whatever terminated the connection; by then it has a `*http.Request`, so
  use `InspectRequest`.
- **GraphQL fragments are not expanded.** A root field reached only through a
  fragment spread is not listed in `RootFields`. Pair a field rule with an
  operation-type rule, which cannot be evaded that way. Batched GraphQL
  (`[{...},{...}]`) reports nothing rather than inspecting only the first
  operation.
- **Path normalization is conservative.** Numeric, UUID, hex and long opaque
  segments collapse; short slugs do not. A policy can be too narrow, never
  accidentally too broad.
- **Plaintext only.** If the client negotiates TLS to the server, there is
  nothing to parse. Termination is the caller's problem.
- **Statements are not transactions.** Each is evaluated independently; there
  is no cross-statement session state.

## Testing

```bash
go test ./...           # unit
go test -race ./...     # concurrency
GOOS=wasip1 GOARCH=wasm go build ./...
```

Every codec is tested against a split-read matrix: the same message is fed in
two chunks at every possible boundary, asserting no statement is emitted from
a fragment and none is lost on reassembly.
