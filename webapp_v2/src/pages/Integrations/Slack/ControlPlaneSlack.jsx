import { Stack, Text, Title } from '@mantine/core'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Code from '@/components/Code'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePlugin } from '../usePlugin'
import SlackConfigurationsTab from './components/SlackConfigurationsTab'

// The control plane's Slack page, sibling of GatewaySlack.jsx. A click on
// Approve is matched to a Hoop user by the email Slack holds for the clicker,
// which needs two scopes the gateway's Slack app never asked for.
function ControlPlaneSlack() {
  const { plugin, status, mutating, saveEnvvars } = usePlugin('slack')

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
          Approvers are matched to Hoop users by their Slack email. Add the <Code>users:read</Code>{' '}
          and <Code>users:read.email</Code> scopes to your Slack App and reinstall it. Without them,
          only users whose Slack ID is set on the Users page can approve.
        </Text>
      </Alert>

      <SlackConfigurationsTab plugin={plugin} saving={mutating} onSave={saveEnvvars} />
    </Stack>
  )
}

export default ControlPlaneSlack
