# ADR-0013: gRPC lanes terminate HTTP/2 in-process; no in-stream codec; descriptor sets required

- **Status:** Accepted
- **Date:** 2026-09-03
- **Author:** @matheusfrancisco
- **Deciders:** —
- **Code:** [`libhoop/v2/codec/grpc/`](../../libhoop/v2/codec/grpc), [`libhoop/v2/codec/types/`](../../libhoop/v2/codec/types), [`sidecar/daemon/grpc.go`](../../sidecar/daemon/grpc.go), [`sidecar/gate/gate.go`](../../sidecar/gate/gate.go), [`sidecar/policy/grpc.go`](../../sidecar/policy/grpc.go)
- **Related:** [ADR-0005](0005-sidecar-flow.md) (the relay flow gRPC deviates from), [ADR-0009](0009-guardrails-and-masking-architecture.md) (the masking architecture this extends), [ADR-0011](0011-sidecar-config-schema.md) (the config schema these keys join)
- **Supersedes / Superseded by:** —

## Context

Every protocol the sidecar speaks arrives as a codec: a decoder in
`libhoop/v2/codec/<name>`, a registration seam in `sidecar/codec/<name>`, and a
lane that pumps bytes through a Gate. `inspect.Codec` is two methods
(`inspect/inspect.go:53-65`), the registry hands out a factory per connection
(`inspect/registry.go:33-49`), and the README says adding a protocol "takes a
new `codec/<name>` package and no other change" (`sidecar/README.md:877-879`).

gRPC breaks that sentence. One requirement frames the decision: gRPC
inspection must work without Envoy. Four measurements narrow the options. The
probes behind them live in `/Users/chico/m/projects/ai/sidecar-grpc-inspection`.

**Today a gRPC client on an `http` lane is a silent bypass.** The HTTP/2 client
preface `PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n` parses as a valid HTTP/1.1 request
line. The codec emits one junk statement with `Operation: other`, a value no
shipped rule set names, so the gate allows it, then errors on the SETTINGS
frame. The gate treats a decode error as "forward the bytes, the upstream's
parser is the authority" (`gate/gate.go:346-351`), leaving `d.Allowed` and
`d.Payload` untouched from `:337`, and `Inspector` drops its buffer
(`inspect/inspect.go:178-183`). The connection works, the relay logs one error
per read, and no statement reaches policy or audit. The fix is a separate
change: the HTTP codec returns `ErrStreamUnsafe` on the preface. This ADR
records the finding because it started the investigation.

**The relay is not a barrier.** A body split across two reads, through a real
Gate with capture on: 65 bytes reach the upstream before any statement exists,
and the verdict lands on the second chunk. `gate.inspect` sets
`Payload: data` before inspecting and only nils it on a deny. The SQL codecs
avoid this race because a `Query` message arrives whole in one read. A gRPC
message spans many DATA frames across many reads, so payload-based denial
wins the race only when the message fits in one read.

**Deny is connection-fatal, and HTTP/2 multiplexes.** `proxy.go:551-570` writes
the deny frame and returns; both pumps unwind and both sockets close.
`gate.Decision` has no stream id and `DenyWriter.Deny` has no scope
(`proxy/proxy.go:49-51`). One connection carries N concurrent RPCs, so denying
one kills the rest.

**Masking protobuf by substitution destroys the message.** Byte-substituting a
value in a real `PreConnectRequest`: 40 bytes become 46, and the client reports
`proto: cannot parse invalid wire-format data` after reading a truncated
`"[REDACTED:U"`. Protobuf length prefixes nest, so a longer value invalidates
its own prefix, its map entry's, and every enclosing message's: the failure
`postgres/rewrite.go:37-47` describes for DataRow, one layer deeper. A relay
that changes a DATA payload's length also diverges the two peers' flow-control
windows, so a masking relay must rewrite WINDOW_UPDATE frames too. At that
point it is an HTTP/2 endpoint pretending to be a pipe.

