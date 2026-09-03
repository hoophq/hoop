# ADR-0013: gRPC lanes terminate HTTP/2 in-process; no in-stream codec; descriptor sets required

- **Status:** Proposed
- **Date:** 2026-09-03
- **Author:** @matheusfrancisco
- **Deciders:** —
- **Code:** [`sidecar/inspect/`](../../sidecar/inspect), [`sidecar/codec/`](../../sidecar/codec), [`sidecar/gate/gate.go`](../../sidecar/gate/gate.go), [`sidecar/policy/`](../../sidecar/policy), [`sidecar/pii/alcatraz/masker.go`](../../sidecar/pii/alcatraz/masker.go), [`sidecar/proxy/`](../../sidecar/proxy)
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

Two constraints frame the options. `sidecar/go.sum` is two lines, both libhoop
(`sidecar/go.mod:1-24`), and `libhoop/v2/**` imports stdlib plus
`libhoop/v2/codec/types`; `hpack` and `protowire` sit outside stdlib, so
anything needing them lives in a nested module or not at all. TLS is the
second constraint: grpc-go refuses plaintext without an explicit
`insecure.NewCredentials()` and enforces ALPN on both ends
(`credentials/tls.go:149-153,179-183` at v1.83.0), so a standalone gRPC lane
must terminate TLS itself and advertise `h2`. `proxy/starttls.go` sets no
`NextProtos` today, on either leg.

## Options considered

1. **Full in-stream codec, `libhoop/v2/codec/grpc`.** The shape every other
   protocol has. Rejected for good: it needs a hand-rolled HPACK decoder to
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
   Costs a second execution model in `daemon/` and `x/net/http2/h2c` plus
   `protobuf-go` in a nested module. Chosen.
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
operator's text) while sibling streams keep flowing. The implementation lives
in a nested module alongside `pii/alcatraz` and `analyzer/vertex`, carrying
`x/net/http2/h2c` and `protobuf-go`; `sidecar/go.sum` stays two lines and the
`wasip1` target survives.

**We will not ship a `codec/grpc`, now or later.** The Codec abstraction is
bytes-in/statements-out over a stream the relay forwards concurrently. The
Context section shows that shape cannot deny a multi-read message, cannot deny
per-stream, and cannot mask. The abstraction carries those limits; no codec
implementation removes them.

**The Gate gains a statement-level entry point.** The h2 lane enters at parsed
messages, the `InspectRequest`/`InspectResponse` shape libhoop designed for
this entry and that has no production caller today
(`libhoop/v2/codec/http/http.go:26-34`). An `ext_proc` front end added later
enters at the same point.

**We will require a descriptor set for any payload inspection.** A gRPC lane
declares `grpc.descriptors`, a serialized `FileDescriptorSet` produced by
`protoc --include_imports --descriptor_set_out` or `buf build -o`. No
schema-less protobuf inspection ships. A lane without one may still authorize
by method and record the trail; it may not mask and may not claim PII
detection.

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

**One Statement per RPC by default.** The lane emits one at the request
headers and a second at the trailers carrying `grpc-status`. Per-message
statements appear only when payload capture is on. A Kubernetes `Watch` is one
RPC and unbounded messages; the default audit trail is one row per call, not
one per frame.

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
    maskable response fields: amount_cents (int64), card_last4 (string),
      customer.email (string), customer.full_name (string),
      customer.id (string), customer.tax_id (string), id (string), labels.value
/billing.v1.Invoices/ListInvoices
    response billing.v1.ListInvoicesResponse
    maskable response fields: invoices.amount_cents (int64), ...,
      invoices.customer.tax_id (string), next_page_token (string)
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
          resources: ["/billing.v1.Invoices/Export*"]
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
          resources: ["/billing.v1.Invoices/Export*"]
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
        # Method identity, glob. One rule covers ExportAll and any Export*
        # added later.
        - name: no-bulk-export
          type: http_resource
          resources: ["/billing.v1.Invoices/Export*"]
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

**Harder.** `daemon/` forks into two execution models: every lane today is a
`proxy.Server`, and a grpc lane is an `http.Server` with its own lifecycle,
idle handling, max-conns and `/config` reporting. Operators provision a server
certificate per standalone gRPC lane, because the lane terminates TLS itself.
The nested module carries `x/net` and `protobuf-go`. Interposition still
breaks upstream mTLS: an upstream that authenticates callers by client
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
for the rest. (b) The audit rate proves unworkable: audit is written per
statement, synchronously, before forwarding (`gate/gate.go:381-397`), and
`session.New` is per connection (`proxy.go:338`); a client that holds one
HTTP/2 connection for days produces one session with millions of statements.
The unit that means "one RPC" is the stream, and neither `session/` nor
`audit/` knows what a stream is. That is a separate decision this ADR does not
make.

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

**Seam**: a package under `sidecar/codec/` that registers a libhoop decoder
and injects the collaborators libhoop may not import. gRPC has no seam; that
absence is this ADR.

**Reframer**: the optional codec capability to rebuild a length-prefixed
stream around masked values. `gate.MaskSupported` tests for it, and the answer
decides whether a relay lane may carry mask rules.

**Descriptor set**: a serialized `google.protobuf.FileDescriptorSet`, the
`.proto` files compiled to protobuf. _Avoid:_ "schema", "proto files", "IDL".

**Content-discovered**: masking driven by a detector over values. Catches the
field nobody labelled; needs no schema.

**Schema-declared**: masking driven by an operator naming a field path.
Exact, and blind to anything unnamed.

**Mask**: replace a value with a marker that preserves its shape
(`[REDACTED:US_SSN]`). _Avoid:_ "scrub", Envoy's word, which means remove the
field. A removed field is indistinguishable from an absent one, which is the
reason to keep the two words apart.
