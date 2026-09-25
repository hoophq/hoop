# ADR-0021: The sidecar serves Prometheus metrics on its admin listener, through a stdlib writer

- **Status:** Proposed
- **Date:** 2026-09-25
- **Author:** @matheusfrancisco
- **Deciders:** @matheusfrancisco
- **Supersedes / Superseded by:** —
- **Related:** ADR-0013 (gRPC terminates HTTP/2 in-process), ADR-0014 (config hot reload), ADR-0017 (usage analytics)

## Context

An operator running `hoop-inspect` cannot answer "is the sidecar slowing my
database down, and if so, which dependency is it?". What exists today:

- `GET /stats` on the admin listener: JSON with `active`, `total`, `denied`
  per listener, plus analyzer calls and cache hits. It has no timing and no
  format a scraper can read.
- `sidecar/analytics` (ADR-0017): per-protocol counts sent to Segment every
  fifteen minutes. It is product telemetry, keyed by protocol and never by
  lane, and an operator cannot read it.

A statement can cross up to three network dependencies before its bytes are
forwarded, and each one adds latency on the data path:

| Dependency | Call site | Default bound |
|---|---|---|
| Upstream database / HTTP / gRPC service | `proxy.Server.dialUpstream` (TCP + `startTLS`), then the two `pump` goroutines | `DialTimeout` 10 s |
| OPA | `policy/opa.go`, `client.Do` + decode, once per statement **in both directions** | `Timeout` 2 s |
| LLM analyzer | `analyzer.Evaluator`, one call site: `e.cfg.Provider.Classify` (`evaluator.go`) for openai, anthropic, gemini and vertex | `e.cfg.Timeout` |

These constraints limit the design:

- **The root module has exactly one dependency**, `github.com/hoophq/libhoop`
  (`sidecar/CLAUDE.md`). `prometheus/client_golang` in the root would add a
  second one, plus its transitive tree (`prometheus/common`,
  `client_model`, `protobuf`).
- **The data path costs one atomic add.** `gate.Config.Metrics` says a hook
  "MUST cost no more than an atomic increment". A histogram observe is more
  than that: a bucket scan plus two or three atomics. Nothing on the
  statement path may allocate, lock, log or look up a map.
- **The admin listener has no authentication.** `store/api.go` says the
  query API "provides no authentication, no authorization, and no CORS".
  The admin mux mounts `/api/*` when `audit.query_sessions > 0` and
  `/events` when `audit.memory_buffer > 0`. Both read the audit trail.
- **Prometheus scrapes from outside the pod.** An admin port bound to
  `127.0.0.1` (the README example) cannot be scraped. To scrape it, the
  operator binds it to the pod IP, and then `/api/*` and `/events` are also
  open to the network. A NetworkPolicy works per port, so it cannot allow
  `/metrics` and block `/api/*` on the same port.
- **The admin mux was designed to be scraped.** The comment above the
  `/api` mount in `serveAdmin` keeps `/healthz` and `/stats` "stable and
  separately scrapeable".
- **Metrics are off unless the operator opens the admin listener.**
  `admin.listen` has no default: `Run` starts `serveAdmin` only when it is
  set (`daemon.go`). An empty config must serve no metrics and time
  nothing.
- **A relay does not correlate a query with its reply.** Database lanes pump
  bytes in both directions independently. Postgres extended protocol and
  HTTP/1 pipelining can have several requests in flight on one connection,
  so "time to the next server chunk" is not the query round trip. MongoDB
  (`responseTo`) and the gRPC lane (one RPC per HTTP/2 stream) are the
  exceptions where the pairing is exact.
- **The gRPC lane's upstream call lives in libhoop**
  (`libhoop/v2/codec/grpc`, ADR-0013). `endpointServer` (`daemon/ssh.go`)
  exposes `Serve`, `Close`, `Addr` and `Stats`, and no per-RPC timing.

## Options considered

1. **`prometheus/client_golang` in the root.** It is the standard library,
   it does the exposition and histograms for us, and it gives Go runtime
   metrics for free. It lost on the one-dependency invariant: the tree it
   pulls in is larger than the sidecar's own code, and the part we use is a
   text format of about a page.

