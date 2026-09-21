# ADR-0020: Sidecar state is connection-scoped; scale by L4 connection affinity

- **Status:** Proposed
- **Date:** 2026-09-21
- **Author:** @matheusfrancisco
- **Deciders:** TBD
- **Code:** [`sidecar/inspect/registry.go`](../../sidecar/inspect/registry.go), [`sidecar/session/`](../../sidecar/session), [`sidecar/proxy/`](../../sidecar/proxy), [`sidecar/store/`](../../sidecar/store), [`deploy/docker-compose/envoy-stack/envoy/envoy.yaml`](../../deploy/docker-compose/envoy-stack/envoy/envoy.yaml)
- **Related:** [ADR-0005](0005-sidecar-flow.md) (the relay flow and the Envoy topology), [ADR-0013](0013-grpc-terminates-http2-in-process.md) (the deferred `ext_proc` shape), [ADR-0014](0014-sidecar-config-hot-reload.md) (why a fleet-wide config rollout is a file copy)
- **Supersedes / Superseded by:** —

## Context

The sidecar parses database and HTTP wire protocols as a relay. Every
protocol it supports carries state that only exists inside one TCP
connection and that a later byte cannot be read without:

| Protocol | State a later byte depends on |
|---|---|
| postgres | `RowDescription` describes every following `DataRow`; `Parse`/`Bind`/`Execute` split one statement across messages |
| mysql | capabilities negotiated in the handshake change what later bytes mean; the codec must know the last client command to read the reply |
| mssql (TDS) | the relay reads through an encrypted login; TDS 7.4 wraps TLS inside PRELOGIN |
| gRPC / HTTP/2 | HPACK dynamic table and stream state |
| HTTP/1 | keep-alive framing only (`Content-Length`, chunked) |

Messages straddle TCP reads, so a relay cannot inspect a packet in
isolation. This is why `inspect.Register` hands out a codec factory, not
an instance (`sidecar/CLAUDE.md`, "Codecs register a factory"): two
connections sharing a codec would corrupt each other's reassembly and
leak one tenant's SQL into another's audit trail.

The sidecar also holds a second kind of state: the audit read side,
`store/` (bounded memory ring or `store/sqlite`) that serves
`GET /api/sessions` on the admin listener. It is derived from the audit
stream and is not consulted on the data path.

Customers deploy the sidecar three ways (`sidecar-binary.md`): as a
process exec'd into an agent container, as a container with its own
lifecycle, or as a bare binary under systemd. Some front it with Envoy
(`envoy-stack`), some with HAProxy or a cloud NLB, some with nothing.
They ask two questions: "does a client have to keep hitting the same
sidecar?" and "how do I run more than one?". The answers must be the
same for every deployment shape and every protocol, or the operator has
to learn the relay's internals to size it.

Constraints:

- The relay is one Go process with no peers; there is no control plane
  in the data path, and ADR-0016's control plane hands out licenses and
  config, not connection routing.
- ADR-0005 decision 1 commits to Envoy as an unmodified proxy: no
  `ext_proc`, no WASM, no custom filter.
- Response masking rewrites frames and needs the column layout from
  earlier in the same stream. A design that loses that layout loses
  masking, which is the response-side control Envoy cannot provide.

## Options considered

1. **Externalize per-connection state to a shared store (Redis, a
   sidecar-to-sidecar mesh).** Any replica could take any packet. Every
   packet would pay a network round trip to fetch and write back parser
   state, on the query path, and the state (an in-progress HPACK table, a
   half-read TDS token stream) has no serializable form the codecs
   expose. Solves a problem no client has: a TCP connection never moves
   between hosts.

2. **Move protocol parsing into Envoy and call the sidecar as a
   stateless verdict service (`ext_proc`).** `gate.EvaluateStatement`
   is already a pure function built for this entry (`gate/gate.go`,
   ADR-0013 option 4). It works for HTTP and gRPC, where Envoy owns the
   protocol. For postgres, mysql and mssql Envoy has no filter that
   reassembles result rows: `postgres_proxy`/`mysql_proxy` are
   request-side contrib filters that emit metadata and drop connections
   with no message. Masking and the operator-authored denial would have
   to be reimplemented as an Envoy extension, which ADR-0005 rejects.
   Deferred for HTTP/gRPC behind Envoy; unavailable for the database
   lanes.

3. **Pool client connections in the sidecar (PgBouncer-style) so fewer
   upstream connections carry the state.** Changes connection semantics
   (`SET`, `PREPARE`, advisory locks, temp tables break across backends),
   collapses per-client identity into one pooled user, and makes the
   relay a Postgres client/server implementation. Pooling is a separate
   product; an operator who needs it puts PgBouncer as the lane's
   `upstream:`.

4. **Keep every byte of state connection-scoped and scale by L4
   connection affinity.** Each client TCP connection gets its own codec,
   its own `session.ID`, its own upstream connection, and drops all three
   on close. Nothing is shared between connections or between processes.
   Any L4 balancer that pins one downstream socket to one upstream
   endpoint for its lifetime (Envoy `tcp_proxy`, HAProxy `mode tcp`,
   nginx `stream`, a cloud NLB, kube-proxy, DNS round-robin) gives the
   affinity for free, because that is what L4 forwarding is.

### Decision matrix

