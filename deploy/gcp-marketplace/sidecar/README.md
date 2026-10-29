# hoop sidecar on Google Kubernetes Engine

User guide for the hoop sidecar Terraform Kubernetes app on Google Cloud
Marketplace. Publishers: see `.claude/skills/gcp-marketplace-publish/` and
`scripts/gcp-marketplace/sidecar.sh`.

## Overview

The hoop sidecar sits between your agents and a database or HTTP API. It
reads every statement on the wire, refuses what the guardrails deny, masks
sensitive values in results, and writes an audit trail. Clients connect to the
sidecar instead of the resource; their code does not change.

This app installs the sidecar chart (`deploy/helm-chart/chart/sidecar`) on a
GKE cluster: one Deployment, a Service for the admin server on port 19000, and
a Service for the resource's port. Install it once per resource it fronts.

Without a license the sidecar runs the free tier: one guardrail rule and one
masking rule. Set `license` to lift the limits. A sidecar on a control plane
uses the organization's license instead, and ignores this one.

## One-time setup

- `gcloud`, `kubectl`, and `terraform` 1.5 or later (or `tofu`).
- A GKE cluster on x86 nodes, or let the module create an Autopilot cluster
  (`create_cluster = true`, `cluster_location` a region).
- The chart does not use the Application resource, so the Application
  CustomResourceDefinition is not needed.

```bash
gcloud auth application-default login
gcloud container clusters get-credentials CLUSTER --location LOCATION
```

## Installation

Once hoop is on Google Cloud Marketplace, its form runs this module and
supplies the chart and the image. The listing is not public yet.

From the command line, use hoop's public release artifacts: the chart from
`releases.hoop.dev` and the image from Docker Hub. Write a root module that
calls this one at a release tag, in an empty directory:

```hcl
# main.tf. RELEASE is a hoop release, for example 1.281.0. Terraform takes
# no variable in source, so the release is written out each time.
module "hoop_sidecar" {
  source = "github.com/hoophq/hoop//deploy/gcp-marketplace/sidecar?ref=RELEASE"

  project_id       = "PROJECT"
  cluster_name     = "CLUSTER"
  cluster_location = "LOCATION"

  # Empty: the chart is a URL, not an oci:// repository.
  helm_chart_repo    = ""
  helm_chart_name    = "https://releases.hoop.dev/release/RELEASE/hoopsidecar-chart-RELEASE.tgz"
  helm_chart_version = "RELEASE"
  image_repo         = "docker.io/hoophq/hoopsidecar"
  image_tag          = "RELEASE-distroless"

  goog_cm_deployment_name = "appdb-sidecar"
  upstream                = "appdb.default.svc.cluster.local:5432"
  guardrail_preset        = "read-only"
}

output "lane_service" { value = module.hoop_sidecar.lane_service }
```

```bash
terraform init
terraform apply
```

The preset needs 1.281.0 or later. Without a license the sidecar starts
on its own, with no login code, from 1.279.2.

### Parameters

| Variable | Meaning |
|---|---|
| `config_source` | Where the config comes from: `simple`, `document`, `configmap` or `control_plane`. Default `simple` |
| `protocol` | `simple`: `postgres`, `mysql`, `mssql`, `mongodb`, `oracle`, `clickhouse` or `http` |
| `upstream` | `simple`: the resource, `host:port` |
| `service_port` | `simple`: the port clients connect to. Default: the protocol's usual port (5432 for postgres) |
| `guardrail_preset` | `simple`: `none` (default, audit only) or `read-only`, one rule that denies the write operations. `postgres` only |
| `config_document` | `document`: the whole config, JSON or YAML. `admin.listen` must be `0.0.0.0:19000` |
| `existing_config_map` | `configmap`: a ConfigMap in `namespace` whose `config.yaml` is the config |
| `control_plane_url`, `control_plane_token` | `control_plane`: take the config from a hoop control plane. The plane must already hold this sidecar's configuration: this mode ships no local document to seed it |
| `lane_ports` | `configmap`, `control_plane`: listener ports to publish on the lane Service, comma separated, e.g. `15432,18080` |
| `service_type` | `ClusterIP` (default) or `InternalLoadBalancer` (reachable from the VPC, never public) |
| `license` | Hoop license document. Empty runs the free tier. Ignored in `control_plane` mode |
| `namespace` | Default `hoop-sidecar`, created when missing |
| `goog_cm_deployment_name` | Required. Names the Helm release and its resources; one per sidecar. Lowercase, at most 45 characters |
| `replicas`, `cpu`, `memory` | Size of the Deployment. Default 1 pod, 500m, 512Mi |

`guardrail_preset = read-only` gives a `simple` install the free tier's one
guardrail rule. It denies the statements the sidecar classifies as `insert`,
`update`, `delete`, `create`, `drop`, `alter`, `truncate`, `grant`, `revoke`,
`call` (`DO`, `CALL`, `EXECUTE`) or `other`. The client gets the message
`hoop: this connection may read the database, not change it`, and the
connection closes. It is for `postgres` only. On the other protocols the same
rule denies some reads and lets some writes pass, so the module refuses it
there. For another rule or another protocol, use `document`.

The rule is as exact as the sidecar's classifier, which today:

