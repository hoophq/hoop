import { useState } from 'react'
import { Stack, Text, Title } from '@mantine/core'
import Tabs from '@/components/Tabs'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePlugin } from '../usePlugin'
import PluginConnectionsList from '../components/PluginConnectionsList'
import SlackChannelsModal from './components/SlackChannelsModal'
import SlackConfigurationsTab from './components/SlackConfigurationsTab'
import { slackAppConfigured } from './helpers'

// The gateway's Slack page, sibling of ControlPlaneSlack.jsx: reviews reach
// the channels set on each connection.
function GatewaySlack() {
  const {
    plugin,
    connections,
    status,
    mutating,
    toggleConnection,
    updateConnectionConfig,
    saveEnvvars,
  } = usePlugin('slack')
  // Chosen once, when the plugin answers: the Slack App before it is set up,
  // since nothing on the list works without it, and the list after. A save
  // does not move the admin off the tab they are on.
  const [tab, setTab] = useState(null)
  if (tab === null && status === 'ready') {
    setTab(slackAppConfigured(plugin) ? 'connections' : 'configurations')
  }
  const [configConnection, setConfigConnection] = useState(null)

  const showLoader = useMinDelay(status === 'loading')

  if (showLoader) return <PageLoader />
  if (status === 'error') return <PageLoader error message="Failed to load the Slack plugin." />

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={1}>Slack</Title>
        <Text c="dimmed">
          Enable Slack on your connections and configure your Slack App to receive reviews.
        </Text>
      </Stack>

      <Tabs value={tab} onChange={setTab}>
        <Tabs.List>
          <Tabs.Tab value="connections">Connections</Tabs.Tab>
          <Tabs.Tab value="configurations">Configurations</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="connections" pt="md">
          <PluginConnectionsList
            plugin={plugin}
            connections={connections}
            mutating={mutating}
            onToggle={toggleConnection}
            renderAction={(connection, enabled) => (
              <Button
                variant="outline"
                size="xs"
                disabled={!enabled}
                onClick={() => setConfigConnection(connection)}
              >
                Configure
              </Button>
            )}
          />
        </Tabs.Panel>

        <Tabs.Panel value="configurations" pt="md">
          <SlackConfigurationsTab plugin={plugin} saving={mutating} onSave={saveEnvvars} />
        </Tabs.Panel>
      </Tabs>

      {configConnection && (
        <SlackChannelsModal
          connection={configConnection}
          plugin={plugin}
          saving={mutating}
          onSave={updateConnectionConfig}
          onClose={() => setConfigConnection(null)}
        />
      )}
    </Stack>
  )
}

export default GatewaySlack
