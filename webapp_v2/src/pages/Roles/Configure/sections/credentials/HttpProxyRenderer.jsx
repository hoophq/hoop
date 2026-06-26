import { Stack, Title } from '@mantine/core'
import PredefinedFields from './shared/PredefinedFields'
import HttpHeaders from './shared/HttpHeaders'
import AllowInsecureSsl from './shared/AllowInsecureSsl'
import AgentSelector from './shared/AgentSelector'

// Generic HTTP proxy renderer for the non-Claude httpproxy subtypes
// (web-application, grafana, kibana). The catalog only carries
// REMOTE_URL for these; the headers list + insecure-SSL toggle are
// added by the React form (CLJS does the same — see network.cljs).
const HTTPPROXY_FIELDS = [
  {
    key: 'remote_url',
    label: 'Remote URL',
    required: true,
    placeholder: 'e.g. https://example.com',
  },
]

export default function HttpProxyRenderer({
  connection,
  availableSources,
  forceNewState,
  hideRoleInfo,
}) {
  return (
    <Stack gap="xl">
      <Stack gap="md">
        <Title order={4}>Environment credentials</Title>
        <PredefinedFields
          connection={connection}
          fields={HTTPPROXY_FIELDS}
          availableSources={availableSources}
          forceNewState={forceNewState}
          hideRoleInfo={hideRoleInfo}
        />
      </Stack>
      <HttpHeaders
        connection={connection}
        availableSources={availableSources}
        hideRoleInfo={hideRoleInfo}
      />
      <AllowInsecureSsl connection={connection} />
      <AgentSelector />
    </Stack>
  )
}
