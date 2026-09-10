import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Stack, Text, Title } from '@mantine/core'
import { ArrowLeft } from 'lucide-react'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { sidecarsService } from '@/services/sidecars'
import MockNotice from '../components/MockNotice'
import SidecarDetails from '../components/SidecarDetails'

// /sidecars/:id — the details card on its own page.
export default function SidecarDetailsPage() {
  const { id } = useParams()
  const navigate = useNavigate()
  // One record per id: `loading` is "the record on screen is not this id's".
  const [result, setResult] = useState({ id: null, sidecar: null, error: null })
  const loading = result.id !== id
  const showLoader = useMinDelay(loading, 500)

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

  if (showLoader) return <PageLoader h={400} />

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
        sidecar && (
          <>
            <Title order={1}>{sidecar.name}</Title>
            <MockNotice />
            <SidecarDetails sidecar={sidecar} />
          </>
        )
      )}
    </Stack>
  )
}
