import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Group, Paper, Stack, Text, Title } from '@mantine/core'
import { ArrowLeft } from 'lucide-react'
import ActionMenu from '@/components/ActionMenu'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import ListenerForm from '../components/ListenerForm'
import { listenerIndexByLabel, listenerLabel, removeListener } from '../listeners'
import DeleteListenerModal from '../sections/DeleteListenerModal'
import SaveProblems from '../sections/SaveProblems'
import { saveErrorMessage, useListenerEditor } from '../useListenerEditor'

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
      {`Back to ${name}`}
    </Button>
  )
}

// The form, once the sidecar it edits is on hand. Split out so the editor's
// state is seeded from a listener that exists, rather than from null on the
// first render and patched by an effect afterwards.
function Editor({ sidecar, index, onDone }) {
  const { form, setField, errors, refused, saving, save, isNew } = useListenerEditor({ sidecar, index })
  const updateSidecar = useSidecarStore((s) => s.updateSidecar)
  const [deleting, setDeleting] = useState(false)
  const [deletingBusy, setDeletingBusy] = useState(false)
  const listeners = sidecar.configuration?.listeners ?? []
  const label = isNew ? null : listenerLabel(listeners[index], index)

  const handleSave = async () => {
    if (await save()) onDone()
  }

  const confirmDelete = async () => {
    setDeletingBusy(true)
    const configuration = removeListener(sidecar.configuration, index)
    const { ok, error } = await updateSidecar(sidecar.id, configuration)
    setDeletingBusy(false)
    if (!ok) {
      showSnackbar({ level: 'error', text: 'Failed to delete the listener.', description: saveErrorMessage(error) })
      return
    }
    setDeleting(false)
    showSnackbar({ level: 'success', text: `Listener "${label}" deleted.` })
    onDone()
  }

  return (
    <>
      <DeleteListenerModal
        label={label}
        lastOne={listeners.length === 1}
        opened={deleting}
        onClose={() => setDeleting(false)}
        onConfirm={confirmDelete}
        loading={deletingBusy}
      />

      <Stack gap="xl">
        <Group justify="space-between" align="flex-start" wrap="nowrap">
          <Stack gap="xs">
            <Parent name={sidecar.name} onClick={onDone} />
            <Title order={1}>{isNew ? 'Add listener' : form.name || label}</Title>
          </Stack>
          <Group gap="sm" wrap="nowrap" flex="0 0 auto">
            {!isNew && (
              <ActionMenu width={200} disabled={saving}>
                <ActionMenu.Item danger onClick={() => setDeleting(true)}>
                  Delete listener
                </ActionMenu.Item>
              </ActionMenu>
            )}
            <Button variant="default" onClick={onDone} disabled={saving}>
              Cancel
            </Button>
            <Button onClick={handleSave} loading={saving}>
              {isNew ? 'Add listener' : 'Save'}
            </Button>
          </Group>
        </Group>

        <SaveProblems refused={refused} sidecarId={sidecar.id} listeners={sidecar.configuration?.listeners} />

        <Paper withBorder radius="md" p="lg">
          <ListenerForm form={form} setField={setField} errors={errors} />
        </Paper>
      </Stack>
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
  const sidecar = useSidecarStore((s) => s.selected)
  const selectedId = useSidecarStore((s) => s.selectedId)
  const selectedLoading = useSidecarStore((s) => s.selectedLoading)
  const error = useSidecarStore((s) => s.selectedError)
  const fetchSidecar = useSidecarStore((s) => s.fetchSidecar)
  const clearSelected = useSidecarStore((s) => s.clearSelected)

  // Through the store, not a request of this page's own: the store owns the
  // loading flag, the error and the cancellation, and hand-rolling them here
  // for a record it already holds is what webapp_v2/CLAUDE.md rules out.
  //
  // It still refetches on entry rather than trusting whatever is in the store:
  // the document is replaced whole on save, so editing a stale copy would
  // write back whatever it was missing. fetchSidecar always issues a request.
  const loading = selectedId !== id || selectedLoading

  useEffect(() => {
    fetchSidecar(id)
    return clearSelected
  }, [id, fetchSidecar, clearSelected])

  const back = () => navigate(`/sidecars/${encodeURIComponent(id)}`)

  if (loading) return <PageLoader h={400} />

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

  // Keyed by the route: a link to another listener must not keep this form.
  return <Editor key={name ?? 'new'} sidecar={sidecar} index={index} onDone={back} />
}
