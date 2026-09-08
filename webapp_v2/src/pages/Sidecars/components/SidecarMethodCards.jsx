import { useNavigate } from 'react-router-dom'
import { Paper, SimpleGrid, Stack, Text, ThemeIcon } from '@mantine/core'
import { CirclePlus, PlugZap } from 'lucide-react'
import Button from '@/components/Button'

export const CONNECT_PATH = '/sidecars/connect'
export const CREATE_PATH = '/sidecars/new'

// The two ways into the fleet (Figma: "SideCars | Empty State" and the "Connect
// a Sidecar" modal). Both end in the same wizard; `mode` only changes its words.
const METHODS = [
  {
    id: 'connect',
    icon: PlugZap,
    title: 'Connect an existing Sidecar',
    description:
      'Link a sidecar that already runs in your infrastructure. You get a token to point it to this control plane.',
    action: 'Connect',
    path: CONNECT_PATH,
  },
  {
    id: 'create',
    icon: CirclePlus,
    title: 'Create and deploy a new Sidecar',
    description: 'Generate the config and deploy a new sidecar with Docker or Kubernetes. Takes about 5 minutes.',
    action: 'Create',
    path: CREATE_PATH,
  },
]

function MethodCard({ method, onPick }) {
  const Icon = method.icon
  return (
    <Paper bg="gray.0" radius="xl" p="md">
      <Stack gap="sm" align="flex-start">
        <ThemeIcon size={48} radius="lg" variant="white" color="dark">
          <Icon size={18} aria-hidden="true" />
        </ThemeIcon>
        <Stack gap={4}>
          <Text size="sm" fw={600}>
            {method.title}
          </Text>
          <Text size="xs" c="dimmed">
            {method.description}
          </Text>
        </Stack>
        <Button size="xs" onClick={() => onPick(method)}>
          {method.action}
        </Button>
      </Stack>
    </Paper>
  )
}

export default function SidecarMethodCards({ onPick }) {
  const navigate = useNavigate()
  const pick = (method) => {
    onPick?.(method)
    navigate(method.path)
  }
  return (
    <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="lg">
      {METHODS.map((method) => (
        <MethodCard key={method.id} method={method} onPick={pick} />
      ))}
    </SimpleGrid>
  )
}
