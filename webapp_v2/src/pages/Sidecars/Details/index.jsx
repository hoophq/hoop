import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Stack, Text, Title } from '@mantine/core'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { sidecarsService } from '@/services/sidecars'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import SidecarDetails from '../components/SidecarDetails'
import { listenerPath, removeListener } from '../listeners'
import DeleteListenerModal from '../sections/DeleteListenerModal'
import { RESTART_NOTE, saveErrorMessage } from '../useListenerEditor'

// /sidecars/:id — the details card on its own page, and the listener controls.
export default function SidecarDetailsPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  // One record per id: `loading` is "the record on screen is not this id's".
  const [result, setResult] = useState({ id: null, sidecar: null, error: null })
  const loading = result.id !== id
  const showLoader = useMinDelay(loading, 500)

  const [deleting, setDeleting] = useState(null)
  const [deletingBusy, setDeletingBusy] = useState(false)
  const updateSidecar = useSidecarStore((s) => s.updateSidecar)

  useEffect(() => {
    let cancelled = false
    sidecarsService
      .get(id)
      .then((data) => {
        if (!cancelled) setResult({ id, sidecar: data, error: null })
      })
      .catch((err) => {
        if (!cancelled) {
          setResult({ id, sidecar: null, error: err.response?.status === 404 ? 'Sidecar not found.' : err.message })
        }
      })
    return () => {
      cancelled = true
    }
  }, [id])

  const { sidecar, error } = result

  // The PUT answers with the stored sidecar, so the page shows what the plane
  // now holds rather than what the form hoped it wrote.
  const onSaved = useCallback((updated) => setResult({ id, sidecar: updated, error: null }), [id])

  const confirmDelete = async () => {
    setDeletingBusy(true)
    const configuration = removeListener(sidecar.configuration, deleting.index)
    const { ok, sidecar: updated, error: err } = await updateSidecar(sidecar.id, configuration)
    setDeletingBusy(false)
    if (!ok) {
      showSnackbar({ level: 'error', text: 'Failed to delete the listener.', description: saveErrorMessage(err) })
      return
    }
    onSaved(updated)
    setDeleting(null)
    showSnackbar({
      level: 'success',
      text: `Listener "${deleting.listener.name}" deleted.`,
      description: RESTART_NOTE,
    })
  }

  // `loading` as well as `showLoader`: useMinDelay only raises its flag from a
  // timeout, so between two /sidecars/:id URLs there is one frame where the
  // loader is not up yet and `result` still holds the previous sidecar. The
  // delay is there to hold the loader on afterwards, not to let that frame
  // render the wrong record.
  if (loading || showLoader) return <PageLoader h={400} />

  const listeners = sidecar?.configuration?.listeners ?? []

  return (
    <Stack gap="xl">
      <DeleteListenerModal
        listener={deleting?.listener}
        lastOne={listeners.length === 1}
        opened={deleting !== null}
        onClose={() => setDeleting(null)}
        onConfirm={confirmDelete}
        loading={deletingBusy}
      />

      <Button
        variant="transparent"
        color="gray"
        leftSection={<ArrowLeft size={16} />}
        onClick={() => navigate('/sidecars')}
        px={0}
        w="fit-content"
      >
        Back
      </Button>

      {error ? (
        <Text c="red">{error}</Text>
      ) : (
        sidecar && (
          <>
            <Title order={1}>{sidecar.name}</Title>
            <SidecarDetails
              sidecar={sidecar}
              listenerActions={{
                onAdd: () => navigate(`/sidecars/${encodeURIComponent(sidecar.id)}/listeners/new`),
                onEdit: (index) => navigate(listenerPath(sidecar.id, listeners[index], index)),
                onDelete: (index) => setDeleting({ index, listener: listeners[index] }),
              }}
            />
          </>
        )
      )}
    </Stack>
  )
}
