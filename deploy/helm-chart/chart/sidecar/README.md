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

**Lane ports are not declared on the pod.** A `containerPort` is informational
in Kubernetes, and a lane bound to a unix socket has no port at all.

Publishing them is `laneServices`, a map of Services — one Kubernetes Service
per entry:

```yaml
laneServices:
  internal:
    enabled: true
    ports:
      - {name: postgres, port: 15432}
      - {name: http, port: 18080}

  public:
    enabled: true
    type: LoadBalancer
    ports:
      - {name: postgres, port: 5432, targetPort: 15432}
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-scheme: internet-facing
    loadBalancerSourceRanges: ["203.0.113.0/24"]
```

A map and not one multi-port Service, because Service type and every cloud
load-balancer knob — internal vs internet-facing, LB class, source ranges,
traffic policy — are per-Service and not per-port. One lane internal and
another public is two objects, and nothing can collapse them into one. A map
also merges across values files, which a list indexed by position does not.

Ports here are literal. The chart does not read them from `config.listeners`,
so keep the two in step: a Service pointing at a port nothing binds is accepted
by Kubernetes and reaches nothing.

The admin Service is separate and unaffected — always ClusterIP, always 19000.

> **Before setting `type: LoadBalancer`.** The relay terminates no client TLS
> unless the lane sets `downstream_tls`, and that is accepted on **postgres,
> grpc and spanner only** — on http, mysql, mssql and mongodb it is refused at
> startup, so those lanes are always plaintext. A public load balancer in front
> of one of them puts credentials and query results in the clear on the
> internet. Expose a lane publicly only when the lane terminates TLS itself, or
> when something in front of it does. `loadBalancerSourceRanges` narrows who
> can reach it; it does not encrypt.

A hand-written Service still works for anything the block does not cover — the
selector is `app.kubernetes.io/name: hoopsidecar` plus
`app.kubernetes.io/instance: <release>`.

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
| `image.tag` | Image tag. Default `latest`, an alias of the unsuffixed `<version>` |
| `image.pullPolicy` | Default `Always` |
| `config` | The sidecar config document, rendered into a ConfigMap and mounted at `/etc/hoop-inspect/config.yaml` |
| `existingConfigMap` | Mount a ConfigMap you manage instead, key `config.yaml`. Mutually exclusive with `config` |
| `configRevision` | Rollout trigger for `existingConfigMap`. Change it whenever that ConfigMap's content changes, or the pods never pick it up |
| `license` | License document or a path to one → `HOOP_LICENSE` |
| `controlPlane.url` | Control Plane to take the running config from → `HOOP_CONTROL_PLANE_URL` |
| `controlPlane.token` | Token from the sidecar's registration → `HOOP_SIDECAR_TOKEN` |
| `analytics.enabled` | Usage analytics to Segment. `false` → `HOOP_SIDECAR_ANALYTICS=off`. Default `true` |
| `analytics.sidecarId` | Stable install identity → `HOOP_SIDECAR_ID`. Default: the release's full name. Hashed before it is sent |
| `analytics.hostId` | Machine identity → `HOOP_HOST_ID`. Default: the node name via the downward API. Hashed before it is sent |
| `extraSecret` | Extra environment variables, as a Secret |
| `service.enabled` | A Service for the admin port, 19000 only. Default `true` |
| `laneServices` | Map of Services publishing lane ports. One Service per entry; empty by default |
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
| `<version>`, and `latest` | Ubuntu 24.04 LTS + the `hoop` binary |
| `<version>-distroless` | distroless static + the `hoop` binary |

The Ubuntu flavour carries no suffix — it is the image, and `latest` points at
it. It keeps a shell, so the default is the flavour you can `kubectl exec` into.
distroless has no package manager and no shell, reports zero OS-package CVEs and
is about two thirds the size; it is the only flavour that is named.

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

## Rolling pods when the config changes

The Deployment carries checksums of everything this chart renders, so editing
`config` rolls the pods on `helm upgrade` by itself.

`existingConfigMap` is the case that needs your help. That object lives outside
the release: the chart cannot see its contents, so its checksum here never
changes and `helm upgrade` produces a byte-identical pod template.

