import { Title, TextInput, Button, Stack, Text, Grid } from '@mantine/core'
import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { ArrowLeft, Info, ListOrdered } from 'lucide-react'
import { useAgentStore } from '@/stores/useAgentStore'
import MethodCard from '@/components/MethodCard'
import StepAccordion from '@/components/StepAccordion'
import { DeploymentMain } from './DeploymentInstructions'

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
    description: 'Setup a new Agent with a Helm chart.',
    iconDark: '/images/kubernetes-dark.svg',
    iconLight: '/images/kubernetes-light.svg',
  },
  {
    id: 'local',
    label: 'Local or VM',
    description: 'Setup a new Agent locally or on a VM.',
    iconDark: '/images/command-line-dark.svg',
    iconLight: '/images/command-line-light.svg',
  },
]

function AgentsCreate() {
  const navigate = useNavigate()
  const { createAgent, loading, agentKey, clearAgentKey } = useAgentStore()
  const [name, setName] = useState('')
  const [created, setCreated] = useState(false)
  const [activeAccordion, setActiveAccordion] = useState('step1')
  const [installMethod, setInstallMethod] = useState('docker')

  useEffect(() => {
    return () => clearAgentKey()
  }, [clearAgentKey])

  const handleCreate = async (e) => {
    e.preventDefault()
    if (!name.trim()) return
    try {
      await createAgent({ name })
      setCreated(true)
      setActiveAccordion('step2')
    } catch {
      // error handled by store
    }
  }

  const hoopKey = agentKey?.token ?? ''

  const step1Content = (
    <Grid gutter="xxl">
      <Grid.Col span={3}>
        <Text fw={500} size="md">Set an Agent name</Text>
        <Text size="sm" c="dimmed" mt={4}>
          This name is used to identify the Agent in your environment.
        </Text>
      </Grid.Col>
      <Grid.Col span={9}>
        <form onSubmit={handleCreate}>
          <Stack gap="lg" align="flex-start">
            <TextInput
              label="Name"
              placeholder="Enter the name of the Agent"
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
              required
              disabled={created}
              style={{ width: 320 }}
            />
            <Button
              type="submit"
              loading={loading}
              disabled={created || !name.trim()}
            >
              {created ? 'Agent created' : 'Create Agent'}
            </Button>
          </Stack>
        </form>
      </Grid.Col>
    </Grid>
  )

  const step2Content = (
    <Stack gap="xxl">
      <Grid gutter="xxl">
        <Grid.Col span={3}>
          <Stack gap="sm">
            <Title order={4}>Installation method</Title>
            <Text size="sm" c="dimmed">
              Select the type of environment to setup the agent in your infrastructure.
            </Text>
          </Stack>
        </Grid.Col>
        <Grid.Col span={9}>
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
        </Grid.Col>
      </Grid>

      {hoopKey && (
        <DeploymentMain installMethod={installMethod} hoopKey={hoopKey} />
      )}
    </Stack>
  )

  return (
    <Stack gap="xl">
      <Button
        variant="transparent"
        color="gray"
        leftSection={<ArrowLeft size={16} />}
        onClick={() => navigate('/agents')}
        px={0}
        w="fit-content"
      >
        Back
      </Button>

      <Stack gap="sm">
        <Title order={1}>Setup new Agent</Title>
        <Text size="md" c="dimmed">
          Follow the steps below to setup a new Agent in your environment
        </Text>
      </Stack>

      <StepAccordion
        value={activeAccordion}
        onChange={setActiveAccordion}
        items={[
          {
            value: 'step1',
            icon: Info,
            title: 'Agent information',
            subtitle: 'Define basic identification properties to create your new Agent.',
            done: created,
            disabled: false,
            content: step1Content,
          },
          {
            value: 'step2',
            icon: ListOrdered,
            title: 'Installation Method',
            subtitle: 'Get Agent deployment details for your preferred method.',
            disabled: !created,
            content: step2Content,
          },
        ]}
      />
    </Stack>
  )
}

export default AgentsCreate