2. **A nested module `metrics/prometheus/`** linked from `cmd/`, the
   `pii/alcatraz` pattern. It keeps the root clean. The costs are a module
   directory, an injection seam through `daemon` for a writer the daemon
   must own anyway (the listener is in `daemon`), and a binary built without
   the plugin that silently serves nothing. Kept as a fallback if the
   stdlib writer grows past its scope.

3. **A separate metrics listener** (`metrics.listen`, empty = off) that
   serves only `/metrics`. It keeps the scrape network away from the audit
   trail with no action from the operator. It lost on cost: one more port
   to bind, to open in the pod spec and to accept in the control-plane
   document, for data the admin port was built to serve. An operator can
   get the same isolation on the admin port by turning off `/api` and
   `/events`.

4. **Add timings to `/stats` JSON.** No new format. It lost because nothing
   scrapes it: every operator would write an exporter, and histograms in
   JSON reinvent the Prometheus format badly.

5. **OpenTelemetry metrics (OTLP push).** Vendor-neutral, and it can carry
   traces later. It lost on dependency weight (the OTel SDK is larger than
   `client_golang`) and on topology: push needs a collector address, and an
   air-gapped relay has none. Prometheus pull needs only a port.

6. **A stdlib writer in the root, narrow observer hooks per package,
   `/metrics` on the admin mux whenever the admin listener runs.** It
   follows the `gate.Metrics` / `analytics.LaneCounter` pattern ADR-0017
   set: the package that measures declares a small interface, and the
   aggregator satisfies it without either importing the other. It reuses
   the admin server that already exists, and it needs no new config key.
   The exposure risk is stated, and warned about at startup, instead of
   prevented by a second port.

7. **A memory budget in MB, with a flush when it fills.** This is the
   right tool for a buffer of samples waiting to be sent (a push model, or
   the audit `async_queue_size`). It does not apply here. A counter or a
   histogram is a fixed set of numbers that the sidecar updates in place.
   A sample is not stored, so memory does not grow with traffic or time.
   Prometheus keeps the history. A flush would reset counters to zero at a
   time the operator did not choose, and `rate()` would lose the
   increments since the last scrape. See "Memory" below for the bound.

## Decision

We will expose Prometheus text-format metrics from a stdlib-only package in
the root module, `sidecar/metrics`, as `GET /metrics` on the admin
listener. When the admin listener runs, `/metrics` is served.

### Configuration: follows the admin listener

```yaml
admin:
  listen: 127.0.0.1:19000   # /healthz /stats /config /events /api/* /metrics
```

- No new key. `admin.listen` set means `/metrics` is served and the data
  path is timed. `admin.listen` empty means no admin server, nil hooks, no
  timing.
- The metrics are off by default because `admin.listen` has no default.
- **Changing `admin.listen` needs a restart.** The `admin` section is
  outside the live reload set (ADR-0014): an edit logs `restart to apply
  it` once.
- `GET /config` and `-validate` list `/metrics` among the admin paths.
- There is no switch to keep the admin port and turn off only the timing.
  To turn the timing off, the operator removes `admin.listen`.

### Exposure warning

The admin port is scraped from the network, and it has no authentication.

- At startup, when `/api/*` or `/events` is mounted and the admin bind host
  is not loopback (an empty host, `0.0.0.0`, `::` or a non-loopback IP),
  the process logs one warning. The warning names the mounted audit
  endpoints and the two keys that turn them off: `audit.query_sessions: 0`
  and `audit.memory_buffer: 0`.
- It is a warning, not a refusal. Binding admin to the pod IP is a choice
  the operator can already make today. A refusal would break deployments
  that put their own proxy or NetworkPolicy in front of the port.
- README "Configuring it" states the same risk beside the `admin` block.

### Metrics

Prefix `hoop_sidecar_`. Durations are in seconds, per Prometheus
conventions.