A rule-only edit does not need one. The relay watches its config file (a
stat every ten seconds), and the kubelet updates a mounted ConfigMap in place
on its sync period, so an edit to guardrails, masking, pii, OPA, a
listener's analyzer block or the `license` key reaches the running pods
without a rollout; the log says `config file configuration applied`. The
chart mounts the directory, not a `subPath`, because a `subPath` file is
never updated.

Everything else — listeners, audit, admin, log_level, the top-level analyzer
section — is bound at startup. The relay logs `restart to apply it` and
keeps serving the document it booted with until the pod is replaced.
`configRevision` is the handle for that. Put anything that changes with the
content in it, and change it in the same commit that changes the ConfigMap:

```yaml
existingConfigMap: my-sidecar-config
configRevision: "sha256-9f2b1c"
```

Or generate it at install time:

```bash
helm upgrade ... \
  --set configRevision=$(kubectl get cm my-sidecar-config -o yaml | sha256sum | cut -c1-16)
```

With `config` you do not need it — the rendered checksum already does the job —
but it still applies, so it doubles as a way to force a rollout on demand. A
GitOps tool that annotates the pods for you (Reloader, Argo CD) makes it
unnecessary. Without any of these,
`kubectl rollout restart deploy/<release>-hoopsidecar` is the manual equivalent.

## Environment variables

The relay reads seven. Six come from the Secret `sidecar-config`; the seventh
is set directly on the container because it defaults to a downward-API field,
which a Secret cannot express:

| Variable | From |
|---|---|
| `HOOP_SIDECAR_CONFIG` | Set to the mount path when `config` or `existingConfigMap` is used |
| `HOOP_LICENSE` | `license` |
| `HOOP_CONTROL_PLANE_URL` | `controlPlane.url` |
| `HOOP_SIDECAR_TOKEN` | `controlPlane.token` |
| `HOOP_SIDECAR_ANALYTICS` | `off` when `analytics.enabled: false`, else empty |
| `HOOP_SIDECAR_ID` | `analytics.sidecarId`, defaulting to the release's full name |
| `HOOP_HOST_ID` | `analytics.hostId`, defaulting to `fieldRef: spec.nodeName` on the container |

Add anything else through `extraSecret`, which becomes a second `envFrom`
Secret — an analyzer provider's API key, for instance.

## Usage analytics

A release build reports anonymous usage to Segment: that the process
started, the shape of its config, how much traffic it judged, why it
stopped. Counts only — no statement text, identity, rule name, prompt or
address. The full event list is under "Usage analytics" in
`sidecar/README.md`; `analytics.enabled: false` switches it off.

Two identities ride on every event, both hashed before they leave the pod.
A pod's hostname is its name and changes on every rollout, so left to
itself each deploy would look like a new sidecar on a new host — and
Segment bills per distinct id per month. The chart defaults both ids to
something that survives a rollout: `HOOP_SIDECAR_ID` to the release's full
name, so one release is one profile however often it rolls, and
`HOOP_HOST_ID` to the node name, so sidecars can be counted per node.
Under `controlPlane.token` the relay uses the token as its identity and
ignores `HOOP_SIDECAR_ID`.

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

A grpc lane whose `descriptors` are `gs://` URLs reads them as a GCP
identity. On GKE, Workload Identity needs no secret: point
`serviceAccount` at the Google account holding `roles/storage.objectViewer`
on the bucket.

```yaml
serviceAccount:
  create: true
  annotations:
    iam.gke.io/gcp-service-account: hoop-sidecar@PROJECT.iam.gserviceaccount.com
```

Off GKE, hand the sidecar a service account key either inline, through the
gateway's own variable so one Secret serves both processes, or as a mounted
file under the standard ADC variable:

```yaml
extraSecret:
  GOOGLE_APPLICATION_CREDENTIALS_JSON: '{"type":"service_account", ...}'
```

```yaml
extraVolumes:
  - name: gcs-reader
    secret:
      secretName: hoop-sidecar-gcs
      defaultMode: 0400
extraVolumeMounts:
  - name: gcs-reader
    mountPath: /run/secrets/gcs
    readOnly: true
extraSecret:
  GOOGLE_APPLICATION_CREDENTIALS: /run/secrets/gcs/key.json
```

The inline variable outranks the file; set but malformed, it is an error
rather than a fallthrough to another identity.

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
