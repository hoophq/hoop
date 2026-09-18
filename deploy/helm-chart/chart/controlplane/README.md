# hoopcontrolplane-chart

Deploys the **hoop control plane**: the gateway binary started with
`hoop start control-plane` (see [ADR-0013](../../../../docs/adr/0013-gateway-control-plane-mode.md)).
It serves the HTTP API and the web app, and administers a fleet of inspection
sidecars.

It carries **no traffic**. No gRPC on `:8010`, no protocol proxies, no agent
controller, and of the six transport plugins only Slack is started. The 21
routes that need the transport answer with an HTTP error rather than hanging.

## Install

```sh
cat - > ./values.yaml <<'YAML'
config:
  POSTGRES_DB_URI: 'postgres://<user>:<pwd>@<db-host>:5432/<dbname>'
  API_URL: 'https://cp.yourdomain.tld'
  IDP_ISSUER: 'https://idp-issuer-url'
  IDP_CLIENT_ID: 'client-id'
  IDP_CLIENT_SECRET: 'client-secret'
YAML
```

```sh
VERSION=$(curl -s https://releases.hoop.dev/release/latest.txt)
helm upgrade --install hoopcontrolplane \
  https://releases.hoop.dev/release/$VERSION/hoopcontrolplane-chart-$VERSION.tgz \
  -f values.yaml
```

## Authentication

There is no `AUTH_METHOD`. Setting any of `IDP_ISSUER`, `IDP_CLIENT_ID`,
`IDP_CLIENT_SECRET` or `IDP_URI` selects OIDC; setting none leaves local auth.

SAML is not configurable by environment at all — `newSamlProvider` reads it
from the database and refuses otherwise, and once that row exists the database
supplies the method too. An `authconfig` row overrides all of the above at
runtime.

## Requirements

- **An external PostgreSQL.** `POSTGRES_DB_URI` is required and `pglite://` is
  refused: it is single-node and serves one connection at a time. The control
  plane runs database migrations and the organization bootstrap at boot.
- **Gateway API CRDs**, only if you set `gatewayApi.enabled`. Without them, set
  `service.type: LoadBalancer` instead.

## The image and the mode

The chart sets no `command` and no `args`, and **neither can be configured** —
both are refused at render. `hoophq/hoopcontrolplane` runs
`hoop start control-plane` from its own `CMD`, behind a `tini` `ENTRYPOINT`.

That subcommand is the only way to select the mode — there is no `APP_MODE`
variable — so letting the image own it keeps exactly one place able to get it
wrong. A command here could boot a full gateway instead: gRPC on `:8010`, every
protocol proxy, all six transport plugins, from a chart named controlplane.
Overriding the entrypoint would also drop tini, and the control plane installs
no signal handler of its own (`gateway/api/server.go` ends in gin's blocking
`route.Run()` with no `http.Server.Shutdown`), so it would then ignore
`SIGTERM` and every pod delete would wait out the kill timeout.

To run a different build, point `repository` and `tag` at an image whose `CMD`
already starts the control plane.

Two flavours, same as `hoopsidecar-chart`:

| tag | rootfs |
|---|---|
| `<version>`, `latest` | Ubuntu 24.04 LTS + the binary, ca-certificates and tini |
| `<version>-distroless` | distroless static + the binary and a static tini |

The Ubuntu flavour carries no suffix and keeps a shell, so the tag you reach
for without thinking is the one you can debug. distroless is about two thirds
the size with no package manager and no shell; name it explicitly.

### The web UI is in the binary

`make embed-webapp` stages the webapp into the binary before `make build-go`,
so both flavours serve the UI with **no configuration and no webapp files on
disk** — which is also what makes a distroless flavour possible.

`STATIC_UI_PATH` is therefore **not rendered**, and setting it through
`extraSecret` breaks the UI: `resolveSource()` returns on a non-empty value
*without checking the path exists* and never falls through to the embed, so any
value serves API-only with a 404 at `/` — and nothing shows it, because
`/api/healthz` still answers 200 and the rollout completes.

## Replicas

`replicas` defaults to **1**. Read this before raising it.

socketmode opens one Slack websocket per process and nothing coordinates them:
no lease, no advisory lock, only in-process mutexes in `gateway/slack`. With
Slack configured for an organization, two replicas post every review twice and
race each other's clicks — and Slack documents that a payload may go to any open
connection with no pattern to rely on, so a click can be handled by the replica
that is not holding the waiting session. The verdict is written and the session
is never released.

Raising `replicas` is safe when **no organization in this deployment has Slack
configured**. Everything else in the process is stateless HTTP over a shared
database.

The chart does not enforce this. Whether Slack is configured lives in a
`private.plugins` row no chart can read. `helm install` prints a warning above
one replica.

Two related constraints the chart also cannot see:

- A gateway and a control plane pointed at **the same database** both read that
  row and both open a socket. One organization must not have Slack configured
  on both at once.
- `GET /api/ws` registers a WebSocket agent in the in-process broker, and
  `/rdpproxy/*` relays RDP through it — so such an agent turns a control plane
  into an RDP data plane. A deployment that must carry no traffic must not
  expose `/api/ws`. The default HTTPRoute matches `/` and therefore does.

