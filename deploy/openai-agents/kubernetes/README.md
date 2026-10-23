# OpenAI Agents API: kubectl through the sidecar

An agent in an OpenAI Agents API hosted sandbox runs kubectl against one
Kubernetes cluster, and only through a hoop sidecar. The sidecar refuses
deletes, masks Secret values in kubectl's JSON responses and records each
request with the OpenAI session id.

```
hosted sandbox, restricted network         your cluster
+----------------------------+   HTTPS    +--------------------+  HTTPS  +------------+
| agent: kubectl             |--egress--->| hoop sidecar       |-------->| API server |
| kubeconfig: vault          |  proxy     | refuse delete,     |         | (token,    |
|   placeholder, session id  |  fills the | mask Secrets, audit|         |  RBAC)     |
+----------------------------+  token     +--------------------+         +------------+
  any other host: refused
```

The agent cannot skip the sidecar:

- The environment template allows one host, the sidecar's. The egress proxy
  refuses every other host, the API server's included.
- The sandbox holds a vault placeholder. The egress proxy writes the real
  token into the `Authorization` header, and only toward the sidecar's host.
- The sidecar refuses a `DELETE` before any byte of it reaches the API
  server, and masks a JSON response before any byte of it reaches the
  sandbox.

## Files

| Path | Holds |
|---|---|
| `values.yaml` | Sidecar chart values: one HTTPS lane to the API server of the cluster it runs in, the `no-delete` guardrail, the `secret-values` mask rule, `analytics.installSource` |
| `rbac.yaml` | The agent's ServiceAccount, its token and its roles: object access in one namespace, plus one cluster-scoped rule for the session extra |
| `openai/` | The environment template, the vault, its credential without the secret, the agent and its instructions, and `kubeconfig.sh`, which runs in the sandbox |
| `openai.sh` | Creates and deletes the OpenAI side with `curl` and `jq`, and starts a session |
| `run.sh`, `demo.sh`, `e2e.sh` | A local cluster, checks with no OpenAI call, and a real session (see "Try it without a public host") |

## Before you start

- A Kubernetes cluster that can publish a `LoadBalancer` Service, and a DNS
  name for it. Below, `SIDECAR_HOST` is that name.
- A TLS certificate for `SIDECAR_HOST` from a public CA.
- `helm`, `kubectl`, `jq`, `curl` and `shasum`.
- `OPENAI_API_KEY`: a key with Agents API access.

## 1. Run the sidecar

The chart runs in the cluster it guards. The lane calls
`https://kubernetes.default.svc` and trusts the CA that Kubernetes mounts
with the pod's ServiceAccount token, so keep that mount on. The agent
manages namespace `demo`, which must exist: to pick another, replace `demo`
in `rbac.yaml` and in `openai/kubeconfig.sh`.

```bash
kubectl create namespace hoop
kubectl -n hoop create secret tls hoop-sidecar-tls --cert=tls.crt --key=tls.key
kubectl apply -f rbac.yaml    # read rbac.yaml first: it decides what the agent may do
helm install hoop-kube oci://ghcr.io/hoophq/helm-charts/hoopsidecar-chart -n hoop -f values.yaml
kubectl -n hoop get svc hoop-kube-hoopsidecar-public    # point SIDECAR_HOST at EXTERNAL-IP
```

## 2. Create the OpenAI side

```bash
SIDECAR_HOST=sidecar.example.com ./openai.sh create
```

It reads the agent's token from the cluster in your current kubectl context
(`KUBE_CONTEXT=` picks another), writes `SIDECAR_HOST` in place of
`sidecar.example.com`, and records each id in `.openai`. The token goes to
the vault through a pipe: it is in no file and no process's argv. It
creates:

- **A file: kubectl.** The sandbox has no kubectl and no route to a
  download host. kubectl is 56 MiB and an environment file holds 50 MiB at
  most, so the file is the release's client tarball (33 MiB), checked
  against its published SHA-512. `KUBECTL_VERSION=` picks the version; keep
  it within one minor release of your API server.
- **An environment template**: `restricted` network access with
  `SIDECAR_HOST` as the only allowed host, the tarball, and
  `openai/kubeconfig.sh`.
- **A vault** with one credential, `KUBE_TOKEN`, bound to `SIDECAR_HOST`.
- **An agent**, `kubernetes-operator`, with `openai/instructions.md`.
  `MODEL=` overrides its model.

## 3. Start a session

```bash
./openai.sh ask 'Which pods run in namespace demo?'
```

The sandbox has no variable that names the session, so the first message
gives the id. The agent then runs `openai/kubeconfig.sh` once: it unpacks
kubectl and writes a kubeconfig in which the agent impersonates its own
ServiceAccount with one extra, `hoop-session`. kubectl sends that extra as
the `Impersonate-Extra-Hoop-Session` header, and the sidecar records it.
The API server runs the request as the same ServiceAccount, with the same
groups.

## 4. Read the audit trail

```bash
kubectl -n hoop logs deploy/hoop-kube-hoopsidecar | jq -cR 'fromjson?
  | select(.direction == "client" and (.kind == "statement" or .kind == "violation"))
  | {timestamp, kind, statement, rule, session: .http.headers["impersonate-extra-hoop-session"],
     command: .http.headers["kubectl-command"]}'
```

