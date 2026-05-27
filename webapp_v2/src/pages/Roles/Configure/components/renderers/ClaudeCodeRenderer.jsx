import { Stack, Title } from '@mantine/core'
import PredefinedFields from './shared/PredefinedFields'
import HttpHeadersSection from './shared/HttpHeadersSection'
import AllowInsecureSslSection from './shared/AllowInsecureSslSection'
import AgentSelectorSection from './shared/AgentSelectorSection'

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

export default function ClaudeCodeRenderer({
  connection,
  availableSources,
  forceNewState,
}) {
  return (
    <Stack gap="xl">
      <Stack gap="md">
        <Title order={4}>Basic info</Title>
        <PredefinedFields
          connection={connection}
          fields={CLAUDE_CODE_FIELDS}
          availableSources={availableSources}
          forceNewState={forceNewState}
        />
      </Stack>
      <HttpHeadersSection
        connection={connection}
        availableSources={availableSources}
        excludeKeys={HEADERS_EXCLUDE}
      />
      <AllowInsecureSslSection connection={connection} />
      <AgentSelectorSection />
    </Stack>
  )
}
