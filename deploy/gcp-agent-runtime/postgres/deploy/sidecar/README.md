# The agent's sidecar

The agent reaches its database only through this sidecar. Install it once,
before `agents-cli deploy`.

This directory is a Terraform root. It calls hoop's GCP Marketplace module at
a release tag, and installs the sidecar chart and image of the same release
(`sidecar_version`) behind an internal load balancer.

## The sidecar alone (free, no license)

1. **Set the rules** in `sidecar.yaml`: the database as `upstream`, and the
   guardrail. The free tier runs one guardrail rule and one masking rule; the
   audit goes to the sidecar's log (`kubectl logs`).

   Give the agent's database login read grants only: `SELECT` on the tables it
   needs, no `CREATE`, and no `EXECUTE` on a function that writes. The rule
   refuses the writes it sees in a statement. A function called from a
   `SELECT`, or `SELECT ... INTO`, writes while the statement reads as a
   `select`, and only the login's grants stop it.
2. **Install it** on a GKE cluster in the database's VPC:

   ```bash
   gcloud auth application-default login
   terraform -chdir=deploy/sidecar init
   terraform -chdir=deploy/sidecar apply \
     -var project_id=PROJECT -var cluster_name=CLUSTER -var cluster_location=LOCATION
   kubectl -n hoop-sidecar get svc appdb-sidecar-hoopsidecar-lanes \
     -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
   ```

   The last command prints the sidecar's internal IP: the agent's
   `APPDB_HOST`.

   Or install the same module from Google Cloud Marketplace, with **Config
   source: document** and the contents of `sidecar.yaml`. Its simple fields
   with **Guardrail: Read-only** install the same operations under a generic
   message; `sidecar.yaml` keeps this agent's own.
3. **Lock the database to the sidecar.** This is what makes the sidecar the
   only way in. Database on GKE: set the selector in `database-policy.yaml` to
   your database pods' labels (and the namespace, if the sidecar's is not
   `hoop-sidecar`; the `app.kubernetes.io/instance` label, if you changed
   `goog_cm_deployment_name`), then `kubectl -n <db namespace> apply -f
   deploy/sidecar/database-policy.yaml`. Check it: a connection straight to
   the database pod must time out. A selector that matches nothing locks
   nothing, with no error. Database on a VM: a firewall rule that admits its
   port only from the sidecar's pods. Cloud SQL: not covered here.

## Later: connect it to a hoop control plane (license required)

The one change is two variables: `control_plane_url` and
`control_plane_token`. With them, the sidecar takes its listeners, rules and
license from the plane, and the plane records the sessions. The agent does not
change.

Before you set them:

- The organization on the plane needs an enterprise (or trial) license; an
  unlicensed plane refuses even to register a sidecar.
- In the plane, register the sidecar (**Sidecars → Add new Sidecar**) with the
  listener and the rules from `sidecar.yaml`, and copy the token: this
  module's control-plane mode carries no local file to seed the plane.
- For the plane to record sessions, turn on `experimental.sidecar_session_events`
  (Settings → Experimental; off by default). The sidecar's own log stays the
  record of truth.

Then:

```bash
TF_VAR_control_plane_token=<token> terraform -chdir=deploy/sidecar apply \
  -var control_plane_url=https://<your control plane> ...
```

A new sidecar pod needs the plane at startup; a running one keeps its last
rules. The chart passes the two values as `HOOP_CONTROL_PLANE_URL` and
`HOOP_SIDECAR_TOKEN`.

## Upgrade

Set the module's `ref` in `main.tf` and `sidecar_version` to the same newer
hoop release, then `terraform init -upgrade` and `apply`.
