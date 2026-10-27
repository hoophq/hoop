import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { useLiveEvent } from '@/hooks/useLiveEvent'
import { LIVE_EVENTS } from '@/services/liveEvents'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useUserStore } from '@/stores/useUserStore'
import AddSidecarModal from './components/AddSidecarModal'
import SidecarMethodCards from './components/SidecarMethodCards'
import SidecarLicenseNotice, { FREE_SIDECAR_SEATS } from './sections/SidecarLicenseNotice'
import SidecarsTable from './sections/SidecarsTable'

export default function Sidecars() {
  const { sidecars, loading, error, fetchSidecars } = useSidecarStore()
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const serverInfoLoaded = useUserStore((s) => s.serverInfoLoaded)

  const [addOpened, { open: openAdd, close: closeAdd }] = useDisclosure(false)
  const navigate = useNavigate()

  // A sidecar on this list that checks in for the first time opens its page:
  // someone just started it, and that page is where its configuration moves
  // to Hoop. One that already checked in keeps the list on screen.
  useLiveEvent(LIVE_EVENTS.SIDECAR_CHECKED_IN, (event) => {
    const listed = sidecars.find((s) => s.id === event.id)
    if (listed && !listed.last_seen_at) navigate(`/sidecars/${event.id}`)
  })

  useEffect(() => {
    fetchSidecars()
  }, [fetchSidecars])

  // First load only: a revisit renders the cached list while it refreshes.
  if (loading && sidecars.length === 0) return <PageLoader h={400} />
  if (error) return <Text c="red">{error}</Text>

  const count = sidecars.length
  // The free plan holds FREE_SIDECAR_SEATS sidecars; the API refuses one more.
  const atFreeLimit = serverInfoLoaded && isFreeLicense && count >= FREE_SIDECAR_SEATS

  return (
    <>
      <AddSidecarModal opened={addOpened} onClose={closeAdd} />

      <Stack gap="xl">
        <Group justify="space-between" align="flex-start">
          <Stack gap="sm">
            <Title order={1}>Sidecars</Title>
            <Text size="lg" c="dimmed">
              Connect your existing sidecars, or create a new one.
            </Text>
          </Stack>
          {count > 0 && (
            <Button onClick={openAdd} disabled={atFreeLimit}>
              Add new Sidecar
            </Button>
          )}
        </Group>

        <SidecarLicenseNotice />

        {count === 0 ? (
          <SidecarMethodCards />
        ) : (
          <Stack gap="sm">
            <Text size="lg" fw={700}>
              {`${count} ${count === 1 ? 'Sidecar' : 'Sidecars'}`}
            </Text>
            <SidecarsTable sidecars={sidecars} />
          </Stack>
        )}
      </Stack>
    </>
  )
}
