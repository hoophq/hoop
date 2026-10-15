import { useEffect, useState } from 'react'
import { Box, Flex, Grid, Group, Image, Stack, Text, Title } from '@mantine/core'
import { ChevronDown, ChevronUp } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Pagination from '@/components/Pagination'
import { PANEL_PAGE_SIZE, pageCount, pageSlice } from '../helpers'
import classes from './GroupListItem.module.css'

function plural(count, noun) {
  return `${count} ${noun}${count === 1 ? '' : 's'}`
}

function PanelSection({ title, description, children }) {
  return (
    <Grid columns={7} gutter="xl">
      <Grid.Col span={2}>
        <Stack gap="xs">
          <Title order={4} fw={500}>
            {title}
          </Title>
          <Text size="sm" c="dimmed">
            {description}
          </Text>
        </Stack>
      </Grid.Col>
      <Grid.Col span={5}>{children}</Grid.Col>
    </Grid>
  )
}

// A bordered list showing one page of `items` at a time.
function PagedList({ items, page, onPageChange, renderItem }) {
  const pages = pageCount(items.length, PANEL_PAGE_SIZE)
  return (
    <Stack gap="sm">
      <Box className={classes.connections} h="fit-content">
        {pageSlice(items, page, PANEL_PAGE_SIZE).map(renderItem)}
      </Box>
      {pages > 1 && (
        <Pagination size="sm" total={pages} value={page} onChange={onPageChange} />
      )}
    </Stack>
  )
}

function ConnectionsSection({ connections, getIconUrl, onShowConnections, onConfigureConnection }) {
  const [page, setPage] = useState(1)

  // Only the visible page needs its type icons: resolving every role of a
  // large group up front is what made this page slow.
  useEffect(() => {
    onShowConnections(pageSlice(connections, page, PANEL_PAGE_SIZE).map((c) => c.id))
  }, [connections, page, onShowConnections])

  return (
    <PanelSection
      title="Resources"
      description="These resource roles can be accessed by this user group."
    >
      {connections.length === 0 ? (
        <Text size="sm" c="dimmed">
          No resource roles in this group
        </Text>
      ) : (
        <PagedList
          items={connections}
          page={page}
          onPageChange={setPage}
          renderItem={(connection) => (
            <Flex
              key={connection.id}
              p="xs"
              align="center"
              justify="space-between"
              gap="sm"
              className={classes.connection}
            >
              <Group gap="xs" align="center" wrap="nowrap" miw={0}>
                <Image
                  src={getIconUrl(connection.id)}
                  alt=""
                  w={16}
                  h={16}
                  miw={16}
                  fit="contain"
                />
                <Text size="sm" truncate>
                  {connection.name}
                </Text>
              </Group>
              <Button
                variant="default"
                size="xs"
                onClick={() => onConfigureConnection(connection.name)}
              >
                Configure
              </Button>
            </Flex>
          )}
        />
      )}
    </PanelSection>
  )
}

function UsersSection({ users, usersFailed }) {
  const [page, setPage] = useState(1)

  let body
  if (usersFailed) {
    body = (
      <Text size="sm" c="red">
        Failed to load users.
      </Text>
    )
  } else if (users.length === 0) {
    body = (
      <Text size="sm" c="dimmed">
        No users in this group
      </Text>
    )
  } else {
    body = (
      <PagedList
        items={users}
        page={page}
        onPageChange={setPage}
        renderItem={(user) => (
          <Flex
            key={user.id}
            p="xs"
            align="center"
            justify="space-between"
            gap="sm"
            className={classes.connection}
          >
            <Stack gap={0} miw={0}>
              <Text size="sm" truncate>
                {user.name || user.email}
              </Text>
              <Text size="xs" c="dimmed" truncate>
                {user.email}
              </Text>
            </Stack>
            {user.status !== 'active' && <Badge variant="inactive">{user.status}</Badge>}
          </Flex>
        )}
      />
    )
  }

  return (
    <PanelSection title="Users" description="These users are members of this user group.">
      {body}
    </PanelSection>
  )
}

// One row of the access control list. Rows stack into a single bordered block,
// so only the first and last ones carry the outer corners. Expanding a row
// reveals the resource roles the group can reach and the users in it.
export default function GroupListItem({
  group,
  isFirst,
  isLast,
  usersFailed,
  getIconUrl,
  onShowConnections,
  onConfigure,
  onConfigureConnection,
}) {
  const [expanded, setExpanded] = useState(false)
  const usersLabel = usersFailed ? 'users unavailable' : plural(group.users.length, 'user')

  return (
    <Box
      className={classes.row}
      data-first={isFirst || undefined}
      data-last={isLast || undefined}
      data-expanded={expanded || undefined}
    >
      <Flex p="lg" align="center" justify="space-between" gap="md">
        <Text fw={500} fz="lg">
          {group.name}
        </Text>

        <Group align="center" gap="lg" wrap="nowrap">
          <Button
            variant="subtle"
            color="gray"
            size="compact-sm"
            onClick={() => setExpanded((value) => !value)}
            rightSection={expanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
          >
            {`${plural(group.connections.length, 'resource role')} · ${usersLabel}`}
          </Button>

          <Button variant="default" onClick={() => onConfigure(group.name)}>
            Configure
          </Button>
        </Group>
      </Flex>

      {expanded && (
        <Stack gap="xl" px="xl" py="lg" className={classes.panel}>
          <ConnectionsSection
            connections={group.connections}
            getIconUrl={getIconUrl}
            onShowConnections={onShowConnections}
            onConfigureConnection={onConfigureConnection}
          />
          <UsersSection users={group.users} usersFailed={usersFailed} />
        </Stack>
      )}
    </Box>
  )
}
