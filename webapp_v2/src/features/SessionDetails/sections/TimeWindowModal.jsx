import { useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import TextInput from '@/components/TextInput'

// The window is picked in local time; the store sends it in UTC.
export default function TimeWindowModal({ opened, loading, onClose, onConfirm }) {
  const [start, setStart] = useState('')
  const [end, setEnd] = useState('')
  const close = () => {
    setStart('')
    setEnd('')
    onClose()
  }
  return (
    <Modal opened={opened} onClose={close} title="Available Time Window" zIndex={400}>
      <form
        onSubmit={(event) => {
          event.preventDefault()
          onConfirm({ start, end })
        }}
      >
        <Stack gap="lg">
          <Text size="sm" c="dimmed">
            {"Select the available time window for executing this session's command."}
          </Text>
          <TextInput label="Start Time" type="time" required value={start} onChange={(e) => setStart(e.currentTarget.value)} />
          <Stack gap={4}>
            <TextInput label="End Time" type="time" required value={end} onChange={(e) => setEnd(e.currentTarget.value)} />
            <Text size="xs" c="dimmed">
              If the end time is earlier than the start time, the time window continues to the next day.
            </Text>
          </Stack>
          <Group justify="space-between">
            <Button variant="light" color="gray" onClick={close} disabled={loading}>
              Cancel
            </Button>
            <Button type="submit" loading={loading}>
              Approve and Set Time Window
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
