import { Stack, Text, Title } from '@mantine/core'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePlugin } from '../usePlugin'
import PluginConnectionsList from '../components/PluginConnectionsList'

function IntegrationsWebhooks() {
  const { plugin, connections, status, mutating, toggleConnection } = usePlugin('webhooks')

  const showLoader = useMinDelay(status === 'loading')

  if (showLoader) return <PageLoader />
  if (status === 'error') return <PageLoader error message="Failed to load the Webhooks plugin." />

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={1}>Webhooks</Title>
        <Text c="dimmed">Enable webhook events for your connections.</Text>
      </Stack>

      <PluginConnectionsList
        plugin={plugin}
        connections={connections}
        mutating={mutating}
        onToggle={toggleConnection}
      />
    </Stack>
  )
}

export default IntegrationsWebhooks
