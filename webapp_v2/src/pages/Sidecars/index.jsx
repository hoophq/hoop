import { useEffect } from 'react'
import { Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useUserStore } from '@/stores/useUserStore'
import AddSidecarModal from './components/AddSidecarModal'
import SidecarMethodCards from './components/SidecarMethodCards'
import SidecarLicenseNotice from './sections/SidecarLicenseNotice'
import SidecarsTable from './sections/SidecarsTable'

/**
 * The control plane landing page for every admin: the fleet of sidecars
 * (Figma: "Sidecars"). Empty, it offers the two ways in; filled, the table and
 * "Add new Sidecar". Deleting one happens on its details page.
 */
export default function Sidecars() {
  const { sidecars, loading, error, fetchSidecars } = useSidecarStore()
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const showLoader = useMinDelay(loading, 500)

  const [addOpened, { open: openAdd, close: closeAdd }] = useDisclosure(false)

  useEffect(() => {
    fetchSidecars()
  }, [fetchSidecars])

  if (showLoader) return <PageLoader h={400} />
  if (error) return <Text c="red">{error}</Text>

  const count = sidecars.length

  return (
    <>
      <AddSidecarModal opened={addOpened} onClose={closeAdd} />

      <Stack gap="xl">
        <Group justify="space-between" align="flex-start">
          <Stack gap="sm">
            <Title order={1}>Sidecars</Title>
            <Text size="lg" c="dimmed">
              Connect existing sidecars to this control plane, or create a new one.
            </Text>
          </Stack>
          {count > 0 && (
            <Button onClick={openAdd} disabled={isFreeLicense}>
              Add new Sidecar
            </Button>
          )}
        </Group>

        <SidecarLicenseNotice />

        {count === 0 ? (
          <SidecarMethodCards />
        ) : (
          <Stack gap="sm">
            <Text size="sm" fw={600}>
              {`${count} ${count === 1 ? 'Sidecar' : 'Sidecars'}`}
            </Text>
            <SidecarsTable sidecars={sidecars} />
          </Stack>
        )}
      </Stack>
    </>
  )
}
