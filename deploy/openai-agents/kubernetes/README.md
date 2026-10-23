# OpenAI Agents API: kubectl through the sidecar

An agent in an OpenAI Agents API hosted sandbox runs kubectl against one
Kubernetes cluster, and only through a hoop sidecar. The sidecar refuses
deletes, masks Secret values in kubectl's JSON responses and records each
request with the OpenAI session id. `deploy/managed-agents/kubernetes` is the
Claude twin.

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
  refuses every other host, the API server's included, and a session cannot
  widen the template's network.
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
| `setup.sh` | Creates and deletes the OpenAI side from `openai/`, and starts a session |
| `openai/` | The request bodies: the environment template, the vault, the credential without its secret, the agent (`agent.json`, instructions in `agent.md`), the session; and `kubeconfig.sh`, which runs in the sandbox |
| `run.sh`, `demo.sh`, `e2e.sh` | A local cluster, checks with no OpenAI call, and a real session (see "Try it without a public host") |

## Before you start

- A Kubernetes cluster that can publish a `LoadBalancer` Service, and a DNS
  name for it. Below, `SIDECAR_HOST` is that name.
- A TLS certificate for `SIDECAR_HOST` from a public CA.
- `helm`, `kubectl`, `shasum`, `curl` 7.76 or later and `jq` 1.6 or later.
- `OPENAI_API_KEY`: a key with Agents API access.
- `hoophq/hoopsidecar` 1.263.0 or later; `values.yaml` pins 1.263.0. Older
  releases report this template's install source as `other`.

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
SIDECAR_HOST=sidecar.example.com ./setup.sh create
```

It reads the agent's token from the cluster in your current kubectl context
(`KUBE_CONTEXT=` picks another), writes `SIDECAR_HOST` in place of
`sidecar.example.com`, and writes each id to `./.openai-ids`. It refuses a
second run until `delete`. The token goes to the vault through a pipe: it is
in no file and no process's argv. `./setup.sh create --dry-run` prints the
request bodies, with no secret and no API call. It creates:

- **A file: kubectl.** The sandbox has no kubectl and no route to a
  download host. kubectl is 56 MiB and an environment file holds 50 MiB at
  most, so the file is the release's client tarball (33 MiB), checked
  against its published SHA-512 on each run. `KUBECTL_VERSION=` picks the
  version; keep it within one minor release of your API server.
- **An environment template**: `restricted` network access with
  `SIDECAR_HOST` as the only allowed host, the tarball, and
  `openai/kubeconfig.sh`.
- **A vault** with one credential, `KUBE_TOKEN`, bound to `SIDECAR_HOST`.
- **An agent**, `kubernetes-operator`, with `openai/agent.md`. `MODEL=`
  overrides its model.

## 3. Start a session

```bash
./setup.sh session 'Which pods run in namespace demo?'
```

The sandbox has no variable that names the session, so the first message
gives the id. The agent then runs `openai/kubeconfig.sh` once: it unpacks
kubectl and writes a kubeconfig in which the agent impersonates its own
ServiceAccount with one extra, `hoop-session`. kubectl sends that extra as
the `Impersonate-Extra-Hoop-Session` header, and the sidecar records it.
The API server runs the request as the same ServiceAccount, with the same
groups.

In your own agent, use `$KUBE_TOKEN` only through that kubeconfig: never in
a kubectl argument, a URL or a body. Toward the sidecar's host the vault
fills a placeholder anywhere in the request. `openai/agent.md` says so to
the model.

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

## 5. Clean up

```bash
./setup.sh delete
helm -n hoop uninstall hoop-kube
kubectl delete -f rbac.yaml
```

`delete` removes the vault, the sessions, the agent, the template and the
file. If it fails halfway, run it again: it retries only what is left.

A network error during `create` or `session` can leave a resource that
`.openai-ids` does not list. Find it with `GET /v1/vaults`, `/v1/agents`,
`/v1/agents/environments/templates`, `/v1/agents/sessions` or `/v1/files`,
by the name `hoop kubernetes`, `kubernetes-operator` or
`kubernetes-client-linux-amd64.tar.gz`, and delete the ones you made.

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
  session. It creates the OpenAI side with `setup.sh`, checks the session's
  output and the audit trail, writes `evidence/<time>/`, and deletes what
  it created. `MODEL=` overrides the agent's model.
- `./run.sh down` removes the tunnels and the cluster.

## Limits

OpenAI's:

- **US data residency only, no ZDR.** The Agents API keeps data in the
  United States only and does not support Zero Data Retention. The sidecar
  does not change this: the prompts and the masked responses the agent
  reads go to OpenAI.
- **Exact hosts only.** `allowed_domains` takes exact host names: no
  wildcard, scheme, port or path. A redirect to another host fails.
- **An idle sandbox is deleted after one hour.** Its files go with it,
  kubectl and its kubeconfig included. The timeout is not configurable.
- **Keep the lane on 443 only.** The egress proxy also lets port 80 through
  to the sidecar's host, unencrypted, and the vault fills nothing there.
- **The vault fills a placeholder anywhere in the request.** Toward the
  sidecar's host it fills the URL and the body too, not only the
  `Authorization` header, and OpenAI has no setting to stop it. An agent
  that runs `kubectl get pod "$KUBE_TOKEN"` reads the real token in the API
  server's `not found` error, and the sidecar's row for that request
  records it. With the token and no caller check, a caller reaches the
  sidecar from outside the sandbox; the sidecar's policy still applies. The
  agent's instructions keep the token in the kubeconfig; a prompt is not a
  control. ENG-871 tracks a fix at the sidecar.
- **Hosted tools are untested.** `agent.json` lists no tools. Whether
  `allowed_domains` limits a hosted tool such as web search is untested; add
  none.
- **The lane's own certificate is untested from a hosted session.**
  `e2e.sh` reaches the lane through a Cloudflare tunnel, so the egress proxy
  saw Cloudflare's certificate, not the one in `hoop-sidecar-tls`, and the
  protocol from the egress proxy to the lane is not checked. kubectl in the
  sandbox speaks HTTP/2 to the egress proxy (`e2e.sh`), and the lane serves
  kubectl HTTP/2 and HTTP/1.1 (`demo.sh`).
- **linux/amd64 only.** The tarball is the amd64 build, the architecture of
  the hosted sandbox.

The sidecar's:

- **No caller check.** kubectl cannot send `X-Hoop-Token`, so the lane
  admits any caller and the API server's token check is the only gate. The
  public URL exposes the API server: a stranger gets its `401`, or what its
  anonymous access allows. Rows name `anonymous`. ENG-811 tracks a bearer
  check at the sidecar.
- **The session id is a label.** It comes from the first message, and the
  agent writes it, so it can write any value. It correlates rows; it does
  not prove identity. A kubeconfig without the extra still works, and its
  rows name no session.
- **One token for every agent.** The API server sees `openai-agent` for
  every session. Per-user rows need a per-user ServiceAccount.
- **The extra needs one cluster-scoped rule.** Kubernetes checks an
  impersonated extra with no namespace, so `rbac.yaml` holds a ClusterRole
  for `userextras/hoop-session`. It grants no access to any object, and the
  agent can impersonate no ServiceAccount but its own.
- **Free tier.** One guardrail rule and one mask rule per process, spent on
  `no-delete` and `secret-values`. These are the Managed Agents kubernetes
  template's rules; CI keeps the two templates equal. A license lifts both
  caps; `values.yaml` carries the `no-secret-contents` rule to add then.
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
- **A response over 16 MiB passes unmasked.** kubectl pages a list at 500
  objects, and a page of large Secrets can still pass that size.
- **One long-lived token.** To rotate it, delete `openai-agent-token`, apply
  `rbac.yaml` again so Kubernetes fills a new one, then run
  `./setup.sh delete` and `./setup.sh create`.
