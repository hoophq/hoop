import { Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'

// Deleting revokes the sidecar's token for good: a sidecar still starting with
// it will be refused.
export default function DeleteSidecarModal({ sidecar, opened, onClose, onConfirm, loading }) {
  return (
    <Modal opened={opened} onClose={onClose} title="Delete sidecar?" size="sm">
      <Stack>
        <Stack gap={4}>
          <Text size="sm">
            {`This removes the sidecar "${sidecar?.name ?? ''}" and revokes its token. A sidecar still using it is refused.`}
          </Text>
          <Text size="sm">This cannot be undone.</Text>
        </Stack>
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button color="red" onClick={onConfirm} loading={loading}>
            Delete sidecar
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
