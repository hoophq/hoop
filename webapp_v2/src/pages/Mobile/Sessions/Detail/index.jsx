import { useCallback, useEffect, useState } from 'react'
import { Card, Divider, Group, Stack, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import { OctagonX } from 'lucide-react'
import { useNavigate, useParams } from 'react-router-dom'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Code from '@/components/Code'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { sessionsService } from '@/services/sessions'
import { formatDateTime } from '@/utils/dates'
import MobileHeader from '../../components/MobileHeader'
import InfoRow from '../../components/InfoRow'
import { reviewStatusVariant, sessionStatusBadge } from '../../statusMaps'

function KillSessionModal({ opened, onClose, saving, onConfirm }) {
  return (
    <Modal opened={opened} onClose={onClose} title="Stop this session?" size="sm">
      <Stack>
        <Text size="sm">
          The running session will be terminated immediately. This cannot be undone.
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="subtle" color="gray" onClick={onClose}>
            Cancel
          </Button>
          <Button color="red" loading={saving} onClick={onConfirm}>
            Stop session
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

function MobileSessionDetail() {
  const { sessionId } = useParams()
  const navigate = useNavigate()
  const [session, setSession] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [saving, setSaving] = useState(false)
  const [opened, { open, close }] = useDisclosure(false)
  const showLoader = useMinDelay(loading, 500)

  const fetchSession = useCallback(async () => {
    try {
      const { data } = await sessionsService.get(sessionId)
      setSession(data)
    } catch {
      setError('Failed to load session.')
    } finally {
      setLoading(false)
    }
  }, [sessionId])

  useEffect(() => {
    fetchSession()
  }, [fetchSession])

  if (showLoader) return <PageLoader h={400} />

  const status = sessionStatusBadge(session?.status)
  const isRunning = session?.status === 'open'
  const review = session?.review

  async function handleKillConfirm() {
    setSaving(true)
    try {
      await sessionsService.kill(session.id)
      notifications.show({ message: 'Session stopped.', color: 'green' })
      close()
      await fetchSession()
    } catch (err) {
      notifications.show({
        message: err.response?.data?.message ?? 'Failed to stop session.',
        color: 'red',
      })
      close()
    } finally {
      setSaving(false)
    }
  }

  return (
    <Stack gap="md">
      <MobileHeader title="Session" backTo="/m/sessions" />

      {error && <Text c="red">{error}</Text>}

      {session && (
        <>
          <Card padding={0} withBorder>
            <InfoRow label="User" value={session.user_name || session.user} />
            <Divider />
            <InfoRow label="Email" value={session.user} />
            <Divider />
            <InfoRow label="Connection" value={session.connection} />
            <Divider />
            <InfoRow label="Type" value={[session.type, session.verb].filter(Boolean).join(' · ')} />
            <Divider />
            <InfoRow label="Status">
              <Badge variant={status.variant}>{status.label}</Badge>
            </InfoRow>
            <Divider />
            <InfoRow label="Started" value={formatDateTime(session.start_date)} />
            <Divider />
            <InfoRow label="Ended" value={session.end_date ? formatDateTime(session.end_date) : '—'} />
            {session.verb === 'exec' && session.exit_code != null && (
              <>
                <Divider />
                <InfoRow label="Exit code" value={String(session.exit_code)} />
              </>
            )}
            {review && (
              <>
                <Divider />
                <InfoRow label="Review">
                  <Badge variant={reviewStatusVariant(review.status)}>{review.status}</Badge>
                </InfoRow>
              </>
            )}
          </Card>

          {session.script?.data && (
            <Stack gap="xs">
              <Text size="sm" fw={600}>
                Input
              </Text>
              <Code block mah={260} w="100%">
                {session.script.data}
              </Code>
            </Stack>
          )}

          {review?.status === 'PENDING' && (
            <Button variant="light" onClick={() => navigate(`/m/reviews/${session.id}`)}>
              View pending review
            </Button>
          )}

          {isRunning && (
            <Button color="red" variant="light" leftSection={<OctagonX size={16} />} onClick={open}>
              Stop session
            </Button>
          )}

          <KillSessionModal
            opened={opened}
            onClose={close}
            saving={saving}
            onConfirm={handleKillConfirm}
          />
        </>
      )}
    </Stack>
  )
}

export default MobileSessionDetail
