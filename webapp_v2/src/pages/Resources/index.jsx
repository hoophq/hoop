import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Center, Group, Loader, Stack, Text, Title, VisuallyHidden } from '@mantine/core'
import Button from '@/components/Button'
import ConfirmDialog from '@/components/ConfirmDialog'
import PageLoader from '@/components/PageLoader'
import Tabs from '@/components/Tabs'
import TestConnectionModal from '@/components/TestConnectionModal'
import { useInfiniteScroll } from '@/hooks/useInfiniteScroll'
import EmptyState from '@/layout/EmptyState'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { useUserStore } from '@/stores/useUserStore'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { showSnackbar } from '@/utils/snackbar'
import { hasFilters, hasMore, TABS, useResourcesStore } from '@/pages/Resources/store'
import FiltersBar from '@/pages/Resources/sections/FiltersBar'
import HoopCliModal from '@/pages/Resources/sections/HoopCliModal'
import ResourceRow from '@/pages/Resources/sections/ResourceRow'
import RoleRow from '@/pages/Resources/sections/RoleRow'
import ViewTabs from '@/pages/Resources/sections/ViewTabs'
import classes from '@/pages/Resources/sections/List.module.css'
import pageClasses from './Resources.module.css'

const GETTING_STARTED_URL = 'https://hoop.dev/docs/introduction/getting-started'

function ListEmpty({ isAdmin }) {
  const navigate = useNavigate()
  return (
    <EmptyState
      title={
        isAdmin
          ? 'Get started by setting up your environment resources in your Organization'
          : 'Discover the resources your organization can connect to securely through Hoop'
      }
      action={{
        label: isAdmin ? 'Setup new Resource' : 'Explore Resource Catalog',
        onClick: () => navigate('/resource-catalog'),
      }}
      docsUrl={GETTING_STARTED_URL}
      docsLabel="getting started documentation"
    />
  )
}

function ListPanel({ tab, children }) {
  const list = useResourcesStore((s) => s.lists[tab])
  const loadMore = useResourcesStore((s) => s.loadMore)
  const load = useResourcesStore((s) => s.load)
  const isAdmin = useUserStore((s) => s.isAdmin)
  const more = hasMore(list)
  const sentinel = useInfiniteScroll(() => loadMore(tab), more && !list.loading)

  // No empty state until the first answer: the loader covers a page with no
  // rows yet (Q1). Rows already on screen stay while they refresh.
  if (list.items.length === 0 && (list.loading || !list.loaded)) return <PageLoader h={400} />
  if (list.items.length === 0 && list.error) {
    return (
      <Stack h={400} align="center" justify="center" gap="md">
        <PageLoader h="auto" error message="Could not load the list." description={list.error} />
        <Button variant="default" onClick={() => load(tab)}>
          Try again
        </Button>
      </Stack>
    )
  }
  if (list.items.length === 0) return <ListEmpty isAdmin={isAdmin} />

  return (
    <Stack gap="md">
      <ul className={classes.list}>{children(list.items)}</ul>
      {more && <div ref={sentinel} />}
      {list.loading && (
        <Center>
          <Loader size="sm" />
        </Center>
      )}
    </Stack>
  )
}

function useDisplayName() {
  const metadata = useConnectionsMetadataStore((s) => s.metadata)
  return (subtype) => metadata?.connections?.find((c) => c.id === subtype)?.name || subtype || ''
}

export default function Resources() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const isAdmin = useUserStore((s) => s.isAdmin)
  const postgresProxyEnabled = useUserStore((s) => s.postgresProxyEnabled)
  const iconFor = useConnectionIconGetter()
  const displayName = useDisplayName()

  const tab = useResourcesStore((s) => s.tab)
  const filters = useResourcesStore((s) => s.filters)
  const resources = useResourcesStore((s) => s.lists[TABS.RESOURCES])
  const current = useResourcesStore((s) => s.lists[s.tab])
  const test = useResourcesStore((s) => s.test)
  const { open, switchTab, deleteRole, testConnection, closeTest } = useResourcesStore.getState()

  const [cliRole, setCliRole] = useState(null)
  const [deleteTarget, setDeleteTarget] = useState(null)
  const [deleting, setDeleting] = useState(false)

  // The tab comes from ?tab= once, on open; a switch does not touch the URL.
  useEffect(() => {
    open(searchParams.get('tab') === TABS.RESOURCES ? TABS.RESOURCES : TABS.ROLES)
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const confirmDelete = async () => {
    setDeleting(true)
    try {
      await deleteRole(deleteTarget)
      showSnackbar({ level: 'success', text: 'Role ' + deleteTarget + ' deleted.' })
      setDeleteTarget(null)
    } catch (err) {
      showSnackbar({
        level: 'error',
        text: 'Failed to delete role.',
        description: err?.response?.data?.message,
      })
    } finally {
      setDeleting(false)
    }
  }

  const status = current.loading
    ? 'Searching ' + tab + '...'
    : current.items.length + ' ' + tab + ' found' + (hasFilters(filters) ? ' with active filters' : '')

  return (
    <>
      <Tabs value={tab} onChange={(next) => next && switchTab(next)} unstyled>
        <Stack gap="md" pb="md" className={pageClasses.toolbar}>
          <Group justify="space-between">
            <Title order={1}>Resources</Title>
            {resources.items.length > 0 && (
              <Button onClick={() => navigate('/resource-catalog')}>
                {isAdmin ? 'Setup new Resource' : 'Explore Resource Catalog'}
              </Button>
            )}
          </Group>
          <Group justify="space-between">
            <ViewTabs />
            <FiltersBar />
          </Group>
          <VisuallyHidden role="status" aria-live="polite" aria-atomic="true">
            {status}
          </VisuallyHidden>
        </Stack>

        <Tabs.Panel value={TABS.RESOURCES}>
          <ListPanel tab={TABS.RESOURCES}>
            {(items) =>
              items.map((resource) => (
                <ResourceRow
                  key={resource.id}
                  resource={resource}
                  iconSrc={iconFor(resource)}
                  iconAlt={displayName(resource.subtype)}
                  isAdmin={isAdmin}
                />
              ))
            }
          </ListPanel>
        </Tabs.Panel>
        <Tabs.Panel value={TABS.ROLES}>
          <ListPanel tab={TABS.ROLES}>
            {(items) =>
              items.map((role) => (
                <RoleRow
                  key={role.id}
                  role={role}
                  iconSrc={iconFor(role)}
                  iconAlt={displayName(role.subtype)}
                  isAdmin={isAdmin}
                  postgresProxyEnabled={postgresProxyEnabled}
                  testing={test.testing && test.connectionName === role.name}
                  onOpenCli={setCliRole}
                  onTest={testConnection}
                  onDelete={setDeleteTarget}
                />
              ))
            }
          </ListPanel>
        </Tabs.Panel>
      </Tabs>

      <HoopCliModal connectionName={cliRole} onClose={() => setCliRole(null)} />

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

      <ConfirmDialog
        opened={Boolean(deleteTarget)}
        title="Delete role?"
        confirmLabel="Confirm and delete"
        danger
        loading={deleting}
        onConfirm={confirmDelete}
        onClose={() => !deleting && setDeleteTarget(null)}
      >
        <Stack gap={4}>
          <Text size="sm">
            {'This action will instantly remove your access to ' + deleteTarget + ' and can not be undone.'}
          </Text>
          <Text size="sm">Are you sure you want to delete this role?</Text>
        </Stack>
      </ConfirmDialog>
    </>
  )
}
