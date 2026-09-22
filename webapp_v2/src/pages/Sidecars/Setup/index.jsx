import { useCallback, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Group, Stack, Text, Title } from '@mantine/core'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import Stepper from '@/components/Stepper'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'
import SidecarDetails from '../components/SidecarDetails'
import NameStep, { SOURCE_CONFIG_FILE, SOURCE_CONTROL_PLANE } from './sections/NameStep'
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
    subtitle: 'Deploy a new sidecar with Docker or Kubernetes, then point it at this control plane.',
    steps: ['Deploy', 'Configure', 'Overview'],
  },
}

/**
 * Three steps: name the sidecar and work through the blocks that connect it,
 * wait for its first check-in, then review what it is configured to run. The
 * token lives in this component's state and dies with it; a refresh restarts
 * at step one.
 */
export default function SidecarSetup({ mode = 'connect' }) {
  const copy = COPY[mode] ?? COPY.connect
  const navigate = useNavigate()
  const apiUrl = useUserStore((s) => s.apiUrl)
  const { createSidecar, deleteSidecar, setLoadFromDisk } = useSidecarStore()

  const [step, setStep] = useState(0)
  const [sidecar, setSidecar] = useState(null)
  const [token, setToken] = useState(null)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState(null)
  const [deleting, setDeleting] = useState(false)
  // True while step 1 is polling. The header button spins on it, so the wizard
  // never offers an action whose outcome is still unknown.
  const [waiting, setWaiting] = useState(false)
  // Which side will own the configuration. The control plane is the default
  // because a sidecar created here is one an admin came to manage from here;
  // the other choice is a real one and it is offered, not hidden.
  const [source, setSource] = useState(SOURCE_CONTROL_PLANE)
  const [sourceSaving, setSourceSaving] = useState(false)

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

  /**
   * Record the choice on the sidecar, not just in this component.
   *
   * The flip is a PATCH that merges one key, so it cannot clobber a
   * configuration the sidecar imported meanwhile. It is applied as soon as it
   * is picked rather than on Finish: the sidecar may hand shake at any moment
   * between the two, and it must not be told the plane owns its configuration
   * when the operator has already said otherwise.
   *
   * A failure leaves the cards on the stored value, which is what `sidecar`
   * carries — the same bargain the details page makes.
   */
  const handleSource = async (next) => {
    // The cards render only after the sidecar exists — NameStep gates the whole
    // block on `created` — so there is always a row to write to. Moving the
    // selection without a row to write it to would leave the choice on screen
    // and nowhere else, which is the one outcome this control cannot have.
    if (!sidecar || next === source || sourceSaving) return
    const previous = source
    setSource(next)
    setSourceSaving(true)
    try {
      const updated = await setLoadFromDisk(sidecar.id, next === SOURCE_CONFIG_FILE)
      setSidecar(updated)
    } catch (err) {
      setSource(previous)
      showSnackbar({
        level: 'error',
        text: 'Could not set the configuration source.',
        description: err.response?.data?.message ?? err.message,
      })
    } finally {
      setSourceSaving(false)
    }
  }

  // The fresh record carries version and last_seen_at; the overview shows both.
  const handleConnected = useCallback((fresh) => {
    setWaiting(false)
    setSidecar(fresh)
    setStep(2)
  }, [])

  // Deleted from somewhere else while this page was open: the banner says so,
  // and the button stops pretending to wait for it.
  const handleGone = useCallback(() => setWaiting(false), [])

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

  // Step 1 has no forward action of its own: the check-in decides, and
  // handleConnected moves on. Cancel, in the banner, is the way out.
  const action = [
    {
      label: 'Continue',
      disabled: !sidecar,
      onClick: () => {
        setWaiting(true)
        setStep(1)
      },
    },
    // Never actionable: while polling it spins, and once the sidecar is gone
    // there is nothing to continue to. Back and Cancel are the ways out.
    { label: 'Continue', loading: waiting, disabled: true },
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
        <Button onClick={action.onClick} disabled={action.disabled} loading={action.loading} flex="0 0 auto">
          {action.label}
        </Button>
      </Group>

      {/* The waiting banner sits above the blocks, which stay mounted through
          step 1. The token exists nowhere else — it is shown once and the
          store never keeps it — so the waiting state must not cover it. */}
      {step === 1 && (
        <WaitingStep
          sidecar={sidecar}
          onConnected={handleConnected}
          onGone={handleGone}
          onDelete={handleDelete}
          onKeep={() => navigate(LIST_PATH)}
          deleting={deleting}
        />
      )}

      {step <= 1 && (
        <NameStep
          mode={mode}
          sidecar={sidecar}
          token={token}
          controlPlaneUrl={apiUrl || window.location.origin}
          creating={creating}
          error={createError}
          onCreate={handleCreate}
          source={source}
          onSourceChange={handleSource}
          sourceSaving={sourceSaving}
        />
      )}

      {step === 2 && sidecar && <SidecarDetails sidecar={sidecar} />}
    </Stack>
  )
}
