import { Fragment, useEffect } from 'react'
import { Box, Card, Divider, Group, Stack, Text } from '@mantine/core'
import { CircleDashed } from 'lucide-react'
import Badge from '@/components/Badge'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import { useMinDelay } from '@/hooks/useMinDelay'
import { useAgentStore } from '@/stores/useAgentStore'
import MobileHeader from '../components/MobileHeader'

function AgentStatusBadge({ status }) {
  const isOnline = status === 'CONNECTED'
  return (
    <Badge
      color={isOnline ? 'green' : 'red'}
      variant="light"
      leftSection={<CircleDashed size={10} />}
    >
      {isOnline ? 'Online' : 'Offline'}
    </Badge>
  )
}

function AgentRow({ agent }) {
  const hostname = agent.metadata?.hostname || agent.hostname
  const version = agent.metadata?.version || agent.version

  return (
    <Box p="md">
      <Group justify="space-between" wrap="nowrap" align="flex-start" gap="sm">
        <Stack gap={2} miw={0} flex={1}>
          <Text fw={600} size="sm" truncate="end">
            {agent.name}
          </Text>
          {hostname && (
            <Text size="xs" c="dimmed" truncate="end">
              {hostname}
            </Text>
          )}
          <Text size="xs" c="dimmed">
            {[agent.mode, version && `v${version}`].filter(Boolean).join(' · ')}
          </Text>
        </Stack>
        <AgentStatusBadge status={agent.status} />
      </Group>
    </Box>
  )
}

function MobileAgents() {
  const { agents, loading, error, fetchAgents } = useAgentStore()
  const showLoader = useMinDelay(loading, 500)

  useEffect(() => {
    fetchAgents()
  }, [fetchAgents])

  if (showLoader) return <PageLoader h={400} />
  if (error) return <Text c="red">{error}</Text>

  return (
    <Stack gap="md">
      <MobileHeader title="Agents" />
      {agents.length === 0 ? (
        <EmptyState
          title="No agents yet"
          description="Agents show up here once they are registered in your organization."
        />
      ) : (
        <Card padding={0} withBorder>
          {agents.map((agent, i) => (
            <Fragment key={agent.id}>
              {i > 0 && <Divider />}
              <AgentRow agent={agent} />
            </Fragment>
          ))}
        </Card>
      )}
    </Stack>
  )
}

export default MobileAgents
