import { Stack, Text, Title } from '@mantine/core'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePlugin } from '../usePlugin'
import PluginConnectionsList from '../components/PluginConnectionsList'

function IntegrationsWebhooks() {
  const { plugin, status, mutating, toggleConnection } = usePlugin('webhooks')

  const showLoader = useMinDelay(status === 'loading')

  if (showLoader) return <PageLoader h={400} />
  if (status === 'error') return <Text c="red">Failed to load the Webhooks plugin.</Text>

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={1}>Webhooks</Title>
        <Text c="dimmed">Enable webhook events for your connections.</Text>
      </Stack>

      <PluginConnectionsList plugin={plugin} mutating={mutating} onToggle={toggleConnection} />
    </Stack>
  )
}

export default IntegrationsWebhooks
