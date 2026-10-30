import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Center, Group, Loader, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import ConfirmDialog from '@/components/ConfirmDialog'
import PageLoader from '@/components/PageLoader'
import { useInfiniteScroll } from '@/hooks/useInfiniteScroll'
import EmptyState from '@/layout/EmptyState'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { canTestConnection, isSidecarMirror } from '@/utils/connectionPolicy'
import { showSnackbar } from '@/utils/snackbar'
import { useResourcesStore } from '@/pages/Resources/store'
import { hasMoreRoles, useConfigureResourceStore } from '@/pages/Resources/Configure/store'
import RoleSummary from '@/pages/Resources/components/RoleSummary'
import classes from '@/pages/Resources/List.module.css'

function RoleActions({ role, testing, onDelete, onTest }) {
  const navigate = useNavigate()
  const mirror = isSidecarMirror(role)
  return (
    <Group gap="sm" wrap="nowrap">
      {!mirror && (
        <Button variant="outline" color="red" size="sm" onClick={() => onDelete(role.name)}>
          Delete
        </Button>
      )}
      {/* Hidden for agent-managed roles, as in the list (Q9). */}
      {role.managed_by !== 'hoopagent' && (
        <Button
          variant="default"
          size="sm"
          onClick={() =>
            navigate('/roles/' + encodeURIComponent(role.name) + '/configure?from_page=resource-configure')
          }
        >
          Configure
        </Button>
      )}
      {!mirror && canTestConnection(role) && (
        <Button variant="light" size="sm" disabled={testing} onClick={() => onTest(role.name)}>
          Test Connection
        </Button>
      )}
    </Group>
  )
}

export default function RolesTab() {
  const roles = useConfigureResourceStore((s) => s.roles)
  const { loadMoreRoles, retryRoles, deleteRole } = useConfigureResourceStore.getState()
  const test = useResourcesStore((s) => s.test)
  const testConnection = useResourcesStore((s) => s.testConnection)
  const iconFor = useConnectionIconGetter()
  const [deleteTarget, setDeleteTarget] = useState(null)
  const [deleting, setDeleting] = useState(false)
  const more = hasMoreRoles(roles)
  const sentinel = useInfiniteScroll(loadMoreRoles, more && !roles.loading && !roles.error)

  const confirmDelete = async () => {
    setDeleting(true)
    try {
      await deleteRole(deleteTarget)
      showSnackbar({ level: 'success', text: 'Role ' + deleteTarget + ' deleted.' })
      setDeleteTarget(null)
    } catch (err) {
      showSnackbar({ level: 'error', text: 'Failed to delete role.', description: err?.response?.data?.message })
    } finally {
      setDeleting(false)
    }
  }

  if (roles.items.length === 0 && (roles.loading || !roles.loaded)) return <PageLoader h={300} />
  if (roles.items.length === 0 && roles.error) {
    return (
      <Stack h={300} align="center" justify="center" gap="md">
        <PageLoader h="auto" error message="Could not load the roles." description={roles.error} />
        <Button variant="default" onClick={retryRoles}>
          Try again
        </Button>
      </Stack>
    )
  }
  if (roles.items.length === 0) {
    return <EmptyState compact title="No roles configured yet" description="Add a new role to get started." />
  }

  return (
    <Stack gap="md">
      <ul className={classes.list}>
        {roles.items.map((role) => (
          <li key={role.id} className={classes.row}>
            <RoleSummary role={role} iconSrc={iconFor(role)} iconAlt="" />
            <RoleActions
              role={role}
              testing={test.testing && test.connectionName === role.name}
              onDelete={setDeleteTarget}
              onTest={testConnection}
            />
          </li>
        ))}
      </ul>
      {more && <div ref={sentinel} />}
      {roles.loading && (
        <Center>
          <Loader size="sm" />
        </Center>
      )}
      {roles.error && !roles.loading && (
        <Stack align="center" gap="xs" role="alert">
          <Text size="sm" c="red">
            {(roles.failedAppend ? 'Could not load more: ' : 'Could not refresh the roles: ') + roles.error}
          </Text>
          <Button variant="default" onClick={retryRoles}>
            Try again
          </Button>
        </Stack>
      )}

      <ConfirmDialog
        opened={Boolean(deleteTarget)}
        title="Delete role?"
        confirmLabel="Confirm and delete"
        danger
        loading={deleting}
        onConfirm={confirmDelete}
        onClose={() => setDeleteTarget(null)}
      >
        <Stack gap={4}>
          <Text size="sm">
            {'This action will instantly remove your access to ' + deleteTarget + ' and can not be undone.'}
          </Text>
          <Text size="sm">Are you sure you want to delete this role?</Text>
        </Stack>
      </ConfirmDialog>
    </Stack>
  )
}
