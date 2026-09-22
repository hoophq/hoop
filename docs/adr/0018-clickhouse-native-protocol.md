# ADR-0018: Clamp ClickHouse native protocol revisions at the sidecar

- **Status:** Proposed
- **Date:** 2026-09-17
- **Author:** @matheusfrancisco
- **Deciders:** TBD
- **Code:** `github.com/hoophq/libhoop/v2/codec/clickhouse`, [`sidecar/codec/clickhouse/`](../../sidecar/codec/clickhouse), [`sidecar/gate/`](../../sidecar/gate), [`sidecar/daemon/clickhouse.go`](../../sidecar/daemon/clickhouse.go)
- **Related:** [ADR-0005](0005-sidecar-flow.md) (the relay flow this codec joins), [ADR-0009](0009-guardrails-and-masking-architecture.md) (the enforcement and re-framing model), [ADR-0011](0011-sidecar-config-schema.md) (the listener schema this protocol extends)
- **Supersedes / Superseded by:** —

## Context

ClickHouse's native TCP protocol carries the query text and typed result
columns needed for guardrails, audit and response masking. Relaying the bytes
as opaque TCP would preserve connectivity and lose all three controls. Its HTTP,
MySQL and PostgreSQL interfaces do not replace native support: they expose
separate ports, client behaviours and protocol limitations, while
`clickhouse-client` and native drivers use port 9000.

The native protocol has no outer packet length. A packet is delimited only by
walking every field, and which fields exist depends on the protocol revision
both peers negotiate in their Hello packets. Reading one packet with the wrong
revision permanently loses the next packet boundary. Gateways must therefore
understand the negotiated layout exactly; best-effort parsing would allow a
statement to reach ClickHouse after inspection had silently stopped.

Results add a separate resource constraint. A Data packet contains a typed
block, optionally split across CityHash-protected LZ4 frames. Frame and block
sizes are peer-controlled. The sidecar must inspect and rebuild a block to mask
values, but it must not buffer a complete analytical result or allocate an
unbounded declared decompression size.

The dependency boundary is fixed. Protocol serialization belongs in the
private `libhoop` module. SQL classification, policy and process configuration
belong in `sidecar/`. `libhoop` remains a leaf and cannot import this repository.

## Options considered

1. **Keep native traffic opaque and document the HTTP or emulation ports.**
   Smallest implementation. It leaves the primary ClickHouse client path with
   no statement guardrails or masking, and the alternative ports have different
   authentication, framing and client compatibility.

2. **Decode whichever revision the peers advertise.** Preserves every current
   feature when the codec ships. It makes a newer client or server capable of
   selecting fields the deployed sidecar has never seen; one such field
   desynchronizes the rest of the connection. Continuous revision chasing is
   not a safe data-path compatibility policy.

3. **Terminate the ClickHouse session and originate a second one.** Gives the
   sidecar independent protocol versions and complete control over compression.
   It also makes the sidecar a ClickHouse client/server implementation, moves
   authentication into it and changes connection semantics. That scope is not
   needed to inspect a byte relay.

4. **Clamp both Hello revisions and relay one implemented vocabulary.** Both
   peers already select the minimum advertised revision. Rewriting both
   advertisements to a known revision uses ClickHouse's compatibility mechanism
   and keeps the sidecar a relay. Features newer than the pin are unavailable
   on that lane, but no peer can select a layout the codec cannot walk.

## Decision

We will support ClickHouse native TCP as an explicit `protocol: clickhouse`
sidecar listener and clamp both Hello revisions to `54450` before inspection or
forwarding.

The implementation follows these rules:

- **One duplex codec per connection.** Query compression and effective revision
  are learned client-side and determine how server packets are decoded. Both
  directions share one synchronized codec instance.
- **Clamp before decode and forward.** The codec exposes a stream filter. It
  buffers only an incomplete Hello head, rewrites revisions above `54450`, then
  becomes a pass-through. Both peers and both codec walkers see the same bytes.
- **Refuse an unreadable stream.** Unknown packet types, malformed framing,
  unsupported compression and revision-dependent flows outside the implemented
  ordinary query path return `ErrStreamUnsafe`. The gate closes the session
  rather than forwarding bytes it can no longer inspect.
