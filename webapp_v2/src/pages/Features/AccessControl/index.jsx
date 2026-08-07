import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { Rotate3d } from 'lucide-react'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import EmptyState from '@/layout/EmptyState'
import FullBleed from '@/layout/FullBleed'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { docsUrl } from '@/utils/docsUrl'
import { showSnackbar } from '@/utils/snackbar'
import { useAccessControlStore } from './store'
import { allGroups, groupsWithPermissions } from './helpers'
import AccessControlPromotion from './components/AccessControlPromotion'
import GroupListItem from './components/GroupListItem'

export default function AccessControl() {
  const navigate = useNavigate()

  const plugin = useAccessControlStore((s) => s.plugin)
  const pluginStatus = useAccessControlStore((s) => s.pluginStatus)
  const groups = useAccessControlStore((s) => s.groups)
  const groupsStatus = useAccessControlStore((s) => s.groupsStatus)
  const connectionsById = useAccessControlStore((s) => s.connectionsById)
  const submitting = useAccessControlStore((s) => s.submitting)
  const fetchPlugin = useAccessControlStore((s) => s.fetchPlugin)
  const fetchGroups = useAccessControlStore((s) => s.fetchGroups)
  const fetchConnections = useAccessControlStore((s) => s.fetchConnections)
  const installPlugin = useAccessControlStore((s) => s.installPlugin)

  const [selectedRole, setSelectedRole] = useState(null)

  const roleFilter = usePaginatedConnections({ pageSize: 50 })
  const getIconUrl = useConnectionIconGetter()

  useEffect(() => {
    fetchPlugin()
    fetchGroups()
  }, [fetchPlugin, fetchGroups])

  const pluginConnections = plugin?.connections

  // The plugin resolves each association's name but not its subtype, which is
  // what picks the type icon — look the connections up separately.
  useEffect(() => {
    const ids = (pluginConnections ?? []).map((c) => c.id)
    if (ids.length > 0) fetchConnections(ids)
  }, [pluginConnections, fetchConnections])

  const permissions = useMemo(
    () => groupsWithPermissions(pluginConnections),
    [pluginConnections],
  )
  const groupNames = useMemo(
    () => allGroups(groups, pluginConnections),
    [groups, pluginConnections],
  )

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
          connections: (permissions[name] ?? []).map((connection) => ({
            ...connection,
            iconUrl: getIconUrl(connectionsById[connection.id]),
          })),
        })),
    [groupNames, permissions, selectedRole, connectionsById, getIconUrl],
  )

  const loading =
    pluginStatus === 'idle' ||
    pluginStatus === 'loading' ||
    groupsStatus === 'idle' ||
    groupsStatus === 'loading'
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
              onSelect={setSelectedRole}
              onClear={() => setSelectedRole(null)}
              options={roleFilter.options}
              loading={roleFilter.loading}
              hasMore={roleFilter.hasMore}
              onLoadMore={roleFilter.loadMore}
              searchValue={roleFilter.searchValue}
              onSearchChange={roleFilter.setSearch}
              onOpen={roleFilter.ensureLoaded}
            />
          </Group>

          {processedGroups.length === 0 ? (
            <EmptyState
              compact
              title={`No groups found for '${selectedRole?.label ?? ''}'`}
              description="Try changing the Resource Roles filter to explore more groups."
            />
          ) : (
            <Box>
              {processedGroups.map((group, idx) => (
                <GroupListItem
                  key={group.name}
                  group={group}
                  isFirst={idx === 0}
                  isLast={idx === processedGroups.length - 1}
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
          )}
        </>
      )}
    </Stack>
  )
}