**Schema-less protobuf loses values as a function of their bytes.** Walking a
real marshalled message with a `protoc --decode_raw`-style heuristic,
`"123-45-6789"` parsed as a nested message: `0x31` reads as "field 6, wire type
1", which eats eight bytes, and `0x38 0x39` reads as "field 7 = 57". The SSN
never reached the string set a detector sees; the email in the same message
did. Flipping the heuristic to prefer printable strings recovers the SSN and
instead mangles any nested message whose bytes are all printable, which is any
message holding one string field of 32 to 126 characters. Both orderings are
unsound, in opposite directions.

Two constraints frame the options. The root sidecar module declares only
`github.com/hoophq/libhoop`; the dependency edge must continue to point from
sidecar to libhoop, never back. Go 1.26's standard library serves TLS HTTP/2
and plaintext h2c, so the reusable endpoint can live with the descriptor,
framing, rendering, masking, and status mechanics in
`libhoop/v2/codec/grpc`. Sidecar behavior is injected through callbacks. TLS
is the second constraint: grpc-go refuses plaintext without an explicit
`insecure.NewCredentials()` and enforces ALPN on both ends
(`credentials/tls.go:149-153,179-183` at v1.83.0), so a standalone gRPC lane
must terminate TLS itself and advertise `h2`. `proxy/starttls.go` sets no
`NextProtos` today, on either leg.

## Options considered

1. **Full in-stream `inspect.Codec` implementation in
   `libhoop/v2/codec/grpc`.** The shape every other protocol has. Rejected for good: it needs a hand-rolled HPACK decoder to
   keep the two-line `go.sum`, it inherits connection-fatal denials, and it
   can never mask, because the flow-control fact above forces a rewriting
   relay to become an HTTP/2 endpoint anyway. At that point the codec is more
   code for less capability. It also fails the standalone requirement:
   clients speak TLS, so a plaintext-only codec still needs a terminator in
   front.
2. **Headers-only in-stream codec.** A third of option 1's cost: method
   identity, `grpc-timeout`, `grpc-status`, deny before the body reaches the
   upstream. Rejected with option 1, and on product grounds: Istio
   `AuthorizationPolicy` matches `paths: ["/pkg.Service/Method"]` with SPIFFE
   principals. Shipping method-level authorization alone reproduces the mesh.
3. **Terminate HTTP/2 in the sidecar and reverse-proxy.** `net/http` owns
   HPACK, framing, flow control and multiplexing; per-stream deny and sound
   masking become ordinary code; it runs with no Envoy and no fronting proxy.
   Costs a second execution model in `daemon/` and a callback seam between
   libhoop's transport and the sidecar's policy, audit, and masking stack. Chosen.
4. **An `ext_proc` service.** Envoy does the protocol work; the sidecar
   supplies detection, policy, audit and the denial message. The cheapest
   path when Envoy is present, and the requirement says Envoy may be absent.
   Deferred: it enters at the statement-level gate entry option 3 builds, so
   adding it later is a second front end rather than a rewrite. Building in
   the other order would not compose.
5. **Schema-less protobuf, no descriptor set.** Zero operator work. Rejected:
   the probes above show it loses PII as a function of payload bytes and
   cannot mask soundly. Shipping it would mean advertising PII detection that
   works until the value it protects happens to parse as a message.
6. **gRPC server reflection instead of a descriptor set.** Self-updating, no
   config. Rejected: most production services disable it, it makes the sidecar
   a gRPC client, and a schema fetched from the service you are policing is a
   trust boundary nobody asked for.

## Decision

