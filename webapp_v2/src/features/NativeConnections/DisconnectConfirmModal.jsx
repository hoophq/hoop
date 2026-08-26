import { Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import { DRAWER_MODAL_Z_INDEX } from './constants'

/**
 * Confirmation for Disconnect, matching the CLJS danger dialog.
 *
 * Local rather than a shared ConfirmDialog: that component is still a roadmap
 * item (Wave 1 / B1.3), and pages currently build their own — see
 * pages/Roles/Configure/index.jsx DeleteConfirmationModal.
 */
export function DisconnectConfirmModal({ opened, onClose, onConfirm, connectionName, loading }) {
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Disconnect session"
      size="md"
      zIndex={DRAWER_MODAL_Z_INDEX}
    >
      <Stack gap="lg">
        <Text fz="sm">
          {`Are you sure you want to disconnect the native client session for "${connectionName}"?`}
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="default" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button color="red" onClick={onConfirm} loading={loading}>
            Disconnect
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
