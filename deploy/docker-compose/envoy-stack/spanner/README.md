# Spanner overlay: through Envoy, and without it

Adds a fourth protocol to the stack, reachable two ways:

```
grpcurl ──TLS──> envoy:8445 ──ext_authz(OPA)──> hoop-inspect:29010 ──h2c──> spanner:9010
grpcurl ──h2c(host :29010)────────────────────> hoop-inspect:29010 ──h2c──> spanner:9010
```

Both paths end at one sidecar lane. The first is the stack's usual
topology: Envoy owns TLS and identity, OPA answers reachability, and
hoop-inspect reads the payloads. The second is the same lane published on
the host, the **without-Envoy** door: a client dials the sidecar directly
over cleartext HTTP/2. Policy and the audit trail are identical on both.

`protocol: spanner` adds one thing over a plain grpc lane: it pulls the
GoogleSQL text out of Spanner RPC payloads (ExecuteSql and friends) into
the statement, so rules that read SQL now read the SQL a Google SDK sends
inside a protobuf field.

## Run it

```bash
./run.sh                 # base stack: certs, sidecar image, compose up
docker compose -f docker-compose.yml -f spanner/docker-compose.spanner.yml up -d --wait
./spanner/demo-spanner.sh   # walks both paths and asserts the audit trail
```

The cached `hoop-inspect:local` must carry `protocol: spanner` (the grpc2
branch); if it predates that, `./run.sh --rebuild` first.

The host needs no gRPC tooling: the overlay carries a `grpcurl` service,
run on demand:

```bash
alias dcs='docker compose -f docker-compose.yml -f spanner/docker-compose.spanner.yml'

# through Envoy: TLS, OPA consulted, then inspected
dcs run --rm grpcurl -insecure -H 'x-hoop-user: alice' \
    -protoset /descriptors/spanner.pb \
    -d '{"database":"projects/demo/instances/demo-instance/databases/demodb"}' \
    envoy:8445 google.spanner.v1.Spanner/CreateSession

# without Envoy: plaintext h2c, straight to the lane
dcs run --rm grpcurl -plaintext -H 'x-hoop-user: alice' \
    -protoset /descriptors/spanner.pb \
    -d '{"database":"projects/demo/instances/demo-instance/databases/demodb"}' \
    hoop-inspect:29010 google.spanner.v1.Spanner/CreateSession
```

A host-installed `grpcurl` reaches the same two doors at `localhost:8445`
(`-insecure`) and `localhost:29010` (`-plaintext`).

## No reflection, so buf builds the descriptors

The Cloud Spanner emulator serves no gRPC reflection, and runtime
lanes never touch reflection (ADR-0013): the lane pins a
FileDescriptorSet and refuses to start without it. The
`spanner-descriptors` init service builds it with `buf` from the public
googleapis tree, filtered to `google/spanner`, into a named volume both
the sidecar and `grpcurl` mount; `hoop-inspect` orders on
`service_completed_successfully`. The `[ -f ]` guard makes re-ups skip the
network fetch. This is the exact invocation `../../gcloud-stack/run.sh`
uses as its fallback after reflection answers Unimplemented.

## The one-rule budget, on a fourth protocol

The lane adds no rules. The free tier's one guardrail and one mask
rule are spent in the defaults, and the spanner lane inherits both. The
demo's CPF beat shows `no-cpf-in-query`, the rule that refuses the
pgwire DELETE and the HTTP query string, refusing a taxpayer id inside
GoogleSQL text.

`config-spanner.yaml` carries the lane's own would-be rule commented out:
`no-destructive-googlesql`, the `operation` rule `../../gcloud-stack` is
built around (lexer-derived verbs; `unknown` as the fail-closed catch for
SQL the scanner cannot read). The demo's DELETE beat prints the emulator's
answer beside it: the frame gets through as shipped. Uncommenting the
rule means freeing the budget (comment out `no-cpf-in-query`, or run a
licensed build).

## Without Envoy, two ways

- **This overlay's direct door**: host `:29010`, cleartext h2c. Identity
  is the unverified `x-hoop-user` header. `identity_header` is a
  proxy-trust contract, and publishing the port breaks it on purpose so
  the demo can call the lane as `mallory`.
- **`../../gcloud-stack`**: the full Envoy-free stack, the same emulator
  and lane with its own buf bootstrap, plus a BigQuery Storage lane, with
  no Envoy or OPA. Start there for the standalone story instead of
  stacking this overlay Envoy-less.

A real Google SDK client rejects both cleartext doors: `grpc-go`
refuses OAuth per-RPC credentials on an insecure channel (emulator clients
dodge this via `SPANNER_EMULATOR_HOST`). The `downstream_tls` block
commented in `config-spanner.yaml` is the shape for that: the lane
terminates TLS itself; `../grpc/docker-compose.standalone.yml` shows the
minted-cert pattern.

## Files

| File | Role |
|---|---|
| `docker-compose.spanner.yml` | the overlay: emulator, buf descriptors init, envoy + sidecar swaps, grpcurl |
| `config-spanner.yaml` | base lanes verbatim plus the `spanner` lane (h2c) |
| `envoy-spanner.yaml` | base Envoy config plus the `:8445` listener and h2 cluster |
| `demo-spanner.sh` | the walk: OPA gate, SELECT, the CPF denial, the DELETE that ships allowed, direct h2c, audit |

The OPA policy is shared with the base stack; `opa/authz.rego` keys the
service on the listener port (`:8443` → httpbin, `:8444` → ledger,
`:8445` → spanner), so one Rego file answers every stack.
