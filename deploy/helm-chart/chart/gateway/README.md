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
| `config.ANALYTICS_TRACKING` | empty | Unset: a valid enterprise license sets the organization's analytics mode to `disabled`, a free organization gets `identified`. `enabled` forces `identified`, `disabled` forces `disabled`. `PUT /api/orgs/analytics-mode` changes it until the next restart. |
| `config.ORG_ID` | empty | UUID the default organization is created with on a fresh database, so `config.API_KEY` can be written before the first boot. An existing organization with another id stops startup: it never renames one. |
