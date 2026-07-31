# Hoop

TODO

## Existing secret

Besides the `config` block (rendered by the chart into the `hoop-config`
Secret), the gateway loads environment variables from a Secret that already
exists in the namespace, created by an external process — External Secrets
Operator, Sealed Secrets, Vault Agent, terraform, `kubectl`. The chart only
references it, it does not create or manage it.

```yaml
existingSecret: hoop-gateway-credentials
```

Every key of the Secret becomes an environment variable, so the key names must
match the gateway env vars (e.g. `POSTGRES_DB_URI`, `IDP_CLIENT_SECRET`). It is
loaded after `hoop-config`, so it overrides the same key defined in `config`.

`config.POSTGRES_DB_URI` and `config.API_URL` are required by the chart even
when their real values come from the Secret. Set them to a placeholder
(e.g. `'from-secret'`) in that case — the value from `existingSecret` overrides
it at runtime.

Changing the Secret does not roll the deployment (the chart cannot checksum a
Secret it does not render); restart it yourself after updating the Secret.