- **Use ClickHouse's column implementation, not a second type system.** The
  codec uses `github.com/ClickHouse/ch-go/proto` to decode and encode native
  blocks. The sidecar injects its ClickHouse lexer and SQL analyzer through
  function values, preserving the one-way module dependency.
- **Bound compressed work before allocation.** The default limits are 16 MiB
  per compressed frame and 64 MiB per decompressed block. Declared compressed
  and decompressed frame sizes are checked before inflation. The codec provides
  a derived bounded wire-reassembly limit to the generic inspector, so a valid
  block above its 8 MiB default is not refused first. Buffers are reused one
  block at a time; a result set is never accumulated.
- **Support the protocol default compression.** Uncompressed and LZ4 blocks are
  accepted and CityHash checksums are verified. ZSTD is refused with an
  operator-facing instruction to use LZ4.
- **Rebuild masked blocks.** `String`, byte strings, `FixedString`,
  `Nullable(String)` and `LowCardinality(String)` are rewritten and re-encoded.
  A nested string shape the codec cannot rebuild, such as `Array(String)` or a
  string-bearing `Map` or `Tuple`, closes a masking-enabled session instead of
  returning cleartext.
- **Return a native denial.** A denied query receives ClickHouse Exception code
  `497` with the configured rule message. The denied Query packet is not sent
  upstream.
- **Treat TLS as TLS-on-connect.** `downstream_tls` terminates the client leg
  before the first native packet. `upstream_tls` independently verifies the
  server leg. Plaintext remains valid when either block is absent.
- **Do not add a feature flag.** Selecting `protocol: clickhouse` is the
  operator's explicit opt-in and leaves every existing listener unchanged.

## Guardrails and data masking use the existing configuration

This decision adds no ClickHouse-specific way to define guardrails or data
masking. A native listener uses the same top-level and per-listener
`guardrails` and `mask` sections defined by ADR-0011. Listener guardrails run
before and concatenate with inherited rules; a listener mode replaces the
inherited mode. Listener mask rules replace inherited mask rules.

The `clickhouse` section configures only codec resource limits:

```yaml
listeners:
  - name: warehouse
    protocol: clickhouse
    listen: 0.0.0.0:9000
    upstream: clickhouse:9000
    guardrails:
      mode: enforce
      rules:
        - name: no-destructive-clickhouse
          type: operation
          operations: [delete, drop, truncate]
          message: destructive ClickHouse statements are not permitted
    mask:
      rules:
        - name: customer-identifiers
          columns: [email, taxpayer_id]
          strategy: redact
    clickhouse:
      max_frame_bytes: 16777216
      max_block_bytes: 67108864
```

No new rule type, masking strategy or policy execution path is introduced.
The codec emits the existing `Statement` type for the existing guardrail chain
and uses the existing gate masking callback while rebuilding native result
blocks. `protocol: clickhouse` opts the listener into native decoding; it does
not create a separate ClickHouse policy vocabulary.

## Consequences

Native ClickHouse clients get the same request guardrails, audit records and
response masking as other protocol-aware database lanes. Modern and older peers
can connect because revision negotiation remains native; they use the pinned
feature set for that connection. Large analytical results remain streamable,
and peer-controlled decompression sizes have explicit per-listener bounds.

The pin is now a compatibility contract. Raising it requires implementing and
testing every intervening revision-gated field in both directions. Features
introduced above `54450`, ZSTD compression and unimplemented packet families
fail closed instead of degrading to opaque relay behaviour.

Masking support is intentionally narrower than ClickHouse's type system. Flat
string columns work; unsupported nested string containers require rewriting the
query to return flat strings or removing masking from that listener. Numeric
and other non-string columns pass through while a block is rebuilt.

`ch-go` becomes a direct dependency of `libhoop`, and the feature has a two-repo
release boundary: publish the `libhoop` codec first, then update the sidecar's
module version. Tests must cover split packets, both compression modes,
checksums, decompression limits, multi-frame blocks, masking, native denials and
a live `clickhouse-server` exchange.
