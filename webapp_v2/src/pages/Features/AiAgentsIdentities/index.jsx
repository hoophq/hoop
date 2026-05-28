import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Group, Stack, Text, Title, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { KeyRound, Unplug } from 'lucide-react'
import { useMinDelay } from '@/hooks/useMinDelay'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import Table from '@/components/Table'
import ActionMenu from '@/components/ActionMenu'
import Modal from '@/components/Modal'
import { aiAgentsService } from '@/services/aiAgents'
import { truncateKey } from '@/utils/maskedKey'

const LIST_PATH = '/features/ai-agents-identities'
const NEW_PATH = '/features/ai-agents-identities/new'

function formatDate(dateStr) {
  if (!dateStr) return '—'
  return new Date(dateStr).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
  })
}

export default function AiAgentsIdentities() {
  const navigate = useNavigate()
  const [agents, setAgents] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [confirmRevoke, setConfirmRevoke] = useState(null)

  const showLoader = useMinDelay(loading)

  async function fetchAgents() {
    try {
      const res = await aiAgentsService.list()
      setAgents(res.data ?? [])
    } catch {
      setError('Failed to load AI agents.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchAgents()
  }, [])

  async function handleRevoke(agent) {
    try {
      await aiAgentsService.revoke(agent.id)
      notifications.show({ message: 'AI Agent deactivated.', color: 'green' })
      fetchAgents()
    } catch {
      notifications.show({ message: 'Failed to deactivate AI Agent.', color: 'red' })
    } finally {
      setConfirmRevoke(null)
    }
  }

  async function handleReactivate(agent) {
    try {
      await aiAgentsService.reactivate(agent.id)
      notifications.show({ message: 'AI Agent reactivated.', color: 'green' })
      fetchAgents()
    } catch {
      notifications.show({ message: 'Failed to reactivate AI Agent.', color: 'red' })
    }
  }

  if (showLoader) return <PageLoader />
  if (error) return <PageLoader error={error} />

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="xs">
          <Title order={1}>AI Agents Identities</Title>
          <Text c="dimmed" size="lg">
            Create scoped, auditable identities for your AI agents.
          </Text>
        </Stack>
        <Button onClick={() => navigate(NEW_PATH)}>Create new AI Agent</Button>
      </Group>

      {agents.length === 0 ? (
        <EmptyState
          title="No AI agents yet"
          description="Create an AI Agent identity to give programmatic, auditable access to Hoop from your AI tools."
          action={{ label: 'Create new AI Agent', onClick: () => navigate(NEW_PATH) }}
        />
      ) : (
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Key</Table.Th>
              <Table.Th>Name</Table.Th>
              <Table.Th>Created at</Table.Th>
              <Table.Th>Created by</Table.Th>
              <Table.Th>Last used at</Table.Th>
              <Table.Th w={40} />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {agents.map((agent) => {
              const isRevoked = agent.status === 'revoked'
              return (
                <Table.Tr key={agent.id}>
                  <Table.Td>
                    {isRevoked ? (
                      <Tooltip label="Deactivated key" position="top">
                        <Group gap="xs" wrap="nowrap">
                          <Unplug size={14} color="var(--mantine-color-gray-7)" />
                          <Text size="sm" c="gray.7" ff="monospace">
                            {truncateKey(agent.masked_key)}
                          </Text>
                        </Group>
                      </Tooltip>
                    ) : (
                      <Group gap="xs" wrap="nowrap">
                        <KeyRound size={14} color="var(--mantine-color-gray-8)" />
                        <Text size="sm" ff="monospace">
                          {truncateKey(agent.masked_key)}
                        </Text>
                      </Group>
                    )}
                  </Table.Td>
                  <Table.Td>{agent.name ?? '—'}</Table.Td>
                  <Table.Td>{formatDate(agent.created_at)}</Table.Td>
                  <Table.Td>{agent.created_by ?? '—'}</Table.Td>
                  <Table.Td>{formatDate(agent.last_used_at)}</Table.Td>
                  <Table.Td>
                    <ActionMenu>
                      <ActionMenu.Item onClick={() => navigate(`${LIST_PATH}/${agent.id}/configure`)}>
                        Configure
                      </ActionMenu.Item>
                      {isRevoked ? (
                        <ActionMenu.Item onClick={() => handleReactivate(agent)}>
                          Reactivate AI Agent
                        </ActionMenu.Item>
                      ) : (
                        <ActionMenu.Item danger onClick={() => setConfirmRevoke(agent)}>
                          Deactivate AI Agent
                        </ActionMenu.Item>
                      )}
                    </ActionMenu>
                  </Table.Td>
                </Table.Tr>
              )
            })}
          </Table.Tbody>
        </Table>
      )}

      <Modal
        opened={Boolean(confirmRevoke)}
        onClose={() => setConfirmRevoke(null)}
        title="Deactivate AI Agent"
      >
        <Stack gap="md">
          <Text size="sm">
            {`Are you sure you want to deactivate '${confirmRevoke?.name ?? ''}'? You can reactivate it later.`}
          </Text>
          <Group justify="flex-end" gap="sm">
            <Button variant="subtle" color="gray" onClick={() => setConfirmRevoke(null)}>
              Cancel
            </Button>
            <Button color="red" onClick={() => handleRevoke(confirmRevoke)}>
              Deactivate
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}
