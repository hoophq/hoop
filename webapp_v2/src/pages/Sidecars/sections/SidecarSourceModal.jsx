import { Group, Stack, Text } from '@mantine/core'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Modal from '@/components/Modal'

// Which side owns a sidecar's configuration. A running sidecar picks the flip
// up on its next check-in, within a minute, and applies what a live process
// can change; the rest it names in its log and takes on its next start.
// Neither direction destroys the stored document.
export default function SidecarSourceModal({ opened, toDisk, storedListeners, onClose, onConfirm, loading }) {
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={toDisk ? 'Load configuration from disk?' : 'Let the control plane own the configuration?'}
      size="sm"
    >
      <Stack>
        <Stack gap={4}>
          {toDisk ? (
            <>
              <Text size="sm">
                {
                  "The control plane will stop delivering this sidecar's configuration and send only its license."
                }
              </Text>
              <Text size="sm">
                The sidecar switches to its own config file; the configuration stored here is kept and not applied.
              </Text>
            </>
          ) : (
            <>
              <Text size="sm">
                {
                  "The control plane takes over this sidecar's configuration."
                }
              </Text>
              {storedListeners === 0 && (
                <Text size="sm">
                  It stores no listeners yet, so there is nothing to deliver until the sidecar restarts and imports its
                  config file.
                </Text>
              )}
            </>
          )}
        </Stack>
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose} disabled={loading}>
            Cancel
          </Button>
          <Button onClick={onConfirm} loading={loading}>
            {toDisk ? 'Load from disk' : 'Use the control plane'}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
