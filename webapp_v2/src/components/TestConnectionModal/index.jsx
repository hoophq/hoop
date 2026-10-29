import { Stack, Group, Text, Title, Loader, ThemeIcon } from '@mantine/core'
import { Check, TriangleAlert, X } from 'lucide-react'
import Alert from '@/components/Alert'
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
  const duration =
    durationMs == null ? null : durationMs < 1000 ? durationMs + ' ms' : (durationMs / 1000).toFixed(1) + ' seconds'
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      withCloseButton={!testing}
      title={<Title order={3} fw={700} c="gray.9">Connectivity Check</Title>}
      size={500}
      centered
    >
      <Stack gap="lg">
        <Stack gap="xs">
          <Text size="sm" c="dimmed">
            {'Resource Role: ' + (connectionName || '')}
          </Text>
          {duration && (
            <Text size="xs" c="dimmed">
              {'Completed in ' + duration}
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
          <Alert color="red" icon={<TriangleAlert size={16} />}>
            <Text size="xs">{errorMessage}</Text>
          </Alert>
        )}

        <Group justify="flex-end">
          <Button onClick={onClose} disabled={testing}>Done</Button>
        </Group>
      </Stack>
    </Modal>
  )
}
