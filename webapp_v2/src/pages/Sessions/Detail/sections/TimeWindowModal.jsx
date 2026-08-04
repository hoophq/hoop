import { useState } from 'react'
import { Group, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import TextInput from '@/components/TextInput'

/**
 * Port of `audit/views/time_window_modal.cljs` (51 LOC). A real `<form>` in v1,
 * so both fields are `required` and Enter submits.
 *
 * The subtitle promises the window wraps past midnight, but v1's own
 * `is-within-time-window?` (formatters.cljs:192) compares plain
 * minutes-of-day and does not wrap. Copy kept verbatim; the gateway decides.
 */
export default function TimeWindowModal({ opened, onClose, onConfirm }) {
  const [startTime, setStartTime] = useState('')
  const [endTime, setEndTime] = useState('')

  const submit = (event) => {
    event.preventDefault()
    onConfirm({ startTime, endTime })
    setStartTime('')
    setEndTime('')
  }

  return (
    <Modal opened={opened} onClose={onClose} size={500}>
      <form onSubmit={submit}>
        <Stack gap="xl">
          <Stack gap="xs">
            <Title order={1} size="h3">
              Available Time Window
            </Title>
            <Text size="md" c="dimmed">
              Select the available time window for executing this session's command.
            </Text>
          </Stack>

          <TextInput
            label="Start Time"
            type="time"
            required
            value={startTime}
            onChange={(event) => setStartTime(event.currentTarget.value)}
          />

          <Stack gap="xs">
            <TextInput
              label="End Time"
              type="time"
              required
              value={endTime}
              onChange={(event) => setEndTime(event.currentTarget.value)}
            />
            <Text size="sm" c="dimmed">
              If the end time is earlier than the start time, the time window continues to
              the next day.
            </Text>
          </Stack>

          <Group justify="space-between" align="center">
            <Button variant="subtle" color="gray" type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit">Approve and Set Time Window</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}
