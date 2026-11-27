# Hoop

- TODO

## Credentials from an existing Secret

| Value | Default | Description |
|---|---|---|
| `existingSecret` | empty | Name of a Secret in the release namespace that the chart does not manage (e.g. made by sealed-secrets). The `hoopgateway` and `defaultagent` containers load every key of it after `hoop-config`, so a key in the Secret overrides the same key in `config`. |

When `existingSecret` is set, `config.POSTGRES_DB_URI`, `config.API_URL` and
`config.IDP_CLIENT_SECRET` are not required. Put them, and any other credential
(`API_KEY`, `WEBHOOK_APPKEY`, `IDP_URI`, `TLS_KEY`, ...), in the Secret. The
chart cannot read the Secret, so it does not check that a key is there: without
`POSTGRES_DB_URI` or `API_URL` the gateway uses its local defaults.

The pods do not restart when the Secret changes. Run
`kubectl rollout restart deployment/hoopgateway` after a change.

With `idpFederatedToken.enabled`, do not put `IDP_CLIENT_SECRET`, or an
`IDP_URI` that holds a secret, in the Secret: the gateway refuses to start with
both. `postgres.enabled` deploys a test
database with a fixed password in the manifest; use an external database.

Setup steps: "Credentials from an existing Secret" in [../../README.md](../../README.md).

## Database pool

| Value | Default | Description |
|---|---|---|
| `config.POSTGRES_MAX_OPEN_CONNS` | `50` | Most PostgreSQL connections one pod opens. Past it a query waits in process instead of taking a server slot, so a burst of API requests cannot exhaust `max_connections` for every caller. The database serves the sum over every pod: size the sum at 2-4 x its vCPUs and divide by the pods that can run at once (the control-plane chart does this from `postgresPool.connectionBudget`). |

## Probes

The gateway opens the API port (8009) last, after its config, the database
migrations and the default organization. The readiness probe checks that port,
so the pod is Ready only when the gateway serves, and `helm --wait` fails on a
gateway that does not start. The `hoopgateway` container has no liveness probe:
it can restart a busy gateway.

With `deploymentStrategy: Recreate`, a failed upgrade still removes the old
pod. Use `helm upgrade --wait --timeout <longer than your longest migration>`.
Helm's default timeout is 5 minutes. Do not use `--atomic`: a rollback during
a migration stops it halfway, the database stays dirty, and the gateway does
not start until you fix it by hand. If the wait fails, look at the pod first.
Run `helm rollback` only when the gateway crashes, or does not answer after it
started; never while a migration still runs.

| Value | Default | Description |
|---|---|---|
| `readinessProbe.enabled` | `true` | Only `false` removes the readiness probe. The pod is then Ready when its containers start. |
| `readinessProbe.periodSeconds` | `10` | Seconds between two checks. |
| `readinessProbe.timeoutSeconds` | `5` | Seconds one check may take. |
| `readinessProbe.failureThreshold` | `3` | Failed checks before the Service stops sending traffic to the pod. |
| `startupProbe.enabled` | `false` | `true` adds a startup probe. It is a deadline: when it ends, the kubelet restarts the container. That stops a migration that runs longer and leaves the database dirty, and the gateway does not start until you fix it by hand. |
| `startupProbe.periodSeconds` | `10` | Seconds between two checks. |
| `startupProbe.timeoutSeconds` | `5` | Seconds one check may take. |
| `startupProbe.failureThreshold` | `360` | Failed checks before the kubelet restarts the container: 1 hour with `periodSeconds: 10`. Set it above your longest migration. |

A field you do not set gets the default above. A release from an older chart,
upgraded with `--reuse-values`, has no probe values and gets the defaults too.

With no handler, each probe opens a TCP connection to the container port `api`
(8009). This works with TLS on or off and with any `API_URL` path. If you
change the gateway's API port with `PORT`, set the same port in the probe. Set
`httpGet`, `tcpSocket`, `exec` or `grpc` to replace the TCP check. Every other
Kubernetes probe field goes to the pod as you write it. An `httpGet` probe must
match the listener: the path is `<API_URL path>/api/healthz`, and with
`TLS_CERT` and `TLS_KEY` set it needs `scheme: HTTPS`. If not, the pod never
becomes Ready.

