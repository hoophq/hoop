import {
  Title,
  Button,
  Group,
  Text,
  Badge,
  Stack,
  Modal,
  Card,
  Box,
  Flex,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Trash2, Zap, CircleDashed } from 'lucide-react'
import { useAgentStore } from '@/stores/useAgentStore'
import { useUserStore } from '@/stores/useUserStore'
import EmptyState from '@/layout/EmptyState'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import { docsUrl } from '@/utils/docsUrl'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'

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

function DeleteAgentModal({ agent, opened, onClose, onConfirm }) {
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Delete agent?"
      centered
      size="sm"
    >
      <Stack>
        <Stack gap={4}>
          <Text size="sm">
            This action will instantly remove the agent{' '}
            <Text component="span" fw={600}>{agent?.name}</Text>{' '}
            and cannot be undone.
          </Text>
          <Text size="sm">Are you sure you want to delete this agent?</Text>
        </Stack>
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose}>
            Cancel
          </Button>
          <Button color="red" onClick={onConfirm}>
            Confirm and delete
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

function AgentRow({ agent, isAdmin, isLast, onDelete }) {
  return (
    <Box
      p="lg"
      style={isLast ? undefined : { borderBottom: '1px solid var(--mantine-color-default-border)' }}
    >
      <Flex align="center">
        <Box style={{ flex: 1 }}>
          <Text size="lg" fw={700}>{agent.name}</Text>
          <Text size="xs" c="dimmed">Version: {agent.version}</Text>
          <Text size="xs" c="dimmed">ID: {agent.id}</Text>
        </Box>
        <Flex align="center" gap="md">
          <AgentStatusBadge status={agent.status} />
          {isAdmin && (
            <Button
              size="xs"
              variant="light"
              color="red"
              leftSection={<Trash2 size={14} />}
              onClick={() => onDelete(agent)}
            >
              Delete
            </Button>
          )}
        </Flex>
      </Flex>
    </Box>
  )
}

function Agents() {
  const navigate = useNavigate()
  const { agents, loading, error, fetchAgents, deleteAgent } = useAgentStore()
  const { user } = useUserStore()
  const [opened, { open, close }] = useDisclosure(false)
  const [selectedAgent, setSelectedAgent] = useState(null)

  const isAdmin = user?.is_admin
  const showLoader = useMinDelay(loading, 500)

  useEffect(() => {
    fetchAgents()
  }, [fetchAgents])

  const handleDeleteClick = (agent) => {
    setSelectedAgent(agent)
    open()
  }

  const handleDeleteConfirm = async () => {
    try {
      await deleteAgent(selectedAgent.id)
      notifications.show({ message: `Agent "${selectedAgent.name}" removed.`, color: 'green' })
    } catch {
      notifications.show({ message: 'Failed to delete agent.', color: 'red' })
    } finally {
      close()
      setSelectedAgent(null)
    }
  }

  if (showLoader) {
    return <PageLoader h={400} />
  }

  if (error) {
    return <Text c="red">{error}</Text>
  }

  return (
    <>
      <DeleteAgentModal
        agent={selectedAgent}
        opened={opened}
        onClose={close}
        onConfirm={handleDeleteConfirm}
      />

      <Stack gap="xxl">
        <Group justify="space-between" align="flex-start">
          <Stack gap="sm">
            <Title order={1}>Agents</Title>
            <Text size="md" c="dimmed">
              View and manage your Agents for your resource roles
            </Text>
            <DocsBtnCallOut text="Learn more about Agents" href={docsUrl.setup.agents} />
          </Stack>
          {isAdmin && agents.length > 0 && (
            <Button onClick={() => navigate('/agents/new')}>
              Setup new Agent
            </Button>
          )}
        </Group>

        {agents.length === 0 ? (
          <EmptyState
            icon={Zap}
            title="No agents yet"
            description="Create a new Agent to your Organization to show it here"
            action={
              isAdmin
                ? { label: 'Setup new Agent', onClick: () => navigate('/agents/new') }
                : null
            }
          />
        ) : (
          <Card padding={0} withBorder>
            {agents.map((agent, i) => (
              <AgentRow
                key={agent.id}
                agent={agent}
                isAdmin={isAdmin}
                isLast={i === agents.length - 1}
                onDelete={handleDeleteClick}
              />
            ))}
          </Card>
        )}
      </Stack>
    </>
  )
}

export default Agents
