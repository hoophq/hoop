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
