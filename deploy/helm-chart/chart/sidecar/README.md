# hoopsidecar-chart

Deploys the hoop inspection sidecar: a relay that decodes the wire protocol
between a client and a database or API, evaluates each statement against
policy, records an audit trail, and masks sensitive values on the way back.

It routes nothing and terminates no downstream TLS. Run it behind something
that already owns the network path and identity — typically an Envoy sidecar
forwarding plaintext over loopback, or a Service in front of these pods.

## Install

```bash
helm install hoopsidecar ./deploy/helm-chart/chart/sidecar -f my-values.yaml
```

A minimum `my-values.yaml`:

```yaml
config:
  admin:
    listen: '0.0.0.0:19000'
  audit:
    file: '-'
  listeners:
    - name: appdb
      protocol: postgres
      listen: '0.0.0.0:15432'
      upstream: appdb.default.svc.cluster.local:5432
```

Clients then reach the lane at `hoopsidecar.<namespace>.svc.cluster.local:15432`.

## Ports

The chart declares exactly one port, literally: **19000**, the admin server.
The same number is written into the Deployment's `containerPort`, the Service,
and both probes. It is not configurable — 19000 is what every config under
`deploy/docker-compose/` binds and what `sidecar/daemon/firstrun.go` names as
the convention.

`config.admin.listen` therefore has to be `0.0.0.0:19000`. A config that omits
it or moves it is **refused at render**, because the relay disables the admin
server when the address is empty and the probes would otherwise poll a closed
port and fail every pod:

```
config.admin.listen is "0.0.0.0:9901" but this chart probes /healthz on 19000.
Use '0.0.0.0:19000'
```

Under `controlPlane.url` or `existingConfigMap` the chart cannot see the
config, so that check does not run and putting admin on 19000 is yours to get
right.

**Lane ports are not declared and not published.** Every port a lane opens
comes from `listeners` in your config and differs per deployment; a
`containerPort` is informational in Kubernetes either way. A lane is reached
through whatever fronts this pod — an Envoy sidecar over loopback, which is
the deployment this relay is built for, or a Service of your own:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: hoopsidecar-lanes
spec:
  selector:
    # Both labels. The instance label is what keeps this Service pointed at
    # one release's pods when several are installed in the namespace.
    app.kubernetes.io/name: hoopsidecar
    app.kubernetes.io/instance: <your release name>
  ports:
    - {name: postgres, port: 15432, targetPort: 15432}
