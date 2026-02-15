import { Title, Button, Group, Table, Loader, Text } from '@mantine/core'
import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAgentStore } from '@/stores/useAgentStore'

function Agents() {
  const navigate = useNavigate()
  const { agents, loading, error, fetchAgents } = useAgentStore()

  useEffect(() => {
    fetchAgents()
  }, [fetchAgents])

  if (loading) return <Loader />
  if (error) return <Text c="red">{error}</Text>

  return (
    <>
      <Group justify="space-between" mb="lg">
        <Title order={2}>Agents</Title>
        <Button onClick={() => navigate('/agents/new')}>New Agent</Button>
      </Group>

      <Table striped highlightOnHover>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Name</Table.Th>
            <Table.Th>Status</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {agents.map((agent) => (
            <Table.Tr key={agent.id}>
              <Table.Td>{agent.name}</Table.Td>
              <Table.Td>{agent.status}</Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </>
  )
}

export default Agents
