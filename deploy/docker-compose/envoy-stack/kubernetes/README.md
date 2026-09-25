# Kubernetes lane

> hoop-inspect 0.1.0

An overlay on the stack in the parent directory: a one-node k3s cluster
behind a `protocol: http` lane, beside the postgres and httpbin ones. Plain
requests (`kubectl get`) and the WebSocket that `kubectl exec` opens cross
the same listener. The lane carries the process's one guardrail and its one
mask rule; the parent lanes carry none in this overlay.

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

## No body anywhere

Every request kubectl makes here is a `GET` with no body: method, path,
query string and headers. On this API that is enough to say what is being
done, and the three controls on the lane read exactly that:

- **A guardrail on a header.** `kubectl get secrets` asks for the table view
  with `Accept: application/json;as=Table;v=v1;g=meta.k8s.io,...`;
  `kubectl get secret X -o yaml` asks for the object with
  `Accept: application/json`. Same path, different intent, and the
  difference is one header. `no-secret-contents` is an `http_header` rule
  scoped to `GET /api/v1/namespaces/*/secrets/*` that matches the second
  form: listing secrets is fine, reading one is refused with the relay's
  403. The rule can only read headers the lane allowlists under
  `http.headers`; naming one it does not capture is refused at load.
- **Masking by JSON key.** `k8s-data` is a `columns: [data]` rule. The
  http codec walks a JSON response value by value and hands each to the
  masker under its key path (`data.ada`, `items.data.password`), so a column
  rule names a JSON key the way it names a result-set column and masks it
  whatever it holds. `kubectl get configmap customers -o json` comes back
  with every `data` value redacted; the table view, which never carries
  `data`, is untouched. The apiserver chunks every response; the codec
  re-chunks what it rewrote, so chunked framing is no longer a reason a
  value comes back in the clear (it was, under byte substitution).
- **AI analysis of a bodiless request.** Off in this overlay (it needs a
  credential), commented in `config-kubernetes.yaml`. With it on, the model
  receives the request line, the resource and the allowlisted headers:

  ```
  GET /api/v1/namespaces/default/secrets/db-credentials
  Accept: application/json
  Kubectl-Command: kubectl get
  X-Hoop-User: alice
  ```

  The cache keys on those headers too, so the table view and the object
  view of one secret are two shapes and two verdicts. `Kubectl-Session`, a
  UUID per invocation, is deliberately not allowlisted: it would make every
  request a model call.

The free tier allows one guardrail and one mask rule per process, which is
why the parent lanes carry none here and why the process defaults are empty.

## What the demo shows

1. **Tier 1.** bob's token resolves to bob, who has no grant on
   `kubernetes`; kubectl prints OPA's body as the server's error. No token
   is a 401 before the apiserver could say the same.
2. **HTTP, allowed.** `kubectl get pods`: TLS terminated by Envoy, HTTP/1.1
   to the relay, TLS again to the apiserver, verified against the cluster CA.
3. **Listing secrets, allowed.** The table view's `Accept` does not match
   the rule.
4. **Reading a secret, denied.** `kubectl get secret db-credentials -o yaml`
   is refused with the relay's 403 by `no-secret-contents`. The apiserver
   never saw it.
5. **Masked by key.** `kubectl get configmap customers -o jsonpath=...`
   prints `[REDACTED:K8S_DATA]` three times; the table view of the same
   ConfigMap is unchanged.
6. **WebSocket.** `kubectl exec demo -- ...` opens
   `GET /api/v1/namespaces/default/pods/demo/exec` with `Upgrade: websocket`
   and is answered `101 Switching Protocols`, subprotocol
   `v5.channel.k8s.io`. The command's output comes back through Envoy, OPA
   and the relay. The audit trail has the GET, the 101, one `ws_message` row
   per frame after it and the `ws_close`, all keyed on the exec path.

## WebSocket, and what the relay sees after the 101

Envoy needs `upgrade_configs: [{upgrade_type: websocket}]` on the listener,
or it answers the Upgrade with a plain 200 and kubectl reports "unable to
upgrade connection". The route timeout is off on this listener, because an
exec or watch stream stays open as long as the user does.

The relay is a byte relay: it forwards what it is given whether or not the
codec produced a statement for it, so the exec session works regardless of
what the codec makes of the frames. The http codec reads RFC 6455 frames
after the 101: each message becomes a `ws_message` row and the close frame a
`ws_close` row, both carrying the opening GET's path and resource, with
`http.proto: websocket`. exec's `v5.channel.k8s.io` frames are binary — one
channel byte, then the stream's bytes — and are recorded as such; only text
messages are ever handed to a masker.

`./kubernetes/demo-kubernetes.sh` prints the trail; the exec session's rows
are the last block.

## Files

| Path | What it is |
|---|---|
| `docker-compose.kubernetes.yml` | the overlay: `k3s`, `k3s-ca`, kubectl on `client`, config swaps for `hoop-inspect` and `envoy`, host port `8447` |
| `envoy-kubernetes.yaml` | base Envoy config plus the `:8447` HTTPS lane with `upgrade_configs` and no route timeout |
| `config-kubernetes.yaml` | sidecar config: `appdb`, `httpbin` and the `kubernetes` lane with its header rule, `data` mask rule, header allowlist and a commented analyzer |
| `tokens.csv` | the apiserver's static token file: alice in `system:masters`, bob in `developers` |
| `kubeconfig.yaml` | kubectl's view from the sandbox: `envoy:8447`, Envoy's cert, both tokens |
| `manifests/demo.yaml` | auto-deployed by k3s: the `demo` pod exec targets, the `customers` ConfigMap masking rewrites, the `db-credentials` Secret the guardrail protects |
| `demo-kubernetes.sh` | the walk above |

`k3s-ca` exists because the cluster CA sits in a root-only directory of the
k3s volume and the sidecar runs as uid 10001; it copies the one file the
sidecar verifies against into a volume the sidecar can read and exits.
`../opa/authz.rego` gains the `kubernetes` service, alice's grant on it and
the token map; nothing else in the parent stack changes.
