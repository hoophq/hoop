// Required credential fields per catalog connection subtype.
// Ported from webapp/src/webapp/connections/constants.cljs::connection-configs-required.
//
// Order in the array drives the rendering order in the Credentials tab.
// `required: true` enforces native HTML5 validation on Save.

export const CATALOG_FIELDS = {
  tcp: [
    { key: 'host', label: 'Host', required: true },
    { key: 'port', label: 'Port', required: true },
  ],
  mysql: [
    { key: 'host', label: 'Host', required: true },
    { key: 'user', label: 'User', required: true },
    { key: 'pass', label: 'Pass', required: true },
    { key: 'port', label: 'Port', required: true },
    { key: 'db', label: 'Db', required: true },
  ],
  postgres: [
    { key: 'host', label: 'Host', required: true },
    { key: 'user', label: 'User', required: true },
    { key: 'pass', label: 'Pass', required: true },
    { key: 'port', label: 'Port', required: true },
    { key: 'db', label: 'Db', required: true },
    { key: 'sslmode', label: 'Sslmode (Optional)', required: false },
  ],
  mssql: [
    { key: 'host', label: 'Host', required: true },
    { key: 'user', label: 'User', required: true },
    { key: 'pass', label: 'Pass', required: true },
    { key: 'port', label: 'Port', required: true },
    { key: 'db', label: 'Db', required: true },
    { key: 'insecure', label: 'Insecure (Optional)', required: false },
  ],
  oracledb: [
    { key: 'host', label: 'Host', required: true },
    { key: 'user', label: 'User', required: true },
    { key: 'pass', label: 'Pass', required: true },
    { key: 'port', label: 'Port', required: true },
    { key: 'sid', label: 'SID', required: true, placeholder: 'SID or Service name' },
  ],
  mongodb: [
    {
      key: 'connection_string',
      label: 'Connection string',
      required: true,
      placeholder: 'mongodb+srv://root:<password>@devcluster.mwb5sun.mongodb.net/',
    },
  ],
  ssh: [
    { key: 'host', label: 'Host', required: true },
    { key: 'port', label: 'Port', required: false },
    { key: 'user', label: 'User', required: true },
    { key: 'pass', label: 'Pass', required: true },
    {
      key: 'authorized_server_keys',
      label: 'Private Key',
      required: true,
      placeholder: 'Enter your private key',
      type: 'textarea',
    },
  ],
  httpproxy: [
    {
      key: 'remote_url',
      label: 'Remote URL',
      required: true,
      placeholder: 'e.g. https://example.com',
    },
  ],
  'claude-code': [
    {
      key: 'remote_url',
      label: 'Anthropic API URL',
      required: true,
      placeholder: 'https://api.anthropic.com',
    },
    {
      key: 'HEADER_X_API_KEY',
      label: 'Anthropic API Key',
      required: true,
      placeholder: 'sk-ant-...',
    },
  ],
  'kubernetes-token': [
    {
      key: 'cluster_url',
      label: 'Cluster URL',
      required: true,
      placeholder: 'e.g. https://kubernetes.default.svc.cluster.local:443',
    },
    {
      key: 'authorization',
      label: 'Authorization token',
      required: true,
      placeholder: 'e.g. jwt.token.example',
    },
  ],
}

// Connection method indicator. Derived from the raw secret values
// since the gateway doesn't store an explicit "method" field. See
// secretsCodec.js for reference detection.
export const CONNECTION_METHODS = {
  MANUAL: 'manual',
  SECRETS_MANAGER: 'secrets_manager',
  AWS_IAM: 'aws_iam',
}

// Whether the type/subtype supports the connection-method selector.
// Currently only database subtypes expose the choice.
export function supportsConnectionMethods({ type } = {}) {
  return type === 'database'
}

// Subtypes that follow the metadata-driven catalog path. Custom-type
// connections without these subtypes use the free-form key/value list.
export function isCatalogSubtype(subtype) {
  return Boolean(subtype && CATALOG_FIELDS[subtype])
}
