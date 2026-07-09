import { useEffect, useMemo, useRef, useState } from 'react'
import { Stack, Title } from '@mantine/core'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Select from '@/components/Select'
import { useUserStore } from '@/stores/useUserStore'
import PredefinedFields from '@/pages/Roles/Configure/sections/credentials/shared/PredefinedFields'
import HttpHeaders from '@/pages/Roles/Configure/sections/credentials/shared/HttpHeaders'
import AllowInsecureSsl from '@/pages/Roles/Configure/sections/credentials/shared/AllowInsecureSsl'
import AgentSelector from '@/pages/Roles/Configure/sections/credentials/shared/AgentSelector'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'
import { decodeSecretValue, encodeSecretValue } from '@/pages/Roles/Configure/utils/secretsCodec'

// Claude Code httpproxy connection. Catalog's `httpproxy/claude-code`
// entry ships an empty credentials list, so all fields live here.
// Matches CLJS webapp/.../resources/configure_role/claude_code_edit.cljs.
//
// Two credential providers share this renderer:
//   anthropic → REMOTE_URL (editable) + HEADER_X_API_KEY
//   vertex    → GCP_REGION + GCP_PROJECT_ID + GCP_SERVICE_ACCOUNT_JSON,
//               with REMOTE_URL derived from the region (never edited)
// The provider is UI-only state, never persisted: a connection is
// Vertex iff any GCP_* secret key exists (key presence survives
// hide_role_info masking). The Vertex option is gated by the
// experimental.claude_code_vertex feature flag, but an already-Vertex
// connection always shows the select so existing config is never hidden.
const ANTHROPIC_FIELDS = [
  {
    key: 'remote_url',
    label: 'Anthropic API URL',
    required: true,
    placeholder: 'https://api.anthropic.com',
  },
  {
    key: 'HEADER_X_API_KEY',
    label: 'Anthropic API Key',
    required: true,
    placeholder: 'sk-ant-...',
  },
]

// Vertex fields are manual-input only (no secrets-manager source
// picker), mirroring CLJS which renders them without the source
// adornment.
const VERTEX_FIELDS = [
  {
    key: 'GCP_REGION',
    label: 'GCP Region',
    required: true,
    placeholder: 'us-east5',
  },
  {
    key: 'GCP_PROJECT_ID',
    label: 'GCP Project ID',
    required: true,
    placeholder: 'my-gcp-project',
  },
  {
    key: 'GCP_SERVICE_ACCOUNT_JSON',
    label: 'Service Account JSON',
    required: true,
    type: 'textarea',
    minRows: 8,
    placeholder: '{\n  "type": "service_account",\n  ...\n}',
  },
]

// HEADER_X_API_KEY is rendered as a dedicated Anthropic API Key field
// above; HttpHeaders hides it so it doesn't appear twice. (The GCP_*
// and REMOTE_URL keys are structurally invisible to HttpHeaders — it
// only lists `envvar:HEADER_*` keys.)
const HEADERS_EXCLUDE = ['envvar:HEADER_X_API_KEY']

const LEGACY_API_KEY = 'envvar:X_API_KEY'
const HEADER_API_KEY = 'envvar:HEADER_X_API_KEY'
const REMOTE_URL_KEY = 'envvar:REMOTE_URL'
const REGION_KEY = 'envvar:GCP_REGION'
const PROJECT_KEY = 'envvar:GCP_PROJECT_ID'
const SA_JSON_KEY = 'envvar:GCP_SERVICE_ACCOUNT_JSON'
const VERTEX_KEYS = [REGION_KEY, PROJECT_KEY, SA_JSON_KEY]
// Everything a provider switch may stage/unstage. The legacy
// X_API_KEY delete is deliberately not listed — dropping the legacy
// key is correct under either provider.
const PROVIDER_SCOPED_KEYS = [HEADER_API_KEY, REMOTE_URL_KEY, ...VERTEX_KEYS]

const VERTEX_FLAG = 'experimental.claude_code_vertex'
const DEFAULT_REGION = 'us-east5'
const DEFAULT_ANTHROPIC_URL = 'https://api.anthropic.com'

function isVertexConnection(connection) {
  const secrets = connection?.secret || {}
  return VERTEX_KEYS.some((k) => k in secrets)
}

// Mirrors CLJS process_form.cljs::claude-code-vertex-remote-url —
// regional endpoint unless the region is blank or the literal "global".
function vertexRemoteUrl(region) {
  const r = (region || '').trim()
  return !r || r === 'global'
    ? 'https://aiplatform.googleapis.com'
    : `https://${r}-aiplatform.googleapis.com`
}

function stageLegacyApiKeyMove(secrets, replaceSecret) {
  const legacyValue = secrets[LEGACY_API_KEY]
  const headerHasValue = Boolean(secrets[HEADER_API_KEY])
  if (!headerHasValue && legacyValue) {
    replaceSecret(HEADER_API_KEY, legacyValue)
  }
}

// Legacy `envvar:X_API_KEY` was renamed to `envvar:HEADER_X_API_KEY`
// (so it travels as an HTTP header to the Anthropic API). Connections
// created before the rename still carry the old key; on first render
// we stage the migration (move value if HEADER_X_API_KEY is empty,
// drop X_API_KEY) so saving completes the rename without the user
// having to retype the API key. Mirrors CLJS claude_code_edit.cljs:41-52.
// The value move only applies to Anthropic connections — staging an
// Anthropic header onto a Vertex config would persist junk — but the
// legacy key itself is dropped either way.
function useLegacyApiKeyMigration(connection, initialProvider) {
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const migratedRef = useRef(false)
  useEffect(() => {
    if (migratedRef.current) return
    migratedRef.current = true
    const secrets = connection?.secret || {}
    if (!(LEGACY_API_KEY in secrets)) return
    if (initialProvider === 'anthropic') {
      stageLegacyApiKeyMove(secrets, replaceSecret)
    }
    deleteSecret(LEGACY_API_KEY)
  }, [connection, initialProvider, replaceSecret, deleteSecret])
}

