import { useState } from 'react'
import { Group, Paper, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { loadsFromConfigFile } from '../config'
import SidecarSourceModal from './SidecarSourceModal'

/**
 * Which side owns this sidecar's configuration, at the foot of its page.
 *
 * This was a switch in the details card's header until it earned its own
 * section. Nothing it does is irreversible — neither direction touches the
 * stored document — but one click retires every rule the control plane
 * distributes to this sidecar, and a running sidecar picks that up within a
 * minute. A control with that reach reads as a setting when it sits beside a
 * name and a timestamp, so it is here instead, where the page also keeps
 * Delete, and it says what it costs before it asks.
 *
 * The request, its error and the stored value stay in useSidecarStore: this
 * renders whatever the store holds, so a failed flip is already back where it
 * was by the time the dialog closes.
 */
export default function SidecarSourceSection({ sidecar }) {
  const setLoadFromDisk = useSidecarStore((s) => s.setLoadFromDisk)
  const fromConfigFile = loadsFromConfigFile(sidecar)
  const listeners = sidecar.configuration?.listeners ?? []

  // The value awaiting confirmation, and whether the dialog is up. Two states
  // rather than one: Mantine keeps the modal mounted through its exit
  // transition, and a target cleared on close would rewrite the copy of the
  // dialog the user is watching leave.
  const [target, setTarget] = useState(false)
  const [asking, setAsking] = useState(false)
  const [saving, setSaving] = useState(false)

  const confirm = async () => {
    if (!asking) return
    setSaving(true)
    try {
      await setLoadFromDisk(sidecar.id, target)
      setAsking(false)
    } catch (error) {
      setAsking(false)
      showSnackbar({
        level: 'error',
        text: 'Could not change the configuration source.',
        description: error.response?.data?.message ?? error.message,
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <Paper withBorder radius="md" p="lg">
        <Group justify="space-between" align="flex-start" gap="lg" wrap="nowrap">
          <Stack gap={4} flex={1} miw={0}>
            <Title order={4}>Configuration source</Title>
            <Text size="sm" c="dimmed">
              {fromConfigFile
                ? 'This sidecar reads its own config file. The control plane sends only its license, and the rules bound to its listeners are not delivered.'
                : "The control plane delivers this sidecar's whole configuration, and the rules bound to its listeners with it."}
            </Text>
          </Stack>
          <Button
            variant="default"
            flex="0 0 auto"
            onClick={() => {
              setTarget(!fromConfigFile)
              setAsking(true)
            }}
          >
            {fromConfigFile ? 'Hand it to the control plane' : 'Switch to the config file'}
          </Button>
        </Group>
      </Paper>

      <SidecarSourceModal
        opened={asking}
        toConfigFile={target}
        storedListeners={listeners.length}
        onClose={() => setAsking(false)}
        onConfirm={confirm}
        loading={saving}
      />
    </>
  )
}
