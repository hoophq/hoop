import {
  Title,
  TextInput,
  Button,
  Stack,
  Accordion,
  Group,
  Text,
  Card,
  SimpleGrid,
  ThemeIcon,
  Anchor,
} from '@mantine/core'
import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Check, Container, Cloud, Monitor, ArrowLeft } from 'lucide-react'
import { useAgentStore } from '@/stores/useAgentStore'
import { DockerDeployment, KubernetesDeployment, LocalDeployment } from './DeploymentInstructions'

const AGENTS_DOCS_URL = 'https://hoop.dev/docs/setup/agents'

const INSTALL_METHODS = [
  {
    id: 'docker',
    label: 'Docker Hub',
    description: 'Setup a new Agent with a Docker image.',
    icon: Container,
  },
  {
    id: 'kubernetes',
    label: 'Kubernetes',
    description: 'Setup a new Agent with a Helm chart.',
    icon: Cloud,
  },
  {
    id: 'local',
    label: 'Local or VM',
    description: 'Setup a new Agent locally or on a VM.',
    icon: Monitor,
  },
]

function MethodCard({ method, selected, onSelect }) {
  const Icon = method.icon
  return (
    <Card
      withBorder
      padding="md"
      style={{ cursor: 'pointer', borderColor: selected ? 'var(--mantine-color-indigo-8)' : undefined }}
      onClick={() => onSelect(method.id)}
    >
      <Group gap="sm">
        <ThemeIcon variant={selected ? 'filled' : 'light'} size="lg">
          <Icon size={18} />
        </ThemeIcon>
        <Stack gap={2}>
          <Text size="sm" fw={500}>{method.label}</Text>
          <Text size="xs" c="dimmed">{method.description}</Text>
        </Stack>
      </Group>
    </Card>
  )
}

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

  return (
    <Stack gap="lg" maw={680}>
      <Group gap="xs">
        <Button
          variant="subtle"
          color="gray"
          leftSection={<ArrowLeft size={16} />}
          onClick={() => navigate('/agents')}
          px="xs"
        >
          Back
        </Button>
      </Group>

      <Stack gap={4}>
        <Title order={2}>Setup new Agent</Title>
        <Text size="sm" c="dimmed">
          Configure a new Agent for your organization.{' '}
          <Anchor href={AGENTS_DOCS_URL} target="_blank" size="sm">
            Learn more
          </Anchor>
        </Text>
      </Stack>

      <Accordion value={activeAccordion} onChange={setActiveAccordion} variant="separated">
        {/* Step 1 */}
        <Accordion.Item value="step1">
          <Accordion.Control>
            <Group gap="xs">
              {created && (
                <ThemeIcon size="sm" color="green" variant="light" radius="xl">
                  <Check size={12} />
                </ThemeIcon>
              )}
              <Text fw={500}>Step 1 — Agent Information</Text>
            </Group>
          </Accordion.Control>
          <Accordion.Panel>
            <form onSubmit={handleCreate}>
              <Stack gap="md" maw={400}>
                <TextInput
                  label="Agent Name"
                  placeholder="Enter agent name"
                  value={name}
                  onChange={(e) => setName(e.currentTarget.value)}
                  required
                  disabled={created}
                />
                <Button
                  type="submit"
                  loading={loading}
                  disabled={created || !name.trim()}
                  leftSection={created ? <Check size={16} /> : null}
                >
                  {created ? 'Agent created' : 'Create Agent'}
                </Button>
              </Stack>
            </form>
          </Accordion.Panel>
        </Accordion.Item>

        {/* Step 2 */}
        <Accordion.Item value="step2">
          <Accordion.Control disabled={!created}>
            <Text fw={500}>Step 2 — Installation Method</Text>
          </Accordion.Control>
          <Accordion.Panel>
            <Stack gap="lg">
              <SimpleGrid cols={3} spacing="sm">
                {INSTALL_METHODS.map((method) => (
                  <MethodCard
                    key={method.id}
                    method={method}
                    selected={installMethod === method.id}
                    onSelect={setInstallMethod}
                  />
                ))}
              </SimpleGrid>

              {hoopKey && (
                <>
                  {installMethod === 'docker' && <DockerDeployment hoopKey={hoopKey} />}
                  {installMethod === 'kubernetes' && <KubernetesDeployment hoopKey={hoopKey} />}
                  {installMethod === 'local' && <LocalDeployment hoopKey={hoopKey} />}
                </>
              )}
            </Stack>
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>
    </Stack>
  )
}

export default AgentsCreate
