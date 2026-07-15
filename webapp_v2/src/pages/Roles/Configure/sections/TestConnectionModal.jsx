import { Stack, Group, Text, Title, Loader, ThemeIcon } from '@mantine/core'
import { Check, X } from 'lucide-react'
import Button from '@/components/Button'
import Modal from '@/components/Modal'

// "Connectivity Check" modal — mirrors the CLJS test-connection-modal.
//
// Two parallel checks run from the store: one fetches the connection's
// current status (agent online/offline), the other actually exercises
// the connection via /connections/:name/test. Each row updates
// independently so the user sees per-probe progress instead of a
// single blocking spinner.

function statusLabel(status) {
  if (status === 'checking') return 'Checking…'
  if (status === 'online') return 'Online'
  if (status === 'offline') return 'Offline'
  if (status === 'successful') return 'Successful'
  if (status === 'failed') return 'Failed'
  return ''
}

function statusColor(status) {
  if (status === 'checking') return 'gray'
  if (status === 'online' || status === 'successful') return 'green'
  return 'red'
}

function StatusRow({ label, status }) {
  const isLoading = status === 'checking'
  const color = statusColor(status)
  const success = status === 'online' || status === 'successful'
  return (
    <Group justify="space-between" align="center">
      <Text size="sm" fw={500}>{label}</Text>
      <Group gap={6} align="center">
        {isLoading ? (
          <Loader size="xs" />
        ) : (
          <ThemeIcon size="sm" variant="light" color={color}>
            {success ? <Check size={12} /> : <X size={12} />}
          </ThemeIcon>
        )}
        <Text size="sm" c={color === 'gray' ? 'dimmed' : color}>
          {statusLabel(status)}
        </Text>
      </Group>
    </Group>
  )
}

export default function TestConnectionModal({
  opened,
  testing,
  agentStatus,
  connectionStatus,
  durationMs,
  errorMessage,
  connectionName,
  onClose,
}) {
  const durationSec = durationMs != null ? (durationMs / 1000).toFixed(1) : null
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      withCloseButton={!testing}
      title={<Title order={3} fw={700} c="gray.9">Connectivity Check</Title>}
      size="md"
      centered
    >
      <Stack gap="lg">
        <Stack gap="xs">
          <Text size="sm" c="dimmed">
            {'Resource Role: ' + (connectionName || '')}
          </Text>
          {durationSec != null && (
            <Text size="xs" c="dimmed">
              {'Completed in ' + durationSec + ' seconds'}
            </Text>
          )}
        </Stack>

        <Stack gap="md">
          <Title order={5} fw={600}>Details</Title>
          <Stack gap="sm">
            <StatusRow label="Agent Status" status={agentStatus} />
            <StatusRow label="Connection Status" status={connectionStatus} />
          </Stack>
        </Stack>

        {connectionStatus === 'failed' && errorMessage && (
          <Text size="sm" c="red">{errorMessage}</Text>
        )}

        <Group justify="flex-end">
          <Button onClick={onClose} disabled={testing}>Done</Button>
        </Group>
      </Stack>
    </Modal>
  )
}