| Metric | Type | Labels | Measured at |
|---|---|---|---|
| `connections_active` | gauge | lane, protocol | `proxy.Server` (reuses `active`) |
| `connections_total` | counter | lane, protocol | `proxy.Server` (reuses `total`) |
| `connection_duration_seconds` | histogram | lane, protocol | `proxy.Server.handle`, around `wg.Wait()` |
| `upstream_dial_duration_seconds` | histogram | lane, protocol, stage=`tcp`\|`tls` | `dialUpstream`, `startTLS` |
| `upstream_dial_errors_total` | counter | lane, protocol, stage | same |
| `decision_duration_seconds` | histogram | lane, protocol, direction | `pump`: chunk read → forward decision. The latency the sidecar adds: inspection, local rules, OPA, analyzer, masking |
| `statements_total` | counter | lane, protocol, verdict=`allow`\|`deny`, source | beside `gate.Metrics.Statement` |
| `opa_request_duration_seconds` | histogram | lane, phase=`gate`\|`decide`, outcome | `policy/opa.go`, around `client.Do` + decode |
| `opa_skipped_total` | counter | lane, phase | the `c.skipped()` paths |
| `analyzer_request_duration_seconds` | histogram | lane, provider, outcome=`ok`\|`error`\|`timeout` | `analyzer.Evaluator`, around `Provider.Classify` |
| `analyzer_calls_total` | counter | lane, provider, status | the `ai_status` values: `ok`, `cached`, `skipped`, `budget_exhausted`, `refused`, `error` |
| `upstream_request_duration_seconds` | histogram | lane, protocol | **MongoDB only**, where the codec pairs a reply to its request through `responseTo` |

- **OPA outcome** is one of `allow`, `deny`, `undefined`, `error`,
  `timeout`. Skips go to `opa_skipped_total`, never to the latency
  histogram, so a lane with `responses: false` does not show fast fake
  calls.
- **Analyzer cache hits and budget refusals** go only to
  `analyzer_calls_total`. The duration histogram holds only real network
  calls to the provider.
- **Buckets are fixed per metric, not configurable.** Each set ends at or
  just above that dependency's default timeout, so a timeout shows as a
  bucket and not only as an error. Upper bounds, in seconds:
  - OPA: `.001 .0025 .005 .01 .025 .05 .1 .25 .5 1 2.5`
  - analyzer: `.05 .1 .25 .5 1 2.5 5 10 30`
  - dial: `.001 .005 .01 .05 .1 .5 1 5 10`
  - decision and Mongo round trip:
    `.00005 .0001 .00025 .0005 .001 .0025 .005 .01 .05 .1 .5 1 5 30`
  - connection: `.1 1 10 60 300 900 3600 14400 43200`. Pooled
    connections live for hours.

### Example scrape

One postgres lane, `appdb`, with OPA and a vertex analyzer, one day after
start. The numbers are made up but consistent: each `+Inf` bucket equals
its `_count`, and closed connections equal `total` minus `active`. A
`# …` line marks series left out of this excerpt; the real output lists
them all.

