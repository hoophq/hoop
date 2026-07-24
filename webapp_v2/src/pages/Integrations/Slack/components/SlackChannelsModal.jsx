import { useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import Modal from '@/components/Modal'
import TextInput from '@/components/TextInput'
import Button from '@/components/Button'

/**
 * Edits the Slack channel IDs of a single connection. The value is a
 * comma-separated list that is trimmed and split into the connection's
 * config array on save.
 */
function SlackChannelsModal({ connection, plugin, saving, onSave, onClose }) {
  const entry = (plugin?.connections ?? []).find((c) => c.id === connection.id)
  const [channels, setChannels] = useState((entry?.config ?? []).join(', '))

  async function handleSave() {
    const config = channels
      .split(',')
      .map((channel) => channel.trim())
      .filter(Boolean)
    const saved = await onSave(connection.id, config)
    if (saved) onClose()
  }

  return (
    <Modal opened onClose={onClose} title="Configurations">
      <Stack gap="md">
        <TextInput
          label="Slack channels"
          description="Provide slack channels to receive connection reviews."
          placeholder="C039AQNN5DF, C031T9LDGAH"
          value={channels}
          onChange={(e) => setChannels(e.currentTarget.value)}
          data-autofocus
        />
        <Text size="xs" c="dimmed">
          {`Channels for the connection '${connection.name}', separated by comma.`}
        </Text>
        <Group justify="flex-end">
          <Button onClick={handleSave} loading={saving}>
            Save
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

export default SlackChannelsModal
