import { isSecretReference, decodeSecretValue, parseReference } from './secretsCodec'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'

// Derive the active connection method from the current envvar values.
//
// The gateway doesn't store an explicit "method" field; it's an emergent
// property of how values are encoded:
//   * any value resolving to `_aws_iam_rds:` → AWS IAM
//   * else if any value resolving to a secret-provider prefix → Secrets Manager
//   * else → Manual
//
// `secrets` is the map returned by the gateway (base64-encoded values).
export function deriveConnectionMethod(secrets = {}) {
  let sawReference = false
  for (const value of Object.values(secrets)) {
    if (!value) continue
    const ref = parseReference(value)
    if (!ref) continue
    if (decodeSecretValue(value).startsWith('_aws_iam_rds:')) {
      return CONNECTION_METHODS.AWS_IAM
    }
    if (isSecretReference(value)) {
      sawReference = true
    }
  }
  return sawReference ? CONNECTION_METHODS.SECRETS_MANAGER : CONNECTION_METHODS.MANUAL
}
