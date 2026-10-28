# Credential swap

An overlay on the stack in the parent directory, stacked on [`gke/`](../gke/README.md):
the sidecar terminates the client's login and authenticates upstream with
a credential of its own. The client holds an **ID token** its identity
provider issued after SSO (`identity: {type: jwt}`; here `fake-google`
answers as `accounts.google.com`). It never holds a database password or a
Google access token, and the upstream never learns a person's name except
where the sidecar propagates it.

```
psql    ──TLS──> envoy :5432 ──TLS──> hoop-inspect :15432 ──TLS──> appdb
 user=alice@example.com                jwt                        as appuser (rw) or app_ro (ro)
 password=<id token>                   select by group            application_name = "alice@example.com via demo"

kubectl ──TLS──> envoy :443  ──h2c──> hoop-inspect :16443 ──TLS──> fake-google ──> k3s
 token: <id token>                     jwt                        as the gateway service account,
                                       gcp_impersonation          Impersonate-User: alice@example.com
                                        ├──> accounts.google.com (fake-google: discovery, JWKS)
                                        ├──> fake-google:8080 (metadata: the pod's identity)
                                        └──> iamcredentials.googleapis.com:generateAccessToken
```

## Run it

```bash
./run.sh --rebuild
docker compose -f docker-compose.yml -f gke/docker-compose.gke.yml \
  -f credentials/docker-compose.credentials.yml up -d --build --wait
./credentials/demo-credentials.sh
docker compose -f docker-compose.yml -f gke/docker-compose.gke.yml \
  -f credentials/docker-compose.credentials.yml down -v
```

`sidecar/scripts/dev/credentials-check.sh` runs the same lanes as a
pass/fail check.

A `gke-pki` volume minted before this overlay existed holds a fake-google
certificate without the `iamcredentials.googleapis.com` and
`accounts.google.com` SANs; `down -v` once.

## What the demo shows

1. **Swap.** alice logs in as `alice@example.com` with her ID token. The
   sidecar verifies it against the issuer's JWKS, the selector maps group
   `dba` to the `rw` pool entry, and the upstream login is `appuser` from
   two files under `/run/hoop-creds`. `pg_stat_activity` names the person
   in `application_name` through `propagate.application_name`.
2. **Least privilege by group.** bob's token selects `ro`: the database sees
   `app_ro` and refuses an `INSERT` itself; the sidecar's guardrail refused
   the `DELETE` before it.
3. **Refusal at the door.** A forged token is refused with the protocol's
   error and no upstream connection. `SET ROLE` inside a swapped session is
   refused by a guardrail every pool lane carries.
4. **Impersonation on the GKE lane.** kubectl's bearer is the ID token. The
   sidecar reads the pod's identity from the metadata server, mints an
   access token for `hoop-sidecar@demo-project.iam.gserviceaccount.com`
   through `generateAccessToken`, and sends `Impersonate-User` with the
   caller. The cluster's RBAC decides per person: alice is admin, bob is
   refused. A forged token is refused by the sidecar.
5. **Audit.** `session_start` carries `credential`, `upstream_user`,
   `identity_type` and `verified`; the token and the database password never
   appear in the trail.

## What is real and what is faked

| Piece | Here | In the customer's environment |
|---|---|---|
| ID token | signed by `fake-google` as `accounts.google.com` at startup, one per person with a `groups` claim, 30 days, in the `gke-tokens` volume | issued by the identity provider after SSO (`gcloud auth print-identity-token`, an Okta or Entra ID token with a groups claim); the lane names the issuer |
| client TLS on the postgres lane | the sidecar's own, keypair from `creds-seed` | the same, or whatever fronts the sidecar |
| database roles | `appuser` and `app_ro`, password files in a named volume | Kubernetes Secrets through `credentials.secretMounts` in the helm chart |
| pod identity | `fake-google:8080` answers the metadata token endpoint | the GCE metadata server under GKE Workload Identity |
| impersonation | fake-google `generateAccessToken`, one allowed account | `iamcredentials.googleapis.com` and `roles/iam.serviceAccountTokenCreator` |
| cluster | k3s: the gateway account may only impersonate; `alice@example.com` is admin | GKE RBAC |

## Files

| Path | What it is |
|---|---|
| `docker-compose.credentials.yml` | the overlay: `creds-seed`, the read-only role, fake-google's metadata server and issuer half, k3s tokens and RBAC, the kubeconfig, the `gke-tokens` volume |
| `config-credentials.yaml` | sidecar config: `credentials` sources, the `appdb` and `gke` lanes with `identity`, `credentials` and `propagate` |
| `seed-roles.sql` | `app_ro`, SELECT only |
| `tokens.csv` | `../kubernetes/tokens.csv` plus the gateway service account |
| `manifests/impersonation.yaml` | RBAC: the gateway impersonates, `alice@example.com` is admin |
| `kubeconfig.yaml` | `../gke/kubeconfig.yaml` with the ID tokens as `tokenFile` bearers |
| `demo-credentials.sh` | the walk above |
