import { Group, Stack, Text, UnstyledButton } from '@mantine/core'
import { ChevronDown, LogOut, MessageCircleQuestion } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import ActionMenu from '@/components/ActionMenu'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'
import { getUserDisplayName } from '@/utils/user'
import { UserAvatar } from './UserAvatar'
import classes from './Header.module.css'

const GITHUB_DISCUSSIONS_URL = 'https://github.com/hoophq/hoop/discussions'

export function UserMenu() {
  const navigate = useNavigate()
  const { user, gatewayVersion, analyticsTracking } = useUserStore()
  const { logout } = useAuthStore()

  const handleLogout = () => {
    logout()
    navigate('/login')
  }

  // Intercom is booted with custom_launcher_selector pointing at this id, so
  // clicking the item opens the messenger. Without analytics the messenger is
  // never booted, so fall back to the public discussions board.
  const handleSupport = () => {
    if (!analyticsTracking) {
      window.open(GITHUB_DISCUSSIONS_URL, '_blank', 'noopener,noreferrer')
    }
  }

  const target = (
    <UnstyledButton className={classes.userButton} aria-label="Open user menu">
      <Group gap={4} wrap="nowrap">
        <UserAvatar user={user} />
        <ChevronDown size={16} aria-hidden="true" />
      </Group>
    </UnstyledButton>
  )

  return (
    <ActionMenu target={target} width={240}>
      {/* Name + email and the gateway version are additions on top of the
          Figma, which shows only the two actions. */}
      <ActionMenu.Label className={classes.menuLabel}>
        <Stack gap={2}>
          <Text fz="sm" fw={600} truncate>
            {getUserDisplayName(user)}
          </Text>
          {user?.email && (
            <Text fz="xs" c="dimmed" truncate>
              {user.email}
            </Text>
          )}
        </Stack>
      </ActionMenu.Label>

      <ActionMenu.Item
        id="intercom-support-trigger"
        className={classes.menuItem}
        leftSection={<MessageCircleQuestion size={16} aria-hidden="true" />}
        onClick={handleSupport}
      >
        Contact support
      </ActionMenu.Item>

      <ActionMenu.Item
        danger
        className={classes.menuItem}
        leftSection={<LogOut size={16} aria-hidden="true" />}
        onClick={handleLogout}
      >
        Log out
      </ActionMenu.Item>

      {gatewayVersion && (
        <Text fz="xs" className={classes.menuFooter}>
          {`Gateway ${gatewayVersion}`}
        </Text>
      )}
    </ActionMenu>
  )
}
