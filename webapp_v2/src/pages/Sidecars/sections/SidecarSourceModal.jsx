import { Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'

// Which side owns a sidecar's configuration. A running sidecar picks the flip
// up on its next check-in, within a minute, and applies what a live process
// can change; the rest it names in its log and takes on its next start.
// Neither direction destroys the stored document.
//
// What the flip DOES cost is said outright: handing the configuration to the
// config file stops every rule bound to this sidecar's listeners from being
// delivered. Nothing else in the app says so, and an admin who reads only the
// title would read this as a preference.
export default function SidecarSourceModal({
  opened,
  toConfigFile,
  storedListeners,
  onClose,
  onConfirm,
  loading,
}) {
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={toConfigFile ? 'Switch to the config file?' : 'Let the control plane own the configuration?'}
      size="sm"
    >
      <Stack>
        <Stack gap={4}>
          {toConfigFile ? (
            <>
              <Text size="sm">
                {
                  "The control plane will stop delivering this sidecar's configuration and send only its license."
                }
              </Text>
              <Text size="sm">
                Guardrails, masking and analyzer rules bound to its listeners stop reaching it. The sidecar runs what
                its config file says instead, and the configuration stored here is kept and not applied.
              </Text>
            </>
          ) : (
            <>
              <Text size="sm">{"The control plane takes over this sidecar's configuration."}</Text>
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
          <Button color={toConfigFile ? 'red' : undefined} onClick={onConfirm} loading={loading}>
            {toConfigFile ? 'Switch to the config file' : 'Use the control plane'}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