```
$ curl -s localhost:19000/metrics
# HELP hoop_sidecar_connections_active Client connections open now.
# TYPE hoop_sidecar_connections_active gauge
hoop_sidecar_connections_active{lane="appdb",protocol="postgres"} 9
# HELP hoop_sidecar_connections_total Client connections accepted.
# TYPE hoop_sidecar_connections_total counter
hoop_sidecar_connections_total{lane="appdb",protocol="postgres"} 412
# HELP hoop_sidecar_connection_duration_seconds Time from accept to close.
# TYPE hoop_sidecar_connection_duration_seconds histogram
hoop_sidecar_connection_duration_seconds_bucket{lane="appdb",protocol="postgres",le="0.1"} 3
hoop_sidecar_connection_duration_seconds_bucket{lane="appdb",protocol="postgres",le="1"} 40
hoop_sidecar_connection_duration_seconds_bucket{lane="appdb",protocol="postgres",le="10"} 118
hoop_sidecar_connection_duration_seconds_bucket{lane="appdb",protocol="postgres",le="60"} 190
hoop_sidecar_connection_duration_seconds_bucket{lane="appdb",protocol="postgres",le="300"} 251
hoop_sidecar_connection_duration_seconds_bucket{lane="appdb",protocol="postgres",le="900"} 310
hoop_sidecar_connection_duration_seconds_bucket{lane="appdb",protocol="postgres",le="3600"} 372
hoop_sidecar_connection_duration_seconds_bucket{lane="appdb",protocol="postgres",le="14400"} 398
hoop_sidecar_connection_duration_seconds_bucket{lane="appdb",protocol="postgres",le="43200"} 403
hoop_sidecar_connection_duration_seconds_bucket{lane="appdb",protocol="postgres",le="+Inf"} 403
hoop_sidecar_connection_duration_seconds_sum{lane="appdb",protocol="postgres"} 612840.5
hoop_sidecar_connection_duration_seconds_count{lane="appdb",protocol="postgres"} 403
# HELP hoop_sidecar_upstream_dial_duration_seconds Time to open the upstream connection, per stage.
# TYPE hoop_sidecar_upstream_dial_duration_seconds histogram
# … stage="tcp" series
hoop_sidecar_upstream_dial_duration_seconds_bucket{lane="appdb",protocol="postgres",stage="tls",le="0.001"} 0
hoop_sidecar_upstream_dial_duration_seconds_bucket{lane="appdb",protocol="postgres",stage="tls",le="0.005"} 47
hoop_sidecar_upstream_dial_duration_seconds_bucket{lane="appdb",protocol="postgres",stage="tls",le="0.01"} 310
hoop_sidecar_upstream_dial_duration_seconds_bucket{lane="appdb",protocol="postgres",stage="tls",le="0.05"} 409
hoop_sidecar_upstream_dial_duration_seconds_bucket{lane="appdb",protocol="postgres",stage="tls",le="0.1"} 412
hoop_sidecar_upstream_dial_duration_seconds_bucket{lane="appdb",protocol="postgres",stage="tls",le="0.5"} 412
hoop_sidecar_upstream_dial_duration_seconds_bucket{lane="appdb",protocol="postgres",stage="tls",le="1"} 412
hoop_sidecar_upstream_dial_duration_seconds_bucket{lane="appdb",protocol="postgres",stage="tls",le="5"} 412
hoop_sidecar_upstream_dial_duration_seconds_bucket{lane="appdb",protocol="postgres",stage="tls",le="10"} 412
hoop_sidecar_upstream_dial_duration_seconds_bucket{lane="appdb",protocol="postgres",stage="tls",le="+Inf"} 412
hoop_sidecar_upstream_dial_duration_seconds_sum{lane="appdb",protocol="postgres",stage="tls"} 3.502
hoop_sidecar_upstream_dial_duration_seconds_count{lane="appdb",protocol="postgres",stage="tls"} 412
# HELP hoop_sidecar_upstream_dial_errors_total Upstream connections that failed, per stage.
# TYPE hoop_sidecar_upstream_dial_errors_total counter
hoop_sidecar_upstream_dial_errors_total{lane="appdb",protocol="postgres",stage="tcp"} 0
hoop_sidecar_upstream_dial_errors_total{lane="appdb",protocol="postgres",stage="tls"} 0
# HELP hoop_sidecar_decision_duration_seconds Time from reading a chunk to the forward decision.
# TYPE hoop_sidecar_decision_duration_seconds histogram
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="5e-05"} 2210
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="0.0001"} 9870
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="0.00025"} 13920
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="0.0005"} 14880
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="0.001"} 15120
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="0.0025"} 15390
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="0.005"} 15600
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="0.01"} 15710
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="0.05"} 15980
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="0.1"} 16210
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="0.5"} 16390
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="1"} 16450
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="5"} 16478
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="30"} 16480
hoop_sidecar_decision_duration_seconds_bucket{lane="appdb",protocol="postgres",direction="client",le="+Inf"} 16480
hoop_sidecar_decision_duration_seconds_sum{lane="appdb",protocol="postgres",direction="client"} 181.37
hoop_sidecar_decision_duration_seconds_count{lane="appdb",protocol="postgres",direction="client"} 16480
# … direction="server" series
# HELP hoop_sidecar_statements_total Judged statements, per verdict and denial source.
# TYPE hoop_sidecar_statements_total counter
hoop_sidecar_statements_total{lane="appdb",protocol="postgres",verdict="allow"} 18233
hoop_sidecar_statements_total{lane="appdb",protocol="postgres",verdict="deny",source="operation"} 11
hoop_sidecar_statements_total{lane="appdb",protocol="postgres",verdict="deny",source="pii"} 4
hoop_sidecar_statements_total{lane="appdb",protocol="postgres",verdict="deny",source="opa"} 2
# HELP hoop_sidecar_opa_request_duration_seconds OPA round trip, request to decoded decision.
# TYPE hoop_sidecar_opa_request_duration_seconds histogram
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="0.001"} 1210
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="0.0025"} 9480
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="0.005"} 15870
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="0.01"} 17420
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="0.025"} 17801
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="0.05"} 17866
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="0.1"} 17884
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="0.25"} 17889
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="0.5"} 17890
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="1"} 17890
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="2.5"} 17890
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="allow",le="+Inf"} 17890
hoop_sidecar_opa_request_duration_seconds_sum{lane="appdb",phase="decide",outcome="allow"} 67.94
hoop_sidecar_opa_request_duration_seconds_count{lane="appdb",phase="decide",outcome="allow"} 17890
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="0.001"} 0
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="0.0025"} 0
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="0.005"} 0
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="0.01"} 0
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="0.025"} 0
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="0.05"} 0
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="0.1"} 0
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="0.25"} 0
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="0.5"} 0
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="1"} 0
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="2.5"} 3
hoop_sidecar_opa_request_duration_seconds_bucket{lane="appdb",phase="decide",outcome="timeout",le="+Inf"} 3
hoop_sidecar_opa_request_duration_seconds_sum{lane="appdb",phase="decide",outcome="timeout"} 6.004
hoop_sidecar_opa_request_duration_seconds_count{lane="appdb",phase="decide",outcome="timeout"} 3
# … outcome="deny" series
# HELP hoop_sidecar_opa_skipped_total Statements OPA was not asked about.
# TYPE hoop_sidecar_opa_skipped_total counter
hoop_sidecar_opa_skipped_total{lane="appdb",phase="decide"} 9120
# HELP hoop_sidecar_analyzer_request_duration_seconds Model call time, network calls only.
# TYPE hoop_sidecar_analyzer_request_duration_seconds histogram
hoop_sidecar_analyzer_request_duration_seconds_bucket{lane="appdb",provider="vertex",outcome="ok",le="0.05"} 0
hoop_sidecar_analyzer_request_duration_seconds_bucket{lane="appdb",provider="vertex",outcome="ok",le="0.1"} 0
hoop_sidecar_analyzer_request_duration_seconds_bucket{lane="appdb",provider="vertex",outcome="ok",le="0.25"} 12
hoop_sidecar_analyzer_request_duration_seconds_bucket{lane="appdb",provider="vertex",outcome="ok",le="0.5"} 141
hoop_sidecar_analyzer_request_duration_seconds_bucket{lane="appdb",provider="vertex",outcome="ok",le="1"} 262
hoop_sidecar_analyzer_request_duration_seconds_bucket{lane="appdb",provider="vertex",outcome="ok",le="2.5"} 309
hoop_sidecar_analyzer_request_duration_seconds_bucket{lane="appdb",provider="vertex",outcome="ok",le="5"} 316
hoop_sidecar_analyzer_request_duration_seconds_bucket{lane="appdb",provider="vertex",outcome="ok",le="10"} 318
hoop_sidecar_analyzer_request_duration_seconds_bucket{lane="appdb",provider="vertex",outcome="ok",le="30"} 318
hoop_sidecar_analyzer_request_duration_seconds_bucket{lane="appdb",provider="vertex",outcome="ok",le="+Inf"} 318
hoop_sidecar_analyzer_request_duration_seconds_sum{lane="appdb",provider="vertex",outcome="ok"} 247.73
hoop_sidecar_analyzer_request_duration_seconds_count{lane="appdb",provider="vertex",outcome="ok"} 318
# … outcome="error" and outcome="timeout" series, one call each
# HELP hoop_sidecar_analyzer_calls_total Analyzer decisions, per ai_status.
# TYPE hoop_sidecar_analyzer_calls_total counter
hoop_sidecar_analyzer_calls_total{lane="appdb",provider="vertex",status="ok"} 318
hoop_sidecar_analyzer_calls_total{lane="appdb",provider="vertex",status="cached"} 1480
hoop_sidecar_analyzer_calls_total{lane="appdb",provider="vertex",status="skipped"} 16430
hoop_sidecar_analyzer_calls_total{lane="appdb",provider="vertex",status="budget_exhausted"} 0
hoop_sidecar_analyzer_calls_total{lane="appdb",provider="vertex",status="refused"} 0
hoop_sidecar_analyzer_calls_total{lane="appdb",provider="vertex",status="error"} 2
```

