import { useState, useEffect } from 'react'
import {
  Anchor,
  Button,
  Divider,
  Group,
  Stack,
  Text,
  Title,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import { useMinDelay } from '@/hooks/useMinDelay'
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
import { usersService } from '@/services/users'
import { authService } from '@/services/auth'
import { docsUrl } from '@/utils/docsUrl'

const STATUS_OPTIONS = [
  { value: 'active', label: 'Active' },
  { value: 'inactive', label: 'Inactive' },
  { value: 'reviewing', label: 'Reviewing' },
]

function generatePassword() {
  const adjectives = ['Fast', 'Blue', 'Happy', 'Strong', 'Bright', 'Silent', 'Quick', 'Dark']
  const colors = ['Red', 'Green', 'Gold', 'Cyan', 'Pink', 'Gray', 'Teal', 'Lime']
  const animals = ['Tiger', 'Eagle', 'Shark', 'Panda', 'Wolf', 'Bear', 'Hawk', 'Fox']
  const pick = (arr) => arr[Math.floor(Math.random() * arr.length)]
  return `${pick(adjectives)}-${pick(colors)}-${pick(animals)}`
}

const CREATE_PREFIX = '__new__:'

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
      notifications.show({ message: 'Name is required.', color: 'red' })
      return
    }
    if (formType === 'create' && !email.trim()) {
      notifications.show({ message: 'Email is required.', color: 'red' })
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
        notifications.show({ message: 'User created.', color: 'green' })
      } else {
        await usersService.update(user.id, payload)
        notifications.show({ message: 'User updated.', color: 'green' })
      }
      onSaved()
      onClose()
    } catch {
      notifications.show({ message: `Failed to ${formType === 'create' ? 'create' : 'update'} user.`, color: 'red' })
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
                  <CopyButton value={password} label="Copy password" size="md" />
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

function statusVariant(status) {
  if (status === 'active') return 'active'
  if (status === 'inactive') return 'inactive'
  return 'warning'
}

export default function Users() {
  const [users, setUsers] = useState([])
  const [groups, setGroups] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [isLocalAuth, setIsLocalAuth] = useState(false)
  const [selectedUser, setSelectedUser] = useState(null)
  const [formType, setFormType] = useState('create')
  const [opened, { open, close }] = useDisclosure(false)

  const showLoader = useMinDelay(loading)

  async function fetchAll() {
    try {
      const [usersRes, groupsRes, serverInfo] = await Promise.all([
        usersService.list(),
        usersService.listGroups(),
        authService.getPublicServerInfo(),
      ])
      setUsers(usersRes.data ?? [])
      setGroups(groupsRes.data ?? [])
      setIsLocalAuth(serverInfo?.auth_method === 'local')
    } catch {
      setError('Failed to load users.')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchAll()
  }, [])

  function handleAdd() {
    setSelectedUser(null)
    setFormType('create')
    open()
  }

  function handleEdit(user) {
    setSelectedUser(user)
    setFormType('update')
    open()
  }

  if (showLoader) return <PageLoader />
  if (error) return <PageLoader error={error} />

  return (
    <>
      <Stack gap="xl">
        <Group justify="space-between" align="flex-start">
          <Stack gap="xs">
            <Title order={1}>Users</Title>
            <Text c="dimmed" size="lg">
              {users.length} {users.length === 1 ? 'Member' : 'Members'}
            </Text>
          </Stack>
          {users.length !== 1 && (
            <Button onClick={handleAdd}>Add User</Button>
          )}
        </Group>

        {users.length === 0 ? (
          <EmptyState
            title="No users yet"
            description="Add your first user to get started."
            action={{ label: 'Add User', onClick: handleAdd }}
          />
        ) : (
          <>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Name</Table.Th>
                  <Table.Th>Email</Table.Th>
                  <Table.Th>Groups</Table.Th>
                  <Table.Th>Status</Table.Th>
                  <Table.Th w={80} />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {[...users]
                  .sort((a, b) => (a.name ?? '').localeCompare(b.name ?? ''))
                  .map((user) => (
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
                        <Button variant="subtle" color="gray" size="xs" onClick={() => handleEdit(user)}>
                          Edit
                        </Button>
                      </Table.Td>
                    </Table.Tr>
                  ))}
              </Table.Tbody>
            </Table>

            {users.length === 1 && (
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
        opened={opened}
        onClose={close}
        formType={formType}
        user={selectedUser}
        groups={groups}
        isLocalAuth={isLocalAuth}
        onSaved={fetchAll}
      />
    </>
  )
}
