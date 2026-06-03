import { Cloud, FileText, KeyRound, ShieldCheck } from 'lucide-react'

// Placeholder rows for the empty-state row in envvars / HTTP headers /
// config files. Their keys never reach the backend (the store's
// buildSecretsPatch filters them), so the UI should render the Key
// input empty too — otherwise the user sees `NEW_KEY_1` as if it were
// their data.
export const PLACEHOLDER_KEY_RE =
  /^(envvar:NEW_KEY_|envvar:HEADER_NEW_HEADER_|filesystem:NEW_FILE_)\d+$/

// Validators for the Key input on each k/v section. Mirrors the CLJS
// rules at configuration_inputs.cljs:10-17, applied per keystroke so
// invalid characters don't make it into the input. Empty is always
// accepted so the user can clear the field.

// POSIX-compatible env-var name: starts with a letter, followed by
// letters/digits/underscores. Used by environment variables and
// configuration file names.
export function isValidPosixKey(value) {
  return value === '' || /^[A-Za-z][A-Za-z0-9_]*$/.test(value)
}

// HTTP header name: any non-whitespace string. Case-sensitive
// (headers like X-Request-Id keep their casing).
export function isValidHeaderKey(value) {
  return value === '' || /^\S+$/.test(value)
}

// Provider prefixes recognised on the wire. References (values
// prefixed with one of these) are not sensitive — they only name where
// to look up the actual secret — so the UI shows them verbatim instead
// of hiding behind a Set badge. `decodeForDisplay` strips the prefix
// because the source picker conveys which provider applies.
export const PROVIDER_PREFIX_RE =
  /^(_aws:|_envjson:|_vaultkv1:|_vaultkv2:|_aws_iam_rds:)/

// Helpers to translate between the form's plaintext view of a secret value
// and the base64-encoded JSONB representation that the gateway stores.
//
// Reference detection mirrors the backend (gateway/api/connections/secrets.go);
// any value whose base64 decodes to one of the well-known provider prefixes
// is a reference (not a sensitive secret) and the UI shows it verbatim
// instead of hiding it behind the write-only "Set" badge.

const REFERENCE_PREFIXES = [
  '_aws:',
  '_envjson:',
  '_vaultkv1:',
  '_vaultkv2:',
  '_aws_iam_rds:',
]

// Source identifiers for the per-field "where does this credential come
// from" selector that appears when the user is in Secrets Manager mode.
// Mirrors CLJS connection_method.cljs::source-text — keep these strings
// identical to the CLJS values so future cross-app debugging maps 1:1.
//
// `AWS_IAM_ROLE` is connection-method-driven (the picker doesn't expose
// it as a per-field source). Listed here so detection round-trips
// existing values that carry the `_aws_iam_rds:` prefix and saves
// re-encode under the same source identifier.
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

// Lucide icons shown next to each source label inside the SourcedInput
// picker. The Manual/Cloud/ShieldCheck triple matches the icons used by
// the Connection Method picker in CredentialsTab.jsx so the visual
// vocabulary stays consistent across the page. Vault gets KeyRound —
// lucide doesn't ship a Vault brand icon and KeyRound communicates the
// "wrapped secret" semantics cleanly.
export const SOURCE_ICONS = {
  [SOURCES.MANUAL]: FileText,
  [SOURCES.VAULT_KV1]: KeyRound,
  [SOURCES.VAULT_KV2]: KeyRound,
  [SOURCES.AWS_SECRETS_MANAGER]: Cloud,
  [SOURCES.AWS_IAM_ROLE]: ShieldCheck,
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

// Returns the source identifier implied by an encoded value's prefix.
// Inline values (no provider prefix) map to 'manual'. IAM RDS and
// envjson references don't surface as source choices today.
export function sourceFromEncodedValue(encoded) {
  if (!encoded) return SOURCES.MANUAL
  const plain = safeAtob(encoded)
  if (plain == null) return SOURCES.MANUAL
  for (const prefix of Object.keys(SOURCE_BY_PREFIX)) {
    if (plain.startsWith(prefix)) return SOURCE_BY_PREFIX[prefix]
  }
  return SOURCES.MANUAL
}

// Wraps a plaintext value in the encoded form for a given source. When
// the source is manual, the value is stored as-is. For provider
// sources, the prefix is prepended before base64-encoding.
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

// Decode + strip provider prefix for display in a credential input.
// The source picker conveys which provider applies, so the bare
// reference id is what the user should see and edit.
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

// Reads the (already-decoded) reference string and returns its parts.
// e.g. "_aws:my-secret:password" -> { provider: '_aws', secretId: 'my-secret', secretKey: 'password' }
// Returns null if the value isn't a recognised reference format.
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
