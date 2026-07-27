import { useEffect } from 'react'
import { Stack, Title, Text } from '@mantine/core'
import Select from '@/components/Select'
import { useAgentStore } from '@/stores/useAgentStore'
import { useConfigureRoleStore } from '@/pages/Roles/Configure/store'

// Lets the admin re-assign which agent runs this connection. The agent
// list is shared with /agents and lives in useAgentStore so we just
// trigger a fetch on mount and read the cached state. Every bespoke
// renderer ends with this section — keeps the agent picker in a
// consistent position across the credentials tab.
export default function AgentSelector() {
  const agents = useAgentStore((s) => s.agents)
  const fetchAgents = useAgentStore((s) => s.fetchAgents)
  const agentId = useConfigureRoleStore((s) => s.drafts.agent_id)
  const setDraft = useConfigureRoleStore((s) => s.setDraft)

  useEffect(() => {
    if (!agents || agents.length === 0) fetchAgents()
  }, [agents, fetchAgents])

  const data = (agents || []).map((a) => ({ value: a.id, label: a.name }))

  return (
    <Stack gap="xs">
      <Title order={5} fw={500}>Agent</Title>
      <Text size="sm" c="dimmed">
        Which agent runs this connection.
      </Text>
      <Select
        placeholder="Select an agent"
        data={data}
        value={agentId || null}
        onChange={(value) => setDraft({ agent_id: value || '' })}
        nothingFoundMessage="No agents available. Set one up in Agents."
        searchable
      />
    </Stack>
  )
}
