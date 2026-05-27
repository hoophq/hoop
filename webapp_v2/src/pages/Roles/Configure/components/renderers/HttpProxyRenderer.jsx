import { Stack, Title } from '@mantine/core'
import PredefinedFields from './shared/PredefinedFields'
import HttpHeadersSection from './shared/HttpHeadersSection'
import AllowInsecureSslSection from './shared/AllowInsecureSslSection'
import AgentSelectorSection from './shared/AgentSelectorSection'

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
        />
      </Stack>
      <HttpHeadersSection
        connection={connection}
        availableSources={availableSources}
      />
      <AllowInsecureSslSection connection={connection} />
      <AgentSelectorSection />
    </Stack>
  )
}
