import { Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'

// Removing the last listener leaves a document the sidecar cannot start from:
// the handshake answers 412 and the process refuses to boot. Saying it here is
// cheaper than the operator finding out from a lane that stopped answering.
export default function DeleteListenerModal({ listener, lastOne, opened, onClose, onConfirm, loading }) {
  return (
    <Modal opened={opened} onClose={onClose} title="Delete listener?" size="sm">
      <Stack>
        <Stack gap={4}>
          <Text size="sm">
            {`This removes the listener "${listener?.name ?? ''}" from the configuration the control plane serves, along with any guardrails and masking rules written on it.`}
          </Text>
          {lastOne && (
            <Text size="sm" c="red">
              It is the only listener. A sidecar with none refuses to start.
            </Text>
          )}
        </Stack>
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button color="red" onClick={onConfirm} loading={loading}>
            Delete listener
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
