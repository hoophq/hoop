import { useState } from 'react'
import { Stack, Text } from '@mantine/core'
import { ShieldCheck } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Select from '@/components/Select'
import { useNativeAccessStore, FLOW_STATUS } from '@/stores/useNativeAccessStore'
import { ACCESS_DURATION_OPTIONS, DEFAULT_ACCESS_DURATION_MINUTES } from './constants'
import { formatDurationSec } from './helpers'

/**
 * The configure-session step. Only review-required connections reach it —
 * everything else skips straight to a persistent credential.
 */
export function RequestAccessPanel({ connectionName, connection }) {
  const [duration, setDuration] = useState(DEFAULT_ACCESS_DURATION_MINUTES)
  const requestAccess = useNativeAccessStore((s) => s.requestAccess)
  const status = useNativeAccessStore((s) => s.statusByName[connectionName])
  const isRequesting = status === FLOW_STATUS.REQUESTING

  const jitSec = connection?.jit_access_duration_sec ?? null

  // Pass the JIT window through in seconds. The CLJS version divided by 60 and
  // the API multiplied it back, losing any window that was not a whole minute.
  const submit = () =>
    requestAccess(connectionName, jitSec ?? Number(duration) * 60)

  return (
    <Stack gap="lg">
      <Text fz="sm" c="dimmed">
        {jitSec
          ? 'This resource requires just-in-time review approval before access is granted.'
          : 'Specify how long you need access to this resource role.'}
      </Text>

      {jitSec ? (
        <Alert color="blue" icon={<ShieldCheck size={16} />}>
          {`This resource has just-in-time access review enabled. You can only request a window of ${formatDurationSec(jitSec)}. A reviewer must approve before you can connect.`}
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

      <Button onClick={submit} loading={isRequesting} disabled={isRequesting}>
        {jitSec ? 'Request Access' : 'Confirm and Connect'}
      </Button>
    </Stack>
  )
}
