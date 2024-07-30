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