```yaml
readinessProbe:
  httpGet:
    path: /api/healthz
    port: api
    scheme: HTTPS
```

## Entra ID without a client secret

| Value | Default | Description |
|---|---|---|
| `idpFederatedToken.enabled` | `false` | Mounts a projected service account token at `/var/run/secrets/hoop/idp/token` and sets `config.IDP_CLIENT_ASSERTION_FILE` to it. The pod runs as the service account `hoopgateway`: set `serviceAccount.create=true`, or create it yourself. |
| `idpFederatedToken.audience` | `api://AzureADTokenExchange` | Audience of the token. |
| `idpFederatedToken.expirationSeconds` | `3600` | Lifetime of the token. The kubelet renews it, and the gateway reads it again on each token request. |
| `config.IDP_CLIENT_ASSERTION_FILE` | empty | Path to a JWT that authenticates the OIDC client in place of `config.IDP_CLIENT_SECRET`. Set one, not both. |

Setup steps: "Entra ID without a client secret" in [../../README.md](../../README.md).

## Boot-time organization settings

All keys apply to the default organization at every gateway boot, so a
rotated value replaces the stored one. None takes effect with
`ORG_MULTI_TENANT=true`: `ORG_ID`, `HOOP_LICENSE` and `ANALYTICS_TRACKING=disabled` are refused.

| Value | Default | Description |
|---|---|---|
| `config.HOOP_LICENSE` | empty | The signed license JSON itself (starts with `{`) or the path of a file that holds it. The gateway verifies the signature, the validity window and `API_URL`'s host, then stores it; a bad document stops startup. |
| `config.ANALYTICS_TRACKING` | empty | Without an enterprise license the organization is always `identified`. With one: `enabled` forces `identified`, `disabled` forces `disabled`, unset keeps the mode set in Settings (a new organization starts `disabled`). |
| `config.ORG_ID` | empty | UUID the default organization is created with on a fresh database, so `config.API_KEY` can be written before the first boot. An existing organization with another id stops startup: it never renames one. |

## OpenTelemetry

The gateway pushes its own signals to an OTLP collector: sidecar handshakes,
session event ingest, the database pool and API latency per route. It is push
only; nothing scrapes the gateway. The keys are the standard `OTEL_*`
variables, with the same transports as the sidecar `telemetry` block, so one
collector serves both. A malformed value stops startup.

| Value | Default | Description |
|---|---|---|
| `config.OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Turns the export on. A URL (`http://otel-collector:4317`), or `host:port` for `grpc`. An `http://` URL sends in the clear. |
| `config.OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` | `grpc` or `http/protobuf`. |
| `config.OTEL_EXPORTER_OTLP_HEADERS` | empty | `key=value,key2=value2`, values URL-encoded. Sent on every export: an `authorization` header for a vendor. |
| `config.OTEL_EXPORTER_OTLP_INSECURE` | `false` | `true` sends a `host:port` endpoint in the clear. A URL's scheme decides for itself. |
| `config.OTEL_EXPORTER_OTLP_CERTIFICATE` | host trust store | Path, inside the container, of a PEM file with the collector's CA. |
| `config.OTEL_EXPORTER_OTLP_TIMEOUT` | `10000` | Milliseconds one export may take. |
| `config.OTEL_METRIC_EXPORT_INTERVAL` | `60000` | Milliseconds between metric exports. |
| `config.OTEL_TRACES_SAMPLER` | empty | Empty sends no spans. Else a standard sampler, such as `parentbased_traceidratio`. |
| `config.OTEL_TRACES_SAMPLER_ARG` | `1` | The ratio of the `traceidratio` samplers, `0` to `1`. |
| `config.OTEL_SERVICE_NAME` | `hoop-gateway` | `service.name` on every signal. |
| `config.OTEL_RESOURCE_ATTRIBUTES` | empty | `key=value,...` added to every signal, such as `deployment.environment=prod`. |
