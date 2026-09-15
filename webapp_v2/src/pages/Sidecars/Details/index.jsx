import { useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Stack, Text, Title } from '@mantine/core'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { useSidecarStore } from '@/stores/useSidecarStore'
import SidecarDetails from '../components/SidecarDetails'

// /sidecars/:id — the details card on its own page. The request, its error and
// its cancellation live in useSidecarStore; this file only asks for an id.
export default function SidecarDetailsPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  const selected = useSidecarStore((s) => s.selected)
  const selectedId = useSidecarStore((s) => s.selectedId)
  const selectedLoading = useSidecarStore((s) => s.selectedLoading)
  const error = useSidecarStore((s) => s.selectedError)
  const fetchSidecar = useSidecarStore((s) => s.fetchSidecar)
  const clearSelected = useSidecarStore((s) => s.clearSelected)

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

  if (loading || showLoader) return <PageLoader h={400} />

  return (
    <Stack gap="xl">
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
            <SidecarDetails sidecar={selected} />
          </>
        )
      )}
    </Stack>
  )
}
