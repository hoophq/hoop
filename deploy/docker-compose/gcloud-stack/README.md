# gcloud-stack: the spanner lane, proven against real emulators

This stack proves one flow end to end: a `protocol: spanner` lane extracts
the GoogleSQL text out of Spanner RPC payloads, classifies it with the
`googlesql` lexer dialect, and an ordinary `operation` guardrail refuses a
`DELETE` **before the upstream sees the frame**. A `DELETE` hiding inside a
raw-string literal stays a select, and SQL the lexer cannot read is refused
as `unknown` (fail-closed). A second, method-only `grpc` lane fronts the
BigQuery Storage API to show the same transport policing RPC identity
without descriptors.

## Ports

| where           | address            | what                                          |
| --------------- | ------------------ | --------------------------------------------- |
| compose network | `spanner:9010`     | Cloud Spanner emulator, gRPC                  |
| compose network | `spanner:9020`     | Cloud Spanner emulator, REST admin (setup)    |
| compose network | `bigquery:9050`    | BigQuery emulator, REST (liveness probe only) |
| compose network | `bigquery:9060`    | BigQuery emulator, Storage API (gRPC)         |
| host `:29010`   | `hoop-inspect`     | spanner lane                                  |
| host `:29060`   | `hoop-inspect`     | bqstorage lane                                |
| host `:19001`   | `hoop-inspect`     | admin (`:19000` inside; 19001 so envoy-stack's 19000 can coexist) |

The emulator ports stay off the host on purpose: the data plane is only
reachable through the sidecar.

## Run it

```sh
./run.sh          # build, start emulators, bootstrap descriptors, start sidecar
./demo.sh         # the asserting walk-through
./run.sh down     # tear down, volumes included
./run.sh --rebuild  # force a sidecar image rebuild first
```

## Why there is no Envoy

envoy-stack answers "who owns TLS, identity and the network path". This
stack answers "can the sidecar read GoogleSQL", and every hop it would add
only obscures which tier refused a statement. The lanes speak h2c on both
sides because the emulators are h2c and authenticate nobody. Real Spanner
would need `upstream_tls` and something in front for the client's leg.

## The descriptor bootstrap (needs the grpc2 branch)

Runtime lanes read **pinned** descriptor sets and never touch reflection
(ADR-0013): the policed service must not rename fields out from under the
policy. So `run.sh` fetches the set once, offline, with
`-grpc-discover spanner -grpc-discover-out /descriptors/spanner.pb` against
the emulator's own reflection service, using `config-discover.yaml` (the
same lanes minus the `grpc:` blocks, so the not-yet-fetched file cannot
block the run that fetches it). `protocol: spanner` and discovery on spanner
lanes land on the `grpc2` branch; the image must be built from it.
bqstorage discovery is allowed to fail (the lane is method-only and runs
without a protoset); `demo.sh` then skips its beats with a notice.

## Caveats

- **BigQuery's query path is not here.** `jobs.query` and friends are REST
  over HTTP, not gRPC; only the Storage API is a gRPC plane. Fronting the
  REST API is an `http` lane's job and out of this stack's scope.
- **One guardrail rule on the free tier.** The budget is spent on the
  spanner lane's operation rule. The bqstorage write-plane fence ships
  commented out in `sidecar/config.yaml`; with `HOOP_LICENSE` set (compose
  passes it through), uncommenting it is the only step and `demo.sh`'s
  AppendRows beat flips to PERMISSION_DENIED.
