import { Group, Stack } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'

/**
 * Confirmation dialog for a destructive or irreversible action.
 *
 * Usage:
 *   <ConfirmDialog
 *     opened={opened}
 *     title="Delete role?"
 *     confirmLabel="Confirm and delete"
 *     danger
 *     loading={deleting}
 *     onConfirm={handleDelete}
 *     onClose={close}
 *   >
 *     <Text size="sm">This cannot be undone.</Text>
 *   </ConfirmDialog>
 */
export default function ConfirmDialog({
  opened,
  title,
  children,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  danger = false,
  loading = false,
  onConfirm,
  onClose,
}) {
  return (
    <Modal opened={opened} onClose={() => !loading && onClose?.()} title={title} size="md">
      <Stack>
        {children}
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose} disabled={loading}>
            {cancelLabel}
          </Button>
          <Button color={danger ? 'red' : undefined} onClick={onConfirm} loading={loading}>
            {confirmLabel}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
