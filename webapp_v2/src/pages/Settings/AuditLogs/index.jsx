import { useState, useEffect, useCallback, useRef } from 'react'
import { Box, Button, Code, Group, Popover, Stack, Text, Title } from '@mantine/core'
import { Network, CircleAlert, ChevronDown, ChevronRight, User, BadgeCheck } from 'lucide-react'
import { useMinDelay } from '@/hooks/useMinDelay'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import Table from '@/components/Table'
import Badge from '@/components/Badge'
import TextInput from '@/components/TextInput'
import DatePickerInput from '@/components/DatePickerInput'
import { auditLogsService } from '@/services/auditLogs'
import { usersService } from '@/services/users'
import classes from './AuditLogs.module.css'

const PAGE_SIZE = 25

function formatTimestamp(ts) {
  if (!ts) return '—'
  const date = new Date(ts)
  const pad = (n) => String(n).padStart(2, '0')
  return `${pad(date.getDate())}/${pad(date.getMonth() + 1)}/${date.getFullYear()} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function formatOperation(action, resourceType, resourceName) {
  const actionText =
    { create: 'Created', update: 'Updated', delete: 'Deleted', revoke: 'Revoke' }[action] ??
    (action ? action.charAt(0).toUpperCase() + action.slice(1) : '')
  const resourceText = resourceName || (resourceType ? resourceType.charAt(0).toUpperCase() + resourceType.slice(1) : 'Resource')
  return `${actionText} ${resourceText}`
}

function OutcomeBadge({ httpStatus }) {
  const isSuccess = httpStatus && httpStatus >= 200 && httpStatus < 300
  return (
    <Badge variant={isSuccess ? 'active' : 'danger'}>
      {isSuccess ? `Success (${httpStatus})` : `Failure (${httpStatus ?? 'ERR'})`}
    </Badge>
  )
}

function ExpandedRow({ log }) {
  return (
    <Box bg="gray.0" p="md">
      <Stack gap="lg">
        <Group gap="xs">
          <Network size={16} />
          <Text size="sm" c="dimmed">IP Address:</Text>
          <Text size="sm" fw={500}>{log.client_ip ?? 'N/A'}</Text>
        </Group>

        <Group gap="xs">
          <BadgeCheck size={16} />
          <Text size="sm" c="dimmed">Result:</Text>
          <OutcomeBadge httpStatus={log.http_status} />
        </Group>

        {(log.http_status == null || log.http_status >= 400) && log.error_message && (
          <Group gap="xs" align="flex-start">
            <CircleAlert size={16} color="var(--mantine-color-red-6)" />
            <Text size="sm" c="dimmed">Error:</Text>
            <Text size="sm" fw={500} c="red.7">{log.error_message}</Text>
          </Group>
        )}

        {log.request_payload_redacted && Object.keys(log.request_payload_redacted).length > 0 && (
          <Stack gap="xs">
            <Text size="sm" fw={700}>Raw Payload</Text>
            <Code block bg="black" c="gray.0" className={classes.codeBlock}>
              {JSON.stringify(log.request_payload_redacted, null, 2)}
            </Code>
          </Stack>
        )}
      </Stack>
    </Box>
  )
}

function UserFilter({ value, onChange, users }) {
  const [search, setSearch] = useState('')
  const [opened, setOpened] = useState(false)

  const adminUsers = users.filter((u) => (u.groups ?? []).includes('admin'))
  const filtered = search
    ? adminUsers.filter((u) => u.email?.toLowerCase().includes(search.toLowerCase()))
    : adminUsers

  return (
    <Popover opened={opened} onChange={setOpened} width={320} position="bottom-start" withinPortal>
      <Popover.Target>
        <Button
          variant={value ? 'light' : 'outline'}
          color="gray"
          size="sm"
          leftSection={<User size={14} />}
          onClick={() => { setSearch(''); setOpened((o) => !o) }}
        >
          {value ?? 'User'}
        </Button>
      </Popover.Target>
      <Popover.Dropdown>
        <Stack gap="xs">
          {value && (
            <Button
              variant="subtle"
              color="gray"
              size="xs"
              fullWidth
              onClick={() => { onChange(null); setOpened(false) }}
            >
              Clear filter
            </Button>
          )}
          <TextInput
            size="xs"
            placeholder="Search users"
            value={search}
            onChange={(e) => setSearch(e.currentTarget.value)}
          />
          <Box mah={240} className={classes.scrollableDropdown}>
            {filtered.length === 0 ? (
              <Text size="xs" c="dimmed" p="xs">No users found</Text>
            ) : (
              <Stack gap={0}>
                {[...filtered].sort((a, b) => a.email?.localeCompare(b.email)).map((u) => (
                  <Button
                    key={u.id}
                    variant={u.email === value ? 'light' : 'subtle'}
                    color="gray"
                    size="xs"
                    fullWidth
                    justify="flex-start"
                    onClick={() => { onChange(u.email === value ? null : u.email); setOpened(false) }}
                  >
                    {u.email}
                  </Button>
                ))}
              </Stack>
            )}
          </Box>
        </Stack>
      </Popover.Dropdown>
    </Popover>
  )
}

export default function AuditLogs() {
  const [logs, setLogs] = useState([])
  const [pagination, setPagination] = useState({ page: 1, total: 0, hasMore: false })
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState(null)
  const [expandedRows, setExpandedRows] = useState(new Set())
  const [users, setUsers] = useState([])

  const [dateRange, setDateRange] = useState([null, null])
  const [actorEmail, setActorEmail] = useState(null)

  const isInitialLoad = useRef(true)
  const showLoader = useMinDelay(loading, 1500)

  const toISO = (date, endOfDay = false) => {
    if (!date) return undefined
    const d = new Date(date)
    if (endOfDay) {
      d.setHours(23, 59, 59, 999)
    } else {
      d.setHours(0, 0, 0, 0)
    }
    return d.toISOString()
  }

  const fetchLogs = useCallback(
    async ({ page = 1, append = false } = {}) => {
      if (append) setLoadingMore(true)
      else if (isInitialLoad.current) setLoading(true)

      try {
        const res = await auditLogsService.list({
          page,
          pageSize: PAGE_SIZE,
          actorEmail: actorEmail ?? undefined,
          createdAfter: toISO(dateRange[0], false),
          createdBefore: toISO(dateRange[1], true),
        })
        const data = res.data?.data ?? []
        const pages = res.data?.pages ?? {}
        const total = pages.total ?? 0
        const loaded = append ? logs.length + data.length : data.length

        setLogs(append ? [...logs, ...data] : data)
        setPagination({
          page: pages.page ?? 1,
          total,
          hasMore: loaded < total,
        })
      } catch {
        setError('Failed to load audit logs.')
      } finally {
        isInitialLoad.current = false
        setLoading(false)
        setLoadingMore(false)
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [actorEmail, dateRange]
  )

  useEffect(() => {
    async function loadUsers() {
      try {
        const res = await usersService.list()
        setUsers(res.data ?? [])
      } catch {
        // non-critical, ignore
      }
    }
    loadUsers()
  }, [])

  useEffect(() => {
    fetchLogs({ page: 1, append: false })
  }, [fetchLogs])

  function toggleRow(id) {
    setExpandedRows((prev) => {
      const next = new Set(prev)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })
  }

  if (showLoader) return <PageLoader />
  if (error) return <PageLoader error={error} />

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <Stack gap="xs">
          <Title order={1}>Internal Audit Logs</Title>
          <Text c="dimmed" size="lg">
            Showing {logs.length} of {pagination.total} logs
          </Text>
        </Stack>
        <Group gap="sm" wrap="wrap">
          <DatePickerInput
            type="range"
            placeholder="Period"
            value={dateRange}
            onChange={setDateRange}
            w={220}
            size="sm"
          />
          <UserFilter value={actorEmail} onChange={setActorEmail} users={users} />
        </Group>
      </Group>

      {logs.length === 0 ? (
        <EmptyState
          title="No audit logs found"
          description="There are no logs matching your current filters."
        />
      ) : (
        <>
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Timestamp</Table.Th>
                <Table.Th>User</Table.Th>
                <Table.Th>Operation</Table.Th>
                <Table.Th w={40} />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {logs.map((log) => {
                const expanded = expandedRows.has(log.id)
                return [
                  <Table.Tr
                    key={log.id}
                    onClick={() => toggleRow(log.id)}
                    className={classes.expandableRow}
                  >
                    <Table.Td>{formatTimestamp(log.created_at)}</Table.Td>
                    <Table.Td>{log.actor_email ?? log.actor_name ?? 'Unknown'}</Table.Td>
                    <Table.Td>
                      {formatOperation(log.action, log.resource_type, log.resource_name)}
                    </Table.Td>
                    <Table.Td>
                      {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                    </Table.Td>
                  </Table.Tr>,
                  expanded && (
                    <Table.Tr key={`${log.id}-expanded`}>
                      <Table.Td colSpan={4} p={0}>
                        <ExpandedRow log={log} />
                      </Table.Td>
                    </Table.Tr>
                  ),
                ]
              })}
            </Table.Tbody>
          </Table>

          {pagination.hasMore && (
            <Group justify="center">
              <Button
                variant="outline"
                color="gray"
                size="sm"
                onClick={() => fetchLogs({ page: pagination.page + 1, append: true })}
                loading={loadingMore}
              >
                Load more
              </Button>
            </Group>
          )}
        </>
      )}
    </Stack>
  )
}
