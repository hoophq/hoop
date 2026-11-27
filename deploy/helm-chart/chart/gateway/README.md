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
variables with a `HOOP_` prefix, with the same transports as the sidecar
`telemetry` block, so one collector serves both. A malformed value stops
startup.

The gateway does not read the standard `OTEL_*` names. A platform can inject
them into every pod (the EKS CloudWatch Observability add-on does), and an
injected value must not stop or configure the gateway. At start the gateway
removes every `OTEL_*` variable from its environment and logs their names,
never their values. A `config.OTEL_*` key fails the chart render. Releases
1.325.0 to 1.328.0 read the standard names: rename those keys to keep the
export.

| Value | Default | Description |
|---|---|---|
| `config.HOOP_OTEL_EXPORTER_OTLP_ENDPOINT` | empty | Turns the export on. A URL (`http://otel-collector:4317`), or `host:port` for `grpc`. An `http://` URL sends in the clear. |
| `config.HOOP_OTEL_EXPORTER_OTLP_PROTOCOL` | `grpc` | `grpc` or `http/protobuf`. |
| `config.HOOP_OTEL_EXPORTER_OTLP_HEADERS` | empty | `key=value,key2=value2`, values URL-encoded. Sent on every export: an `authorization` header for a vendor. |
| `config.HOOP_OTEL_EXPORTER_OTLP_INSECURE` | `false` | `true` sends a `host:port` endpoint in the clear. A URL's scheme decides for itself. |
| `config.HOOP_OTEL_EXPORTER_OTLP_CERTIFICATE` | host trust store | Path, inside the container, of a PEM file with the collector's CA. |
| `config.HOOP_OTEL_EXPORTER_OTLP_TIMEOUT` | `10000` | Milliseconds one export may take. |
| `config.HOOP_OTEL_METRIC_EXPORT_INTERVAL` | `60000` | Milliseconds between metric exports. |
| `config.HOOP_OTEL_TRACES_SAMPLER` | empty | Empty sends no spans. Else a standard sampler, such as `parentbased_traceidratio`. |
| `config.HOOP_OTEL_TRACES_SAMPLER_ARG` | `1` | The ratio of the `traceidratio` samplers, `0` to `1`. |
| `config.HOOP_OTEL_SERVICE_NAME` | `hoop-gateway` | `service.name` on every signal. |
| `config.HOOP_OTEL_RESOURCE_ATTRIBUTES` | empty | `key=value,...` added to every signal, such as `deployment.environment=prod`. |