- reads some writes as `select`, so they pass: `EXPLAIN ANALYSE` and
  `EXPLAIN ("analyze")` run the statement they explain, `SELECT ... INTO`
  creates a table, and `SELECT f()` runs whatever `f` writes. Give the
  database login only the privileges the client needs.
- reads `SAVEPOINT`, `LISTEN` and `DISCARD ALL` as `other`, so they are
  denied. A client that opens a nested transaction (a savepoint) on a read
  path loses its connection.

The config schema is in `sidecar/README.md`; worked configs are under
`deploy/docker-compose/*/sidecar/config.yaml`. In `document` mode the lane
Service publishes the port of every TCP listener, so ports are written once.
`grpc`, `spanner` and `ssh` lanes need settings beyond `simple`; use
`document`.

The deploy form takes the config as YAML or JSON in a multi-line field. For
example, one Postgres lane that refuses `DROP` and `TRUNCATE`:

```yaml
admin:
  listen: 0.0.0.0:19000
guardrails:
  mode: enforce
  rules:
    - name: no-drop
      type: operation
      operations: [drop, truncate]
listeners:
  - name: appdb
    protocol: postgres
    listen: 0.0.0.0:15432
    upstream: appdb:5432
```

From the command line, in `main.tf`: `config_source = "document"` and
`config_document = file("config.yaml")`.

### Pin the image to a digest

`image_tag` accepts a tag and a digest together; the digest wins. In
`main.tf`:

```hcl
image_tag = "RELEASE-distroless@sha256:DIGEST"
```

## Basic usage

The `lane_service` output is the Service clients use, on the resource's port.
Point the client there instead of the resource:

```bash
kubectl -n hoop-sidecar port-forward svc/LANE_SERVICE 5432
psql -h 127.0.0.1 -U USER DATABASE
```

Credentials do not change: the sidecar passes the login through to the
resource.

The `admin_service` output serves `/healthz`, `/stats` and `/config` on 19000:

```bash
kubectl -n hoop-sidecar port-forward svc/ADMIN_SERVICE 19000
curl -s localhost:19000/stats
```

In `simple` mode the audit trail goes to the pod's log, so Cloud Logging
collects it. A `document` or ConfigMap config decides that itself with
`audit.file`: `-` is the pod's log, a path is a file in the pod. Each
statement is a JSON line; a denied one has `"kind":"violation"` and the rule:

```bash
kubectl -n hoop-sidecar logs deploy/appdb-sidecar-hoopsidecar | grep '"kind":"violation"'
gcloud logging read 'resource.type="k8s_container" AND resource.labels.namespace_name="hoop-sidecar"
  AND jsonPayload.kind="violation"' --freshness 1h --format 'value(jsonPayload.statement,jsonPayload.rule)'
```

The Deployment is `<goog_cm_deployment_name>-hoopsidecar`.

TLS: on `postgres`, `clickhouse`, `http`, `grpc` and `spanner` lanes the
sidecar can terminate client TLS (`downstream_tls` in a `document` config).
The other protocols are plaintext between client and sidecar, which is why no
public load balancer is offered.

## Lock the database to the sidecar

The sidecar sees only what passes through it. A client that reaches the
database directly, with the login, goes around it. On GKE, a NetworkPolicy in
the database's namespace admits the sidecar's pods only. Autopilot clusters
enforce it; a Standard cluster needs Dataplane V2 or network policy enforcement.

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: database-only-from-hoop-sidecar
  namespace: DB_NAMESPACE
spec:
  podSelector:
    matchLabels: {app: appdb}          # your database pods' labels
  policyTypes: [Ingress]
  ingress:
    - from:
        - namespaceSelector:
            matchLabels: {kubernetes.io/metadata.name: hoop-sidecar}
          podSelector:
            matchLabels:
              app.kubernetes.io/name: hoopsidecar
              app.kubernetes.io/instance: appdb-sidecar   # goog_cm_deployment_name
      ports: [{port: 5432, protocol: TCP}]
```

Check it: a connection straight to the database must time out, and one
through the lane Service must work. A selector that matches nothing locks
nothing, with no error. NetworkPolicies add up: another policy that selects
the database's pods and admits other clients keeps them in. List them with
`kubectl -n DB_NAMESPACE get networkpolicy`, and test a direct connection
from each workload that had access. A database outside the cluster needs its own firewall
rule; this guide does not cover it.

## Back up and restore

The sidecar keeps no state. Its config is the Terraform variables (or your
ConfigMap, or the control plane); back those up.

## Image updates

From the command line: write the newer release in every place `main.tf` names
RELEASE, then `terraform init -upgrade` and `terraform apply`. From
Marketplace: deploy the newer release. The Deployment rolls one pod at a time;
connections on a replaced pod close.

## Scaling

Set `replicas`. Each pod is an independent proxy and the lane Service spreads
connections across them.

## Deletion

```bash
terraform destroy   # in the directory of main.tf
kubectl delete namespace hoop-sidecar
kubectl -n DB_NAMESPACE delete networkpolicy database-only-from-hoop-sidecar
```

Delete the NetworkPolicy with the sidecar: while it stays, it admits only the
sidecar's pods, so no client reaches the database.

`terraform destroy` removes the release. The namespace stays because other
releases may share it; delete it when nothing else uses it. A cluster made with
`create_cluster = true` is removed by `terraform destroy`.