An allowed statement carries no `source` label: Prometheus treats an
absent label and an empty one as the same series, so the writer omits it.

### Example queries

Each question from the Context section, as PromQL:

| Question | Query |
|---|---|
| What does the sidecar add, p95? | `histogram_quantile(0.95, sum by (lane, le) (rate(hoop_sidecar_decision_duration_seconds_bucket{direction="client"}[5m])))` |
| Is OPA near its timeout? | `histogram_quantile(0.99, sum by (lane, le) (rate(hoop_sidecar_opa_request_duration_seconds_bucket{phase="decide"}[5m])))` |
| How often does OPA time out? | `sum by (lane) (rate(hoop_sidecar_opa_request_duration_seconds_count{outcome="timeout"}[5m]))` |
| Is the model slow, p95? | `histogram_quantile(0.95, sum by (lane, provider, le) (rate(hoop_sidecar_analyzer_request_duration_seconds_bucket{outcome="ok"}[15m])))` |
| How often does the model call fail? | `sum by (lane, provider) (rate(hoop_sidecar_analyzer_calls_total{status="error"}[15m])) / sum by (lane, provider) (rate(hoop_sidecar_analyzer_calls_total{status=~"ok\|error"}[15m]))` |
| Is the cache working? | `sum by (lane) (rate(hoop_sidecar_analyzer_calls_total{status="cached"}[1h])) / sum by (lane) (rate(hoop_sidecar_analyzer_calls_total{status=~"ok\|cached\|error"}[1h]))` |
| Is the database slow to accept connections? | `histogram_quantile(0.99, sum by (lane, stage, le) (rate(hoop_sidecar_upstream_dial_duration_seconds_bucket[5m])))` |
| Is MongoDB slow to answer? | `histogram_quantile(0.99, sum by (lane, le) (rate(hoop_sidecar_upstream_request_duration_seconds_bucket{protocol="mongodb"}[5m])))` |
| What is being refused? | `sum by (lane, source) (rate(hoop_sidecar_statements_total{verdict="deny"}[1h]))` |

