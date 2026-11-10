import { useEffect, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Group, Image, Stack, Text, Title } from '@mantine/core'
import { ArrowLeft, Plus } from 'lucide-react'
import Button from '@/components/Button'
import ConfirmDialog from '@/components/ConfirmDialog'
import FormFooter, { FORM_FOOTER_CLEARANCE } from '@/components/FormFooter'
import PageLoader from '@/components/PageLoader'
import Tabs from '@/components/Tabs'
import TestConnectionModal from '@/components/TestConnectionModal'
import EmptyState from '@/layout/EmptyState'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { showSnackbar } from '@/utils/snackbar'
import { useResourcesStore } from '@/pages/Resources/store'
import { roleCountKnown, useConfigureResourceStore } from '@/pages/Resources/Configure/store'
import InformationTab from '@/pages/Resources/Configure/InformationTab'
import RolesTab from '@/pages/Resources/Configure/RolesTab'

const TABS = { INFORMATION: 'information', ROLES: 'roles' }

function DeleteResourceDialog({ opened, resourceName, roleCount, deleting, onDelete, onViewRoles, onClose }) {
  // The gateway refuses to delete a resource that has roles (Q5).
  if (roleCount > 0) {
    return (
      <ConfirmDialog
        opened={opened}
        title="Delete resource?"
        confirmLabel="View roles"
        onConfirm={onViewRoles}
        onClose={onClose}
      >
        <Text size="sm">
          {resourceName +
            (roleCount === 1 ? ' has 1 role.' : ' has ' + roleCount + ' roles.') +
            ' Delete its roles first, then delete the resource.'}
        </Text>
      </ConfirmDialog>
    )
  }
  return (
    <ConfirmDialog
      opened={opened}
      title="Delete resource?"
      confirmLabel="Confirm and delete"
      danger
      loading={deleting}
      onConfirm={onDelete}
      onClose={onClose}
    >
      <Stack gap={4}>
        <Text size="sm">
          {'This action will instantly remove your access to ' + resourceName + ' and can not be undone.'}
        </Text>
        <Text size="sm">Are you sure you want to delete this resource?</Text>
      </Stack>
    </ConfirmDialog>
  )
}

export default function ConfigureResource() {
  const navigate = useNavigate()
  const { resourceName } = useParams()
  const [searchParams] = useSearchParams()
  const iconFor = useConnectionIconGetter()

  const resource = useConfigureResourceStore((s) => s.resource)
  const loading = useConfigureResourceStore((s) => s.loading)
  const notFound = useConfigureResourceStore((s) => s.notFound)
  const error = useConfigureResourceStore((s) => s.error)
  const saving = useConfigureResourceStore((s) => s.saving)
  const roles = useConfigureResourceStore((s) => s.roles)
  const { open, loadResource, rename, deleteResource, reset } = useConfigureResourceStore.getState()
  const test = useResourcesStore((s) => s.test)
  const closeTest = useResourcesStore((s) => s.closeTest)

  // The tab comes from ?tab= once; a switch does not touch the URL.
  const [tab, setTab] = useState(() => (searchParams.get('tab') === TABS.ROLES ? TABS.ROLES : TABS.INFORMATION))
  const [name, setName] = useState('')
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleting, setDeleting] = useState(false)

  // A rename moves the URL to the new name; the store already holds it.
  useEffect(() => {
    if (useConfigureResourceStore.getState().name !== resourceName) open(resourceName)
  }, [resourceName, open])

  useEffect(() => () => {
    reset()
    closeTest()
  }, [reset, closeTest])

  useEffect(() => {
    if (resource) setName(resource.name)
  }, [resource])

  const dirty = Boolean(resource) && name.trim() !== '' && name !== resource.name

  const save = async () => {
    try {
      if (!(await rename(name))) return
      showSnackbar({ level: 'success', text: 'Resource name updated successfully!' })
      navigate('/resources/configure/' + encodeURIComponent(name), { replace: true })
    } catch (err) {
      showSnackbar({
        level: 'error',
        text: 'Failed to update resource name.',
        description: err?.response?.data?.message,
      })
    }
  }

  const confirmDelete = async () => {
    setDeleting(true)
    try {
      if (!(await deleteResource())) return
      showSnackbar({ level: 'success', text: 'Resource deleted successfully!' })
      navigate('/resources?tab=resources')
    } catch (err) {
      setDeleting(false)
      setDeleteOpen(false)
      showSnackbar({ level: 'error', text: 'Failed to delete resource.', description: err?.response?.data?.message })
    }
  }

  if (loading || (!resource && !notFound && !error)) return <PageLoader h={400} />
  if (notFound) {
    return (
      <EmptyState
        title="Resource not found"
        description={'No resource is named ' + resourceName + '.'}
        action={{ label: 'Go to Resources', onClick: () => navigate('/resources?tab=resources') }}
      />
    )
  }
  if (error) {
    return (
      <Stack h={400} align="center" justify="center" gap="md">
        <PageLoader h="auto" error message="Could not load the resource." description={error} />
        <Button variant="default" onClick={loadResource}>
          Try again
        </Button>
      </Stack>
    )
  }

  return (
    <>
      <Stack gap="xl" pb={FORM_FOOTER_CLEARANCE}>
        <Group justify="space-between" align="flex-start">
          <Stack gap="xs">
            <Title order={2}>Configure Resource</Title>
            <Group gap="sm" align="center">
              <Image src={iconFor(resource)} w={20} h={20} alt="" />
              <Text>{resource.name}</Text>
            </Group>
          </Stack>
          <Button
            size="sm"
            leftSection={<Plus size={16} />}
            onClick={() => navigate('/resources/' + encodeURIComponent(resource.name) + '/add-role')}
          >
            Add New Role
          </Button>
        </Group>

        <Tabs value={tab} onChange={(next) => next && setTab(next)}>
          <Tabs.List mb="lg">
            <Tabs.Tab value={TABS.INFORMATION}>Resource Information</Tabs.Tab>
            <Tabs.Tab value={TABS.ROLES}>Resource Roles</Tabs.Tab>
          </Tabs.List>
          <Tabs.Panel value={TABS.INFORMATION} pt="lg">
            <InformationTab resource={resource} name={name} saving={saving} onNameChange={setName} />
          </Tabs.Panel>
          <Tabs.Panel value={TABS.ROLES} pt="lg">
            <RolesTab />
          </Tabs.Panel>
        </Tabs>

        <FormFooter
          left={
            <Button variant="default" leftSection={<ArrowLeft size={16} />} onClick={() => navigate(-1)}>
              Back
            </Button>
          }
        >
          <Button variant="transparent" color="red" disabled={!roleCountKnown(roles)} onClick={() => setDeleteOpen(true)}>
            Delete
          </Button>
          <Button loading={saving} disabled={!dirty} onClick={save}>
            Save
          </Button>
        </FormFooter>
      </Stack>

      <DeleteResourceDialog
        opened={deleteOpen}
        resourceName={resource.name}
        roleCount={roles.total}
        deleting={deleting}
        onDelete={confirmDelete}
        onViewRoles={() => {
          setDeleteOpen(false)
          setTab(TABS.ROLES)
        }}
        onClose={() => setDeleteOpen(false)}
      />

      <TestConnectionModal
        opened={test.opened}
        testing={test.testing}
        agentStatus={test.agentStatus}
        connectionStatus={test.connectionStatus}
        durationMs={test.durationMs}
        errorMessage={test.errorMessage}
        connectionName={test.connectionName}
        onClose={closeTest}
      />
    </>
  )
}