## Probes

**Off by default.** `readinessProbe` and `livenessProbe` are both `{}`, and an
empty one renders nothing: the kubelet treats the container as ready as soon as
it starts and never restarts it for health. Whatever you put under either key
is rendered verbatim — any handler the [Probe schema][probe] accepts.

Off rather than a default, because the chart cannot know the listener's
protocol. `TLS_CERT` and `TLS_KEY` decide it, and either can arrive through
`extraSecret` or `existingSecret`, which no template can read. The kubelet does
not negotiate, so an `httpGet` probe must name a scheme that is right: HTTP
against a TLS listener gets a `400` and the pod never becomes ready. Trust is
not the issue — kubelet HTTPS probes always skip certificate verification and
there is no field to change that.

Health is `GET /api/healthz` on 8009, under `API_URL`'s path if it has one. In
control-plane mode that route is a static `200` that checks nothing, but
`StartAPI()` runs last — after migrations, the org bootstrap and the IdP — so
an answer at all means bootstrap finished.

```yaml
# plaintext listener
readinessProbe:
  httpGet:
    path: /api/healthz
    port: 8009
  initialDelaySeconds: 10
  periodSeconds: 10
livenessProbe:
  httpGet:
    path: /api/healthz
    port: 8009
  initialDelaySeconds: 10
  periodSeconds: 10
```

For a TLS listener, add `scheme: HTTPS` inside `httpGet`. A `tcpSocket` check
on 8009 serves either listener from one stanza, at the cost of not calling the
endpoint.

Ten seconds and not the sidecar's five, for that bootstrap.

[probe]: https://kubernetes.io/docs/reference/kubernetes-api/workload-resources/pod-v1/#Probe

## TLS

Two keys: `config.TLS_CERT` and `config.TLS_KEY`.

**Set both to serve HTTPS, leave both empty to serve plaintext.** That is the
whole rule and it is the one the binary uses — nothing is generated, and half a
pair serves plaintext rather than failing. Nothing here reaches the probes: the
chart renders none, so an `httpGet` probe you write must name `scheme: HTTPS`
itself.

Both values go through `envloader`, so `base64://<b64>` and `file:///path` work;
mount the file with `extraVolumes`.

`USE_TLS` and `HOOP_TLSCA` are not rendered. Nothing reads `USE_TLS` any
longer, and `HOOP_TLSCA` is the CA the in-process gRPC clients verify the
gateway's certificate with — clients that only run in gateway mode.

## Exposing it

`gatewayApi` renders a `Gateway` (optional) and an `HTTPRoute`. Unlike the
gateway chart, both `parentRefs` and `rules` have working defaults — the default
rule is a `PathPrefix` on `API_URL`'s path backed by this chart's Service on
8009, which is the whole control plane.

```yaml
gatewayApi:
  enabled: true
  gateway:
    gatewayClassName: istio
    listeners:
    - name: http
      hostname: cp.yourdomain.tld
      port: 80
      protocol: HTTP
      allowedRoutes:
        namespaces: {from: Same}
  httpRoute:
    hostnames: [cp.yourdomain.tld]
```

Turning the Service off while the default route is in use is refused too: that
route's backend is the Service, so it would resolve to nothing. Set
`httpRoute.rules` naming a backend of your own, or leave the Service on.

To attach to a Gateway somebody else owns, set `createGateway: false` and name
it in `httpRoute.parentRefs` — the chart refuses to render without it, because
a route with no parent attaches to nothing and looks installed.

There is no Ingress template and no GRPCRoute. A cluster without the Gateway API
CRDs uses `service.type: LoadBalancer`.

## `API_URL` with a path

`appconfig` parses `API_URL` with `url.Parse` and mounts every route under its
path component. `https://cp.example.tld/hoop` serves health at
`/hoop/api/healthz`. The default HTTPRoute match follows it, and so must any
`httpGet` probe you write.

`API_URL` must carry a scheme. Without one the hostname is parsed as a path and
every route is mounted under it; the chart refuses that.

## What this chart does not render

The control plane reads a fraction of what the gateway does. The keys below are
the gateway's; this chart does not render them, and naming one under `config`
does nothing. Use `extraSecret` if you have a reason to set one anyway.

