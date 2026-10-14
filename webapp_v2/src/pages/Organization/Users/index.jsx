import { useState, useEffect, useCallback, useRef } from 'react'
import {
  Anchor,
  Divider,
  Group,
  Stack,
  Text,
  Title,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { Search } from 'lucide-react'
import { useMinDelay } from '@/hooks/useMinDelay'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import Table from '@/components/Table'
import Badge from '@/components/Badge'
import Modal from '@/components/Modal'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import Select from '@/components/Select'
import MultiSelect from '@/components/MultiSelect'
import CopyButton from '@/components/CopyButton'
import Pagination from '@/components/Pagination'
import Tooltip from '@/components/Tooltip'
import { usersService } from '@/services/users'
import { authService } from '@/services/auth'
import { docsUrl } from '@/utils/docsUrl'
import { formatFullDate, formatRelativeTime } from '@/utils/datetime'
import { showSnackbar } from '@/utils/snackbar'
import { STATUS_FILTER_OPTIONS, STATUS_OPTIONS, generatePassword, statusVariant } from './shared'

const CREATE_PREFIX = '__new__:'
const PAGE_SIZE = 50
const SEARCH_DEBOUNCE_MS = 300

function UserFormModal({ opened, onClose, formType, user, groups, isLocalAuth, onSaved }) {
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [selectedGroups, setSelectedGroups] = useState([])
  const [status, setStatus] = useState('active')
  const [slackId, setSlackId] = useState('')
  const [password] = useState(() => generatePassword())
  const [saving, setSaving] = useState(false)
  const [groupOptions, setGroupOptions] = useState([])
  const [groupSearch, setGroupSearch] = useState('')

  useEffect(() => {
    if (opened) {
      setName(user?.name ?? '')
      setEmail(user?.email ?? '')
      setSelectedGroups(user?.groups ?? [])
      setStatus(user?.status ?? 'active')
      setSlackId(user?.slack_id ?? '')
      setGroupOptions(groups.map((g) => ({ value: g.name ?? g, label: g.name ?? g })))
      setGroupSearch('')
    }
  }, [opened, user, groups])

  const exactMatch = groupOptions.some((o) => o.value === groupSearch)
  const creatableGroupData = groupSearch && !exactMatch
    ? [...groupOptions, { value: `${CREATE_PREFIX}${groupSearch}`, label: `+ Create "${groupSearch}"` }]
    : groupOptions

  function handleGroupChange(values) {
    const resolved = []
    for (const v of values) {
      if (v.startsWith(CREATE_PREFIX)) {
        const created = v.slice(CREATE_PREFIX.length)
        setGroupOptions((prev) => [...prev, { value: created, label: created }])
        resolved.push(created)
      } else {
        resolved.push(v)
      }
    }
    setSelectedGroups(resolved)
    setGroupSearch('')
  }

  async function handleSubmit(e) {
    e.preventDefault()
    if (!name.trim()) {
      showSnackbar({ level: 'error', text: 'Name is required.' })
      return
    }
    if (formType === 'create' && !email.trim()) {
      showSnackbar({ level: 'error', text: 'Email is required.' })
      return
    }
    setSaving(true)
    try {
      const payload = { name, groups: selectedGroups, slack_id: slackId, email }
      if (formType === 'update') {
        payload.id = user.id
        payload.status = status
      }
      if (formType === 'create' && isLocalAuth) {
        payload.password = password
      }
      if (formType === 'create') {
        await usersService.create(payload)
        showSnackbar({ level: 'success', text: 'User created.' })
      } else {
        await usersService.update(user.id, payload)
        showSnackbar({ level: 'success', text: 'User updated.' })
      }
      onSaved()
      onClose()
    } catch {
      showSnackbar({ level: 'error', text: `Failed to ${formType === 'create' ? 'create' : 'update'} user.` })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={formType === 'create' ? 'Create a new user' : `Edit ${user?.name ?? 'user'}`}
      size="lg"
    >
      <form onSubmit={handleSubmit}>
        <Stack gap="md">
          <TextInput
            label="Name"
            placeholder="Your name"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            required
          />
          <MultiSelect
            label="Groups"
            placeholder="Select groups…"
            data={creatableGroupData}
            value={selectedGroups}
            onChange={handleGroupChange}
            searchable
            clearable
            searchValue={groupSearch}
            onSearchChange={setGroupSearch}
          />
          {formType === 'create' && (
            <TextInput
              label="Email"
              type="email"
              placeholder="user@yourcompany.com"
              value={email}
              onChange={(e) => setEmail(e.currentTarget.value)}
              required
            />
          )}
          {formType === 'update' && (
            <Select
              label="Status"
              data={STATUS_OPTIONS}
              value={status}
              onChange={setStatus}
              required
            />
          )}
          <TextInput
            label="Slack ID"
            placeholder="U12345678"
            value={slackId}
            onChange={(e) => setSlackId(e.currentTarget.value)}
          />
          {formType === 'create' && isLocalAuth && (
            <>
              <Divider />
              <Stack gap="xs">
                <Title order={5}>Password</Title>
                <Text size="xs" c="dimmed">
                  Copy and send this password to the invited user. You can see this password only this time.
                </Text>
                <Group gap="sm" wrap="nowrap">
                  <PasswordInput value={password} readOnly flex={1} />
                  <CopyButton value={password} label="Copy password" />
                </Group>
              </Stack>
            </>
          )}
          <Group justify="flex-end" gap="sm">
            <Button variant="outline" color="gray" type="button" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" loading={saving}>
              {formType === 'create' ? 'Create' : 'Update'}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}

// The Users page of both products: free-form groups. A login through an
// identity provider replaces them with the groups the provider sends.
export default function Users() {
  const [users, setUsers] = useState([])
  const [total, setTotal] = useState(0)
  // The user count of the org without filters. The "Invite Users" promo is
  // about the org, not about what the current filter returns.
  const [orgTotal, setOrgTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [emailInput, setEmailInput] = useState('')
  const [email, setEmail] = useState('')
  const [status, setStatus] = useState(null)
  const [groups, setGroups] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [isLocalAuth, setIsLocalAuth] = useState(false)
  // The form reads the groups and the auth method. The page waits for both:
  // a late answer would reset an open form, or hide the local auth password.
  const [groupsReady, setGroupsReady] = useState(false)
  const [serverInfoReady, setServerInfoReady] = useState(false)
  const [selectedUser, setSelectedUser] = useState(null)
  const [formType, setFormType] = useState('create')
  const [opened, { open, close }] = useDisclosure(false)
  // Bumped on every open so UserFormModal remounts. The modal stays mounted with only
  // `opened` toggling, so without this its initial state — including the generated
  // password — is computed once for the lifetime of the page.
  const [formKey, setFormKey] = useState(0)
  const openForm = () => { setFormKey((n) => n + 1); open() }
  // Each users request takes the next number and commits only while it is the
  // latest, so a slow answer for an old filter cannot replace a newer one.
  const requestIdRef = useRef(0)
  const loadedRef = useRef(false)
  const debounceRef = useRef(null)

  const showLoader = useMinDelay(loading || !groupsReady || !serverInfoReady)

  const fetchUsers = useCallback(async () => {
    const requestId = ++requestIdRef.current
    try {
      const { data } = await usersService.listPaginated({ page, pageSize: PAGE_SIZE, email, status })
      if (requestId !== requestIdRef.current) return
      const matches = data?.pages?.total ?? 0
      // A save can move the only user of the last page out of the filter.
      const lastPage = Math.max(1, Math.ceil(matches / PAGE_SIZE))
      if (page > lastPage) {
        setPage(lastPage)
        return
      }
      setUsers(data?.data ?? [])
      setTotal(matches)
      if (!email && !status) setOrgTotal(matches)
      loadedRef.current = true
    } catch {
      if (requestId !== requestIdRef.current) return
      if (loadedRef.current) {
        showSnackbar({ level: 'error', text: 'Failed to load users.' })
      } else {
        setError('Failed to load users.')
      }
    } finally {
      setLoading(false)
    }
  }, [page, email, status])

  const fetchGroups = useCallback(async () => {
    try {
      const { data } = await usersService.listGroups()
      setGroups(data ?? [])
    } catch {
      setError('Failed to load users.')
    } finally {
      setGroupsReady(true)
    }
  }, [])

  useEffect(() => {
    fetchUsers()
  }, [fetchUsers])

  useEffect(() => {
    fetchGroups()
    authService.getPublicServerInfo()
      .then((serverInfo) => setIsLocalAuth(serverInfo?.auth_method === 'local'))
      .catch(() => setError('Failed to load users.'))
      .finally(() => setServerInfoReady(true))
    return () => {
      if (debounceRef.current) clearTimeout(debounceRef.current)
    }
  }, [fetchGroups])

  function handleEmailChange(event) {
    const value = event.currentTarget.value
    setEmailInput(value)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => {
      debounceRef.current = null
      setPage(1)
      setEmail(value.trim())
    }, SEARCH_DEBOUNCE_MS)
  }

  function handleStatusChange(value) {
    setPage(1)
    setStatus(value)
  }

  function handleSaved() {
    fetchUsers()
    fetchGroups()
  }

  function handleAdd() {
    setSelectedUser(null)
    setFormType('create')
    openForm()
  }

  function handleEdit(user) {
    setSelectedUser(user)
    setFormType('update')
    openForm()
  }

  if (showLoader) return <PageLoader />
  if (error) return <PageLoader error={error} />

  const filtered = Boolean(email || status)
  const totalPages = Math.ceil(total / PAGE_SIZE)

  return (
    <>
      <Stack gap="xl">
        <Group justify="space-between" align="flex-start">
          <Stack gap="xs">
            <Title order={1}>Users</Title>
            <Text c="dimmed" size="lg">
              {`${total} ${total === 1 ? 'Member' : 'Members'}`}
            </Text>
          </Stack>
          {orgTotal !== 1 && (
            <Button onClick={handleAdd}>Add User</Button>
          )}
        </Group>

        {orgTotal > 1 && (
          <Group gap="sm">
            <TextInput
              aria-label="Search by email"
              placeholder="Search by email"
              value={emailInput}
              onChange={handleEmailChange}
              leftSection={<Search size={16} />}
              w={280}
            />
            <Select
              aria-label="Filter by status"
              placeholder="All statuses"
              data={STATUS_FILTER_OPTIONS}
              value={status}
              onChange={handleStatusChange}
              clearable
              w={180}
            />
          </Group>
        )}

        {users.length === 0 ? (
          filtered ? (
            <EmptyState
              title="No users found"
              description="No users match the current filters."
            />
          ) : (
            <EmptyState
              title="No users yet"
              description="Add your first user to get started."
              action={{ label: 'Add User', onClick: handleAdd }}
            />
          )
        ) : (
          <>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Email</Table.Th>
                  <Table.Th>Groups</Table.Th>
                  <Table.Th>Status</Table.Th>
                  <Table.Th>Last login</Table.Th>
                  <Table.Th w={80} />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {users.map((user) => (
                  <Table.Tr key={user.id}>
                    <Table.Td>{user.name ?? '—'}</Table.Td>
                    <Table.Td>{user.email ?? '—'}</Table.Td>
                    <Table.Td>
                      <Text size="sm" c="dimmed">
                        {(user.groups ?? []).join(', ') || '—'}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant={statusVariant(user.status)}>
                        {user.status ?? '—'}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      {user.last_login_at ? (
                        <Tooltip label={formatFullDate(user.last_login_at)}>
                          <Text size="sm" span>{formatRelativeTime(user.last_login_at)}</Text>
                        </Tooltip>
                      ) : '—'}
                    </Table.Td>
                    <Table.Td>
                      <Button variant="subtle" color="gray" size="sm" onClick={() => handleEdit(user)}>
                        Edit
                      </Button>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>

            {totalPages > 1 && (
              <Group justify="center">
                <Pagination total={totalPages} value={page} onChange={setPage} />
              </Group>
            )}

            {orgTotal === 1 && (
              <Stack flex={1} mih="30vh" align="center" py="xxl">
                <Stack flex={1} align="center" justify="center" gap="lg">
                  <Text size="sm" c="dimmed" ta="center" maw={400}>
                    Invite users and setup team-based permissions and approval workflows for secure resource access
                  </Text>
                  <Button onClick={handleAdd}>Invite Users</Button>
                </Stack>
                <Text mt="auto" size="sm" c="dimmed" ta="center">
                  {'Need more information? Check out '}
                  <Anchor href={docsUrl.clients.webApp.userManagement} target="_blank" size="sm">
                    User Management documentation
                  </Anchor>
                  {'.'}
                </Text>
              </Stack>
            )}
          </>
        )}
      </Stack>

      <UserFormModal
        key={opened ? formKey : 'closed'}
        opened={opened}
        onClose={close}
        formType={formType}
        user={selectedUser}
        groups={groups}
        isLocalAuth={isLocalAuth}
        onSaved={handleSaved}
      />
    </>
  )
}