Each request writes one `client` row: `statement`, or `violation` when the
sidecar refuses it. An admitted request also writes a `server` row for its
response, and a masked response a `masked` row.

## What to expect

| The agent runs | Result |
|---|---|
| `kubectl get pods` | The pods |
| `kubectl delete pod web-...` | `Error from server (Forbidden): this agent may not delete cluster objects`; the API server receives nothing |
| `kubectl get secret db-credentials -o json` | Each value, and the `last-applied-configuration` annotation, read `[REDACTED:K8S_DATA]` |
| `kubectl get secrets` | Names, types and key counts |
| `kubectl scale deploy/web --replicas=3` | Scaled: `rbac.yaml` grants the scale subresource, and the sidecar refuses only deletes |
| `kubectl get pods -n kube-system` | `Forbidden` from the API server: `rbac.yaml` grants one namespace |
| `curl` to the API server's own host | `CONNECT tunnel failed, response 403`: the egress proxy reaches no host but the sidecar's |

## Try it without a public host

`run.sh` starts a k3d cluster with this template: the chart from this
repository, `values.yaml` and `rbac.yaml` unchanged, a self-signed
certificate, and a demo namespace with a Deployment, a Secret and a
ConfigMap. It uses other ports than the Managed Agents template, so both
can run at once.

- `./run.sh && ./demo.sh` checks the kubectl, identity, audit and "Limits"
  claims on your machine, with no OpenAI call, through the kubeconfig that
  `openai/kubeconfig.sh` writes.
- `./run.sh --tunnel && ./e2e.sh` adds two Cloudflare quick tunnels, one to
  the lane and one straight to the API server, and runs a real hosted
  session. It creates the OpenAI side with `openai.sh`, checks the
  session's output and the audit trail, writes `evidence/<time>/`, and
  deletes what it created. `MODEL=` overrides the agent's model.
- `./run.sh down` removes the tunnels and the cluster.

## Limits

- **No caller check at the sidecar.** kubectl cannot send `X-Hoop-Token`, so
  the lane admits any caller and the API server's token check is the only
  gate. The public URL exposes the API server: a stranger gets its `401`, or
  what its anonymous access allows. Rows name `anonymous`. ENG-811 tracks a
  bearer check at the sidecar.
- **The session id is the agent's to send.** It comes from the first
  message, and the agent writes it: a row names the session the agent
  claims. A kubeconfig without the extra still works, and its rows name no
  session.
- **The vault fills a placeholder anywhere in a request.** In a URL or a
  body too, not only in the `Authorization` header (ENG-860), and the vault
  has no setting to limit it. An agent that runs
  `kubectl get pod "$KUBE_TOKEN"` reads the real token in the API server's
  `not found` error, and the sidecar's row for that request records it.
  With the token and no caller check, a caller reaches the sidecar from
  outside the sandbox; the sidecar's policy still applies. ENG-871 tracks a
  fix at the sidecar.
- **The extra needs one cluster-scoped rule.** Kubernetes checks an
  impersonated extra with no namespace, so `rbac.yaml` holds a ClusterRole
  for `userextras/hoop-session`. It grants no access to any object, and the
  agent can impersonate no ServiceAccount but its own.
- **Free tier.** One guardrail rule and one mask rule per process, spent on
  `no-delete` and `secret-values`. A license lifts both caps; `values.yaml`
  carries the `no-secret-contents` rule to add then.
- **Only JSON is masked.** kubectl asks for JSON. A client that asks for YAML
  or Protobuf gets Secret values in clear, and the agent can send one with
  curl and `$KUBE_TOKEN`. This includes a YAML Table with
  `?includeObject=Object`, which carries each whole Secret; as JSON, the
  mask covers it. The licensed `no-secret-contents` rule refuses YAML and
  Protobuf reads. Until then, `rbac.yaml` grants Secret read in one
  namespace only.
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
  sandbox speaks HTTP/2 to the egress proxy (`e2e.sh`), and the lane serves
  kubectl HTTP/2 (`demo.sh`). `e2e.sh` reaches the lane through a Cloudflare
  tunnel, so the protocol from the egress proxy to the lane is not checked.
  The lane serves HTTP/1.1 too.
- **A response over 16 MiB passes unmasked.** kubectl pages a list at 500
  objects, and a page of large Secrets can still pass that size.
- **One long-lived token.** To rotate it, delete `openai-agent-token`, apply
  `rbac.yaml` again so Kubernetes fills a new one, then run
  `./openai.sh delete` and `./openai.sh create`.
- **linux/amd64 only.** The tarball is the amd64 build, the architecture of
  the hosted sandbox.
- **OpenAI's own limits.** The Agents API keeps data in the US only and is
  not eligible for Zero Data Retention
  ([Agents API](https://developers.openai.com/api/docs/guides/agents-api/overview)).
  `allowed_domains` takes exact hosts only: a redirect target needs its own
  entry. A sandbox with no activity and no keep-alive for an hour can be
  deleted, and the timeout is not configurable
  ([hosted environments](https://developers.openai.com/api/docs/guides/agents-api/environments/openai-hosted)).
