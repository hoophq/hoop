import { isSecretReference, decodeSecretValue, parseReference } from './secretsCodec'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'
import { SOURCES } from './secretsCodec'

// Derive the connection method (and provider, when method is Secrets
// Manager) from the envvar values' prefixes. Mirrors CLJS
// connection_method.cljs::infer-connection-method. Tie-break order
// (Vault > AWS Secrets Manager) follows CLJS — in practice a connection
// only carries one provider's references.
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

// Wrapper for callers that only need the method.
export function deriveConnectionMethod(secrets = {}) {
  return deriveConnectionInfo(secrets).method
}

export { isSecretReference }
