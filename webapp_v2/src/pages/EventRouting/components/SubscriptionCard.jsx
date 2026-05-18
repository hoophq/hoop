import { Box, Button, Card, Divider, Group, Stack, Text } from '@mantine/core'
import { ChevronRight } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import Code from '@/components/Code'
import StatusBadge from './StatusBadge'

function formatRelative(iso) {
  if (!iso) return null
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return null
  const diffMs = Date.now() - date.getTime()
  if (diffMs < 0) return iso.slice(0, 16).replace('T', ' ')
  const sec = Math.floor(diffMs / 1000)
  if (sec < 60) return `${sec}s ago`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min}m ago`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}h ago`
  const day = Math.floor(hr / 24)
  if (day < 30) return `${day}d ago`
  return iso.slice(0, 10)
}

function Meta({ label, children }) {
  return (
    <Group gap={4} align="center" wrap="nowrap">
      <Text size="xs" c="dimmed">{label}</Text>
      {children}
    </Group>
  )
}

export default function SubscriptionCard({ sub }) {
  const navigate = useNavigate()
  const eventName = sub.eventTypes?.[0]
  const lastDispatchedAt = sub.updatedAt // No dedicated field; updated_at is the closest proxy until dispatches are loaded.
  const lastSeen = formatRelative(lastDispatchedAt)
  const hasFailures = (sub.failedCount7d || 0) > 0

  return (
    <Card padding="md" withBorder>
      <Stack gap="sm">
        <Group justify="space-between" align="flex-start" wrap="nowrap" gap="md">
          <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
            <Group gap="sm" align="center" wrap="nowrap">
              <Text size="md" fw={600} truncate>{sub.name}</Text>
              <StatusBadge status={sub.status} />
              {hasFailures && (
                <Box
                  w={8}
                  h={8}
                  bg="red.6"
                  style={{ borderRadius: '50%' }}
                  aria-label="Has recent failures"
                />
              )}
            </Group>
            {sub.description && (
              <Text size="sm" c="dimmed" lineClamp={2}>{sub.description}</Text>
            )}
          </Stack>
          <Button
            variant="default"
            size="xs"
            rightSection={<ChevronRight size={14} />}
            onClick={() => navigate(`/features/event-routing/${sub.id}`)}
          >
            Configure
          </Button>
        </Group>

        <Divider />

        <Group gap="md" wrap="wrap">
          <Meta label="Event">
            {eventName ? <Code>{eventName}</Code> : <Text size="xs" c="dimmed">—</Text>}
          </Meta>
          <Meta label="Role">
            <Text size="xs">{sub.connectionName || sub.connectionId || '—'}</Text>
          </Meta>
          <Meta label="Runbook">
            <Text size="xs" ff="monospace" truncate>{sub.runbookFile || '—'}</Text>
          </Meta>
        </Group>

        <Group gap="md" wrap="wrap">
          {lastSeen && (
            <Text size="xs" c="dimmed">{`Last updated ${lastSeen}`}</Text>
          )}
          <Text size="xs" c="dimmed">
            {`${sub.deliveredCount7d || 0} delivered`}
          </Text>
          <Text size="xs" c={hasFailures ? 'red' : 'dimmed'}>
            {`${sub.failedCount7d || 0} failed`}
          </Text>
          <Text size="xs" c="dimmed">in the last 7 days</Text>
        </Group>
      </Stack>
    </Card>
  )
}
