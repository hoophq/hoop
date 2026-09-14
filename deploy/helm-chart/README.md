# Hoop Helm Chart

- Website: https://hoop.dev
- Documentation: https://hoop.dev/docs

[Helm](https://helm.sh) must be installed to use this chart.
Please refer to Helm's [documentation](https://helm.sh/docs/) to get started.

## Installing Self Hosted

Installing latest version of hoop. For different version check out the [releases page](https://github.com/hoophq/hoop/releases)

> Please refer to [gateway configuration reference](https://hoop.dev/docs/configuring/gateway)
> and [Kubernetes configuration](https://hoop.dev/docs/self-hosting/kubernetes) for more information.

The example below are the minimal requirements to deploy the gateway:

```sh
cat - > ./values.yaml <<EOF
# gateway base configuration
config:
  POSTGRES_DB_URI: 'postgres://<user>:<pwd>@<db-host>:<port>/<dbname>'
  API_URL: 'https://hoopdev.yourdomain.tld'
  IDP_CLIENT_ID: 'client-id'
  IDP_CLIENT_SECRET: 'client-secret'
  IDP_ISSUER: 'https://idp-issuer-url'
EOF
```

```sh
VERSION=$(curl -s https://releases.hoop.dev/release/latest.txt)
helm upgrade --install hoop \
  https://releases.hoop.dev/release/$VERSION/hoop-chart-$VERSION.tgz \
  -f values.yaml
```

## Installing Hoop Agent

Please refer to [agent configuration reference](https://hoop.dev/docs/setup/kubernetes) for more information.

```sh
VERSION=$(curl -s https://releases.hoop.dev/release/latest.txt)
helm upgrade --install hoopagent https://releases.hoop.dev/release/$VERSION/hoopagent-chart-$VERSION.tgz \
    --set 'config.HOOP_KEY='
```

## Installing the inspection sidecar

`hoopsidecar-chart` deploys the inspection relay: it decodes the wire protocol
between a client and a database or API, evaluates each statement against
policy, records an audit trail, and masks sensitive values on the way back. It
is independent of the gateway and agent charts — install it on its own, as many
times as you have upstreams to front.

Everything the relay does is decided by the config document under `config:`.
There is no startable default, so the chart refuses to render a Deployment that
could not start: a config naming no listeners, an admin server on a port other
than 19000, a control plane with no token. See
[chart/sidecar/README.md](./chart/sidecar/README.md) for the full reference.

```sh
cat - > ./sidecar-values.yaml <<EOF
config:
  # Required, and on 19000: the chart probes /healthz there.
  admin:
    listen: '0.0.0.0:19000'
  audit:
    file: '-'          # stdout, for the container log pipeline
  mask:
    rules:
      - {name: emails, entities: [EMAIL_ADDRESS], strategy: redact}
  listeners:
    - name: appdb
      protocol: postgres
      listen: '0.0.0.0:15432'
      upstream: appdb.default.svc.cluster.local:5432
EOF
```

```sh
VERSION=$(curl -s https://releases.hoop.dev/release/latest.txt)
helm upgrade --install hoopsidecar \
  https://releases.hoop.dev/release/$VERSION/hoopsidecar-chart-$VERSION.tgz \
  -f sidecar-values.yaml
```

Validate a config change before it reaches a pod. This takes the config
document itself — everything nested under `config:` above, with that key
stripped — not the values file:

```sh
hoop start sidecar --config sidecar-config.yaml --validate
```

Only the admin port is declared and published. Lane ports differ per config, so
a lane is reached through whatever fronts the pod — an Envoy sidecar over
loopback, or a Service of your own carrying the chart's selector labels
(`app.kubernetes.io/name` and `app.kubernetes.io/instance`).

Every resource is named after its release, so installing the chart once per
upstream in the same namespace is safe.

## Installing the clean (AGPL/SSPL-free) line

`hoop-ng-chart` and `hoopagent-ng-chart` are drop-in equivalents of
`hoop-chart` and `hoopagent-chart` that default their images to the clean-only
`hoophq/hoop-ng` and `hoophq/hoopdev-ng` repositories. Those repositories have
no dirty history, so a deployment can never accidentally pull an image that
contains AGPL/SSPL packages. They accept the same values as the base charts
(nested under the `hoop-chart:` / `hoopagent-chart:` key).

> `hoop-ng-chart` / `hoopagent-ng-chart` are the packaged artifact names only;
> the installed Helm release name (`hoop` / `hoopagent` below) is unchanged, so
> switching an existing install to the clean line is an in-place upgrade.

```sh
VERSION=$(curl -s https://releases.hoop.dev/release/latest.txt)
# gateway
helm upgrade --install hoop \
  https://releases.hoop.dev/release/$VERSION/hoop-ng-chart-$VERSION.tgz \
  -f values.yaml
# agent
helm upgrade --install hoopagent \
  https://releases.hoop.dev/release/$VERSION/hoopagent-ng-chart-$VERSION.tgz \
  --set 'hoopagent-chart.config.HOOP_KEY='
```

## Development

To add new configuration(s)

1. Go to `./chart/gateway|agent/templates/secrets-config.yaml`
2. Add any relevant environment variables
3. Edit `./chart/gateway|agent/values.yaml` and add defaults or any necessary comment

Test it by running

```sh
helm template ./chart/<component>/ -f yourvalues.yaml
```

Use helm lint to see if everything is ok

```sh
helm lint ./chart/<component>/ -f yourvalues.yaml
```
