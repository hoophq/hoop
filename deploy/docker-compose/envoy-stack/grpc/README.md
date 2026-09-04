# gRPC overlay: through Envoy, and without it

Adds a third protocol to the stack, twice over:

```
grpcurl ──TLS──> envoy:8444 ──ext_authz(OPA)──> hoop-inspect:18443 ──h2c──> ledger:9000
grpcurl ──h2c(host :18443)────────────────────> hoop-inspect:18443 ──h2c──> ledger:9000
```

One sidecar lane, two ways in. The first is the stack's usual topology:
Envoy owns TLS and identity, OPA answers reachability, hoop-inspect
inspects. The second is the same lane published on the host, which is the
**without-Envoy** deployment: an app dials the sidecar directly over
cleartext HTTP/2, the pattern for a sidecar container sharing a pod with
exactly one workload (ADR-0013's h2c mode). Same policy, same masking, same
audit trail; the lane cannot tell the two apart, and the demo makes that
the point.

## Run it

```bash
./run.sh                 # base stack: certs, sidecar image, compose up
docker compose -f docker-compose.yml -f grpc/docker-compose.grpc.yml up -d --wait
./grpc/demo-grpc.sh      # walks both paths and prints the audit trail
```

Nothing gRPC is needed on the host: the overlay carries a `grpcurl`
service, run on demand:

```bash
alias dcg='docker compose -f docker-compose.yml -f grpc/docker-compose.grpc.yml'

# through Envoy: TLS, OPA consulted, then inspected
dcg run --rm grpcurl -insecure -H 'x-hoop-user: alice' \
    -protoset /descriptors/ledger.pb \
    -d '{"id":"INV-1001"}' envoy:8444 demo.v1.Ledger/GetInvoice

# without Envoy: plaintext h2c, straight to the lane
dcg run --rm grpcurl -plaintext -H 'x-hoop-user: alice' \
    -protoset /descriptors/ledger.pb \
    -d '{"id":"INV-1001"}' hoop-inspect:18443 demo.v1.Ledger/GetInvoice
```

A host-installed `grpcurl` reaches the same two doors at
`localhost:8444` (`-insecure`) and `localhost:18443` (`-plaintext`).

## The dummy application

[`app/`](app) is `demo.v1.Ledger`: two unary RPCs over string fields
([`ledger.proto`](app/ledger.proto)), served by a **hand-rolled gRPC server
with no gRPC library** — stdlib `net/http` with h2c, the 5-byte message
framing and the `grpc-status` trailer written by hand
([`main.go`](app/main.go)). That keeps the image dependency-free and the
build offline, and it keeps the stack's claim honest: every gram of
protocol intelligence demonstrably lives in the sidecar, because the
upstream has none.

No reflection either, which is why every `grpcurl` call passes
`-protoset`. The image build compiles `ledger.proto` into a
`FileDescriptorSet` with `protoc`, and the `grpc-descriptors` init service
publishes it to a volume both consumers mount:

- **hoop-inspect** reads it at `grpc.descriptors`. A gRPC lane enforces
  method-level rules without one, but cannot capture, mask or PII-scan a
  payload (ADR-0013) — and both of this stack's live rules read payloads.
- **grpcurl** reads it to encode requests against a server that cannot
  describe itself.

Edit the proto, `dcg build ledger`, `dcg up -d`, and both consumers see the
new schema.

## What the demo shows

| Beat | Path | What decides |
|---|---|---|
| alice reaches ledger, bob gets 403 | Envoy | OPA, on `:path` — method identity, tier 1 |
| `customer_email` returns `[REDACTED:EMAIL_ADDRESS]` | both | the process's one mask rule, inherited; the sidecar re-encodes the frame |
| a CPF inside a request field → `PermissionDenied` | both | the process's one guardrail, `no-cpf-in-query`, scanning the decoded message |
| `ExportAll` answers | both | nothing: `no-bulk-export` sits commented out, over the one-rule limit |
| direct `:18443` call works, as anyone | direct | nobody: identity is an unverified header off the proxy path |

The lane adds **no rules**. The free tier's one guardrail and one mask rule
are spent in the defaults, and the gRPC lane inherits both — the same
budget now covering a third protocol and second masking mechanism
(re-encoding, like postgres reframing; unlike HTTP byte substitution).
`config-grpc.yaml` carries the lane's own would-be rules commented out:
`no-bulk-export` (`http_resource` matching the RPC path) and a
`grpc_status` outcome rule.

## Identity, and what the direct path costs

The lane sets `identity_header: x-hoop-user`, which is a **proxy-trust**
contract: safe when nothing but the authenticating proxy can reach the
listener. Publishing `:18443` on the host breaks that on purpose so the
without-Envoy path is demonstrable — the demo calls it as `mallory` to make
the cost concrete. A real standalone deployment either keeps the h2c hop
inside the pod, or gives the lane `downstream_tls` and client certificates
and drops the header.

`grpc-go` refuses cleartext without an explicit
`insecure.NewCredentials()`, and `grpcurl` needs `-plaintext`; a client
left on TLS defaults fails the direct dial. That is the protocol working
as designed — opt into plaintext, or terminate TLS at the lane.

## Standalone: the lane owns TLS (the second way without Envoy)

The overlay's direct `:18443` path is cleartext h2c, trusted because the
hop stays inside the pod. `docker-compose.standalone.yml` is the other
without-Envoy shape, and it is not an overlay — a complete stack of its
own where the lane terminates the client's TLS itself:

```
grpcurl ──TLS──> hoop-inspect:18443 ──h2c──> ledger:9000
```

```bash
# from this directory, with the envoy stack down (same host ports)
docker compose -f docker-compose.standalone.yml up -d --wait --build
./demo-standalone.sh
```

`config-standalone.yaml` gives the lane `downstream_tls` (a self-signed
pair minted once by the `lane-certs` init service) and drops
`identity_header` — there is no proxy to trust, so sessions record
`principal=anonymous`; client certificates are the standalone way to name
a caller. Policy and masking are the stack's same two rules, and the demo
ASSERTS the mask: it exits non-zero unless `customer_email` comes back as
`[REDACTED:EMAIL_ADDRESS]` and the clear value never reaches the client.
It also shows the flip side of TLS ownership: the `-plaintext` dial that
is the overlay's direct path fails here at the handshake, before gRPC
exists.

## Files

| File | Role |
|---|---|
| `docker-compose.grpc.yml` | the overlay: ledger, descriptors volume, envoy + sidecar swaps, grpcurl |
| `config-grpc.yaml` | base lanes verbatim plus the `ledger` grpc lane (h2c) |
| `envoy-grpc.yaml` | base Envoy config plus the `:8444` gRPC listener and h2 cluster |
| `docker-compose.standalone.yml` | self-contained no-Envoy stack: the lane terminates TLS |
| `config-standalone.yaml` | one TLS lane, no identity header, same two rules |
| `app/` | proto, hand-rolled server, image build (binary + descriptor set) |
| `demo-grpc.sh` | the overlay walk (through Envoy, and direct h2c) |
| `demo-standalone.sh` | the standalone walk; asserts the masking |

The OPA policy is shared with the base stack; `opa/authz.rego` keys the
service on the listener port (`:8443` → httpbin, `:8444` → ledger), so one
Rego file answers both.
