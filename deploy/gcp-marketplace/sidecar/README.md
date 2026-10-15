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

From Google Cloud Marketplace, pick the sidecar and fill in the form. From the
command line, with this directory as the module:

```bash
terraform init
terraform apply \
  -var project_id=PROJECT -var cluster_name=CLUSTER -var cluster_location=LOCATION \
  -var helm_chart_repo=oci://REGISTRY/PATH -var helm_chart_name=hoopsidecar-chart \
  -var helm_chart_version=MAJOR.MINOR \
  -var image_repo=REGISTRY/PATH/hoopsidecar -var image_tag=MAJOR.MINOR \
  -var goog_cm_deployment_name=appdb-sidecar \
  -var upstream=appdb.default.svc.cluster.local:5432
```

The chart and image locations are the ones the Marketplace listing shows.

### Parameters

| Variable | Meaning |
|---|---|
| `config_source` | Where the config comes from: `simple`, `document`, `configmap` or `control_plane`. Default `simple` |
| `protocol` | `simple`: `postgres`, `mysql`, `mssql`, `mongodb`, `oracle`, `clickhouse` or `http` |
| `upstream` | `simple`: the resource, `host:port` |
| `service_port` | `simple`: the port clients connect to. Default: the protocol's usual port (5432 for postgres) |
| `config_document` | `document`: the whole config, JSON or YAML. `admin.listen` must be `0.0.0.0:19000` |
| `existing_config_map` | `configmap`: a ConfigMap in `namespace` whose `config.yaml` is the config |
| `control_plane_url`, `control_plane_token` | `control_plane`: take the config from a hoop control plane. The plane must already hold this sidecar's configuration: this mode ships no local document to seed it |
| `lane_ports` | `configmap`, `control_plane`: listener ports to publish on the lane Service, comma separated, e.g. `15432,18080` |
| `service_type` | `ClusterIP` (default) or `InternalLoadBalancer` (reachable from the VPC, never public) |
| `license` | Hoop license document. Empty runs the free tier. Ignored in `control_plane` mode |
| `namespace` | Default `hoop-sidecar`, created when missing |
| `goog_cm_deployment_name` | Required. Names the Helm release and its resources; one per sidecar. Lowercase, at most 45 characters |
| `replicas`, `cpu`, `memory` | Size of the Deployment. Default 1 pod, 500m, 512Mi |

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

From the command line: `-var config_document="$(cat config.yaml)"`.

### Pin the image to a digest

`image_tag` accepts a tag and a digest together; the digest wins:

```bash
-var image_tag="MAJOR.MINOR@sha256:DIGEST"
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
`audit.file`: `-` is the pod's log, a path is a file in the pod.

TLS: on `postgres`, `clickhouse`, `http`, `grpc` and `spanner` lanes the
sidecar can terminate client TLS (`downstream_tls` in a `document` config).
The other protocols are plaintext between client and sidecar, which is why no
public load balancer is offered.

## Back up and restore

The sidecar keeps no state. Its config is the Terraform variables (or your
ConfigMap, or the control plane); back those up.

## Image updates

Apply again with a newer `image_tag` and `helm_chart_version`, or deploy the
newer release from Marketplace. The Deployment rolls one pod at a time;
connections on a replaced pod close.

## Scaling

Set `replicas`. Each pod is an independent proxy and the lane Service spreads
connections across them.

## Deletion

```bash
terraform destroy   # same -var flags as apply
kubectl delete namespace hoop-sidecar
```

`terraform destroy` removes the release. The namespace stays because other
releases may share it; delete it when nothing else uses it. A cluster made with
`create_cluster = true` is removed by `terraform destroy`.