| Key | Why not |
|---|---|
| `GRPC_URL`, `DEFAULT_AGENT_GRPC_*` | no gRPC listener and no default agent container |
| `USE_TLS`, `HOOP_TLSCA` | nothing reads `USE_TLS` any more; `HOOP_TLSCA` is the gRPC client's verify CA, and those clients are gateway-only |
| `HOOP_TLS_SKIP_VERIFY`, `GATEWAY_ALLOW_PLAINTEXT` | gRPC transport settings |
| `TLS_CA` | read nowhere in `gateway/`; the gateway chart's key is a typo for `HOOP_TLSCA` |
| `HOOP_SPIFFE_*` | agent JWT-SVID validation. No agent authenticates here |
| `DLP_*`, `MSPRESIDIO_*`, `GOOGLE_APPLICATION_CREDENTIALS_JSON` | masking runs on the agent and in the sidecar |
| `RDP_*`, `SSH_CLIENT_HOST_KEY` | protocol proxies, not started |
| `PLUGIN_AUDIT_PATH`, `PLUGIN_INDEX_PATH` | the audit plugin is not started; this chart mounts no WAL volume |
| `AGENTCONTROLLER_CREDENTIALS` | the agent controller is not started |
| `ASK_AI_CREDENTIALS` | the analyzer credential belongs with the process that calls the model |
| `WEBHOOK_APPKEY`, `WEBHOOK_APPURL` | the webhooks plugin is a transport plugin and is not started |
| `LICENSE_SIGNING_KEY` | issuer-side. ADR-0016 has the control plane hand down the signed document it reads from the database; it never signs one |
| `INTEGRATION_AWS_INSTANCE_ROLE_ALLOW` | agent-side credential resolution |
| `EVENT_ROUTING_WORKERS` | falls back to a default; nobody has needed to tune it here |
| `API_KEY` | deprecated |
| `IDP_URI`, `URL_TOKEN_EXCHANGE`, `ORG_MULTI_TENANT` | not exposed by this chart |
| `WEBAPP_USERS_MANAGEMENT`, `DISABLE_SESSIONS_DOWNLOAD`, `DISABLE_CLIPBOARD_COPY_CUT` | web app toggles, out of scope |
| `AUTH_METHOD` | inferred: any `IDP_*` key selects OIDC, none leaves local. SAML is configured in the database and cannot be selected by environment |
| `STATIC_UI_PATH` | the web UI is compiled into the binary; any path here serves API-only with a 404 at `/` while the pod stays ready. Setting it through `extraSecret` breaks the UI |
| `ANALYTICS_TRACKING`, `PGREST_ROLE`, `ADMIN_USERNAME` | dead keys in the gateway chart. Nothing reads the first two by those names; `ADMIN_USERNAME` is a constant default in `gateway/storagev2/types/const.go` |

## Values

| Key | Default | Description |
|---|---|---|
| `nameOverride` / `fullnameOverride` | `''` | Override the base resource name. Both feed the immutable Deployment selector |
| `image.repository` | `hoophq/hoopcontrolplane` | |
| `image.tag` | `latest` | |
| `image.pullPolicy` | `Always` | |
| `config.POSTGRES_DB_URI` | — | **Required.** External PostgreSQL |
| `config.API_URL` | — | **Required.** Must carry a scheme |
| `config.IDP_ISSUER` | `''` | Setting any `IDP_*` key selects OIDC. Requires `IDP_CLIENT_ID` and `IDP_CLIENT_SECRET` |
| `config.IDP_CLIENT_ID` / `IDP_CLIENT_SECRET` / `IDP_CUSTOM_SCOPES` / `IDP_GROUPS_CLAIM` / `IDP_AUDIENCE` | `''` | |
| `config.MIGRATION_PATH_FILES` | `''` | Migrations are embedded and the image ships no SQL files; set this only to override them with files you mount |
| `config.GIN_MODE` | `release` | |
| `config.LOG_ENCODING` / `config.LOG_LEVEL` | `json` / `info` | |
| `config.TLS_CERT` / `config.TLS_KEY` | `''` | Both set = HTTPS, both empty = plaintext |
| `extraSecret` | `{}` | Extra environment variables, rendered into a Secret |
| `existingSecret` | `''` | A Secret the chart references but does not manage. Loaded last, so it overrides |
| `replicas` | `1` | See Replicas |
| `deploymentStrategy.type` | `Recreate` | At one replica, RollingUpdate's default surge runs two Slack sockets per upgrade |
| `readinessProbe` / `livenessProbe` | `{}` / `{}` | Off. Rendered verbatim when set — see Probes |
| `service.enabled` / `service.type` / `service.annotations` | `true` / `ClusterIP` / `{}` | One port, 8009 |
| `gatewayApi.enabled` | `false` | |
| `gatewayApi.createGateway` | `true` | `false` makes `httpRoute.parentRefs` required |
| `gatewayApi.gateway.*` | see `values.yaml` | `name`, `annotations`, `gatewayClassName`, `listeners`, `addresses`, `infrastructure`, `backendTLS` (experimental channel only) |
| `gatewayApi.httpRoute.*` | see `values.yaml` | `name`, `annotations`, `hostnames`, `parentRefs`, `rules` |
| `extraVolumes` / `extraVolumeMounts` | `[]` | |
| `resources` | 1024m/1Gi limits, 256m/512Mi requests | |
| `nodeSelector` / `tolerations` / `affinity` | `{}` / `[]` / `{}` | |
| `podAnnotations` / `deploymentAnnotations` | `{}` | |
| `podSecurityContext` / `securityContext` | `{}` | |
| `serviceAccount.create` / `.name` / `.annotations` | `false` / `''` / `{}` | `name` alone names an existing account |

## The port is 8009 and is not configurable

It is read by the Deployment's `containerPort`, the Service and the default
HTTPRoute backend. `config.PORT` set to anything else is refused: the Service
would route to a closed port, and so would any probe you wrote against 8009.