In the scrape above, `decision_duration` puts 95% of client chunks under
10 ms (15 710 of 16 480 at `le="0.01"`). 500 chunks (about 3%) take
between 50 ms and 30 s. That slow group is close to the 320 model calls,
which average about 780 ms (247.73 s / 318). OPA averages about 3.8 ms
(67.94 s / 17 890), so here the analyzer is what an operator tunes first.

### Cardinality

- **Allowed labels:** lane, protocol, direction, phase, stage, provider,
  verdict, source, outcome, status. Each one comes from the config or from
  a fixed set of values.
- **Never labels:** user or subject, peer address, upstream address,
  database, table, rule name, statement text, gRPC method. They are
  unbounded, and statement-level facts already have one home: the audit
  trail.
- **Lane names are allowed** here even though ADR-0017 forbids them in
  analytics. Analytics sends data to Segment; this port is scraped by the
  operator's own Prometheus, from an admin port the operator opened.

### Data-path cost

- **Series are created when a lane is built, not per call.** The lane gets
  pointers to its series, so an observation does no label lookup, no map
  access and no lock.
- A histogram observe is a scan of at most 16 bounds, then atomic adds for
  the bucket and the count, and a CAS loop for the float sum. This relaxes
  the one-atomic-add rule in `gate.Config.Metrics` **only while the admin
  listener runs**; the comment on that field is updated to say so.
- **No admin listener means nil hooks.** The call sites check for nil
  before calling `time.Now()`, so a relay without `admin.listen` costs one
  branch per site.
- **Reload.** Analyzer and OPA blocks reload live (ADR-0014), so `provider`
  can change on a running lane. The reload build creates the new series,
  next to the lane swap. Series that no lane uses stay exported with their
  last values until restart, the Prometheus convention for counters.

### Memory

Memory is fixed when the lanes are built. It does not grow with traffic,
with time, or with the number of scrapes. So there is no MB budget and no
flush (option 7).

- A histogram series holds its bucket counts, `+Inf`, `_count` and `_sum`:
  at most 18 `uint64` words, plus its label text written once when the
  series is created. About 300 bytes. A counter or gauge series is about
  150 bytes.
