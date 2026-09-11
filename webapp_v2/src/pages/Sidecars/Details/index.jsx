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
  const error = useSidecarStore((s) => s.selectedError)
  const fetchSidecar = useSidecarStore((s) => s.fetchSidecar)

  // "What the store went to fetch is not what this route asks for." Derived
  // rather than read from a flag, because the effect below runs after the
  // first render of a new id: a flag would still say "idle" for that frame and
  // the previous sidecar would paint under the new URL.
  const loading = selectedId !== id
  const showLoader = useMinDelay(loading, 500)

  useEffect(() => {
    fetchSidecar(id)
  }, [id, fetchSidecar])

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
