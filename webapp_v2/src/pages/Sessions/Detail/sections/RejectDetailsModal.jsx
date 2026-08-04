import { useState } from 'react'
import { Group, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import Textarea from '@/components/Textarea'

/**
 * Port of `sessions/components/reject_details_modal.cljs` (32 LOC).
 *
 * Divergence from v1, deliberate: v1 has a single global modal slot, so opening
 * this one *destroyed* the session-details modal underneath and ran its cleanup
 * — after confirming, the user did not return to the details. Here it stacks,
 * which reads as an implementation limit in v1 rather than intent.
 */
export default function RejectDetailsModal({ opened, onClose, onConfirm }) {
  const [comment, setComment] = useState('')

  const confirm = () => {
    onConfirm({ comment })
    setComment('')
  }

  return (
    <Modal opened={opened} onClose={onClose} size={500}>
      <Stack gap="xl">
        <Stack gap="xs">
          <Title order={1} size="h3">
            Reject Details
          </Title>
          <Text size="md" c="dimmed">
            Optionally include more details for this access request rejection.
          </Text>
        </Stack>

        <Textarea
          label="Comment"
          placeholder="Share the details here..."
          value={comment}
          onChange={(event) => setComment(event.currentTarget.value)}
        />

        <Group justify="space-between" align="center">
          <Button variant="light" color="gray" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={confirm}>Confirm and Reject</Button>
        </Group>
      </Stack>
    </Modal>
  )
}
