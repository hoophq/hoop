import { Card, Group, Stack, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { Send } from 'lucide-react'
import Button from '@/components/Button'
import Code from '@/components/Code'
import Modal from '@/components/Modal'
import { useEventRoutingStore } from '../store'
import DispatchBadge from './DispatchBadge'

export default function ReplayDispatchModal({ subId }) {
  const replayTarget = useEventRoutingStore((s) => s.replayTarget)
  const setReplayTarget = useEventRoutingStore((s) => s._setReplayTarget)
  const replayDispatch = useEventRoutingStore((s) => s.replayDispatch)

  const onClose = () => setReplayTarget(null)
  const onConfirm = async () => {
    if (!replayTarget || !subId) return
    try {
      await replayDispatch(subId, replayTarget.id)
      notifications.show({ message: 'Dispatch replayed.', color: 'green' })
    } catch (e) {
      notifications.show({ message: e?.response?.data?.message || 'Failed to replay.', color: 'red' })
    } finally {
      setReplayTarget(null)
    }
  }

  return (
    <Modal opened={!!replayTarget} onClose={onClose} title="Replay dispatch" size="sm">
      <Stack>
        <Text size="sm">
          The stored event payload will be re-dispatched. The replay creates a new audited
          session and goes through the same approval and masking guardrails.
        </Text>
        {replayTarget && (
          <Card padding="sm" withBorder>
            <Stack gap={4}>
              <Group gap="sm" align="center">
                <Code bg="indigo.1" c="indigo.9">{replayTarget.eventType}</Code>
                <DispatchBadge status={replayTarget.status} />
              </Group>
              <Text size="xs" c="dimmed" ff="monospace">
                {(replayTarget.dispatchedAt || replayTarget.createdAt || '').replace('T', ' ').slice(0, 19) || '—'}
              </Text>
            </Stack>
          </Card>
        )}
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose}>Cancel</Button>
          <Button leftSection={<Send size={14} />} onClick={onConfirm}>Replay now</Button>
        </Group>
      </Stack>
    </Modal>
  )
}
