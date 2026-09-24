import { useState } from 'react'
import { Stack, Text, Title } from '@mantine/core'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Code from '@/components/Code'
import PageLoader from '@/components/PageLoader'
import Tabs from '@/components/Tabs'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePlugin } from '../usePlugin'
import SidecarSlackChannelsTab from './SidecarSlackChannelsTab'
import SlackConfigurationsTab from './components/SlackConfigurationsTab'

// The control plane's Slack page, sibling of GatewaySlack.jsx. A click on
// Approve is matched to a Hoop user by Slack ID, then by the email Slack holds
// for the clicker, which needs scopes the gateway's Slack app never asked for. Reviews go
// to the channels set per sidecar or listener, and to the default channel.
function ControlPlaneSlack() {
  const { plugin, status, mutating, saveEnvvars } = usePlugin('slack')
  const [tab, setTab] = useState('sidecars')

  const showLoader = useMinDelay(status === 'loading')

  if (showLoader) return <PageLoader />
  if (status === 'error') return <PageLoader error message="Failed to load the Slack plugin." />

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={1}>Slack</Title>
        <Text c="dimmed">Configure your Slack App to receive the reviews your sidecars hold.</Text>
      </Stack>

      <Alert color="blue" variant="light" icon={<Info size={16} />} radius="md">
        <Text size="sm">
          {'Approvers are matched to Hoop users by their Slack ID, then by their Slack email. Add the '}
          <Code>users:read</Code>
          {', '}
          <Code>users:read.email</Code>
          {' and '}
          <Code>usergroups:read</Code>
          {' scopes to your Slack App and reinstall it. The last one lets Settings, Provisioning import reviewers from Slack user groups.'}
        </Text>
      </Alert>

      <Tabs value={tab} onChange={setTab}>
        <Tabs.List>
          <Tabs.Tab value="sidecars">Sidecars</Tabs.Tab>
          <Tabs.Tab value="configurations">Configurations</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="sidecars" pt="md">
          <SidecarSlackChannelsTab />
        </Tabs.Panel>

        <Tabs.Panel value="configurations" pt="md">
          <SlackConfigurationsTab plugin={plugin} saving={mutating} onSave={saveEnvvars} />
        </Tabs.Panel>
      </Tabs>
    </Stack>
  )
}

export default ControlPlaneSlack
