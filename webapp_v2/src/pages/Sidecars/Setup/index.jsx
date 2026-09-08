import { useCallback, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Anchor, Group, Stack, Text, Title } from '@mantine/core'
import { ArrowLeft, Info, SquareArrowOutUpRight } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Stepper from '@/components/Stepper'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { showSnackbar } from '@/utils/snackbar'
import SidecarDetails from '../components/SidecarDetails'
import { useConnectionsByName } from '../useConnectionsByName'
import NameStep from './sections/NameStep'
import WaitingStep from './sections/WaitingStep'

const LIST_PATH = '/sidecars'

// The two entries of SidecarMethodCards share this wizard; only the words
// differ (Figma: "Connect an existing Sidecar" and "Create and deploy a new
// Sidecar").
const COPY = {
  connect: {
    title: 'Connect an existing Sidecar',
    subtitle: 'Link a sidecar that already runs in your infrastructure. You get a token to point it to this control plane.',
    steps: ['Connect', 'Configure', 'Overview'],
  },
  create: {
    title: 'Create and deploy a new Sidecar',
    subtitle: 'Generate the config and deploy a new Sidecar with Docker or Kubernetes.',
    steps: ['Deploy', 'Configure', 'Overview'],
  },
}

/**
 * Three steps: name the sidecar and copy its token, wait for its handshake,
 * review what the control plane knows. The token lives in this component's
 * state and dies with it; a refresh restarts at step one.
 */
export default function SidecarSetup({ mode = 'connect' }) {
  const copy = COPY[mode] ?? COPY.connect
  const navigate = useNavigate()
  const apiUrl = useUserStore((s) => s.apiUrl)
  const { createSidecar, deleteSidecar } = useSidecarStore()
  const connectionsByName = useConnectionsByName()

  const [step, setStep] = useState(0)
  const [sidecar, setSidecar] = useState(null)
  const [token, setToken] = useState(null)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState(null)
  const [deleting, setDeleting] = useState(false)

  const handleCreate = async (name) => {
    setCreating(true)
    setCreateError(null)
    try {
      const { token: issued, ...created } = await createSidecar({ name })
      setSidecar(created)
      setToken(issued)
    } catch (err) {
      setCreateError(err.response?.data?.message || 'Failed to create the sidecar.')
    } finally {
      setCreating(false)
    }
  }

  // The fresh record carries version and last_seen_at; the overview shows both.
  const handleConnected = useCallback((fresh) => {
    setSidecar(fresh)
    setStep(2)
  }, [])

  const handleDelete = async () => {
    setDeleting(true)
    try {
      await deleteSidecar(sidecar.id)
      showSnackbar({ level: 'success', text: `Sidecar "${sidecar.name}" removed.` })
      navigate(LIST_PATH, { replace: true })
    } catch (err) {
      showSnackbar({ level: 'error', text: 'Failed to delete the sidecar.', description: err.response?.data?.message })
      setDeleting(false)
    }
  }

  const action = [
    { label: 'Continue', disabled: !sidecar, onClick: () => setStep(1) },
    { label: 'Continue without waiting', disabled: false, onClick: () => setStep(2) },
    { label: 'Finish', disabled: false, onClick: () => navigate(LIST_PATH) },
  ][step]

  return (
    <Stack gap="xl">
      <Button
        variant="transparent"
        color="gray"
        leftSection={<ArrowLeft size={16} />}
        onClick={() => navigate(LIST_PATH)}
        px={0}
        w="fit-content"
      >
        Back
      </Button>

      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Stack gap="md">
          <Stack gap="sm">
            <Title order={1}>{copy.title}</Title>
            <Text size="lg" c="dimmed">
              {copy.subtitle}
            </Text>
          </Stack>
          <Stepper active={step} w="fit-content">
            {copy.steps.map((label) => (
              <Stepper.Step key={label} label={label} />
            ))}
          </Stepper>
        </Stack>
        <Button onClick={action.onClick} disabled={action.disabled} flex="0 0 auto">
          {action.label}
        </Button>
      </Group>

      {mode === 'create' && step === 0 && (
        <Alert color="blue" variant="light" radius="md" icon={<Info size={16} />}>
          <Group justify="space-between" align="center" wrap="nowrap" gap="md">
            <Text size="sm">See how to deploy the sidecar in our docs. Then add these fields to your configuration file.</Text>
            <Anchor href={docsUrl.sidecar.readme} target="_blank" rel="noopener noreferrer" size="xs" fw={500}>
              <Group gap={4} wrap="nowrap">
                Go to Docs
                <SquareArrowOutUpRight size={14} aria-hidden="true" />
              </Group>
            </Anchor>
          </Group>
        </Alert>
      )}

      {step === 0 && (
        <NameStep
          mode={mode}
          sidecar={sidecar}
          token={token}
          controlPlaneUrl={apiUrl || window.location.origin}
          creating={creating}
          error={createError}
          onCreate={handleCreate}
        />
      )}

      {step === 1 && (
        <WaitingStep
          sidecar={sidecar}
          onConnected={handleConnected}
          onDelete={handleDelete}
          onKeep={() => navigate(LIST_PATH)}
          deleting={deleting}
        />
      )}

      {step === 2 && sidecar && <SidecarDetails sidecar={sidecar} connectionsByName={connectionsByName} />}
    </Stack>
  )
}
