import { useEffect, useRef } from 'react'
import { Stack, Title } from '@mantine/core'
import PredefinedFields from './shared/PredefinedFields'
import HttpHeadersSection from './shared/HttpHeadersSection'
import AllowInsecureSslSection from './shared/AllowInsecureSslSection'
import AgentSelectorSection from './shared/AgentSelectorSection'
import { useConfigureRoleStore } from '../../store'

// Claude Code httpproxy connection. Catalog's `httpproxy/claude-code`
// entry ships an empty credentials list, so all fields live here.
// Matches CLJS webapp/.../resources/configure_role/claude_code_edit.cljs.
const CLAUDE_CODE_FIELDS = [
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

// HEADER_X_API_KEY is rendered as a dedicated Anthropic API Key field
// above; HttpHeadersSection hides it so it doesn't appear twice.
const HEADERS_EXCLUDE = ['envvar:HEADER_X_API_KEY']

const LEGACY_API_KEY = 'envvar:X_API_KEY'
const HEADER_API_KEY = 'envvar:HEADER_X_API_KEY'

// Legacy `envvar:X_API_KEY` was renamed to `envvar:HEADER_X_API_KEY`
// (so it travels as an HTTP header to the Anthropic API). Connections
// created before the rename still carry the old key; on first render
// we stage the migration (move value if HEADER_X_API_KEY is empty,
// drop X_API_KEY) so saving completes the rename without the user
// having to retype the API key. Mirrors CLJS claude_code_edit.cljs:41-52.
function useLegacyApiKeyMigration(connection) {
  const replaceSecret = useConfigureRoleStore((s) => s.replaceSecret)
  const deleteSecret = useConfigureRoleStore((s) => s.deleteSecret)
  const migratedRef = useRef(false)
  useEffect(() => {
    if (migratedRef.current) return
    migratedRef.current = true
    const secrets = connection?.secret || {}
    if (!(LEGACY_API_KEY in secrets)) return
    const legacyValue = secrets[LEGACY_API_KEY]
    const headerHasValue = Boolean(secrets[HEADER_API_KEY])
    if (!headerHasValue && legacyValue) {
      replaceSecret(HEADER_API_KEY, legacyValue)
    }
    deleteSecret(LEGACY_API_KEY)
  }, [connection, replaceSecret, deleteSecret])
}

export default function ClaudeCodeRenderer({
  connection,
  availableSources,
  forceNewState,
  hideRoleInfo,
}) {
  useLegacyApiKeyMigration(connection)
  return (
    <Stack gap="xl">
      <Stack gap="md">
        <Title order={4}>Basic info</Title>
        <PredefinedFields
          connection={connection}
          fields={CLAUDE_CODE_FIELDS}
          availableSources={availableSources}
          forceNewState={forceNewState}
          hideRoleInfo={hideRoleInfo}
        />
      </Stack>
      <HttpHeadersSection
        connection={connection}
        availableSources={availableSources}
        excludeKeys={HEADERS_EXCLUDE}
        hideRoleInfo={hideRoleInfo}
      />
      <AllowInsecureSslSection connection={connection} />
      <AgentSelectorSection />
    </Stack>
  )
}
