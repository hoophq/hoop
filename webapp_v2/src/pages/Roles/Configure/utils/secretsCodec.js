// Sentinel keys used for the blank row that the UI keeps visible at the
// bottom of envvars / HTTP headers / config files. buildSecretsPatch
// drops them before save.
export const PLACEHOLDER_KEY_RE =
  /^(envvar:NEW_KEY_|envvar:HEADER_NEW_HEADER_|filesystem:NEW_FILE_)\d+$/

// POSIX env-var name validator. Mirrors CLJS configuration_inputs.cljs.
// Empty is accepted so the user can clear the field.
export function isValidPosixKey(value) {
  return value === '' || /^[A-Za-z][A-Za-z0-9_]*$/.test(value)
}

// HTTP header name validator: any non-whitespace token.
export function isValidHeaderKey(value) {
  return value === '' || /^\S+$/.test(value)
}

export const PROVIDER_PREFIX_RE =
  /^(_aws:|_envjson:|_vaultkv1:|_vaultkv2:|_aws_iam_rds:)/

// Helpers to translate between the form's plaintext view and the
// base64-encoded representation the gateway stores. Reference detection
// mirrors gateway/api/connections/secrets.go::IsSecretReference.

const REFERENCE_PREFIXES = [
  '_aws:',
  '_envjson:',
  '_vaultkv1:',
  '_vaultkv2:',
  '_aws_iam_rds:',
]

// Source identifiers — keep identical to CLJS connection_method.cljs
// values so the wire encoding stays compatible across both webapps.
// AWS_IAM_ROLE isn't a user-pickable source; listed so detection +
// re-encoding round-trip values that already carry its prefix.
export const SOURCES = {
  MANUAL: 'manual-input',
  VAULT_KV1: 'vault-kv1',
  VAULT_KV2: 'vault-kv2',
  AWS_SECRETS_MANAGER: 'aws-secrets-manager',
  AWS_IAM_ROLE: 'aws-iam-role',
}

export const SOURCE_LABELS = {
  [SOURCES.MANUAL]: 'Manual',
  [SOURCES.VAULT_KV1]: 'Vault KV v1',
  [SOURCES.VAULT_KV2]: 'Vault KV v2',
  [SOURCES.AWS_SECRETS_MANAGER]: 'AWS Secrets Manager',
  [SOURCES.AWS_IAM_ROLE]: 'AWS IAM Role',
}

const PREFIX_BY_SOURCE = {
  [SOURCES.VAULT_KV1]: '_vaultkv1:',
  [SOURCES.VAULT_KV2]: '_vaultkv2:',
  [SOURCES.AWS_SECRETS_MANAGER]: '_aws:',
  [SOURCES.AWS_IAM_ROLE]: '_aws_iam_rds:',
}

const SOURCE_BY_PREFIX = {
  '_vaultkv1:': SOURCES.VAULT_KV1,
  '_vaultkv2:': SOURCES.VAULT_KV2,
  '_aws:': SOURCES.AWS_SECRETS_MANAGER,
  '_aws_iam_rds:': SOURCES.AWS_IAM_ROLE,
}

export function sourceFromEncodedValue(encoded) {
  if (!encoded) return SOURCES.MANUAL
  const plain = safeAtob(encoded)
  if (plain == null) return SOURCES.MANUAL
  for (const prefix of Object.keys(SOURCE_BY_PREFIX)) {
    if (plain.startsWith(prefix)) return SOURCE_BY_PREFIX[prefix]
  }
  return SOURCES.MANUAL
}

// Prepends the source's prefix (if any) and base64-encodes.
export function encodeSecretForSource(plain, source) {
  if (plain == null) return ''
  const prefix = PREFIX_BY_SOURCE[source]
  return prefix ? btoa(prefix + plain) : btoa(plain)
}

function safeAtob(encoded) {
  try {
    return atob(encoded)
  } catch {
    return null
  }
}

export function decodeSecretValue(encoded) {
  if (!encoded) return ''
  const plain = safeAtob(encoded)
  return plain == null ? '' : plain
}

// Decode + strip the provider prefix — the source picker conveys which
// provider applies, so the bare reference id is what the user edits.
export function decodeForDisplay(encoded) {
  if (!encoded) return ''
  return decodeSecretValue(encoded).replace(PROVIDER_PREFIX_RE, '')
}

export function encodeSecretValue(plain) {
  if (plain == null) return ''
  return btoa(plain)
}

export function isSecretReference(encoded) {
  if (!encoded) return false
  const plain = safeAtob(encoded)
  if (plain == null) return false
  return REFERENCE_PREFIXES.some((p) => plain.startsWith(p))
}

// Parses a reference value into its parts —
// "_aws:my-secret:password" → { provider: 'aws', secretId: 'my-secret', secretKey: 'password' }.
// Returns null for non-reference values.
export function parseReference(encoded) {
  if (!encoded) return null
  const plain = safeAtob(encoded)
  if (plain == null) return null
  for (const prefix of REFERENCE_PREFIXES) {
    if (plain.startsWith(prefix)) {
      const rest = plain.slice(prefix.length)
      const [secretId = '', secretKey = ''] = rest.split(':')
      return { provider: prefix.replace(/[:_]/g, ''), secretId, secretKey, raw: plain }
    }
  }
  return null
}
