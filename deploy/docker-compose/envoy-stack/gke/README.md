# GKE Connect Gateway lane

An overlay on the stack in the parent directory: kubectl through a GKE
Connect Gateway, behind a customer's transparent MITM Envoy. Real kubectl
and a real k3s API server; Google itself is faked by `fake-google`, so the
stack needs no Google account and creates nothing on Google Cloud.

```
kubectl ──TLS h2──> envoy :443 ──h2c──> hoop-inspect :16443 ──TLS──> fake-google ──TLS──> k3s :6443
         MITM, customer CA            google_identity           connect gateway
                                        └──TLS──> fake-google /tokeninfo
```

## Run it

```bash
./run.sh --rebuild                                                                  # parent stack, sidecar image from this tree
docker compose -f docker-compose.yml -f gke/docker-compose.gke.yml up -d --build --wait
./gke/demo-gke.sh
docker compose -f docker-compose.yml -f gke/docker-compose.gke.yml down -v
```

`k3s` is `privileged`, as k3s-in-docker has to be. Host ports: `8448` is
the MITM listener (`:443` inside), `8449` the loop listener.

## What is real and what is faked

| Piece | Here | In the customer's environment |
|---|---|---|
| kubectl, kubeconfig URL | real, `https://connectgateway.googleapis.com/v1/projects/.../gkeMemberships/demo` | the same |
| DNS | compose aliases: `connectgateway.googleapis.com` is Envoy, `oauth2.googleapis.com` is fake-google | corporate DNS / transparent interception |
| Envoy MITM | real Envoy, certificate from the stack's "customer CA" (`gke-pki`) | their Envoy and CA |
| Google bearer | static `tok-alice` / `tok-bob` in the kubeconfig | `gke-gcloud-auth-plugin` |
| tokeninfo | fake-google `POST /tokeninfo`, Google's response shape | `oauth2.googleapis.com` |
| Connect Gateway | fake-google: checks the bearer, strips the prefix, forwards as a k3s user | Google, impersonating the IAM user |
| cluster | real k3s with RBAC: alice is `system:masters`, bob has no grants | GKE |

Not covered: real Google tokens, Connect Gateway's IAM checks, and whether
Connect Gateway itself supports `exec`/`port-forward`.

## What the demo shows

1. **Transparent.** kubectl keeps the real Connect Gateway URL. Envoy
   intercepts with the customer CA and sends **h2c** to the sidecar; the
   lane terminates HTTP/2 itself, so nothing is downgraded.
2. **Identity from the bearer.** `google_identity: {}` verifies kubectl's
   token with tokeninfo (default URL), trusted through `trust.ca_file`.
   Envoy runs no `ext_authz`. bob is recorded as bob and refused by the
   cluster's RBAC; a forged token is refused by the sidecar with 403.
3. **Plain Kubernetes rules.** `no-secret-contents` names
   `/api/v1/namespaces/*/secrets/**`; the lane removes the
   `/v1/projects/*/locations/*/gkeMemberships/*` prefix before rules see the
   path. Listing secrets works; `-o yaml` on one is denied.
4. **Masking through HTTP/2.** The ConfigMap's `data` comes back redacted.
5. **kubectl exec.** Upgrades cannot ride HTTP/2: Envoy would turn one into
   an extended CONNECT, which the lane answers with 501. So Envoy routes
   requests with an `Upgrade` header to the same sidecar port as HTTP/1.1.
   A customer Envoy with an h2 upstream to the sidecar needs the same split.
6. **Loop.** Envoy `:8449` routes to the `gke-loop` lane, whose upstream is
   `:8449` again: a MITM that also intercepts the sidecar's own egress. The
   second pass carries the sidecar's own `Via` and is refused with
   "request loop". The `gke` lane avoids it the way a customer must: its
   upstream bypasses the interception.
7. **Audit.** One session per caller, `principal` from the Google identity,
   and no bearer token anywhere in the trail.

## Files

| Path | What it is |
|---|---|
| `docker-compose.gke.yml` | the overlay: `gke-pki`, `fake-google`, `k3s`, `k3s-ca`, kubectl on `client`, config swaps for `hoop-inspect` and `envoy` |
| `envoy-gke.yaml` | parent listeners plus the `:443` MITM (h2c and HTTP/1.1 upgrade clusters) and the `:8449` loop |
| `config-gke.yaml` | sidecar config: `trust`, the `gke` lane (`google_identity`, one guardrail, one mask rule) and the `gke-loop` lane |
| `kubeconfig.yaml` | the Connect Gateway URL, the customer CA, alice's and bob's Google tokens |
| `fakegoogle/` | tokeninfo + Connect Gateway stand-in, stdlib only |
| `demo-gke.sh` | the walk above |

The cluster manifests and static tokens are `../kubernetes/manifests/demo.yaml`
and `../kubernetes/tokens.csv`.
