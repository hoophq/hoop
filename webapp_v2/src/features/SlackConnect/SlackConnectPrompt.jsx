import { Group, Stack, Text } from '@mantine/core'
import { Link2 } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import { useSlackConnect } from './useSlackConnect'

// Sits at the top of the shell content, under the license banner.
export default function SlackConnectPrompt() {
  const { showPrompt, user, openModal, dismissPrompt } = useSlackConnect()
  if (!showPrompt) return null

  return (
    <Alert variant="light" py="sm" icon={<Link2 size={18} />} title="Connect Slack">
      <Stack gap="sm" align="flex-start">
        <Text size="sm">
          Connect your Slack user to approve requests in Slack and to get messages about your requests.
        </Text>
        <Group gap="sm">
          <Button size="sm" onClick={openModal}>
            Connect Slack
          </Button>
          <Button size="sm" variant="subtle" color="gray" onClick={() => dismissPrompt(user.id)}>
            Not now
          </Button>
        </Group>
      </Stack>
    </Alert>
  )
}