**A `grpc` lane terminates HTTP/2 in-process and reverse-proxies to its
upstream; the byte-pump relay plays no part.** The lane terminates client TLS
advertising `h2` via ALPN (h2c serves a lane behind a plaintext front),
originates upstream TLS with `h2` in `NextProtos`, decodes payloads through
the descriptor set, masks, re-encodes, and denies a single stream with a
trailers-only response (`grpc-status: 7`, `grpc-message` carrying the
operator's text) while sibling streams keep flowing. The HTTP/2 endpoint,
descriptor loading, frame handling, protobuf rendering, masking, and status
handling live in `libhoop/v2/codec/grpc`. The sidecar's Gate, policy, audit,
identity, and Statement adapter live in `sidecar/daemon` and are injected
through libhoop callbacks. The dependency edge remains one-way.

**`libhoop/v2/codec/grpc` is a protocol package, not an `inspect.Codec`
implementation.** The Codec abstraction is
bytes-in/statements-out over a stream the relay forwards concurrently. The
Context section shows that shape cannot deny a multi-read message, cannot deny
per-stream, and cannot mask. The abstraction carries those limits; no codec
implementation removes them. `libhoop/v2/codec/types.GRPC` is the
canonical protocol name, while registry lookup remains unsupported on
purpose.

**The Gate gains a statement-level entry point.** The h2 lane enters at parsed
messages, the `InspectRequest`/`InspectResponse` shape libhoop designed for
this entry and that has no production caller today
(`libhoop/v2/codec/http/http.go:26-34`). An `ext_proc` front end added later
enters at the same point.

**We will require a descriptor set for any payload inspection.** A gRPC lane
declares `grpc.descriptors` — one serialized `FileDescriptorSet` produced by
`protoc --include_imports --descriptor_set_out` or `buf build -o`, or a LIST
of them for the multi-team shape where every service's CI ships its own
artifact and no central re-bundle pipeline exists. Sets merge into one
schema at startup: a shared import embedded in several sets dedupes when
the copies are byte-identical, and two DIVERGED copies of one file, or two
sets defining the same service, refuse to load with an error naming both
artifacts — never a silent winner, because the loser is somebody's payloads
misdescribed. No schema-less protobuf inspection ships. A lane without any
set may still authorize by method and record the trail; it may not mask and
may not claim PII detection.

**`columns:` names protobuf field paths.** `Masker.MaskCell(column, value)`
(`gate/gate.go:53-62`) means "the protocol named this value, here it is"; the
masker keys `byColumn` on the lowercased name (`masker.go:258-259`). The lane
passes `customer.tax_id` as `column` and the masker needs no change. Maps use
`labels.value` with keys preserved; repeated fields are addressed through the
element. We will not add a `fields:` key.

**gRPC statements carry no operation.** `Operation` is `OpCall` for every RPC
and no rule may key on it. We refuse method-name heuristics (`Get*` means
read): a false negative such as `GetAndPurge` or `ListAndArchive` is a write
classified as a read on a read-only lane, and a naming convention proves
nothing about side effects. `idempotency_level` is the honest signal, defaults
to `IDEMPOTENCY_UNKNOWN`, and few services set it, so it degrades to the
heuristic. Policy gets an identity axis instead: method globs through
`http_resource` against an **un-normalized** resource, plus one new
`grpc_status` type, because `grpc-status` lives in the trailers and `:status`
is 200 on every live RPC.

**Two lifecycle Statements per RPC by default.** The lane emits one at the
request headers and one at the trailers carrying `grpc-status`. Per-message
statements appear only when payload capture is on. A Kubernetes `Watch` is one
RPC and unbounded messages; the default audit trail is two rows per call, not
one per frame. Each RPC is also one audit session: an HTTP/2 connection is a
poor audit boundary for multiplexed outcomes.

**All four RPC shapes flow through one pipeline: unary, server-streaming,
client-streaming, and bidirectional.** The endpoint is a full-duplex HTTP/2
reverse proxy flushing every frame as it arrives, and inspection is
per-message in each direction (`FrameReader` + the message callbacks in
`libhoop/v2/codec/grpc/server.go`), so a statement lands when its frame
does, not when the stream ends. Nothing keys on the descriptor's streaming
bits: a `stream` method needs no extra configuration and cannot be refused
for being one. Mid-stream denial granularity follows direction — a denied
request message is withheld from the upstream and the RPC ends with the
operator's trailers; a denied response message ends only that stream, and
sibling RPCs on the connection keep flowing. The lifecycle rows above and
the statement-volume consequence below are what long-lived streams change;
the data path is the same code for all four shapes.

**A descriptor mismatch degrades or refuses, and `grpc.strict` picks
which — except for masking, which always refuses.** By default the
descriptor set is validated at startup (a missing or malformed file still
refuses to load), and at runtime a capturing lane DEGRADES on what it
cannot read: an RPC whose path the set does not define is forwarded with
method-level inspection only, and a message that does not decode as its
declared type travels uninspected — each degradation logged, never
silent. With `grpc.strict: true` those become refusals: the undescribed
method is refused BEFORE the upstream is dialed — `FAILED_PRECONDITION
(9)`, "the descriptor set does not define /pkg.Service/Method" — and the
undecodable message ends the RPC with `INTERNAL (13)`, the offending
frame withheld from whichever side it was crossing to. `strict` stands on
its own: it requires a descriptor set and enforces it with or without
capture — a strict lane that captures nothing still verifies every
message decodes, emits no statement for it, and refuses what it cannot
read. Masking is the carve-out and it is not configurable: a lane with
mask rules refuses undescribed methods and undecodable responses
regardless of `strict`, because a redactor that forwards a payload it
cannot decode has leaked the very bytes it exists to rewrite. A lane with
neither capture, masking nor `strict` forwards everything the set does
not name (or runs with no set at all), with method-level policy and the
two lifecycle statements still applying — the "may still authorize by
method" tier above. The rule: capture visibility may degrade loudly when
the operator accepts that; redaction never may, and `strict` is the
operator refusing degradation outright.

## Usage

Both deployments share the `guardrails` and `mask` blocks, because both
produce the same statements. Each file below holds one guardrail rule and one
mask rule, so it loads under the build caps (`daemon/limits.go:37-40`).

### The schema behind these examples

Every example fronts this service, and `billing.pb` is its compiled form:

```proto
syntax = "proto3";
package billing.v1;

service Invoices {
  rpc GetInvoice   (GetInvoiceRequest)   returns (Invoice);
  rpc ListInvoices (ListInvoicesRequest) returns (ListInvoicesResponse);
  rpc ExportAll    (ExportRequest)       returns (stream Invoice);
}

message GetInvoiceRequest   { string invoice_id = 1; }
message ExportRequest       { string since = 1; }
message ListInvoicesRequest { string customer_id = 1; int32 page_size = 2; }
message ListInvoicesResponse {
  repeated Invoice invoices = 1;
  string next_page_token    = 2;
}

message Customer {
  string id        = 1;
  string full_name = 2;
  string email     = 3;
  string tax_id    = 4;
}

message Invoice {
  string   id           = 1;
  Customer customer     = 2;
  int64    amount_cents = 3;
  string   card_last4   = 4;
  map<string, string> labels = 5;
}
```

The operator compiles it in the CI job that builds the service:

```bash
protoc --include_imports --descriptor_set_out=billing.pb billing.proto
buf build -o billing.pb        # equivalent; includes imports by default
```

`--include_imports` is load-bearing. Without it the set omits transitive
dependencies and the lane refuses to start with
`could not resolve import "google/protobuf/timestamp.proto": not found`,
which beats degrading to a schema-less walk whose `columns:` rules can no
longer fire.

`-validate` prints the field paths the set makes maskable, so a typo in a
`columns:` value is a diff instead of a rule that never fires:

```
/billing.v1.Invoices/GetInvoice
    request  billing.v1.GetInvoiceRequest
    response billing.v1.Invoice
    maskable response fields: card_last4, customer.email, customer.full_name,
      customer.id, customer.tax_id, id, labels.value
/billing.v1.Invoices/ListInvoices
    response billing.v1.ListInvoicesResponse
    maskable response fields: invoices.card_last4, ...,
      invoices.customer.tax_id, next_page_token
/billing.v1.Invoices/ExportAll
    response billing.v1.Invoice           (stream)
```


### Without Envoy

```
grpc client ──TLS+ALPN h2──▶ grpc lane ──TLS+ALPN h2──▶ billing:50051
                             (sidecar)
```

The lane owns both TLS legs. `downstream_tls` already exists with exactly the
fields this needs (`cert_file`, `key_file`; `daemon/config.go:179-192`); this
ADR widens its protocol gate, which today refuses everything except postgres
(`config.go:834-839`), to accept grpc lanes, where the lane presents the
certificate on connect and advertises `h2`.

```yaml
listeners:
  - name: billing
    protocol: grpc
    listen: 0.0.0.0:8443
    upstream: billing:50051
    downstream_tls:               # the lane is the TLS endpoint
      cert_file: /etc/hoop/tls/billing.crt
      key_file:  /etc/hoop/tls/billing.key
    upstream_tls: {}              # originate TLS to the backend, ALPN h2
    grpc:
      descriptors: /etc/hoop/descriptors/billing.pb
    guardrails:
      rules:
        - name: no-bulk-export
          type: http_resource
          resources: ["/billing.v1.Invoices/ExportAll"]
          message: bulk export is not permitted through this proxy
    mask:
      rules:
        - {name: cards, entities: [CREDIT_CARD], strategy: redact}
```

The client connects to the lane instead of the service and trusts the lane's
certificate. An upstream that requires client certificates from callers does
not work behind this lane; the Consequences section records that limit.

### Without Envoy, plaintext downstream

`downstream_tls` is optional. Omit it and the lane listens for cleartext
HTTP/2 (h2c). Two conditions apply. The client must opt into plaintext,
because grpc-go refuses cleartext without an explicit
`insecure.NewCredentials()` and grpcurl needs `-plaintext`; a client left on
its TLS defaults fails the dial. And the hop must be one you trust, because
gRPC carries credentials in metadata (`authorization: Bearer ...`) and a
plaintext hop exposes them to anything on the path.

Same host or same pod satisfies both, which is the sidecar-container pattern
this module is named for: the app dials loopback or a unix socket in
plaintext, the plaintext hop never leaves the pod, and the lane originates
TLS upstream.

```
app ──h2c, loopback/unix──▶ grpc lane ──TLS+ALPN h2──▶ billing:50051
```

```yaml
listeners:
  - name: billing
    protocol: grpc
    network: unix                 # or listen: 127.0.0.1:18443
    listen: /var/run/hoop/billing.sock
    upstream: billing:50051
    upstream_tls: {}
    grpc:
      descriptors: /etc/hoop/descriptors/billing.pb
```

Keep an h2c listener off shared networks. A lane bound to 0.0.0.0 with no
`downstream_tls` hands every bearer token on it to the network, and nothing
in the config refuses that combination; the unix-socket form removes the
port entirely, the same argument [Transport](../../sidecar/README.md)
already makes for the other protocols.

### With Envoy

```
grpc client ──TLS h2──▶ Envoy ──h2c, loopback──▶ grpc lane ──TLS h2──▶ billing:50051
```

Envoy keeps TLS and identity, the posture every other lane already assumes,
and forwards cleartext HTTP/2 to the lane. The lane drops `downstream_tls`,
binds loopback or a unix socket, and reads the subject Envoy verified from a
header (`identity_header`, `config.go:194-201`; safe here because only Envoy
can reach the listener).

```yaml
listeners:
  - name: billing
    protocol: grpc
    listen: 127.0.0.1:18443       # or network: unix + a socket path
    upstream: billing:50051
    identity_header: x-hoop-user
    upstream_tls: {}
    grpc:
      descriptors: /etc/hoop/descriptors/billing.pb
    guardrails:
      rules:
        - name: no-bulk-export
          type: http_resource
          resources: ["/billing.v1.Invoices/ExportAll"]
          message: bulk export is not permitted through this proxy
    mask:
      rules:
        - {name: cards, entities: [CREDIT_CARD], strategy: redact}
```

Envoy's cluster for the lane must speak HTTP/2 cleartext:

```yaml
clusters:
  - name: hoop_grpc_lane
    typed_extension_protocol_options:
      envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
        "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
        explicit_http_config:
          http2_protocol_options: {}     # h2c toward the lane
    load_assignment:
      cluster_name: hoop_grpc_lane
      endpoints:
        - lb_endpoints:
            - endpoint:
                address: {socket_address: {address: 127.0.0.1, port_value: 18443}}
```

The deferred `ext_proc` mode (option 4) is a third shape, where Envoy holds
both application sockets and calls the sidecar as a processing service. When
it lands, the `guardrails` and `mask` blocks above move over unchanged; the
listener block swaps `listen`/`upstream` for the address the ext_proc service
binds.

### The fuller rule surface, against this schema

The deployment examples stay at one rule each to load under the build caps.
This block shows the combinations the schema supports; like the README's own
worked config, it exceeds the caps as written and exists to teach the axes.

```yaml
listeners:
  - name: billing
    protocol: grpc
    listen: 127.0.0.1:18443
    upstream: billing:50051
    upstream_tls: {}
    grpc:
      descriptors: /etc/hoop/descriptors/billing.pb
      capture_payload: true       # required by the pii rule below; off, and
      max_payload_bytes: 65536    # masking still works while policy sees no payload
      metadata: [x-tenant-id]

    guardrails:
      rules:
        # Method identity, exact.
        - name: no-bulk-export
          type: http_resource
          resources: ["/billing.v1.Invoices/ExportAll"]
          message: bulk export is not permitted through this proxy

        # Service identity, exact. Tables carries billing.v1.invoices the way
        # resourceTables carries the HTTP resource, so a table rule fences a
        # whole service.
        - name: invoices-only
          type: table
          tables: [billing.v1.invoices]
          require_table_match: true
          message: only the Invoices service crosses this lane

        # Payload content. Scans Text, which holds the rendered payload only
        # under capture_payload: true; without capture this rule loads and
        # matches nothing.
        - name: no-taxpayer-ids-in-requests
          type: pii
          entities: [US_SSN]
          message: do not send a taxpayer id; it lands in the upstream's logs

        # Outcome, from the trailers. grpc-status is the RPC's result;
        # :status is 200 on every live RPC. defer records a finding for OPA
        # and the audit trail instead of denying.
        - name: authz-failures-are-findings
          type: grpc_status
          statuses: [permission_denied, unauthenticated]
          action: defer

    mask:
      rules:
        # Schema-declared, exact: the operator names the field, the whole
        # value is rewritten, the one entity is the audit label. Needs
        # grpc.descriptors; refused at startup without it. Paths anchor at
        # the response message root, so the ListInvoices response names the
        # same field through its repeated element.
        - {name: taxpayer, entities: [US_SSN],
           columns: [customer.tax_id, invoices.customer.tax_id],
           strategy: partial, keep_last: 4}      # 123-45-6789 -> ***-**-6789

        # A field no detector recognizes. Detecting a person's name takes
        # NER, which this module does not wire; naming the column is the only
        # way to protect it. The label is free-form because a column rule
        # runs no detector.
        - {name: names, entities: [PERSON_NAME], columns: [customer.full_name],
           strategy: redact}                     # -> [REDACTED:PERSON_NAME]

        # Stable pseudonym: hash keeps the value joinable across responses
        # without revealing it.
        - {name: customer-ids, entities: [CUSTOMER_ID], columns: [customer.id],
           strategy: hash}                       # -> sha256:9f86d081884c7d65

        # Map values, keys preserved. The .value suffix is the whole map
        # convention.
        - {name: label-values, entities: [INTERNAL_LABEL], columns: [labels.value],
           strategy: mask}                       # -> ********

        # Content-discovered, no schema needed: the detector scans every
        # string value, so the card number pasted into a label or a field
        # added next sprint is caught without a rule change.
        - {name: cards, entities: [CREDIT_CARD], strategy: redact}
```

Paths anchor at the root of each method's response message, which is the
shape `-validate` prints: `customer.tax_id` covers `GetInvoice` and
`ExportAll`, and `invoices.customer.tax_id` covers `ListInvoices`, with
repeated fields addressed through the element. Anchoring at the message type
instead (one path covering `billing.v1.Customer.tax_id` everywhere it
appears) would halve the rule; it also breaks the moment the operator wants
the field masked in one method and visible in another, so root anchoring is
the recorded choice. The last rule needs no path at all: it fires wherever a
detector matches a value, which is the half of the product no field
enumeration replaces.


## Consequences

**Easier.** Standalone deployments work: the lane owns both TLS legs and needs
nothing in front of it. Masking is sound. The lane decodes, rewrites, and
re-encodes with every nested length prefix recomputed, and skips flow-control
bookkeeping because `net/http` is the endpoint on both sides. Per-stream
denial is a trailers-only response the client renders as `PermissionDenied`
with the operator's message, the cleanest denial of any shipped protocol. We
hand-roll none of the code RFC 7541 makes dangerous.

**Harder.** The daemon supports two execution models: relay lanes use a
`proxy.Server`, while a grpc lane configures the HTTP/2 endpoint in
`libhoop/v2/codec/grpc` and injects sidecar policy, audit, identity, and masking
through callbacks. Libhoop owns the grpc lane's lifecycle, idle handling,
connection limit, and stats. Operators provision a server certificate per
standalone gRPC lane, because the lane terminates TLS itself. Interposition
still breaks upstream mTLS: an upstream that authenticates callers by client
certificate sees the lane's certificate. No deployment shape fixes that, so we
document the limit instead of working around it. `stripChannelBinding`
(`starttls.go:129-184`) is the SCRAM precedent; mTLS offers no equivalent
downgrade.

**Committed to.** Owning an HTTP/2 endpoint surface: h2c, ALPN, per-stream
lifecycle, and the descriptor pipeline. Differentiating on content-discovered
masking. Envoy's `proto_api_scrubber` (absent in v1.34.0, present on
1.40.0-dev) removes fields an operator enumerated per method, and its matchers
see headers and CEL attributes only, so it cannot express "remove this field
if its value looks like an SSN". We can, standalone or composed. Method-level
authorization belongs to the mesh.

