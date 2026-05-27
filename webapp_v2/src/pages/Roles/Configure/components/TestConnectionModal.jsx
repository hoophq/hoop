import { Stack, Group, Text, Button, Loader, ThemeIcon } from '@mantine/core'
import { Check, X } from 'lucide-react'
import Modal from '@/components/Modal'

// "Connectivity Check" modal — replaces the inline notification with a
// dedicated dialog that mirrors the legacy CLJS test-connection-modal.
//
// The gateway's /connections/:name/test endpoint only returns a single
// boolean (Success), so the per-status detail that the CLJS modal had
// (Agent Status / Connection Status separately) collapses into a single
// pass/fail row here. Duration is measured client-side from when the
// request was issued.

function statusRow(label, status) {
  const isLoading = status === 'checking'
  const isSuccess = status === 'success'
  const color = isLoading ? 'gray' : isSuccess ? 'green' : 'red'
  const text = isLoading ? 'Checking…' : isSuccess ? 'Successful' : 'Failed'
  return (
    <Group justify="space-between">
      <Text size="sm" fw={500}>{label}</Text>
      <Group gap={6}>
        {isLoading ? (
          <Loader size="xs" />
        ) : (
          <ThemeIcon size="sm" variant="light" color={color}>
            {isSuccess ? <Check size={12} /> : <X size={12} />}
          </ThemeIcon>
        )}
        <Text size="sm" c={color === 'gray' ? 'dimmed' : color}>{text}</Text>
      </Group>
    </Group>
  )
}

export default function TestConnectionModal({
  opened,
  testing,
  result,
  connectionName,
  onClose,
}) {
  const status = testing ? 'checking' : result?.success ? 'success' : 'failed'
  const durationSec =
    !testing && result?.durationMs != null ? (result.durationMs / 1000).toFixed(1) : null

  return (
    <Modal opened={opened} onClose={onClose} title="Connectivity Check" size="md" centered>
      <Stack gap="lg">
        <Stack gap={4}>
          <Text size="sm" c="dimmed">
            {'Resource Role: ' + (connectionName || '')}
          </Text>
          {durationSec != null && (
            <Text size="xs" c="dimmed">
              {'Completed in ' + durationSec + ' seconds'}
            </Text>
          )}
        </Stack>

        <Stack gap="xs">
          <Text fw={600} size="sm">Details</Text>
          {statusRow('Connection Status', status)}
        </Stack>

        {!testing && result?.success === false && result?.message && (
          <Text size="sm" c="red">{result.message}</Text>
        )}

        <Group justify="flex-end">
          <Button onClick={onClose} disabled={testing}>
            Done
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
