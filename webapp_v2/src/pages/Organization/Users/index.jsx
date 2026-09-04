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
import { useModeConfig } from '@/modes'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { ROLE_ADMIN, roleLabel, roleOptions, roleToGroups } from '@/utils/roles'
import { showSnackbar } from '@/utils/snackbar'

const STATUS_OPTIONS = [
  { value: 'active', label: 'Active' },
  { value: 'inactive', label: 'Inactive' },
  { value: 'reviewing', label: 'Reviewing' },
]

// The gateway activates a local user with this password immediately and never forces a
// change, so it is the account's real credential. The previous generator drew from three
// eight-word lists with Math.random — 512 possibilities — and because the value lived in
// component state of a modal that never unmounts, every user invited before a page
// reload got the SAME one.
//
// Readability still matters, since an admin copies this into a message by hand: four
// words from a larger list plus digits, drawn from crypto.getRandomValues.
const WORDS = [
  'amber', 'anchor', 'atlas', 'beacon', 'bridge', 'canyon', 'cedar', 'cobalt',
  'compass', 'copper', 'coral', 'delta', 'ember', 'falcon', 'forest', 'granite',
  'harbor', 'indigo', 'ivory', 'jasper', 'juniper', 'lantern', 'marble', 'meadow',
  'mercury', 'nimbus', 'onyx', 'orbit', 'pepper', 'quartz', 'quiver', 'ridge',
  'river', 'saffron', 'sierra', 'silver', 'summit', 'thunder', 'timber', 'tundra',
  'velvet', 'walnut', 'willow', 'zephyr',
]

function generatePassword() {
  const bytes = crypto.getRandomValues(new Uint32Array(5))
  const words = Array.from(bytes.slice(0, 4), (n) => WORDS[n % WORDS.length])
  return `${words.join('-')}-${String(bytes[4] % 10000).padStart(4, '0')}`
}

const CREATE_PREFIX = '__new__:'

// `usersForm` (modes): 'groups' edits free-form groups, the gateway way;
// 'roles' assigns one role (utils/roles) and round-trips every other group
// untouched, so an IdP-synced group survives an edit here.
function UserFormModal({ opened, onClose, formType, user, groups, isLocalAuth, usersForm, onSaved }) {
  const adminRoleName = useUserStore((s) => s.adminRoleName)
  const approverRoleName = useUserStore((s) => s.approverRoleName)
  const byRole = usersForm === 'roles'
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [selectedGroups, setSelectedGroups] = useState([])
  const [role, setRole] = useState(ROLE_ADMIN)
  const [otherGroups, setOtherGroups] = useState([])
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
      setRole(user?.role ?? ROLE_ADMIN)
      setOtherGroups((user?.groups ?? []).filter((g) => g !== adminRoleName && g !== approverRoleName))
      setStatus(user?.status ?? 'active')
      setSlackId(user?.slack_id ?? '')
      setGroupOptions(groups.map((g) => ({ value: g.name ?? g, label: g.name ?? g })))
      setGroupSearch('')
    }
  }, [opened, user, groups, adminRoleName, approverRoleName])

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
      const groupsPayload = byRole
        ? [...roleToGroups(role, adminRoleName, approverRoleName), ...otherGroups]
        : selectedGroups
      const payload = { name, groups: groupsPayload, slack_id: slackId, email }
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
          {byRole ? (
            <Select
              label="Role"
              data={roleOptions(role)}
              value={role}
              onChange={setRole}
              required
            />
          ) : (
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
          )}
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

function statusVariant(status) {
  if (status === 'active') return 'active'
  if (status === 'inactive') return 'inactive'
  return 'warning'
}

export default function Users() {
  const { usersForm } = useModeConfig()
  const byRole = usersForm === 'roles'
  const [users, setUsers] = useState([])
  const [groups, setGroups] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [isLocalAuth, setIsLocalAuth] = useState(false)
  const [selectedUser, setSelectedUser] = useState(null)
  const [formType, setFormType] = useState('create')
  const [opened, { open, close }] = useDisclosure(false)
  // Bumped on every open so UserFormModal remounts. The modal stays mounted with only
  // `opened` toggling, so without this its initial state — including the generated
  // password — is computed once for the lifetime of the page.
  const [formKey, setFormKey] = useState(0)
  const openForm = () => { setFormKey((n) => n + 1); open() }

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
    openForm()
  }

  function handleEdit(user) {
    setSelectedUser(user)
    setFormType('update')
    openForm()
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
                  <Table.Th>{byRole ? 'Role' : 'Groups'}</Table.Th>
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
                          {byRole ? roleLabel(user.role) : (user.groups ?? []).join(', ') || '—'}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Badge variant={statusVariant(user.status)}>
                          {user.status ?? '—'}
                        </Badge>
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
        key={opened ? formKey : 'closed'}
        opened={opened}
        onClose={close}
        formType={formType}
        user={selectedUser}
        groups={groups}
        isLocalAuth={isLocalAuth}
        usersForm={usersForm}
        onSaved={fetchAll}
      />
    </>
  )
}