```

The numbers to use are this project's conventions, the same ones every config
under `deploy/docker-compose/` binds:

| Port | Lane |
|---|---|
| 19000 | admin: `/healthz`, `/stats`, `/config`, `/events`, `/api/*` |
| 15432 | postgres |
| 18080 | http |
| 11433 | mssql |
| 18443 | grpc |
| 29010 | spanner |

`mysql` and `mongodb` have no established number here — pick your own.

## Configuration

| Value | Description |
|---|---|
| `image.repository` | Container image. Default `hoophq/hoopsidecar` |
| `image.tag` | Image tag. Default `latest`, an alias of `<version>-minimal` |
| `image.pullPolicy` | Default `Always` |
| `config` | The sidecar config document, rendered into a ConfigMap and mounted at `/etc/hoop-inspect/config.yaml` |
| `existingConfigMap` | Mount a ConfigMap you manage instead, key `config.yaml`. Mutually exclusive with `config` |
| `license` | License document or a path to one → `HOOP_LICENSE` |
| `controlPlane.url` | Control Plane to take the running config from → `HOOP_CONTROL_PLANE_URL` |
| `controlPlane.token` | Token from the sidecar's registration → `HOOP_SIDECAR_TOKEN` |
| `extraSecret` | Extra environment variables, as a Secret |
| `service.enabled` | A Service for the admin port, 19000 only. Default `true` |
| `service.type` | Default `ClusterIP` |
| `probe.initialDelaySeconds`, `probe.periodSeconds` | Probe timing. The port is fixed at 19000 |
| `service.annotations` | Annotations on the Service |
| `extraVolumes` / `extraVolumeMounts` | For what the config references by path: CA files, credentials |
| `nameOverride`, `fullnameOverride` | Change the name chart-owned resources are built from |
| `replicas` | Default `1` |
| `deploymentStrategy` | Default `RollingUpdate` |
| `resources` | CPU/memory requests and limits |
| `nodeSelector`, `tolerations`, `affinity` | Pod assignment |
| `podAnnotations`, `deploymentAnnotations` | Annotations |
| `serviceAccount.create`, `serviceAccount.name`, `serviceAccount.annotations` | Service account. `name` with `create: false` points at an existing one (GKE Workload Identity) |

The chart reads the config document only to check that admin is on 19000.
Everything the
relay does — protocols, guardrails, masking, audit, PII entities, AI analysis
— is decided in that document. Its schema is in `sidecar/README.md`, with
worked examples under `deploy/docker-compose/*/sidecar/config.yaml`.

Validate a config change before it reaches a pod:

```bash
hoop start sidecar --config my-config.yaml --validate
```

## The image

`hoophq/hoopsidecar` publishes two flavours of `Dockerfile.sidecar`:

| Tag | Base |
|---|---|
| `<version>-minimal`, and `latest` | Ubuntu 24.04 LTS + the `hoop` binary |
| `<version>-distroless` | distroless static + the `hoop` binary |

`latest` is minimal and only ever minimal — it keeps a shell, so the default
is the flavour you can `kubectl exec` into. distroless has no package manager
and no shell, reports zero OS-package CVEs and is about two thirds the size;
name it explicitly to get it.

The chart sets no `command`. It expects the image's own entrypoint to run the
relay and to read the config at `/etc/hoop-inspect/config.yaml` — the path the
chart mounts and passes in `HOOP_SIDECAR_CONFIG`.

The relay binary also ships inside the images you already pull, as
`hoop start sidecar` from 1.149.0: `hoophq/hoopdev`, `hoophq/hoop` and
`hoophq/hoopagent`. None of them *defaults* to the relay — hoopdev and
hoopagent start an agent — so running one here means putting the command back
in the Deployment:

```yaml
command: ["hoop", "start", "sidecar"]
```

`command:` and not a bare `args:`: those images carry no ENTRYPOINT, so `args:`
replaces CMD entirely and runs nothing. Their invocation contract is in
`Dockerfile.agent`.

## Environment variables

The relay reads four, and the chart sets all four from the Secret
`sidecar-config`:

| Variable | From |
|---|---|
| `HOOP_SIDECAR_CONFIG` | Set to the mount path when `config` or `existingConfigMap` is used |
| `HOOP_LICENSE` | `license` |
| `HOOP_CONTROL_PLANE_URL` | `controlPlane.url` |
| `HOOP_SIDECAR_TOKEN` | `controlPlane.token` |

Add anything else through `extraSecret`, which becomes a second `envFrom`
Secret — an analyzer provider's API key, for instance.

## Credentials referenced by path

Mount secrets holding a credential with `defaultMode: 0400`. Kubernetes writes
secret files `0644` by default and the relay refuses to read a credential at
that mode, naming it:

```yaml
extraVolumes:
  - name: vertex-key
    secret:
      secretName: hoop-sidecar-llm
      defaultMode: 0400        # required
extraVolumeMounts:
  - name: vertex-key
    mountPath: /run/secrets/vertex
    readOnly: true
```

## Differences from the agent chart

Two, both deliberate:

- **`deploymentStrategy` defaults to `RollingUpdate`**, not `Recreate`. This
  process sits in the data path between a client and its database; `Recreate`
  would close every connection on every config change with nothing listening
  in between. Replicas hold no shared state and no shared static key, so
  raising `replicas` is safe.
- **`extraVolumes` / `extraVolumeMounts` exist.** The config document
  references CA files and credentials by path, so the chart has to be able to
  put them there.

There is no `spiffe` block. SPIFFE authenticates an agent to the gateway; this
process does not dial the gateway. It authenticates to a control plane with
`controlPlane.token`, or to nothing at all when it runs standalone.