- One lane, one analyzer provider, the maximum series:
  - **19 histograms:**
    - 1 connection duration;
    - 2 dial (`tcp`, `tls`);
    - 2 decision (`client`, `server`);
    - 10 OPA (2 phases × 5 outcomes);
    - 3 analyzer (3 outcomes);
    - 1 Mongo round trip.
  - **About 28 counters and gauges:**
    - 2 connections (active, total);
    - 2 dial errors;
    - 1 allowed statements, plus up to 15 denial sources;
    - 2 OPA skipped;
    - 6 analyzer statuses.
  - Total: about 10 KB per lane.
- 100 lanes is about 1 MB. The label rules in "Cardinality" are what keep
  this bound true: no label takes a value from traffic.
- The one way the count grows after startup is a reload that brings a new
  analyzer provider to a lane: 9 new series, about 2 KB, kept until
  restart. It grows once per new provider, not per statement.
- A scrape streams the text straight to the response writer. It does not
  build the whole body in memory first. The body is about 35 KB per lane
  (about 3.5 MB at 100 lanes), so scrape at 15 s or slower.
- Nothing here stores samples, so nothing needs a queue, a size cap or a
  drop policy. A future push exporter (OTLP, remote write) would need all
  three, and a new ADR.

### Package boundaries

- `proxy`, `policy` and `analyzer` each declare a narrow observer interface
  in their own package, the way `gate` declares `Metrics`. `sidecar/metrics`
  satisfies them structurally and imports none of them.
- `daemon` mounts the `/metrics` handler on the admin mux in `serveAdmin`,
  builds the series per lane, and hands each package its observer.
- `sidecar/analytics` is unchanged. Segment counts and Prometheus series are
  two consumers of the same facts. Neither reads the other.

## Consequences

**Easier.**
- An operator can alert on OPA p99 near its timeout, see analyzer latency
  and error rate per provider, and separate "the database is slow" (dial,
  Mongo round trip) from "the sidecar is slow" (`decision_duration`).
- `decision_duration` is the number to quote when someone asks what the
  relay costs.

**Harder.**
- We own an exposition writer. Its output must stay valid Prometheus text
  (`# HELP`/`# TYPE`, `_bucket{le=...}` ordering, `+Inf`, label escaping).
  A nested test-only module parses it with `prometheus/common/expfmt`, the
  way `lexer/conformance` checks the lexer against PostgreSQL's parser. A
  change to the writer is not verified until that suite passes.
- No Go runtime metrics (`go_goroutines`, GC) without `client_golang`. If
  operators need them, add a few from `runtime/metrics` to the writer.
  Do not add the dependency.
- `/metrics` shares a port with the unauthenticated audit query API. The
  operator must turn off `/api` and `/events` or control access to the
  port. The startup warning and the README say so; nothing enforces it.
- Every relay with `admin.listen` set pays the timing cost. There is no way
  to keep `/stats` and turn off only `/metrics`. If a histogram bug hurts
  the data path, the fix is a release, or removing `admin.listen`.

**Committed to.**
- Metric names and labels are an operator contract: dashboards and alerts
  depend on them. Renaming or removing one needs a deprecation window, the
  same way `README.md` "Deprecated fields" handles config keys.
- `/metrics` stays on the admin mux and follows `admin.listen`. A separate
  listener (option 3) or an on/off key is a new ADR that supersedes this
  one.

**Not covered here, and why.**
- **Query round-trip latency for Postgres, MySQL, MSSQL, ClickHouse,
  HTTP/1.** The relay cannot pair a reply with its request when requests
  are pipelined. A histogram that is correct for some traffic and wrong for
  the rest is worse than none. Add it per protocol when that codec can
  report the pairing, and document which lanes carry it.
- **gRPC per-RPC upstream latency.** The call is inside
  `libhoop/v2/codec/grpc`. It needs a new injected timing callback there
  (libhoop stays a leaf: a func value, no import back), so it is a separate
  libhoop change.
- **Audit sink latency and async queue depth, control-plane heartbeat
  latency, time spent in a review hold.** Useful, same pattern, left out to
  keep this change bounded.

**Revisit if** the writer needs exemplars, native histograms or
OpenMetrics negotiation. At that point option 2 (a nested module with
`client_golang`) costs less than keeping a writer ourselves.
