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
