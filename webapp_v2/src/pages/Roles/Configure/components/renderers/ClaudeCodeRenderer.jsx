import { Stack, Title } from '@mantine/core'
import PredefinedFields from './shared/PredefinedFields'
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
      <AllowInsecureSslSection connection={connection} />
      <AgentSelectorSection />
    </Stack>
  )
}
