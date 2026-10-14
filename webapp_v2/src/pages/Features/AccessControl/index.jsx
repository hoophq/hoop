import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { Rotate3d } from 'lucide-react'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import Pagination from '@/components/Pagination'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import EmptyState from '@/layout/EmptyState'
import FullBleed from '@/layout/FullBleed'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { docsUrl } from '@/utils/docsUrl'
import { showSnackbar } from '@/utils/snackbar'
import { useAccessControlStore } from './store'
import {
  GROUPS_PAGE_SIZE,
  allGroups,
  groupsWithPermissions,
  pageCount,
  pageSlice,
  usersByGroup,
} from './helpers'
import AccessControlPromotion from './components/AccessControlPromotion'
import GroupListItem from './components/GroupListItem'
import RoleUsers from './sections/RoleUsers'

export default function AccessControl() {
  const navigate = useNavigate()

  const plugin = useAccessControlStore((s) => s.plugin)
  const pluginStatus = useAccessControlStore((s) => s.pluginStatus)
  const groups = useAccessControlStore((s) => s.groups)
  const groupsStatus = useAccessControlStore((s) => s.groupsStatus)
  const users = useAccessControlStore((s) => s.users)
  const usersStatus = useAccessControlStore((s) => s.usersStatus)
  const connectionsById = useAccessControlStore((s) => s.connectionsById)
  const roleUsers = useAccessControlStore((s) => s.roleUsers)
  const submitting = useAccessControlStore((s) => s.submitting)
  const fetchPlugin = useAccessControlStore((s) => s.fetchPlugin)
  const fetchGroups = useAccessControlStore((s) => s.fetchGroups)
  const fetchUsers = useAccessControlStore((s) => s.fetchUsers)
  const fetchConnections = useAccessControlStore((s) => s.fetchConnections)
  const fetchRoleUsers = useAccessControlStore((s) => s.fetchRoleUsers)
  const clearRoleUsers = useAccessControlStore((s) => s.clearRoleUsers)
  const installPlugin = useAccessControlStore((s) => s.installPlugin)

  const [selectedRole, setSelectedRole] = useState(null)
  const [page, setPage] = useState(1)

  const roleFilter = usePaginatedConnections({ pageSize: 50 })
  const getConnectionIcon = useConnectionIconGetter()

  useEffect(() => {
    fetchPlugin()
    fetchGroups()
    fetchUsers()
  }, [fetchPlugin, fetchGroups, fetchUsers])

  useEffect(() => {
    if (selectedRole) fetchRoleUsers(selectedRole.value, 1)
    else clearRoleUsers()
  }, [selectedRole, fetchRoleUsers, clearRoleUsers])

  const pluginConnections = plugin?.connections

  // The plugin resolves each association's name but not its subtype, which is
  // what picks the type icon. Each expanded group asks for its visible page.
  const getIconUrl = useCallback(
    (id) => getConnectionIcon(connectionsById[id]),
    [connectionsById, getConnectionIcon],
  )

  const permissions = useMemo(
    () => groupsWithPermissions(pluginConnections),
    [pluginConnections],
  )
  const groupNames = useMemo(
    () => allGroups(groups, pluginConnections),
    [groups, pluginConnections],
  )

  const members = useMemo(() => usersByGroup(users), [users])

  const processedGroups = useMemo(
    () =>
      groupNames
        .filter(
          (name) =>
            !selectedRole ||
            (permissions[name] ?? []).some((c) => c.id === selectedRole.value),
        )
        .map((name) => ({
          name,
          connections: permissions[name] ?? [],
          users: members[name] ?? [],
        })),
    [groupNames, permissions, selectedRole, members],
  )
  const visibleGroups = pageSlice(processedGroups, page, GROUPS_PAGE_SIZE)
  const groupPages = pageCount(processedGroups.length, GROUPS_PAGE_SIZE)

  const selectRole = (role) => {
    setSelectedRole(role)
    setPage(1)
  }

  // Users gate the loader so no row shows a count of 0 before they arrive; a
  // failed users load still renders the list, marked per row. The promotion
  // screen lists no group, so it does not wait for them.
  const loading =
    pluginStatus === 'idle' ||
    pluginStatus === 'loading' ||
    groupsStatus === 'idle' ||
    groupsStatus === 'loading' ||
    (pluginStatus === 'success' && (usersStatus === 'idle' || usersStatus === 'loading'))
  const showLoader = useMinDelay(loading, 500)

  const goCreate = () => navigate('/features/access-control/new')

  const handleActivate = async () => {
    const { ok, error } = await installPlugin()
    if (ok) {
      showSnackbar({ level: 'success', text: 'Access Control activated.' })
    } else {
      showSnackbar({
        level: 'error',
        text: error?.response?.data?.message || 'Failed to activate Access Control.',
      })
    }
  }

  if (showLoader) {
    return <PageLoader h={300} />
  }

  // A failed load leaves both lists empty, which would otherwise fall through
  // to the empty state and tell an admin they have no groups configured.
  if (pluginStatus === 'error' || groupsStatus === 'error') {
    return <PageLoader error h={300} message="Failed to load access control." />
  }

  // The plugin gates the whole feature: until it is enabled every user reaches
  // every resource role, so there is nothing to list.
  if (pluginStatus === 'not-installed') {
    return (
      <FullBleed>
        <AccessControlPromotion onActivate={handleActivate} activating={submitting} />
      </FullBleed>
    )
  }

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="sm">
          <Title order={1}>Access Control</Title>
          <Box>
            <Text size="md" c="dimmed">
              Manage which user groups have access to specific resource roles.
            </Text>
            <Text size="md" c="dimmed">
              Control permissions and enhance security for your organization.
            </Text>
          </Box>
        </Stack>
        {groupNames.length > 0 && <Button onClick={goCreate}>Create Group</Button>}
      </Group>

      {groupNames.length === 0 ? (
        <EmptyState
          title="No Access Control Groups available to manage yet"
          action={{ label: 'Create Group', onClick: goCreate }}
          docsUrl={docsUrl.features.accessControl}
          docsLabel="access control documentation"
        />
      ) : (
        <>
          <Group gap="sm">
            <AsyncValueFilter
              icon={Rotate3d}
              label="Resource Roles"
              placeholder="Search resource roles"
              selected={selectedRole}
              onSelect={selectRole}
              onClear={() => selectRole(null)}
              options={roleFilter.options}
              loading={roleFilter.loading}
              hasMore={roleFilter.hasMore}
              onLoadMore={roleFilter.loadMore}
              searchValue={roleFilter.searchValue}
              onSearchChange={roleFilter.setSearch}
              onOpen={roleFilter.ensureLoaded}
            />
          </Group>

          {selectedRole && (
            <RoleUsers
              roleName={selectedRole.label}
              roleUsers={roleUsers}
              onPageChange={(next) => fetchRoleUsers(selectedRole.value, next)}
            />
          )}

          {processedGroups.length === 0 ? (
            <EmptyState
              compact
              title={`No groups found for '${selectedRole?.label ?? ''}'`}
              description="Try changing the Resource Roles filter to explore more groups."
            />
          ) : (
            <Stack gap="md">
              <Box>
                {visibleGroups.map((group, idx) => (
                  <GroupListItem
                    key={group.name}
                    group={group}
                    isFirst={idx === 0}
                    isLast={idx === visibleGroups.length - 1}
                    usersFailed={usersStatus === 'error'}
                    getIconUrl={getIconUrl}
                    onShowConnections={fetchConnections}
                    onConfigure={(name) =>
                      navigate(
                        `/features/access-control/edit?group=${encodeURIComponent(name)}`,
                      )
                    }
                    onConfigureConnection={(name) =>
                      navigate(`/roles/${encodeURIComponent(name)}/configure`)
                    }
                  />
                ))}
              </Box>
              {groupPages > 1 && (
                <Pagination total={groupPages} value={page} onChange={setPage} />
              )}
            </Stack>
          )}
        </>
      )}
    </Stack>
  )
}
