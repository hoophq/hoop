# Kubernetes lane

> hoop-inspect 0.1.0

An overlay on the stack in the parent directory: a one-node k3s cluster
behind a `protocol: http` lane, beside the postgres and httpbin ones. Plain
requests (`kubectl get`) and the WebSocket that `kubectl exec` opens cross
the same listener. The lane adds no rules; it inherits the process's one
guardrail rule.

```
kubectl ──TLS──> envoy :8447 ──ext_authz──> hoop-inspect :16443 ──TLS──> k3s :6443
                 websocket upgrade allowed  http codec                  kube-apiserver
```

## Run it

```bash
./run.sh                                                                            # parent stack
docker compose -f docker-compose.yml -f kubernetes/docker-compose.kubernetes.yml up -d --wait
./kubernetes/demo-kubernetes.sh
docker compose -f docker-compose.yml -f kubernetes/docker-compose.kubernetes.yml down -v
```

The first `up` takes a minute or two: k3s boots a kubelet and pulls
`busybox` for the demo pod, and `--wait` holds until that pod runs. The
`k3s` service is `privileged`, as k3s-in-docker has to be.

Ports: `8447` on the host is Envoy's Kubernetes listener. Inside the compose
network kubectl dials `envoy:8447`; the `client` container carries a
kubeconfig aimed there, with Envoy's certificate as the CA and two contexts,
`alice` (default) and `bob`:

```bash
docker compose -f docker-compose.yml -f kubernetes/docker-compose.kubernetes.yml \
  exec -T client kubectl get pods
docker compose -f docker-compose.yml -f kubernetes/docker-compose.kubernetes.yml \
  exec -T client kubectl exec demo -- hostname
```

From the host, with your own kubectl: `--server https://localhost:8447
--certificate-authority envoy/certs/server.crt --token alice-token`.

## Identity is the bearer token here

kubectl cannot add a header, so `X-Hoop-User` is unavailable on this lane.
`../opa/authz.rego` reads the identity off `Authorization: Bearer` on port
8447 instead, through a static token-to-user map that mirrors
`tokens.csv`, the file the apiserver authenticates against. Same stand-in
for a verified JWT as the header on the other lanes, different carrier.

OPA writes the user it resolved back onto the request as `x-hoop-user`, so
Envoy's access log and the sidecar's audit row name the same principal. The
token itself never reaches the audit trail: `Authorization` cannot be
allowlisted, and the relay refuses a config that tries.

## What the demo shows

1. **Tier 1.** bob's token resolves to bob, who has no grant on
   `kubernetes`; kubectl prints OPA's body as the server's error. No token
   is a 401 before the apiserver could say the same.
2. **HTTP, allowed.** `kubectl get pods`: TLS terminated by Envoy, HTTP/1.1
   to the relay, TLS again to the apiserver, verified against the cluster CA.
3. **HTTP, denied.** `kubectl get configmap 111.444.777-35` is refused with
   the relay's 403 by `no-cpf-in-query`, the process's one guardrail rule,
   which scans the request line. The apiserver never saw it.
4. **HTTP, not masked.** The `customers` ConfigMap carries the fixture from
   `../upstream/seed.sql`. The apiserver answers every request with
   `Transfer-Encoding: chunked`, and http masking substitutes bytes only
   under a `Content-Length` it can correct, so the emails come back in the
   clear. `config-kubernetes.yaml` switches the mask rule off on this lane
   rather than carry one that never fires.
5. **WebSocket.** `kubectl exec demo -- ...` opens
   `GET /api/v1/namespaces/default/pods/demo/exec` with `Upgrade: websocket`
   and is answered `101 Switching Protocols`, subprotocol
   `v5.channel.k8s.io`. The command's output comes back through Envoy, OPA
   and the relay. The audit trail has the GET, the 101 and the allowlisted
   `upgrade` and `sec-websocket-protocol` headers.

## WebSocket, and what the relay sees after the 101

Envoy needs `upgrade_configs: [{upgrade_type: websocket}]` on the listener,
or it answers the Upgrade with a plain 200 and kubectl reports "unable to
upgrade connection". The route timeout is off on this listener, because an
exec or watch stream stays open as long as the user does.

The relay is a byte relay: it forwards what it is given whether or not the
codec produced a statement for it, so the exec session works regardless of
what the codec makes of the frames. What lands in the audit trail after the
101 depends on the http codec in the libhoop checkout this stack builds
from (`../sidecar/Dockerfile` takes it from `../../../../libhoop`):

- **The pinned codec** (`sidecar/go.mod`'s version) knows CONNECT tunnels
  and nothing about 101. It records the GET and the 101, then holds the
  frames as an incomplete HTTP head until they exceed 1 MiB or a byte
  sequence in them fails to parse, and forwards throughout. A short exec
  produces two rows and no error; a long one produces an `inspect: malformed
  message` error row per megabyte per direction. Frames are opaque to policy
  and masking either way.
- **A WebSocket-aware codec** reads RFC 6455 frames after the 101: each
  message becomes a `ws_message` row and the close frame a `ws_close` row,
  both carrying the opening GET's path and resource, with `http.proto:
  websocket`. Text messages the server sends can be masked by re-framing;
  exec's `v5.channel.k8s.io` frames are binary and are never handed to a
  masker.

`./kubernetes/demo-kubernetes.sh` prints the trail; read the exec session's
rows to see which one you have.

## Files

| Path | What it is |
|---|---|
| `docker-compose.kubernetes.yml` | the overlay: `k3s`, `k3s-ca`, kubectl on `client`, config swaps for `hoop-inspect` and `envoy`, host port `8447` |
| `envoy-kubernetes.yaml` | base Envoy config plus the `:8447` HTTPS lane with `upgrade_configs` and no route timeout |
| `config-kubernetes.yaml` | sidecar config: `appdb`, `httpbin` and the new `kubernetes` lane, verified upstream TLS, masking off |
| `tokens.csv` | the apiserver's static token file: alice in `system:masters`, bob in `developers` |
| `kubeconfig.yaml` | kubectl's view from the sandbox: `envoy:8447`, Envoy's cert, both tokens |
| `manifests/demo.yaml` | auto-deployed by k3s: the `demo` pod exec targets and the `customers` ConfigMap |
| `demo-kubernetes.sh` | the walk above |

`k3s-ca` exists because the cluster CA sits in a root-only directory of the
k3s volume and the sidecar runs as uid 10001; it copies the one file the
sidecar verifies against into a volume the sidecar can read and exits.
`../opa/authz.rego` gains the `kubernetes` service, alice's grant on it and
the token map; nothing else in the parent stack changes.
