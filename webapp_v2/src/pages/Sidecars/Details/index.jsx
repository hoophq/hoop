import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Stack, Text, Title } from '@mantine/core'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import SidecarDetails from '../components/SidecarDetails'
import { listenerLabel, listenerPath, removeListener } from '../listeners'
import DeleteListenerModal from '../sections/DeleteListenerModal'
import SidecarSourceSection from '../sections/SidecarSourceSection'
import { saveErrorMessage } from '../useListenerEditor'

// /sidecars/:id — the details card on its own page, and the listener controls.
// The request, its error and its cancellation live in useSidecarStore; this
// file only asks for an id. A save needs no callback back into the page either:
// updateSidecar writes the stored document into `selected`, so the card renders
// what the plane now holds rather than what the form hoped it wrote.
export default function SidecarDetailsPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const selected = useSidecarStore((s) => s.selected)
  const selectedId = useSidecarStore((s) => s.selectedId)
  const selectedLoading = useSidecarStore((s) => s.selectedLoading)
  const error = useSidecarStore((s) => s.selectedError)
  const fetchSidecar = useSidecarStore((s) => s.fetchSidecar)
  const clearSelected = useSidecarStore((s) => s.clearSelected)
  const updateSidecar = useSidecarStore((s) => s.updateSidecar)

  const [deleting, setDeleting] = useState(null)
  const [deletingBusy, setDeletingBusy] = useState(false)

  // Two conditions, and both are needed.
  //   selectedId !== id — the store is not even looking at this route's id.
  //     True for the frame between a URL change and the effect below, which a
  //     flag alone would miss: it would still read "idle" and paint the
  //     previous sidecar under the new URL.
  //   selectedLoading — the store went for this id but has no answer yet.
  //     Without it the loader lifts after useMinDelay's 500 ms and a slower
  //     request renders an empty body.
  const loading = selectedId !== id || selectedLoading
  const showLoader = useMinDelay(loading, 500)

  // Dropping the record on unmount keeps this page from opening on a stale one.
  useEffect(() => {
    fetchSidecar(id)
    return clearSelected
  }, [id, fetchSidecar, clearSelected])

  const confirmDelete = async () => {
    setDeletingBusy(true)
    const configuration = removeListener(selected.configuration, deleting.index)
    const { ok, error: err } = await updateSidecar(selected.id, configuration)
    setDeletingBusy(false)
    if (!ok) {
      showSnackbar({ level: 'error', text: 'Failed to delete the listener.', description: saveErrorMessage(err) })
      return
    }
    setDeleting(null)
    showSnackbar({ level: 'success', text: `Listener "${deleting.label}" deleted.` })
  }

  if (loading || showLoader) return <PageLoader h={400} />

  const listeners = selected?.configuration?.listeners ?? []

  return (
    <Stack gap="xl">
      <DeleteListenerModal
        label={deleting?.label}
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
        selected && (
          <>
            <Title order={1}>{selected.name}</Title>
            <SidecarDetails
              sidecar={selected}
              listenerActions={{
                onAdd: () => navigate(`/sidecars/${encodeURIComponent(selected.id)}/listeners/new`),
                onEdit: (index) => navigate(listenerPath(selected.id, listeners[index], index)),
                // The label, not listener.name: `name` is optional in the
                // document, and the daemon's own fallback is what the row, the
                // logs and the audit rows already call this listener.
                onDelete: (index) =>
                  setDeleting({ index, listener: listeners[index], label: listenerLabel(listeners[index], index) }),
              }}
            />
            {/* Last on the page, with Delete's weight and none of its
                finality: flipping the source retires every rule the plane
                distributes to this sidecar, so it is not a header control. */}
            <SidecarSourceSection sidecar={selected} />
          </>
        )
      )}
    </Stack>
  )
}
