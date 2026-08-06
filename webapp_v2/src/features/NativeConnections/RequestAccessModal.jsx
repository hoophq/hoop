import { useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import { ShieldCheck } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import Select from '@/components/Select'
import { useNativeAccessStore, FLOW_STATUS } from '@/stores/useNativeAccessStore'
import {
  ACCESS_DURATION_OPTIONS,
  DEFAULT_ACCESS_DURATION_MINUTES,
  DRAWER_MODAL_Z_INDEX,
} from './constants'
import { formatDurationSec } from './helpers'

function RequestAccessForm({ connectionName, connection, onCancel }) {
  const [duration, setDuration] = useState(DEFAULT_ACCESS_DURATION_MINUTES)
  const requestAccess = useNativeAccessStore((s) => s.requestAccess)
  const status = useNativeAccessStore((s) => s.statusByName[connectionName])
  const isRequesting = status === FLOW_STATUS.REQUESTING

  const jitSec = connection?.jit_access_duration_sec ?? null

  // Pass the JIT window through in seconds. The CLJS version divided by 60 and
  // the API multiplied it back, losing any window that was not a whole minute.
  const submit = () => requestAccess(connectionName, jitSec ?? Number(duration) * 60)

  return (
    <Stack gap="lg">
      <Text fz="sm" c="dimmed">
        {jitSec
          ? `Access to "${connectionName}" is gated on just-in-time review approval.`
          : `Specify how long you need access to "${connectionName}".`}
      </Text>

      {/* sky, not blue: `blue` is not defined in theme.js and falls back to
          Mantine's stock palette, outside the product's identity. */}
      {jitSec ? (
        <Alert color="sky" icon={<ShieldCheck size={16} />}>
          {`This resource role has a fixed just-in-time window of ${formatDurationSec(jitSec)}. A reviewer must approve the request before the credentials are issued.`}
        </Alert>
      ) : (
        <Stack gap="xs">
          <Select
            label="Access duration"
            placeholder="Select duration"
            data={ACCESS_DURATION_OPTIONS}
            value={duration}
            onChange={setDuration}
            allowDeselect={false}
          />
          <Text fz="sm" c="dimmed">
            Your access will automatically expire after this period
          </Text>
        </Stack>
      )}

      <Group justify="flex-end" gap="sm">
        <Button variant="default" onClick={onCancel} disabled={isRequesting}>
          Cancel
        </Button>
        <Button onClick={submit} loading={isRequesting}>
          {jitSec ? 'Request access' : 'Confirm and connect'}
        </Button>
      </Group>
    </Stack>
  )
}

/**
 * The duration step, as a dialog over the drawer.
 *
 * The design keeps the drawer open behind it — "so we don't lose any visual
 * clues and take advantage of already in memory state" — so this is mounted
 * once by the drawer rather than per row, and the row underneath keeps whatever
 * state it had. On success the store opens that row on the credentials.
 *
 * Only review-gated roles reach it; everything else connects in one click.
 */
export function RequestAccessModal() {
  const requestFor = useNativeAccessStore((s) => s.requestFor)
  const closeRequestDialog = useNativeAccessStore((s) => s.closeRequestDialog)
  const connection = useNativeAccessStore((s) =>
    requestFor ? s.connectionByName[requestFor] : null
  )

  return (
    <Modal
      opened={Boolean(requestFor)}
      onClose={closeRequestDialog}
      title="Ask access"
      size="md"
      zIndex={DRAWER_MODAL_Z_INDEX}
    >
      {/* Keyed so the duration resets between roles instead of carrying over. */}
      {requestFor && (
        <RequestAccessForm
          key={requestFor}
          connectionName={requestFor}
          connection={connection}
          onCancel={closeRequestDialog}
        />
      )}
    </Modal>
  )
}
