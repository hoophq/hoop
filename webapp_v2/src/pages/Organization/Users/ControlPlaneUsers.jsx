import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
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
import Switch from '@/components/Switch'
import TagsInput from '@/components/TagsInput'
import CopyButton from '@/components/CopyButton'
import { usersService } from '@/services/users'
import { authService } from '@/services/auth'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { showSnackbar } from '@/utils/snackbar'
import { STATUS_OPTIONS, generatePassword, statusVariant } from './shared'

/**
 * The control plane's Users page, sibling of GatewayUsers.jsx.
 *
 * Groups come from the identity provider (ADR-0019): SCIM, a directory sync or
 * the login claim writes them, so this page shows them and does not edit them.
 * The one exception is local auth, which has no identity provider and keeps
 * editing groups here. Administrator is hoop's own group and stays a switch.
 */

function UserFormModal({ opened, onClose, formType, user, isLocalAuth, onSaved }) {
  // ADMIN_USERNAME renames the admin group, so its name comes from /serverinfo.
  const adminRoleName = useUserStore((s) => s.adminRoleName)
  const currentEmail = useUserStore((s) => s.user?.email)
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [isAdmin, setIsAdmin] = useState(true)
  const [otherGroups, setOtherGroups] = useState([])
  const [groupOptions, setGroupOptions] = useState([])
  const [status, setStatus] = useState('active')
  const [slackId, setSlackId] = useState('')
  const [password] = useState(() => generatePassword())
  const [saving, setSaving] = useState(false)
  const isSelf = formType === 'update' && user?.email === currentEmail

  useEffect(() => {
    if (opened) {
      setName(user?.name ?? '')
      setEmail(user?.email ?? '')
      setIsAdmin(user ? (user.groups ?? []).includes(adminRoleName) : true)
      setOtherGroups((user?.groups ?? []).filter((g) => g !== adminRoleName))
      setStatus(user?.status ?? 'active')
      setSlackId(user?.slack_id ?? '')
    }
  }, [opened, user, adminRoleName])

  useEffect(() => {
    if (!opened || !isLocalAuth) return
    let cancelled = false
    usersService
      .listGroups()
      .then(({ data }) => {
        if (!cancelled) setGroupOptions((Array.isArray(data) ? data : []).filter((g) => g !== adminRoleName))
      })
      // A group can still be typed without the list.
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [opened, isLocalAuth, adminRoleName])

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
      // PUT /users replaces the whole list, so the groups this page does not
      // edit travel back untouched with the admin switch.
      const groups = [...(isAdmin || isSelf ? [adminRoleName] : []), ...otherGroups]
      const payload = { name, groups, slack_id: slackId, email }
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
          <Switch
            label="Administrator"
            description={isSelf ? 'You cannot remove your own administrator access.' : 'Full access to the control plane.'}
            checked={isAdmin || isSelf}
            disabled={isSelf}
            onChange={(e) => setIsAdmin(e.currentTarget.checked)}
          />
          {isLocalAuth ? (
            <TagsInput
              label="Groups"
              description="Name these groups as reviewers on an analyzer rule."
              placeholder="Select or type a group"
              data={groupOptions}
              value={otherGroups}
              onChange={setOtherGroups}
            />
          ) : (
            formType === 'update' && (
              <Stack gap={4}>
                <Text size="sm" fw={500}>
                  Groups
                </Text>
                {otherGroups.length > 0 ? (
                  <Group gap="xs">
                    {otherGroups.map((g) => (
                      <Badge key={g} variant="light" color="gray">
                        {g}
                      </Badge>
                    ))}
                  </Group>
                ) : (
                  <Text size="sm" c="dimmed">
                    No groups.
                  </Text>
                )}
                <Text size="xs" c="dimmed">
                  Groups come from your identity provider.
                </Text>
              </Stack>
            )
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
            description="Only needed when the Slack app cannot read emails."
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

export default function ControlPlaneUsers() {
  const adminRoleName = useUserStore((s) => s.adminRoleName)
  const [users, setUsers] = useState([])
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
      const [usersRes, serverInfo] = await Promise.all([
        usersService.list(),
        authService.getPublicServerInfo(),
      ])
      setUsers(usersRes.data ?? [])
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
            {!isLocalAuth && (
              <Text size="sm" c="dimmed">
                {'Reviewers and their groups come from your identity provider. Set it up in '}
                <Anchor component={Link} to="/settings/provisioning" size="sm">
                  Provisioning
                </Anchor>
                {'.'}
              </Text>
            )}
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
                  .map((user) => {
                    const groups = user.groups ?? []
                    const others = groups.filter((g) => g !== adminRoleName)
                    return (
                      <Table.Tr key={user.id}>
                        <Table.Td>
                          <Group gap="xs" wrap="nowrap">
                            <Text size="sm">{user.name ?? '—'}</Text>
                            {groups.includes(adminRoleName) && (
                              <Badge variant="light" color="indigo">
                                Admin
                              </Badge>
                            )}
                          </Group>
                        </Table.Td>
                        <Table.Td>{user.email ?? '—'}</Table.Td>
                        <Table.Td>
                          <Text size="sm" c="dimmed">
                            {others.length > 0 ? others.join(', ') : '—'}
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
                    )
                  })}
              </Table.Tbody>
            </Table>

            {users.length === 1 && (
              <Stack flex={1} mih="30vh" align="center" py="xxl">
                <Stack flex={1} align="center" justify="center" gap="lg">
                  <Text size="sm" c="dimmed" ta="center" maw={400}>
                    Invite administrators to manage sidecars. Reviewers come from your identity provider.
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
        isLocalAuth={isLocalAuth}
        onSaved={fetchAll}
      />
    </>
  )
}