**Revisit if.** (a) Every target deployment turns out Envoy-fronted: add the
`ext_proc` front end (option 4) onto the same gate entry and keep the h2 lane
for the rest. (b) The audit rate for payload capture proves unworkable: the
sidecar opens one callback handler and audit session per RPC, while each
captured request or response message is another statement. A streaming RPC
that runs for days can therefore produce millions of statements in one
session. Sampling or aggregating those message statements is a separate
decision this ADR does not make.

## Terms

**Lane**: one listener and the policy, mask rules and codec options it
resolved at startup. _Avoid:_ "listener" for the resolved whole; that is the
config block.

**Statement**: the inspected unit a policy evaluates. One SQL statement, one
HTTP request or response, one gRPC call. _Avoid:_ "request", "message",
"event"; the last is the audit record of a statement.

**Gate**: the component that orders inspection, policy, audit and masking into
one allow/deny answer.

**Relay lane**: a lane whose transport is the byte-pump `proxy.Server`. Every
protocol before this ADR. A gRPC lane is not one.

**Seam**: the dependency-inversion boundary where sidecar behavior enters
libhoop without a reverse import. For gRPC, `libhoop/v2/codec/grpc` exposes RPC
callbacks and `sidecar/daemon` implements and injects them; there is no
`sidecar/codec/grpc` registration package.

