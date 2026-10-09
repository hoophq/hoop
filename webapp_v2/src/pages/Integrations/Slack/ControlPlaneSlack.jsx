import { useState } from 'react'
import { Stack, Text, Title } from '@mantine/core'
import PageLoader from '@/components/PageLoader'
import Tabs from '@/components/Tabs'
import { useMinDelay } from '@/hooks/useMinDelay'
import { docsUrl } from '@/utils/docsUrl'
import { usePlugin } from '@/pages/Integrations/usePlugin'
import SidecarSlackChannelsTab from '@/pages/Integrations/Slack/SidecarSlackChannelsTab'
import SlackConfigurationsTab from '@/pages/Integrations/Slack/components/SlackConfigurationsTab'
import { slackAppConfigured } from '@/pages/Integrations/Slack/helpers'

// The control plane's Slack page, sibling of GatewaySlack.jsx. Reviews go to
// each listener's channels; the Configurations channel is the fallback for a
// listener with none. The scopes and the approver match live in the docs.
function ControlPlaneSlack() {
  const { plugin, status, mutating, saveEnvvars } = usePlugin('slack')
  // Chosen once, when the plugin answers: the Slack App before it is set up,
  // since nothing on the list works without it, and the list after. A save
  // does not move the admin off the tab they are on.
  const [tab, setTab] = useState(null)
  if (tab === null && status === 'ready') {
    setTab(slackAppConfigured(plugin) ? 'listeners' : 'configurations')
  }

  const showLoader = useMinDelay(status === 'loading')

  if (showLoader) return <PageLoader />
  if (status === 'error') return <PageLoader error message="Failed to load the Slack plugin." />

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={1}>Slack</Title>
        <Text c="dimmed">Send sidecar approval requests to Slack. Approvers decide there without signing in.</Text>
      </Stack>

      <Tabs value={tab} onChange={setTab}>
        <Tabs.List>
          <Tabs.Tab value="listeners">Listeners</Tabs.Tab>
          <Tabs.Tab value="configurations">Configurations</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="listeners" pt="md">
          <SidecarSlackChannelsTab />
        </Tabs.Panel>

        <Tabs.Panel value="configurations" pt="md">
          <SlackConfigurationsTab
            plugin={plugin}
            saving={mutating}
            onSave={saveEnvvars}
            channelLabel="Fallback channel"
            channelDescription="Receives approval requests from listeners with no channel."
            docsHref={docsUrl.controlPlane.slack}
          />
        </Tabs.Panel>
      </Tabs>
    </Stack>
  )
}

export default ControlPlaneSlack
