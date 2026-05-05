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
import { apiKeysService } from '@/services/apiKeys'

function formatDate(dateStr) {
  if (!dateStr) return '—'
  return new Date(dateStr).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
  })
}

function truncateKey(key) {
  if (!key) return '—'
  return key.length > 12 ? `${key.slice(0, 10)}...` : key
}

export default function ApiKeys() {
  const navigate = useNavigate()
  const [keys, setKeys] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [confirmRevoke, setConfirmRevoke] = useState(null)

  const showLoader = useMinDelay(loading)

  async function fetchKeys() {
    try {
      const res = await apiKeysService.list()
      setKeys(res.data ?? [])
    } catch {
      setError('Failed to load API keys.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchKeys()
  }, [])

  async function handleRevoke(key) {
    try {
      await apiKeysService.revoke(key.id)
      notifications.show({ message: 'API key deactivated.', color: 'green' })
      fetchKeys()
    } catch {
      notifications.show({ message: 'Failed to deactivate key.', color: 'red' })
    } finally {
      setConfirmRevoke(null)
    }
  }

  async function handleActivate(key) {
    try {
      await apiKeysService.reactivate(key.id)
      notifications.show({ message: 'API key activated.', color: 'green' })
      fetchKeys()
    } catch {
      notifications.show({ message: 'Failed to activate key.', color: 'red' })
    }
  }

  if (showLoader) return <PageLoader />
  if (error) return <PageLoader error={error} />

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="xs">
          <Title order={1}>API Keys</Title>
          <Text c="dimmed" size="lg">
            Create and manage API Keys
          </Text>
        </Stack>
        <Button onClick={() => navigate('/settings/api-keys/new')}>Create new API key</Button>
      </Group>

      {keys.length === 0 ? (
        <EmptyState
          title="No API keys yet"
          description="Create an API key to authenticate programmatic access to Hoop."
          action={{ label: 'Create new API key', onClick: () => navigate('/settings/api-keys/new') }}
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
            {keys.map((key) => {
              const isRevoked = key.status === 'revoked'
              return (
                <Table.Tr key={key.id}>
                  <Table.Td>
                    {isRevoked ? (
                      <Tooltip label="Deactivated key" position="top">
                        <Group gap="xs" wrap="nowrap">
                          <Unplug size={14} color="var(--mantine-color-gray-7)" />
                          <Text size="sm" c="gray.7" ff="monospace">
                            {truncateKey(key.masked_key)}
                          </Text>
                        </Group>
                      </Tooltip>
                    ) : (
                      <Group gap="xs" wrap="nowrap">
                        <KeyRound size={14} color="var(--mantine-color-gray-8)" />
                        <Text size="sm" ff="monospace">
                          {truncateKey(key.masked_key)}
                        </Text>
                      </Group>
                    )}
                  </Table.Td>
                  <Table.Td>{key.name ?? '—'}</Table.Td>
                  <Table.Td>{formatDate(key.created_at)}</Table.Td>
                  <Table.Td>{key.created_by ?? '—'}</Table.Td>
                  <Table.Td>{formatDate(key.last_used_at)}</Table.Td>
                  <Table.Td>
                    <ActionMenu>
                      <ActionMenu.Item onClick={() => navigate(`/settings/api-keys/${key.id}/configure`)}>
                        Configure
                      </ActionMenu.Item>
                      {isRevoked ? (
                        <ActionMenu.Item onClick={() => handleActivate(key)}>Activate</ActionMenu.Item>
                      ) : (
                        <ActionMenu.Item danger onClick={() => setConfirmRevoke(key)}>
                          Deactivate
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
        title="Deactivate API key"
      >
        <Stack gap="md">
          <Text size="sm">
            Are you sure you want to deactivate &apos;<strong>{confirmRevoke?.name}</strong>&apos;? You can reactivate it later.
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
