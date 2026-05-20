import { Box, Card, Divider, Group, Stack, Text } from '@mantine/core'
import { useNavigate } from 'react-router-dom'
import Button from '@/components/Button'
import Code from '@/components/Code'
import StatusBadge from './StatusBadge'

function Meta({ label, children }) {
  return (
    <Group gap={4} align="center" wrap="nowrap">
      <Text size="xs" c="dimmed">{`${label}:`}</Text>
      {children}
    </Group>
  )
}

export default function SubscriptionCard({ sub }) {
  const navigate = useNavigate()
  const eventName = sub.eventTypes?.[0]
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
            variant="light"
            color="gray"
            size="xs"
            onClick={() => navigate(`/features/event-routing/${sub.id}`)}
          >
            Configure
          </Button>
        </Group>

        <Divider />

        <Group gap="md" wrap="wrap">
          <Meta label="Event">
            {eventName ? <Code bg="indigo.1" c="indigo.9">{eventName}</Code> : <Text size="xs" c="dimmed">—</Text>}
          </Meta>
          <Meta label="Role">
            <Text size="xs">{sub.connectionName || '—'}</Text>
          </Meta>
          <Meta label="Runbook">
            <Text size="xs" ff="monospace" truncate>{sub.runbookFile || '—'}</Text>
          </Meta>
        </Group>
      </Stack>
    </Card>
  )
}
