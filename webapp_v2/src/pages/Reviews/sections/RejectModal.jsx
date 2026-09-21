import { Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import Textarea from '@/components/Textarea'

export default function RejectModal({
  opened,
  onClose,
  onConfirm,
  loading,
  reason,
  onReasonChange,
}) {
  return (
    // Above Mantine's default 200: this one opens over the review detail.
    <Modal opened={opened} onClose={onClose} title="Reject statement" zIndex={400}>
      <Stack gap="lg">
        <Text size="sm">
          The sidecar denies this statement and the reason reaches whoever asks about it.
        </Text>
        <Textarea
          label="Reason (optional)"
          placeholder="e.g. run this against the replica instead"
          value={reason}
          onChange={(e) => onReasonChange(e.currentTarget.value)}
          minRows={3}
          autosize
        />
        <Group justify="flex-end" gap="sm">
          <Button variant="subtle" color="gray" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button color="red" loading={loading} onClick={() => onConfirm(reason.trim())}>
            Reject
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
