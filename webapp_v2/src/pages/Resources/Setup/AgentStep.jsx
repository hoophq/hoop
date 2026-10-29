import { useEffect } from 'react'
import { Box, Stack } from '@mantine/core'
import { Plus } from 'lucide-react'
import Button from '@/components/Button'
import MethodCard from '@/components/MethodCard'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import { docsUrl } from '@/utils/docsUrl'
import { DeploymentMain } from '@/pages/Agents/Create/DeploymentInstructions'
import StepHeader from './components/StepHeader'
import { useResourceSetupStore } from './store'

const INSTALL_METHODS = [
  {
    id: 'docker',
    label: 'Docker Hub',
    description: 'Setup a new Agent with a Docker image.',
    iconDark: '/images/docker-dark.svg',
    iconLight: '/images/docker-light.svg',
  },
  {
    id: 'kubernetes',
    label: 'Kubernetes',
    description: 'Setup a new Agent with Helm package manager.',
    iconDark: '/images/kubernetes-dark.svg',
    iconLight: '/images/kubernetes-light.svg',
  },
]

function NewAgent() {
  const newAgent = useResourceSetupStore((s) => s.newAgent)
  const installMethod = useResourceSetupStore((s) => s.installMethod)
  const setNewAgentName = useResourceSetupStore((s) => s.setNewAgentName)
  const setInstallMethod = useResourceSetupStore((s) => s.setInstallMethod)
  const createAgent = useResourceSetupStore((s) => s.createAgent)
  const created = newAgent.status === 'creating' || newAgent.status === 'ready'

  return (
    <Stack gap="xxlAlt">
      <SectionRow
        title="Set an Agent name"
        description="This name is used to identify your Agent in your environment."
      >
        <Box
          component="form"
          onSubmit={(event) => {
            event.preventDefault()
            createAgent()
          }}
        >
          <Stack gap="md" align="flex-start">
            <TextInput
              label="Name"
              placeholder="e.g. mycompany-agent"
              value={newAgent.name}
              required
              disabled={created}
              w="100%"
              onChange={(e) => setNewAgentName(e.currentTarget.value)}
            />
            {!created && <Button type="submit">Create Agent</Button>}
          </Stack>
        </Box>
      </SectionRow>

      {created && (
        <SectionRow
          title="Installation method"
          description="Select the type of environment to setup the Agent in your service."
        >
          <Stack gap="sm">
            {INSTALL_METHODS.map((method) => (
              <MethodCard
                key={method.id}
                method={method}
                selected={installMethod === method.id}
                onSelect={setInstallMethod}
              />
            ))}
          </Stack>
        </SectionRow>
      )}

      {newAgent.status === 'ready' && newAgent.token && (
        <DeploymentMain installMethod={installMethod} hoopKey={newAgent.token} />
      )}
    </Stack>
  )
}

export default function AgentStep() {
  const agents = useResourceSetupStore((s) => s.agents)
  const agentsLoading = useResourceSetupStore((s) => s.agentsLoading)
  const agentId = useResourceSetupStore((s) => s.agentId)
  const agentMode = useResourceSetupStore((s) => s.agentMode)
  const loadAgents = useResourceSetupStore((s) => s.loadAgents)
  const selectAgent = useResourceSetupStore((s) => s.selectAgent)
  const setAgentMode = useResourceSetupStore((s) => s.setAgentMode)
  const creating = agentMode === 'create'

  useEffect(() => {
    loadAgents()
  }, [loadAgents])

  return (
    <Stack gap="xxlAlt" pt="xl">
      <StepHeader
        title="Setup your Organization Agents"
        description="The Agent serves as the component linking your private infrastructure to Hoop's services. It functions as a proxy, connecting to a central gateway and exposing services within its network scope. Select or create one to get started."
        docs={{ href: docsUrl.concepts.agents, topic: 'Agents' }}
      />

      <SectionRow
        title="Select an Agent"
        description="The Agent that reaches this resource from your network."
      >
        <Stack gap="md" align="flex-start">
          <Select
            label="Agent Name"
            placeholder={agentsLoading ? 'Loading agents...' : 'Select an Agent'}
            data={agents.map((agent) => ({ value: agent.id, label: agent.name }))}
            value={agentId}
            onChange={(value) => value && selectAgent(value)}
            allowDeselect={false}
            required
            disabled={creating}
            w="100%"
          />
          {!creating && (
            <Button variant="subtle" leftSection={<Plus size={16} />} onClick={() => setAgentMode('create')}>
              Create new Agent
            </Button>
          )}
        </Stack>
      </SectionRow>

      {creating && <NewAgent />}
    </Stack>
  )
}
