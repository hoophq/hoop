import {
  decodeSecretValue,
  encodeSecretValue,
  isSecretReference,
} from '@/pages/Roles/Configure/utils/secretsCodec'

export const FEDERATION_PROVIDERS = {
  GCP_IAM: 'gcp_iam',
  GCP_OAUTH: 'gcp_oauth',
}

export const SA_JSON_KEY = 'filesystem:GOOGLE_APPLICATION_CREDENTIALS'
export const PROJECT_KEY = 'envvar:CLOUDSDK_CORE_PROJECT'

export const OUTPUT_ENV_VARS = {
  [FEDERATION_PROVIDERS.GCP_IAM]: [
    'GOOGLE_APPLICATION_CREDENTIALS',
    'CLOUDSDK_CORE_PROJECT',
    'HOOP_FEDERATED_PRINCIPAL',
  ],
  [FEDERATION_PROVIDERS.GCP_OAUTH]: [
    'HOOP_GCP_ACCESS_TOKEN',
    'CLOUDSDK_CORE_PROJECT',
    'HOOP_FEDERATED_PRINCIPAL',
  ],
}

export const DEFAULT_FEDERATION_FORM = {
  builtin_provider: FEDERATION_PROVIDERS.GCP_IAM,
  admin_credentials_json: '',
  extra_config: { project_id: '' },
  identity_source_attribute: '$.user.email',
  identity_target_template: '{user.email}',
  fallback_policy: 'deny',
  token_ttl_seconds: 3600,
}

// GET never returns admin_credentials_json, so the form starts it blank.
export function formFromConfig(config) {
  if (!config) return DEFAULT_FEDERATION_FORM
  return {
    builtin_provider: config.builtin_provider || FEDERATION_PROVIDERS.GCP_IAM,
    admin_credentials_json: '',
    extra_config: { ...config.extra_config, project_id: config.extra_config?.project_id || '' },
    identity_source_attribute: config.identity_source_attribute || '$.user.email',
    identity_target_template: config.identity_target_template || '{user.email}',
    fallback_policy: config.fallback_policy || 'deny',
    token_ttl_seconds: config.token_ttl_seconds || 3600,
  }
}

// A blank admin_credentials_json is omitted: the gateway keeps the stored one.
export function buildFederationConfig(form) {
  const config = {
    hook_source: 'builtin',
    builtin_provider: form.builtin_provider,
    extra_config: form.extra_config,
    identity_source_attribute: form.identity_source_attribute,
    identity_target_template: form.identity_target_template,
    fallback_policy: form.fallback_policy,
    token_ttl_seconds: form.token_ttl_seconds,
  }
  if (form.admin_credentials_json.trim()) {
    config.admin_credentials_json = form.admin_credentials_json
  }
  return config
}

// A stored gcp_iam key is no OAuth client, and the reverse: a provider switch
// needs new credentials.
export function hasUsableStoredCredentials(config, form) {
  return Boolean(config?.has_admin_credentials) && config.builtin_provider === form.builtin_provider
}

export function federationFormsEqual(a, b) {
  return JSON.stringify(a) === JSON.stringify(b)
}

// gcp_iam's service-account key doubles as the connection's static
// GOOGLE_APPLICATION_CREDENTIALS, which the "static" fallback runs on.
// gcp_oauth's admin JSON is an OAuth client and must never land there.
export function staticSecretsMirror(form, currentSecrets = {}) {
  const out = {}
  const saJson = form.admin_credentials_json.trim()
  if (form.builtin_provider === FEDERATION_PROVIDERS.GCP_IAM && saJson) {
    out[SA_JSON_KEY] = encodeSecretValue(form.admin_credentials_json)
  }
  const projectId = form.extra_config.project_id.trim()
  if (projectId && decodeSecretValue(currentSecrets[PROJECT_KEY] || '') !== projectId) {
    out[PROJECT_KEY] = encodeSecretValue(projectId)
  }
  return out
}

// The test endpoint reads no stored credential, so an untouched gcp_iam form
// falls back to the inline service-account key the connection already holds.
export function adminCredentialsForTest(form, currentSecrets = {}, hideRoleInfo = false) {
  if (form.admin_credentials_json.trim()) return form.admin_credentials_json
  if (form.builtin_provider !== FEDERATION_PROVIDERS.GCP_IAM || hideRoleInfo) return ''
  const stored = currentSecrets[SA_JSON_KEY]
  if (!stored || isSecretReference(stored)) return ''
  return decodeSecretValue(stored)
}

export function buildFederationTestPayload({ email, form, agentId, connection, adminCredentials }) {
  const projectId = form.extra_config.project_id
  return {
    user_email: email,
    config: { ...buildFederationConfig(form), admin_credentials_json: adminCredentials },
    connection: {
      agent_id: agentId,
      type: connection.type || 'custom',
      subtype: connection.subtype || 'bigquery',
      command: ['bq', 'query', '--use_legacy_sql=false', '--format=pretty'],
      test_script: 'SELECT 1',
      envs: projectId ? { CLOUDSDK_CORE_PROJECT: projectId } : {},
    },
  }
}

export function federationOutcomeSnackbar(outcome, reason) {
  if (outcome === 'success') return { level: 'success', text: 'Google account connected' }
  if (outcome === 'error') {
    return {
      level: 'error',
      text: 'Google authorization failed' + (reason ? ': ' + reason.replaceAll('_', ' ') : ''),
    }
  }
  return { level: 'info', text: 'Google authorization finished' }
}