export default function ClaudeCodeRenderer({
  connection,
  availableSources,
  forceNewState,
  hideRoleInfo,
}) {
  const initialProvider = useMemo(
    () => (isVertexConnection(connection) ? 'vertex' : 'anthropic'),
    [connection],
  )
  const [provider, setProvider] = useState(initialProvider)
  const flagOn = useUserStore((s) => !!s.featureFlags?.[VERTEX_FLAG])
  const showProviderSelect = flagOn || initialProvider === 'vertex'

  const stagedSecrets = useConfigureRoleStore((s) => s.stagedSecrets)
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const cancelSecretChange = useConfigureRoleStore((s) => s.cancelSecretChange)

  useLegacyApiKeyMigration(connection, initialProvider)

  // Switching provider stages explicit deletes for the inactive
  // provider's keys — unlike the CLJS flow, which rebuilds the whole
  // secret map on save, the PATCH model preserves untouched keys, so a
  // switched connection would otherwise keep stale credentials (and the
  // agent goes down the Vertex path whenever GCP_SERVICE_ACCOUNT_JSON
  // is present). Switching back to the persisted provider restores the
  // pristine state.
  const handleProviderChange = (next) => {
    if (!next || next === provider) return
    setProvider(next)
    PROVIDER_SCOPED_KEYS.forEach((key) => cancelSecretChange(key))
    const secrets = connection?.secret || {}
    if (next === initialProvider) {
      // Back to the persisted provider: re-stage the legacy value move
      // the blanket cancel above just dropped (the legacy key's delete
      // lives outside PROVIDER_SCOPED_KEYS and survives).
      if (next === 'anthropic' && LEGACY_API_KEY in secrets) {
        stageLegacyApiKeyMove(secrets, replaceSecret)
      }
      return
    }
    if (next === 'vertex') {
      if (HEADER_API_KEY in secrets) deleteSecret(HEADER_API_KEY)
      // Region default + derived REMOTE_URL are seeded by the effect
      // below so they also survive a connection-method switch wiping
      // the staging area.
    } else {
      VERTEX_KEYS.forEach((key) => {
        if (key in secrets) deleteSecret(key)
      })
      replaceSecret(REMOTE_URL_KEY, encodeSecretValue(DEFAULT_ANTHROPIC_URL))
    }
  }

  // Single source of truth for the derived REMOTE_URL and the region
  // default while in Vertex mode. Converges in at most two passes:
  // seed region → derive URL → value-equal no-op.
  useEffect(() => {
    if (provider !== 'vertex') return
    const switched = initialProvider !== 'vertex'
    const stagedRegion = stagedSecrets[REGION_KEY]
    if (!switched && !stagedRegion) {
      // Already-Vertex connection with an untouched (possibly masked)
      // region: never clobber the stored REMOTE_URL with a derivation
      // from an unknown region — echoing null preserves it. Also drops
      // a stale derivation if the user edited region and then Restored.
      if (stagedSecrets[REMOTE_URL_KEY]) cancelSecretChange(REMOTE_URL_KEY)
      return
    }
    if (switched && !stagedRegion) {
      replaceSecret(REGION_KEY, encodeSecretValue(DEFAULT_REGION))
      return
    }
    const encoded = encodeSecretValue(
      vertexRemoteUrl(decodeSecretValue(stagedRegion.value)),
    )
    if (stagedSecrets[REMOTE_URL_KEY]?.value !== encoded) {
      replaceSecret(REMOTE_URL_KEY, encoded)
    }
  }, [provider, initialProvider, stagedSecrets, replaceSecret, cancelSecretChange])

  return (
    <Stack gap="xl">
      <Stack gap="md">
        <Title order={4}>Basic info</Title>
        {showProviderSelect && (
          <Select
            label="Provider"
            allowDeselect={false}
            value={provider}
            onChange={handleProviderChange}
            data={[
              { value: 'anthropic', label: 'Anthropic API' },
              { value: 'vertex', label: 'Google Vertex AI' },
            ]}
          />
        )}
        {provider === 'vertex' ? (
          <>
            <Alert variant="light" color="gray" icon={<Info size={16} />}>
              Claude Code runs in Vertex mode against hoop. hoop mints a
              short-lived token from the service account below and proxies
              requests to Google Vertex AI.
            </Alert>
            <PredefinedFields
              connection={connection}
              fields={VERTEX_FIELDS}
              availableSources={null}
              forceNewState={forceNewState}
              hideRoleInfo={hideRoleInfo}
            />
          </>
        ) : (
          <PredefinedFields
            connection={connection}
            fields={ANTHROPIC_FIELDS}
            availableSources={availableSources}
            forceNewState={forceNewState}
            hideRoleInfo={hideRoleInfo}
          />
        )}
      </Stack>
      <HttpHeaders
        connection={connection}
        availableSources={availableSources}
        excludeKeys={HEADERS_EXCLUDE}
        hideRoleInfo={hideRoleInfo}
      />
      <AllowInsecureSsl connection={connection} />
      <AgentSelector />
    </Stack>
  )
}
