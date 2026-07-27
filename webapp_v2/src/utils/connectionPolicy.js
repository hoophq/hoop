// Connection-shape policies that the React frontend needs but
// connections-metadata.json doesn't carry. CLJS keeps the equivalent
// rules hardcoded too (see references inline).
//
// Consolidated here so the next person who wants to push these into the
// JSON metadata sees the whole set in one place. Adding a new policy
// flag? Add it here, not scattered across page components.

// Connection method indicator. Derived from the raw secret values since
// the gateway doesn't store an explicit "method" field. See
// secretsCodec.js for the value-prefix-based detection.
export const CONNECTION_METHODS = {
  MANUAL: 'manual',
  SECRETS_MANAGER: 'secrets_manager',
  AWS_IAM: 'aws_iam',
}

// Subtypes that route to the free-form custom editor regardless of
// whether the JSON catalog has a credentials entry for them. Mirrors
// the CLJS exclusion set at
// webapp/src/webapp/resources/configure_role/credentials_tab.cljs:17.
// "tcp" / "ssh" sit in the metadata catalog with HOST/PORT credentials,
// but the CLJS chose free-form for the custom + tcp/ssh combination
// because those are generic shell-style wrappers; honour that decision.
const FREE_FORM_CUSTOM_SUBTYPES = new Set([
  'tcp',
  'httpproxy',
  'ssh',
  'linux-vm',
  'claude-code',
])
export function isFreeFormCustomSubtype(subtype) {
  return Boolean(subtype) && FREE_FORM_CUSTOM_SUBTYPES.has(subtype)
}

// Which subtypes can authenticate via AWS IAM Role. CLJS limits this to
// MySQL and Postgres because those are the only RDS auth backends the
// gateway/agent currently support
// (webapp/.../setup/connection_method.cljs:92).
const AWS_IAM_ROLE_SUBTYPES = new Set(['postgres', 'mysql'])
export function supportsAwsIam(subtype) {
  return AWS_IAM_ROLE_SUBTYPES.has(subtype)
}

// Whether the connection is eligible for the Test Connection action.
// Mirrors the CLJS can-test-connection? predicate. Databases and
// applications are universally testable; a few subtype-specific
// connections (AWS shell paths) also are.
const TESTABLE_TYPES = new Set(['database', 'application'])
const TESTABLE_SUBTYPES = new Set([
  'postgres',
  'mysql',
  'mssql',
  'oracledb',
  'mongodb',
  'dynamodb',
  // Keep the legacy and current AWS resource subtypes. The Test
  // Connection action drives a server-side probe by connection name,
  // not by credential shape, so honouring both spellings here doesn't
  // collide with the metadata-driven catalog (which uses the prefixed
  // names only).
  'cloudwatch',
  'aws-cloudwatch',
])
export function canTestConnection({ type, subtype } = {}) {
  if (TESTABLE_SUBTYPES.has(subtype)) return true
  return TESTABLE_TYPES.has(type)
}

// Which connections can launch the native-client flow via the command
// palette. Direct, HTTP-proxy, custom-native, and Kubernetes paths each
// have their own list — see CommandPalette/ConnectionActionsPage.
const NATIVE_CLIENT_DIRECT_SUBTYPES = new Set([
  'postgres',
  'ssh',
  'github',
  'git',
])
const NATIVE_CLIENT_HTTP_PROXY_SUBTYPES = new Set([
  'httpproxy',
  'kibana',
  'grafana',
  'claude-code',
])
const NATIVE_CLIENT_CUSTOM_SUBTYPES = new Set(['rdp', 'aws-ssm'])
const NATIVE_CLIENT_KUBERNETES_SUBTYPES = new Set([
  'kubernetes-token',
  'kubernetes',
  'kubernetes-eks',
])
export function canAccessNativeClient(connection) {
  if (!connection || connection.access_mode_connect !== 'enabled') return false
  const { subtype, type } = connection
  return (
    NATIVE_CLIENT_DIRECT_SUBTYPES.has(subtype) ||
    NATIVE_CLIENT_HTTP_PROXY_SUBTYPES.has(subtype) ||
    NATIVE_CLIENT_KUBERNETES_SUBTYPES.has(subtype) ||
    (type === 'custom' && NATIVE_CLIENT_CUSTOM_SUBTYPES.has(subtype))
  )
}
