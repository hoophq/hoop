import {
  Title,
  Button,
  Group,
  Table,
  Loader,
  Text,
  Badge,
  ActionIcon,
  Stack,
  Anchor,
  Center,
  Modal,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Trash2, Zap } from 'lucide-react'
import { useAgentStore } from '@/stores/useAgentStore'
import { useUserStore } from '@/stores/useUserStore'
import EmptyState from '@/components/EmptyState'

const AGENTS_DOCS_URL = 'https://hoop.dev/docs/setup/agents'

function AgentStatusBadge({ status }) {
  const isOnline = status === 'CONNECTED'
  return (
    <Badge color={isOnline ? 'green' : 'red'} variant="light">
      {isOnline ? 'Online' : 'Offline'}
    </Badge>
  )
}

function DeleteAgentModal({ agent, opened, onClose, onConfirm }) {
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Delete Agent"
      centered
      size="sm"
    >
      <Stack>
        <Text size="sm">
          This action will instantly remove the agent{' '}
          <Text component="span" fw={600}>{agent?.name}</Text>{' '}
          and cannot be undone.
        </Text>
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

function Agents() {
  const navigate = useNavigate()
  const { agents, loading, error, fetchAgents, deleteAgent } = useAgentStore()
  const { user } = useUserStore()
  const [opened, { open, close }] = useDisclosure(false)
  const [selectedAgent, setSelectedAgent] = useState(null)

  const isAdmin = user?.is_admin

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

  if (loading) {
    return (
      <Center h={200}>
        <Loader />
      </Center>
    )
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

      <Stack gap="lg">
        <Group justify="space-between" align="flex-start">
          <Stack gap={4}>
            <Title order={2}>Agents</Title>
            <Text size="sm" c="dimmed">
              View and manage your Agents for your resource roles.{' '}
              <Anchor href={AGENTS_DOCS_URL} target="_blank" size="sm">
                Learn more
              </Anchor>
            </Text>
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
          <Table highlightOnHover>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Name</Table.Th>
                <Table.Th>Version</Table.Th>
                <Table.Th>ID</Table.Th>
                <Table.Th>Status</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {agents.map((agent) => (
                <Table.Tr key={agent.id}>
                  <Table.Td fw={500}>{agent.name}</Table.Td>
                  <Table.Td>
                    <Text size="xs" c="dimmed">{agent.version}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="xs" c="dimmed">{agent.id}</Text>
                  </Table.Td>
                  <Table.Td>
                    <AgentStatusBadge status={agent.status} />
                  </Table.Td>
                  <Table.Td>
                    {isAdmin && (
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        onClick={() => handleDeleteClick(agent)}
                      >
                        <Trash2 size={16} />
                      </ActionIcon>
                    )}
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Stack>
    </>
  )
}

export default Agents
