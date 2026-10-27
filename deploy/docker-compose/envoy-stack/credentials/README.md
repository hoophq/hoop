# Credential swap

An overlay on the stack in the parent directory, stacked on [`gke/`](../gke/README.md):
the sidecar terminates the client's login and authenticates upstream with
a credential of its own. The client holds a **hoop credential**, a key the
control plane minted for one person on one lane. It never holds a database
password or a Google token, and the upstream never learns a person's name
except where the sidecar propagates it.

```
psql    ──TLS──> envoy :5432 ──TLS──> hoop-inspect :15432 ──TLS──> appdb
 user=alice@example.com                hoop_credential            as appuser (rw) or app_ro (ro)
 password=hlc_...                      select by group            application_name = "alice@example.com via demo"

kubectl ──TLS──> envoy :443  ──h2c──> hoop-inspect :16443 ──TLS──> fake-google ──> k3s
 token: hlc_...                        hoop_credential            as the gateway service account,
                                       gcp_impersonation          Impersonate-User: alice@example.com
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
certificate without the `iamcredentials.googleapis.com` SAN; `down -v`
once.

## What the demo shows

1. **Swap.** alice logs in as `alice@example.com` with her key. The sidecar
   verifies it against the `credential_set`, the selector maps group `dba`
   to the `rw` pool entry, and the upstream login is `appuser` from two
   files under `/run/hoop-creds`. `pg_stat_activity` names the person in
   `application_name` through `propagate.application_name`.
2. **Least privilege by group.** bob's key selects `ro`: the database sees
   `app_ro` and refuses an `INSERT` itself; the sidecar's guardrail refused
   the `DELETE` before it.
3. **Refusal at the door.** A wrong key is refused with the protocol's
   error and no upstream connection. `SET ROLE` inside a swapped session is
   refused by a guardrail every pool lane carries.
4. **Impersonation on the GKE lane.** kubectl's bearer is a hoop key. The
   sidecar reads the pod's identity from the metadata server, mints an
   access token for `hoop-sidecar@demo-project.iam.gserviceaccount.com`
   through `generateAccessToken`, and sends `Impersonate-User` with the
   caller. The cluster's RBAC decides per person: alice is admin, bob is
   refused. A forged key is refused by the sidecar.
5. **Audit.** `session_start` carries `credential`, `upstream_user`,
   `identity_type` and `verified`; the key and the database password never
   appear in the trail.

## What is real and what is faked

| Piece | Here | In the customer's environment |
|---|---|---|
| hoop credential | four static keys; `credential_set` in `config-credentials.yaml` holds their hashes | minted with `POST /sidecars/<name>/lanes/<lane>/credentials`, served to the sidecar by the control plane |
| client TLS on the postgres lane | the sidecar's own, keypair from `creds-seed` | the same, or whatever fronts the sidecar |
| database roles | `appuser` and `app_ro`, password files in a named volume | Kubernetes Secrets through `credentials.secretMounts` in the helm chart |
| pod identity | `fake-google:8080` answers the metadata token endpoint | the GCE metadata server under GKE Workload Identity |
| impersonation | fake-google `generateAccessToken`, one allowed account | `iamcredentials.googleapis.com` and `roles/iam.serviceAccountTokenCreator` |
| cluster | k3s: the gateway account may only impersonate; `alice@example.com` is admin | GKE RBAC |

## Files

| Path | What it is |
|---|---|
| `docker-compose.credentials.yml` | the overlay: `creds-seed`, the read-only role, fake-google's metadata server, k3s tokens and RBAC, the kubeconfig |
| `config-credentials.yaml` | sidecar config: `credentials` sources, the `appdb` and `gke` lanes with `identity`, `credentials`, `propagate`, and the static `credential_set` |
| `seed-roles.sql` | `app_ro`, SELECT only |
| `tokens.csv` | `../kubernetes/tokens.csv` plus the gateway service account |
| `manifests/impersonation.yaml` | RBAC: the gateway impersonates, `alice@example.com` is admin |
| `kubeconfig.yaml` | `../gke/kubeconfig.yaml` with hoop keys as bearers |
| `demo-credentials.sh` | the walk above |
