import { useCallback, useEffect, useState } from 'react'
import { Card, Divider, Group, Stack, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import { useParams } from 'react-router-dom'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import Switch from '@/components/Switch'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usersService } from '@/services/users'
import { useUserStore } from '@/stores/useUserStore'
import MobileHeader from '../../components/MobileHeader'
import InfoRow from '../../components/InfoRow'
import { statusVariant } from '../statusVariant'

function StatusToggleModal({ opened, onClose, user, nextStatus, saving, onConfirm }) {
  const activating = nextStatus === 'active'
  const name = user?.name || user?.email || 'this user'

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={activating ? 'Activate user?' : 'Deactivate user?'}
      size="sm"
    >
      <Stack>
        <Text size="sm">
          {activating
            ? `${name} will regain access to the system.`
            : `${name} will lose access to the system until reactivated.`}
        </Text>
        <Group justify="flex-end" gap="sm">
          <Button variant="subtle" color="gray" onClick={onClose}>
            Cancel
          </Button>
          <Button color={activating ? 'green' : 'red'} loading={saving} onClick={onConfirm}>
            {activating ? 'Activate' : 'Deactivate'}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

function MobileUserDetail() {
  const { userId } = useParams()
  const me = useUserStore((s) => s.user)
  const [user, setUser] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [saving, setSaving] = useState(false)
  const [opened, { open, close }] = useDisclosure(false)
  const showLoader = useMinDelay(loading, 500)

  const fetchUser = useCallback(async () => {
    try {
      const { data } = await usersService.get(userId)
      setUser(data)
    } catch {
      setError('Failed to load user.')
    } finally {
      setLoading(false)
    }
  }, [userId])

  useEffect(() => {
    fetchUser()
  }, [fetchUser])

  if (showLoader) return <PageLoader h={400} />

  const isSelf = me?.id === user?.id
  const canToggle = user && !isSelf && user.status !== 'invited'
  const nextStatus = user?.status === 'active' ? 'inactive' : 'active'

  async function handleToggleConfirm() {
    setSaving(true)
    try {
      // PUT /users replaces groups entirely — always resend the current ones.
      await usersService.update(user.id, {
        id: user.id,
        name: user.name,
        email: user.email,
        groups: user.groups ?? [],
        slack_id: user.slack_id ?? '',
        status: nextStatus,
      })
      notifications.show({
        message: `User ${nextStatus === 'active' ? 'activated' : 'deactivated'}.`,
        color: 'green',
      })
      close()
      await fetchUser()
    } catch (err) {
      notifications.show({
        message: err.response?.data?.message ?? 'Failed to update user.',
        color: 'red',
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Stack gap="md">
      <MobileHeader title={user?.name || user?.email || 'User'} backTo="/m/users" />

      {error && <Text c="red">{error}</Text>}

      {user && (
        <>
          <Card padding={0} withBorder>
            <InfoRow label="Email" value={user.email} />
            <Divider />
            <InfoRow label="Role" value={user.role} />
            <Divider />
            <InfoRow label="Status">
              <Badge variant={statusVariant(user.status)}>{user.status ?? '—'}</Badge>
            </InfoRow>
            <Divider />
            <InfoRow label="Slack ID" value={user.slack_id || '—'} />
            <Divider />
            <InfoRow label="Groups">
              {(user.groups ?? []).length === 0 ? (
                <Text size="sm">—</Text>
              ) : (
                <Group gap="xs" justify="flex-end">
                  {user.groups.map((group) => (
                    <Badge key={group} variant="outline" color="gray">
                      {group}
                    </Badge>
                  ))}
                </Group>
              )}
            </InfoRow>
          </Card>

          {canToggle && (
            <Card withBorder padding="md">
              <Group justify="space-between" wrap="nowrap">
                <Stack gap={2}>
                  <Text size="sm" fw={600}>
                    Active account
                  </Text>
                  <Text size="xs" c="dimmed">
                    Inactive users cannot access the system.
                  </Text>
                </Stack>
                <Switch checked={user.status === 'active'} onChange={open} />
              </Group>
            </Card>
          )}

          <StatusToggleModal
            opened={opened}
            onClose={close}
            user={user}
            nextStatus={nextStatus}
            saving={saving}
            onConfirm={handleToggleConfirm}
          />
        </>
      )}
    </Stack>
  )
}

export default MobileUserDetail
