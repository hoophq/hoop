import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Group, Paper, Stack, Text, Title } from '@mantine/core'
import { ArrowLeft } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { sidecarsService } from '@/services/sidecars'
import ListenerForm from '../components/ListenerForm'
import { RESTART_NOTE, useListenerEditor } from '../useListenerEditor'

// The form, once the sidecar it edits is on hand. Split out so the editor's
// state is seeded from a listener that exists, rather than from null on the
// first render and patched by an effect afterwards.
function Editor({ sidecar, index, onDone }) {
  const { form, setField, errors, saving, save, isNew } = useListenerEditor({ sidecar, index })

  const handleSave = async () => {
    if (await save()) onDone()
  }

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={1}>{isNew ? 'Add listener' : form.name || 'Edit listener'}</Title>
        <Text c="dimmed">{`One upstream of ${sidecar.name}, with its own protocol and bind address.`}</Text>
      </Stack>

      <Alert color="yellow" variant="light" radius="md">
        <Text size="sm">{`The sidecar keeps serving its current listeners until it restarts. ${RESTART_NOTE}`}</Text>
      </Alert>

      <Paper withBorder radius="md" p="lg">
        <ListenerForm form={form} setField={setField} errors={errors} />
      </Paper>

      <Group justify="flex-end">
        <Button variant="subtle" color="gray" onClick={onDone} disabled={saving}>
          Cancel
        </Button>
        <Button onClick={handleSave} loading={saving}>
          {isNew ? 'Add listener' : 'Save listener'}
        </Button>
      </Group>
    </Stack>
  )
}

/**
 * Shell B: the listener form on its own route.
 *
 * /sidecars/:id/listeners/new adds one, /sidecars/:id/listeners/:name edits
 * the one with that name. The URL keys on the name because that is what an
 * operator can read and share; the editor works on the position it resolves
 * to, so a rename stays one edit.
 */
export default function SidecarListenerPage() {
  const { id, name } = useParams()
  const navigate = useNavigate()
  const isNew = name === undefined
  const [result, setResult] = useState({ id: null, sidecar: null, error: null })
  const loading = result.id !== id

  // Refetched on entry rather than read from the fleet store: the document is
  // replaced whole on save, so editing a stale copy would write back whatever
  // it was missing.
  useEffect(() => {
    let cancelled = false
    sidecarsService
      .get(id)
      .then((sidecar) => {
        if (!cancelled) setResult({ id, sidecar, error: null })
      })
      .catch((err) => {
        if (!cancelled) {
          setResult({
            id,
            sidecar: null,
            error: err.response?.status === 404 ? 'Sidecar not found.' : err.message,
          })
        }
      })
    return () => {
      cancelled = true
    }
  }, [id])

  const back = () => navigate(`/sidecars/${encodeURIComponent(id)}`)

  if (loading) return <PageLoader h={400} />

  const { sidecar, error } = result
  const index = isNew ? null : (sidecar?.configuration?.listeners ?? []).findIndex((l) => l.name === name)

  return (
    <Stack gap="xl">
      <Button
        variant="transparent"
        color="gray"
        leftSection={<ArrowLeft size={16} />}
        onClick={back}
        px={0}
        w="fit-content"
      >
        Back
      </Button>

      {error && <Text c="red">{error}</Text>}

      {/* A named listener that is not in the document is an error, not an
          empty form: saving one would add a second listener under a name the
          operator thinks they are editing. */}
      {!error && !isNew && index === -1 && <Text c="red">{`No listener named "${name}" on this sidecar.`}</Text>}

      {sidecar && (isNew || index >= 0) && <Editor sidecar={sidecar} index={index} onDone={back} />}
    </Stack>
  )
}