| | 1. Shared state store | 2. `ext_proc` verdict service | 3. Pool in the sidecar | 4. Connection-scoped, L4 affinity |
|---|---|---|---|---|
| Works for postgres/mysql/mssql | ✗ state not serializable | ✗ Envoy has no row-level parser | ✓ | ✓ |
| Works for HTTP/gRPC | ✓ | ✓ request side; ✗ response masking | n/a | ✓ |
| Response masking preserved | ✗ | ✗ on DB lanes | ✓ | ✓ |
| Per-client identity in audit | ✓ | ✓ | ✗ pooled user | ✓ |
| Data-path latency added | 1 RTT per packet | 1 RPC per statement | 0 | 0 |
| Requires Envoy | ✗ | ✓ | ✗ | ✗ any L4 balancer |
| Requires Envoy extension | ✗ | ✓ for DB lanes | ✗ | ✗ |
| New infrastructure | store cluster | none | none | none |
| Failure of one replica | connections survive in theory; parser state was never serializable so no | connections drop | connections drop | connections drop; clients reconnect to a live replica |
| Fleet config consistency | store + files | Envoy + files | files | files only (ADR-0014 hot reload) |
| Complexity added to the relay | high | medium | high | none: describes what exists |

Option 4 is the only column with no ✗ and no new moving part. Options 1
and 2 attempt to remove a coupling that TCP itself already provides;
option 3 trades the identity guarantee the audit trail is built on for a
capacity problem that belongs to a pooler.

## Decision

We will keep all sidecar data-path state scoped to one TCP connection
and scale the sidecar horizontally by running N identical processes
behind any L4 balancer that preserves connection affinity.

Concretely:

- **One connection, one codec, one upstream socket, one `session.ID`.**
  A codec instance is never shared across connections (`inspect.Register`
  factory), and no field on a lane, a gate or the daemon carries a value
  derived from one connection that another connection reads.
- **No cross-connection or cross-process data-path state.** No shared
  store, no peer discovery, no leader. `store/` remains a per-process
  read model for the admin API; losing it loses queryability of that
  process's trail, never a verdict.
- **The affinity requirement is exactly "one downstream TCP connection
  reaches one sidecar process for its lifetime."** Different connections
  from the same client may land on different processes. This is the
  natural behaviour of Envoy `tcp_proxy` (one upstream connection per
  downstream connection, chosen at accept), kube-proxy, HAProxy `mode
  tcp`, nginx `stream`, cloud NLBs and DNS round-robin. No hash policy,
  sticky session or consistent hashing is required or recommended.
- **Scaling is one cluster per lane, N endpoints each.** The envoy-stack
  clusters are `STRICT_DNS` on the compose service name; N replicas
  resolve to N endpoints with no config change. Bare binaries list N
  `host:port` pairs under `lb_endpoints` (or a DNS name with N records)
  with active health checks against `/healthz` and outlier detection so a
  dead process is ejected before its DNS TTL.
- **Every process runs the same `config.yaml` and the same license.**
  Config is distributed from one source (ConfigMap, artifact, Ansible)
  and hot-reloads per ADR-0014; a half-rolled fleet is a rollout state,
  not a design state.
- **Audit aggregates through the log pipeline, not the admin API.**
  `audit.file: "-"` (stdout JSONL) per process is the fleet trail;
  `/api/sessions` and `/stats` answer for one process.
- **HTTP/1 and gRPC lanes** follow the same rule with a finer LB unit:
  `http_connection_manager` may balance per request or per stream, which
  is safe because those lanes are request-bounded.

## Consequences

**What we win.**

- Horizontal scale with zero relay code: capacity is "start another
  process", availability is "start it on another host".
- Restart-safe by construction. A process dies, its sockets close,
  drivers reconnect, the balancer routes them elsewhere. There is no
  state to recover, replay or reconcile.
- Deployment-shape independence. The same answer holds for a binary
  under systemd, a compose replica, a Kubernetes Deployment, with or
  without Envoy. Operators do not need to know which protocol a lane
  speaks to size or balance it.
- Tenant isolation is a property of the process model, not of a lock.
  One connection cannot observe another's reassembly, verdicts or rows.
- Response masking and operator-authored denials keep working on every
  protocol, because the process that read the `RowDescription` is the
  process that rewrites the `DataRow`.
- Latency: no round trip is added to the query path by scaling.

**What gets harder.**

- Load distributes by connection count, not query rate. One long-lived
  connection from a single client cannot be spread. Clients with small
  pools see uneven replicas under `ROUND_ROBIN`; `LEAST_REQUEST` (which
  counts active connections on `tcp_proxy`) balances long-lived
  connections better.
- A replica loss drops its in-flight connections. Clients see a
  disconnect and reconnect; there is no transparent failover. This is
  the same behaviour as losing the database's own network path.
- `GET /api/sessions` is per process. A fleet-wide query needs the log
  pipeline or a per-process `store/sqlite` that something else reads.
- N processes on one host need N sets of listen ports; the sidecar does
  not do kernel-level `SO_REUSEPORT` sharing. Scale across hosts for
  availability rather than across ports for CPU.

**What we are committed to.**

- No future feature may introduce state that one connection writes and
  another reads on the data path without superseding this ADR. Rate
  limits per user, session caches, cross-connection prepared-statement
  tracking and similar all fall under that rule.
- MongoDB stays the documented exception on routing, not on state: a
  replica-set-aware driver follows advertised member addresses around the
  balancer entirely unless `directConnection=true`
  (`sidecar/README.md`, mongodb section). That is a topology hazard the
  operator must close; it does not change the state model.

**Revisit if.**

- A protocol lands whose session state genuinely spans connections
  (server-assigned session tokens resumed on a new socket). It would
  need either client-side affinity by token or a shared store for that
  token only, scoped and justified in its own ADR.
- Every target deployment turns out Envoy-fronted and the HTTP/gRPC
  lanes want the `ext_proc` shape (ADR-0013 revisit clause). That adds a
  stateless entry for those lanes; it does not change this decision for
  the database lanes.
- A customer requires transparent connection failover across replicas.
  Nothing short of terminating the client protocol and originating a new
  upstream session (option 3's cost) delivers it, and that is a
  different product.
