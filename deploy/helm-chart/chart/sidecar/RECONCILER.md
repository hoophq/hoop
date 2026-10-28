# Reconciler contract

What a GitOps reconciler sends the control plane for one sidecar, in order. Every call is idempotent; re-apply on each sync. Admin token in `Authorization: Bearer`; paths under `/api`.
## 1. The sidecar: owner and lanes

`PUT /sidecars/<name>` replaces the stored document. `owner_email` is who reviews and anonymous sessions are filed under when a statement names nobody; the empty string clears it, omitting it keeps the stored one. A first sync creates it with `POST /sidecars` (same body plus `name`; the response carries the one-time `token`, unused under `controlPlane.identity`).

```json
{
  "owner_email": "dba@acme.com",
  "configuration": {
    "admin": {"listen": "0.0.0.0:19000"},
    "credentials": {"secret_files": {"dir": "/run/secrets"}},
    "listeners": [{
      "name": "appdb", "protocol": "postgres", "listen": "0.0.0.0:15432",
      "upstream": "appdb.default.svc:5432",
      "downstream_tls": {"cert_file": "/etc/hoop-inspect/tls/tls.crt", "key_file": "/etc/hoop-inspect/tls/tls.key"},
      "identity": {"type": "jwt", "issuer": "https://login.acme.com"},
      "credentials": {
        "pool": [
          {"name": "ro", "source": "secret_files", "user_file": "/run/secrets/appdb-ro/user", "password_file": "/run/secrets/appdb-ro/password"},
          {"name": "rw", "source": "secret_files", "user_file": "/run/secrets/appdb-rw/user", "password_file": "/run/secrets/appdb-rw/password"}
        ],
        "select": {"rules": [{"when": {"identity.groups": "dba"}, "credential": "rw"}, {"credential": "ro"}]}
      },
      "context": {"env": "prod", "team": "payments"},
      "propagate": {"application_name": "{{.Principal}} via {{.Context.team}}"}
    }]
  }
}
```

A `422` names the lane and the key the sidecar would refuse (`identity` beside `identity_header`, a pool `source` the build lacks, `credentials` on an `ica` lane).

## 2. Slack channels per lane
`PUT /sidecars/<name>/slack-channels`. A lane left out posts to the org default.
```json
{"listeners": [{"name": "appdb", "channels": ["C0APPDB"]}]}
```

## 3. Switching the sidecar to SPIRE

A sidecar bound to a service account keeps that issuer. Moving it to `controlPlane.identity: spiffe` (a new issuer, the trust domain) is three steps:

1. `POST /sidecar-service-accounts` `{"name": "spire", "issuer": "https://<trust-domain>", "audience": "<identityAudience or control plane URL>", "claim": "sub", "subject_pattern": "spiffe://<trust-domain>/ns/*/sa/hoop-sidecar", "name_template": "<name>", "jwks": {…}, "adopt_existing_sidecars": true}`. `jwks` is the SPIRE server's JWT bundle when the plane cannot reach its OIDC discovery provider.
2. `DELETE /sidecars/<name>/identity` → `204` clears the binding.
3. Roll the chart with `identity: spiffe`. The next handshake rebinds; a pod still presenting the old token is `401` until it rolls. SPIRE side: one entry per sidecar service account, and one per `spiffe_id` a `spiffe` pool names.
