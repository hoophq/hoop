import { useState } from 'react'
import { Anchor, Grid, Group, Stack, Text, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import PasswordInput from '@/components/PasswordInput'
import Button from '@/components/Button'

const SLACK_DOCS_URL = 'https://hoop.dev/docs/integrations/slack'

// GET /plugins/slack returns envvars base64-encoded; on decode failure the
// raw value is shown (mirrors the legacy base64-decode-safe behavior).
function decodeBase64Safe(value) {
  if (!value) return ''
  try {
    return atob(value)
  } catch {
    return value
  }
}

/**
 * Slack App token configuration: pre-fills the decoded tokens and saves
 * them base64-encoded via PUT /plugins/slack/config.
 */
function SlackConfigurationsTab({ plugin, saving, onSave }) {
  const savedBotToken = plugin?.config?.envvars?.SLACK_BOT_TOKEN
  const savedAppToken = plugin?.config?.envvars?.SLACK_APP_TOKEN

  const [botToken, setBotToken] = useState(() => decodeBase64Safe(savedBotToken))
  const [appToken, setAppToken] = useState(() => decodeBase64Safe(savedAppToken))

  // Re-sync the inputs when the saved values change (e.g. after a refetch
  // following save) — state-during-render pattern, keyed by the raw values.
  const [prevSaved, setPrevSaved] = useState({ bot: savedBotToken, app: savedAppToken })
  if (prevSaved.bot !== savedBotToken || prevSaved.app !== savedAppToken) {
    setPrevSaved({ bot: savedBotToken, app: savedAppToken })
    setBotToken(decodeBase64Safe(savedBotToken))
    setAppToken(decodeBase64Safe(savedAppToken))
  }

  function handleSave() {
    if (!botToken.trim() || !appToken.trim()) {
      notifications.show({ message: 'Both tokens are required.', color: 'red' })
      return
    }
    let payload
    try {
      payload = {
        SLACK_BOT_TOKEN: btoa(botToken.trim()),
        SLACK_APP_TOKEN: btoa(appToken.trim()),
      }
    } catch {
      notifications.show({ message: 'Tokens must contain only ASCII characters.', color: 'red' })
      return
    }
    onSave(payload, 'Slack app configured!')
  }

  return (
    <Grid columns={3} gutter="xl">
      <Grid.Col span={1}>
        <Stack gap="xs">
          <Title order={4}>Slack App Configurations</Title>
          <Text size="sm" c="dimmed">
            {'Here you will integrate with your Slack App. Please visit our doc to '}
            <Anchor href={SLACK_DOCS_URL} target="_blank" size="sm" fw={600} underline="always">
              learn how to create a Slack App.
            </Anchor>
          </Text>
        </Stack>
      </Grid.Col>
      <Grid.Col span={2}>
        <Stack gap="md">
          <PasswordInput
            label="Slack bot token"
            value={botToken}
            onChange={(e) => setBotToken(e.currentTarget.value)}
          />
          <PasswordInput
            label="Slack app token"
            value={appToken}
            onChange={(e) => setAppToken(e.currentTarget.value)}
          />
          <Group justify="flex-end">
            <Button onClick={handleSave} loading={saving}>
              Save
            </Button>
          </Group>
        </Stack>
      </Grid.Col>
    </Grid>
  )
}

export default SlackConfigurationsTab
