# Claude Managed Agents: kubectl through the sidecar

A Claude Managed Agents cloud agent runs kubectl against one Kubernetes
cluster, and only through a hoop sidecar. The sidecar refuses deletes, masks
Secret values in kubectl's JSON responses and records each request.

```
cloud sandbox, limited networking          your cluster
+----------------------------+   HTTPS    +--------------------+  HTTPS  +------------+
| agent: kubectl             |--egress--->| hoop sidecar       |-------->| API server |
| kubeconfig: vault          |  proxy     | refuse delete,     |         | (token,    |
|   placeholder              |  fills the | mask Secrets, audit|         |  RBAC)     |
+----------------------------+  token     +--------------------+         +------------+
  any other host: refused
```

The agent cannot skip the sidecar:

- The environment allows one host, the sidecar's. The egress proxy refuses
  every other host, the API server's included.
- The sandbox holds a vault placeholder. The egress proxy writes the real
  token into the `Authorization` header, and only toward the sidecar's host.
- The sidecar refuses a `DELETE` before any byte of it reaches the API
  server, and masks a JSON response before any byte of it reaches the
  sandbox.

## Files

| Path | Holds |
|---|---|
| `values.yaml` | Sidecar chart values: one HTTPS lane to the API server of the cluster it runs in, the `no-delete` guardrail, the `secret-values` mask rule, `analytics.installSource` |
| `rbac.yaml` | The agent's ServiceAccount, its token and its roles |
| `ant/` | An `ant apply` project: the environment, the vault, an example agent |
| `credentials/` | The vault credential, without its secret |
| `run.sh`, `demo.sh`, `e2e.sh` | A local cluster, checks with no Anthropic call, and a real session (see "Try it without a public host") |

## Before you start

- A Kubernetes cluster that can publish a `LoadBalancer` Service, and a DNS
  name for it. Below, `SIDECAR_HOST` is that name.
- A TLS certificate for `SIDECAR_HOST` from a public CA.
- `helm`, `kubectl`, `jq`, `curl`, and `ant` 1.42.0 or later
  (`brew install anthropics/tap/ant`).
- `ANTHROPIC_API_KEY`: a key with Managed Agents access.

## 1. Run the sidecar

The chart runs in the cluster it guards. The lane calls
`https://kubernetes.default.svc` and trusts the CA that Kubernetes mounts
with the pod's ServiceAccount token, so keep that mount on. In `rbac.yaml`,
replace `demo` with the namespace the agent manages.

```bash
kubectl create namespace hoop
kubectl -n hoop create secret tls hoop-sidecar-tls --cert=tls.crt --key=tls.key
kubectl apply -f rbac.yaml    # read rbac.yaml first: it decides what the agent may do
helm install hoop-kube oci://ghcr.io/hoophq/helm-charts/hoopsidecar-chart -n hoop -f values.yaml
kubectl -n hoop get svc hoop-kube-hoopsidecar-public    # point SIDECAR_HOST at EXTERNAL-IP
```

## 2. Create the environment, vault and agent

Replace `sidecar.example.com` with `SIDECAR_HOST` in three files:
`ant/environments/hoop-kubernetes.yaml`, `ant/agents/kubernetes-operator.md`
and `credentials/kube-token.yaml`. Then:

```bash
cd ant && ant apply .    # writes claude-lock.json; commit it
lockid() { jq -r --arg f "$1" '.resources[$f].id' claude-lock.json; }
AGENT_ID=$(lockid ./agents/kubernetes-operator.md)
ENV_ID=$(lockid ./environments/hoop-kubernetes.yaml)
VAULT_ID=$(lockid ./vaults/hoop-kubernetes.yaml)
cd ..
```

## 3. Upload kubectl

The sandbox has no kubectl, no setup step, and no route to a download host.
Upload the official binary once, as a file each session mounts. Pick a
version within one minor release of your API server.

```bash
v=v1.35.5; base=https://dl.k8s.io/release/$v/bin/linux/amd64
curl -fsSLO "$base/kubectl"
echo "$(curl -fsSL "$base/kubectl.sha256")  kubectl" | shasum -a 256 -c
FILE_ID=$(ant beta:files upload --file kubectl --transform id --raw-output)
```

## 4. Add the credential

`ant apply` cannot hold a secret, so the credential is created from its file
plus the token. `@<(...)` keeps the token out of the process list and off
the disk.

```bash
ant beta:vaults:credentials create --vault-id "$VAULT_ID" \
  --auth.secret-value @<(kubectl -n hoop get secret claude-agent-token -o jsonpath='{.data.token}' | base64 -d) \
  < credentials/kube-token.yaml
```

The agent writes `$KUBE_TOKEN` into its kubeconfig. The sidecar forwards the
`Authorization` header unchanged, so the API server checks the real token
and RBAC decides as usual.

## 5. Start a session

