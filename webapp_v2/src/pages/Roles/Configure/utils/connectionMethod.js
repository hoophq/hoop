import { isSecretReference, decodeSecretValue, parseReference } from './secretsCodec'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'
import { SOURCES } from './secretsCodec'

// Derive the active connection method AND the Secrets Manager provider
// from the current envvar values. Mirrors the CLJS reference at
// webapp/src/webapp/connections/views/setup/connection_method.cljs:60-84
// (`infer-connection-method`).
//
// Rules:
//   * any value resolving to `_aws_iam_rds:` → { method: AWS_IAM,         provider: null }
//   * else any `_aws:` / `_vaultkv1:` / `_vaultkv2:` reference
//                                          → { method: SECRETS_MANAGER, provider: <first match> }
//   * else                                 → { method: MANUAL,           provider: null }
//
// Scan order matches CLJS: Vault wins over AWS Secrets Manager when both
// are present, because that's how CLJS handles the ambiguity. Practically
// a single connection should only ever carry one provider's references,
// so the tie-break rarely matters.
export function deriveConnectionInfo(secrets = {}) {
  let sawVaultKv1 = false
  let sawVaultKv2 = false
  let sawAwsSecretsManager = false

  for (const value of Object.values(secrets)) {
    if (!value) continue
    const ref = parseReference(value)
    if (!ref) continue
    const plain = decodeSecretValue(value)
    if (plain.startsWith('_aws_iam_rds:')) {
      return { method: CONNECTION_METHODS.AWS_IAM, provider: null }
    }
    if (plain.startsWith('_vaultkv1:')) sawVaultKv1 = true
    else if (plain.startsWith('_vaultkv2:')) sawVaultKv2 = true
    else if (plain.startsWith('_aws:')) sawAwsSecretsManager = true
  }

  if (sawVaultKv1) {
    return { method: CONNECTION_METHODS.SECRETS_MANAGER, provider: SOURCES.VAULT_KV1 }
  }
  if (sawVaultKv2) {
    return { method: CONNECTION_METHODS.SECRETS_MANAGER, provider: SOURCES.VAULT_KV2 }
  }
  if (sawAwsSecretsManager) {
    return { method: CONNECTION_METHODS.SECRETS_MANAGER, provider: SOURCES.AWS_SECRETS_MANAGER }
  }
  return { method: CONNECTION_METHODS.MANUAL, provider: null }
}

// Backwards-compatible wrapper. New callers should use deriveConnectionInfo
// so they can pick up the provider too.
export function deriveConnectionMethod(secrets = {}) {
  return deriveConnectionInfo(secrets).method
}

// Kept exported because `isSecretReference` was previously used elsewhere.
// (No callers in the current tree, but the symbol was public-ish.)
export { isSecretReference }
