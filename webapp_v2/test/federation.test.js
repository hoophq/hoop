import { describe, expect, it } from 'vitest'
import { CONNECTION_METHODS } from '@/utils/connectionPolicy'
import { deriveConnectionInfo } from '@/pages/Roles/Configure/utils/connectionMethod'
import { encodeSecretValue } from '@/pages/Roles/Configure/utils/secretsCodec'
import {
  adminCredentialsForTest,
  buildFederationConfig,
  buildFederationTestPayload,
  DEFAULT_FEDERATION_FORM,
  federationOutcomeSnackbar,
  formFromConfig,
  PROJECT_KEY,
  SA_JSON_KEY,
  staticSecretsMirror,
} from '@/pages/Roles/Configure/utils/federation'

const STORED = {
  id: 'f1',
  hook_source: 'builtin',
  builtin_provider: 'gcp_oauth',
  has_admin_credentials: true,
  identity_source_attribute: '$.user.email',
  identity_target_template: 'sa-{user.email}',
  fallback_policy: 'deny',
  token_ttl_seconds: 1800,
  extra_config: { project_id: 'proj', scopes: ['https://www.googleapis.com/auth/bigquery'] },
}

const SA_JSON = '{"type":"service_account"}'

describe('federation form', () => {
  it('round-trips a stored config and keeps unknown extra_config keys', () => {
    const config = buildFederationConfig(formFromConfig(STORED))
    expect(config).toEqual({
      hook_source: 'builtin',
      builtin_provider: 'gcp_oauth',
      extra_config: STORED.extra_config,
      identity_source_attribute: '$.user.email',
      identity_target_template: 'sa-{user.email}',
      fallback_policy: 'deny',
      token_ttl_seconds: 1800,
    })
  })

  it('omits a blank admin credential so the gateway keeps the stored one', () => {
    expect(buildFederationConfig(DEFAULT_FEDERATION_FORM)).not.toHaveProperty('admin_credentials_json')
    const typed = { ...DEFAULT_FEDERATION_FORM, admin_credentials_json: SA_JSON }
    expect(buildFederationConfig(typed).admin_credentials_json).toBe(SA_JSON)
  })
})

describe('staticSecretsMirror', () => {
  const form = {
    ...DEFAULT_FEDERATION_FORM,
    admin_credentials_json: SA_JSON,
    extra_config: { project_id: 'proj' },
  }

  it('mirrors the gcp_iam key and a changed project id', () => {
    expect(staticSecretsMirror(form, {})).toEqual({
      [SA_JSON_KEY]: encodeSecretValue(SA_JSON),
      [PROJECT_KEY]: encodeSecretValue('proj'),
    })
  })

  it('never writes the gcp_oauth client JSON as a GCP credentials file', () => {
    const oauth = { ...form, builtin_provider: 'gcp_oauth' }
    expect(staticSecretsMirror(oauth, {})).not.toHaveProperty(SA_JSON_KEY)
  })

  it('skips a project id the connection already holds', () => {
    const current = { [PROJECT_KEY]: encodeSecretValue('proj') }
    expect(staticSecretsMirror(form, current)).not.toHaveProperty(PROJECT_KEY)
  })
})

describe('adminCredentialsForTest', () => {
  const blank = { ...DEFAULT_FEDERATION_FORM }
  const secrets = { [SA_JSON_KEY]: encodeSecretValue(SA_JSON) }

  it('prefers the typed credential', () => {
    expect(adminCredentialsForTest({ ...blank, admin_credentials_json: 'typed' }, secrets)).toBe('typed')
  })

  it('falls back to the inline service-account key for gcp_iam', () => {
    expect(adminCredentialsForTest(blank, secrets)).toBe(SA_JSON)
  })

  it('does not read a masked or referenced key', () => {
    expect(adminCredentialsForTest(blank, secrets, true)).toBe('')
    const ref = { [SA_JSON_KEY]: encodeSecretValue('_aws:my-secret:key') }
    expect(adminCredentialsForTest(blank, ref)).toBe('')
  })
})

describe('buildFederationTestPayload', () => {
  it('sends the bq probe with the project id', () => {
    const payload = buildFederationTestPayload({
      email: 'a@b.co',
      form: formFromConfig({ ...STORED, builtin_provider: 'gcp_iam' }),
      agentId: 'agent-1',
      connection: { type: 'custom', subtype: 'bigquery' },
      adminCredentials: SA_JSON,
    })
    expect(payload.user_email).toBe('a@b.co')
    expect(payload.config.admin_credentials_json).toBe(SA_JSON)
    expect(payload.connection).toEqual({
      agent_id: 'agent-1',
      type: 'custom',
      subtype: 'bigquery',
      command: ['bq', 'query', '--use_legacy_sql=false', '--format=pretty'],
      test_script: 'SELECT 1',
      envs: { CLOUDSDK_CORE_PROJECT: 'proj' },
    })
  })
})

describe('federationOutcomeSnackbar', () => {
  it('maps the consent callback outcome', () => {
    expect(federationOutcomeSnackbar('success')).toEqual({ level: 'success', text: 'Google account connected' })
    expect(federationOutcomeSnackbar('error', 'consent_denied').text).toBe(
      'Google authorization failed: consent denied',
    )
  })
})

describe('deriveConnectionInfo', () => {
  it('reports IAM federation when the role has a federation config', () => {
    expect(deriveConnectionInfo({}, { federated: true }).method).toBe(CONNECTION_METHODS.IAM_FEDERATION)
    expect(deriveConnectionInfo({}).method).toBe(CONNECTION_METHODS.MANUAL)
  })
})