**Reframer**: the optional codec capability to rebuild a length-prefixed
stream around masked values. `gate.MaskSupported` tests for it, and the answer
decides whether a relay lane may carry mask rules.

**Descriptor set**: a serialized `google.protobuf.FileDescriptorSet`, the
`.proto` files compiled to protobuf. _Avoid:_ "schema", "proto files", "IDL".

**Payload capture** (`grpc.capture_payload`): the per-lane switch that turns
decoded messages into per-message Statements. On, each request message is
rendered (proto-name JSON, truncated at `max_payload_bytes`) and evaluated
BEFORE its frame is forwarded, each response message after masking, and the
renderings reach everything that reads content: `pii` and `pattern_match`
rules, the OPA input document, the audit trail (one row per message), and a
configured analyzer. It also refuses compressed request payloads and strips
`Grpc-Accept-Encoding`, because the lane cannot inspect what it cannot
decode. Off — the default — only the two lifecycle Statements exist, so
policy sees method identity and outcome, never content. Masking does not
depend on it. It is the DATA-EXPOSURE opt-in, which is why no rule enables
it implicitly: a pii rule inherited from the process defaults must not
silently start copying payloads into the evidence pipeline. _Avoid:_
"logging", "sniffing"; capture feeds policy and evidence, not a packet dump.

**Content-discovered**: masking driven by a detector over values. Catches the
field nobody labelled; needs no schema.

**Schema-declared**: masking driven by an operator naming a field path.
Exact, and blind to anything unnamed.

**Mask**: replace a value with a marker that preserves its shape
(`[REDACTED:US_SSN]`). _Avoid:_ "scrub", Envoy's word, which means remove the
field. A removed field is indistinguishable from an absent one, which is the
reason to keep the two words apart.