```bash
SESSION_ID=$(ant beta:sessions create --agent "$AGENT_ID" \
  --environment-id "$ENV_ID" --vault-id "$VAULT_ID" \
  --resource "{type: file, file_id: $FILE_ID, mount_path: /kubectl}" \
  --transform id --raw-output)
ant beta:sessions:events send --session-id "$SESSION_ID" --event \
  "{type: user.message, content: [{type: text, text: 'Which pods run in namespace demo?'}]}"
ant beta:sessions connect --session-id "$SESSION_ID"
```

## 6. Read the audit trail

```bash
kubectl -n hoop logs deploy/hoop-kube-hoopsidecar | jq -cR 'fromjson?
  | select(.direction == "client" and (.kind == "statement" or .kind == "violation"))
  | {timestamp, kind, statement, rule, command: .http.headers["kubectl-command"]}'
```

Each request writes one `client` row: `statement`, or `violation` when the
sidecar refuses it. An admitted request also writes a `server` row for its
response, and a masked response a `masked` row.

## What to expect

| The agent runs | Result |
|---|---|
| `kubectl get pods` | The pods |
| `kubectl delete pod web-...` | `Error from server (Forbidden): this agent may not delete cluster objects`; the API server receives nothing |
| `kubectl get secret db-credentials -o yaml` | Each value, and the `last-applied-configuration` annotation, read `[REDACTED:K8S_DATA]` |
| `kubectl get secrets` | Names, types and key counts |
| `kubectl scale deploy/web --replicas=3` | Scaled: `rbac.yaml` grants the scale subresource, and the sidecar refuses only deletes |
| `curl` to the API server's own host | `Could not resolve host`: the sandbox reaches no host but the sidecar's |

## Try it without a public host

`run.sh` starts a k3d cluster with this template: the chart from this
repository, `values.yaml` and `rbac.yaml` unchanged, a self-signed
certificate, and a demo namespace with a Deployment, a Secret and a
ConfigMap.

- `./run.sh && ./demo.sh` checks every claim above on your machine, with no
  Anthropic call, and that the lane serves kubectl over HTTP/2.
- `./run.sh --tunnel && ./e2e.sh` adds two Cloudflare quick tunnels, one to
  the lane and one straight to the API server, and runs a real cloud
  session. It uploads kubectl, applies the `ant` files from a temporary
  copy, checks the session's output and the audit trail, writes
  `evidence/<time>/`, and removes what it created. `MODEL=` overrides the
  agent's model.
- `./run.sh down` removes the tunnels and the cluster.

## Limits

- **No caller check at the sidecar.** kubectl cannot send `X-Hoop-Token`, so
  the lane admits any caller and the API server's token check is the only
  gate. The public URL exposes the API server: a stranger gets its `401`, or
  what its anonymous access allows. Rows name `anonymous`. ENG-811 tracks a
  bearer check at the sidecar.
- **No session id in rows.** kubectl cannot add a header. Correlate rows by
  time and by `Kubectl-Command`.
- **Free tier.** One guardrail rule and one mask rule per process, spent on
  `no-delete` and `secret-values`. A license lifts both caps; `values.yaml`
  carries the `no-secret-contents` rule to add then.
- **Only JSON is masked.** kubectl asks for JSON. A client that asks for YAML
  or Protobuf gets Secret values in clear, and the agent can send one with
  curl and `$KUBE_TOKEN`. The licensed `no-secret-contents` rule refuses
  both. Until then, `rbac.yaml` grants Secret read in one namespace only.
- **`data` is masked on every object.** A ConfigMap's values read
  `[REDACTED:K8S_DATA]` too: a mask rule names a JSON key, not a resource.
- **Client-side `kubectl apply` fails.** It reads the masked
  `last-applied-configuration` annotation. Use `kubectl apply --server-side`.
- **The sidecar does not mask a workload's output.** A pod spec can put a
  Secret in a file or an environment variable, and `kubectl logs` prints it
  as text. `rbac.yaml` grants no create, patch or update on pods or
  workloads, and no `exec`, `attach` or `port-forward`. Keep it that way.
- **Delete means the `DELETE` method.** An eviction is a `POST` and scaling
  to zero is a `PATCH`. `rbac.yaml` grants no eviction, but it grants the
  scale subresource: remove it if zero replicas is as bad as a delete.
- **HTTP/2 toward a real `LoadBalancer` is untested.** kubectl in the
  sandbox speaks HTTP/2 to the egress proxy, and the lane serves kubectl
  HTTP/2 (`demo.sh`). `e2e.sh` reaches the lane through a Cloudflare tunnel,
  so the protocol from the egress proxy to the lane is not checked. The lane
  serves HTTP/1.1 too.
- **A response over 16 MiB passes unmasked.** kubectl pages a list at 500
  objects, and a page of large Secrets can still pass that size.
- **One long-lived token.** To rotate it, delete `claude-agent-token`, apply
  `rbac.yaml` again so Kubernetes fills a new one, and replace the vault
  credential.
