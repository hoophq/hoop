import { useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import Textarea from '@/components/Textarea'

// Opens over the session details, which stays open after the decision.
export default function RejectModal({ opened, loading, onClose, onConfirm }) {
  const [comment, setComment] = useState('')
  const close = () => {
    setComment('')
    onClose()
  }
  return (
    <Modal opened={opened} onClose={close} title="Reject Details" zIndex={400}>
      <Stack gap="lg">
        <Text size="sm" c="dimmed">
          Optionally include more details for this access request rejection.
        </Text>
        <Textarea
          label="Comment"
          placeholder="Share the details here..."
          value={comment}
          onChange={(e) => setComment(e.currentTarget.value)}
          minRows={3}
          autosize
        />
        <Group justify="space-between">
          <Button variant="light" color="gray" onClick={close} disabled={loading}>
            Cancel
          </Button>
          <Button color="red" loading={loading} onClick={() => onConfirm(comment)}>
            Confirm and Reject
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
