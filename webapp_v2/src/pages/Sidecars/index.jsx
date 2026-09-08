import { useEffect, useState } from 'react'
import { Group, Stack, Text, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import AddSidecarModal from './components/AddSidecarModal'
import SidecarMethodCards from './components/SidecarMethodCards'
import DeleteSidecarModal from './sections/DeleteSidecarModal'
import SidecarLicenseNotice from './sections/SidecarLicenseNotice'
import SidecarsTable from './sections/SidecarsTable'
import { useConnectionsByName } from './useConnectionsByName'

/**
 * The control plane landing page for every admin: the fleet of sidecars
 * (Figma: "License has (not) sidecards"). Empty, it offers the two ways in;
 * filled, the table and "Add new Sidecar".
 */
export default function Sidecars() {
  const { sidecars, loading, error, fetchSidecars, deleteSidecar } = useSidecarStore()
  const connectionsByName = useConnectionsByName()
  const showLoader = useMinDelay(loading, 500)

  const [addOpened, { open: openAdd, close: closeAdd }] = useDisclosure(false)
  const [deleting, setDeleting] = useState(null)
  const [deleteBusy, setDeleteBusy] = useState(false)

  useEffect(() => {
    fetchSidecars()
  }, [fetchSidecars])

  const handleDeleteConfirm = async () => {
    setDeleteBusy(true)
    try {
      await deleteSidecar(deleting.id)
      showSnackbar({ level: 'success', text: `Sidecar "${deleting.name}" removed.` })
      setDeleting(null)
    } catch (err) {
      showSnackbar({ level: 'error', text: 'Failed to delete the sidecar.', description: err.response?.data?.message })
    } finally {
      setDeleteBusy(false)
    }
  }

  if (showLoader) return <PageLoader h={400} />
  if (error) return <Text c="red">{error}</Text>

  const count = sidecars.length

  return (
    <>
      <AddSidecarModal opened={addOpened} onClose={closeAdd} />
      <DeleteSidecarModal
        sidecar={deleting}
        opened={!!deleting}
        onClose={() => setDeleting(null)}
        onConfirm={handleDeleteConfirm}
        loading={deleteBusy}
      />

      <Stack gap="xl">
        <Group justify="space-between" align="flex-start">
          <Stack gap="sm">
            <Title order={1}>Sidecars</Title>
            <Text size="lg" c="dimmed">
              Connect existing sidecars to this control plane, or create a new one.
            </Text>
          </Stack>
          {count > 0 && <Button onClick={openAdd}>Add new Sidecar</Button>}
        </Group>

        <SidecarLicenseNotice />

        {count === 0 ? (
          <SidecarMethodCards />
        ) : (
          <Stack gap="sm">
            <Text size="sm" fw={600}>
              {`${count} ${count === 1 ? 'Sidecar' : 'Sidecars'}`}
            </Text>
            <SidecarsTable sidecars={sidecars} connectionsByName={connectionsByName} onDelete={setDeleting} />
          </Stack>
        )}
      </Stack>
    </>
  )
}
