import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Paper, Stack, Text, Title } from '@mantine/core'
import { ArrowLeft } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import FormFooter, { FORM_FOOTER_CLEARANCE } from '@/components/FormFooter'
import PageLoader from '@/components/PageLoader'
import { sidecarsService } from '@/services/sidecars'
import ListenerForm from '../components/ListenerForm'
import { listenerIndexByLabel, listenerLabel } from '../listeners'
import { RESTART_NOTE, useListenerEditor } from '../useListenerEditor'

// The sidecar this listener belongs to, above its own name. There is no
// Breadcrumbs component in the app and one consumer does not earn one; this is
// the shape pages/Rulepacks/Detail uses, as a Button rather than a Group with
// an onClick so it is reachable from the keyboard.
function Parent({ name, onClick }) {
  return (
    <Button
      variant="transparent"
      color="gray"
      leftSection={<ArrowLeft size={16} />}
      onClick={onClick}
      px={0}
      w="fit-content"
      size="compact-sm"
    >
      {name}
    </Button>
  )
}

// The form, once the sidecar it edits is on hand. Split out so the editor's
// state is seeded from a listener that exists, rather than from null on the
// first render and patched by an effect afterwards.
function Editor({ sidecar, index, onDone }) {
  const { form, setField, errors, saving, save, isNew } = useListenerEditor({ sidecar, index })

  const handleSave = async () => {
    if (await save()) onDone()
  }

  return (
    <>
      <Stack gap="xl" pb={FORM_FOOTER_CLEARANCE}>
        <Stack gap="xs">
          <Parent name={sidecar.name} onClick={onDone} />
          <Title order={1}>{isNew ? 'Add listener' : form.name || listenerLabel(null, index)}</Title>
          <Text c="dimmed">
            {isNew
              ? `A new lane on ${sidecar.name}: one upstream, one protocol, its own bind address.`
              : `Listener on ${sidecar.name}.`}
          </Text>
        </Stack>

        <Alert color="yellow" variant="light" radius="md">
          <Text size="sm">{`The sidecar keeps serving its current listeners until it restarts. ${RESTART_NOTE}`}</Text>
        </Alert>

        <Paper withBorder radius="md" p="lg">
          <ListenerForm form={form} setField={setField} errors={errors} />
        </Paper>
      </Stack>

      {/* Pinned, because the form runs past the fold as soon as Advanced is
          open and the page header already carries the search. */}
      <FormFooter>
        <Button variant="subtle" color="gray" onClick={onDone} disabled={saving}>
          Cancel
        </Button>
        <Button onClick={handleSave} loading={saving}>
          {isNew ? 'Add listener' : 'Save listener'}
        </Button>
      </FormFooter>
    </>
  )
}

/**
 * One listener of one sidecar, on its own route.
 *
 * /sidecars/:id/listeners/new adds one; /sidecars/:id/listeners/:name edits the
 * one that label resolves to. The editor then works on the position, so a
 * rename is one edit rather than a delete and an insert.
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
  const index = isNew ? null : listenerIndexByLabel(sidecar?.configuration?.listeners, name)

  if (error) {
    return (
      <Stack gap="xl">
        <Parent name="Sidecars" onClick={() => navigate('/sidecars')} />
        <Text c="red">{error}</Text>
      </Stack>
    )
  }

  // A label that is not in the document is an error, not an empty form: saving
  // one would add a second listener under a name the operator thinks they are
  // editing.
  if (!isNew && index === -1) {
    return (
      <Stack gap="xl">
        <Parent name={sidecar.name} onClick={back} />
        <Text c="red">{`No listener named "${name}" on this sidecar.`}</Text>
      </Stack>
    )
  }

  return <Editor sidecar={sidecar} index={index} onDone={back} />
}
