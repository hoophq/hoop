import { useNavigate } from 'react-router-dom'
import { Box, Group, Image, Stack, Text } from '@mantine/core'
import { ChevronDown, EllipsisVertical } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import ActionMenu from '@/components/ActionMenu'
import Button from '@/components/Button'
import { useBridgeStore } from '@/stores/useBridgeStore'
import { useNativeAccessStore } from '@/stores/useNativeAccessStore'
import {
  canAccessNativeClient,
  canConnect,
  canHoopCli,
  canOpenWebTerminal,
  canTestConnection,
  isSidecarMirror,
} from '@/utils/connectionPolicy'
import classes from './List.module.css'

const capitalize = (s = '') => s.charAt(0).toUpperCase() + s.slice(1)

export default function RoleRow({
  role,
  iconSrc,
  iconAlt,
  isAdmin,
  postgresProxyEnabled,
  testing,
  onOpenCli,
  onTest,
  onDelete,
}) {
  const navigate = useNavigate()
  const native = canAccessNativeClient(role, { postgresProxyEnabled })
  const online = role.status === 'online'

  const openWebTerminal = () => {
    navigate('/client?role=' + encodeURIComponent(role.name))
    useBridgeStore.getState().syncPrimaryConnectionFromUrl()
  }

  return (
    <li className={classes.row}>
      <Group gap="md" wrap="nowrap" miw={0}>
        <Image src={iconSrc} alt={iconAlt} w={24} h={24} fit="contain" loading="lazy" />
        <Stack gap={0} miw={0}>
          <Text component="h3" fw={500} truncate>
            {role.name}
          </Text>
          <Text size="xs" c="dimmed">
            {role.resource_name}
          </Text>
          <Group gap={4}>
            <Box w={6} h={6} bg={online ? 'green.6' : 'red.6'} bdrs="xl" aria-hidden="true" />
            <Text size="xs" c="dimmed">
              {capitalize(role.status)}
            </Text>
          </Group>
        </Stack>
      </Group>

      <Group gap="lg" wrap="nowrap">
        {canConnect(role) && (
          <ActionMenu
            width={220}
            target={
              <Button variant="light" size="sm" rightSection={<ChevronDown size={14} />} aria-label={'Connect to ' + role.name}>
                Connect
              </Button>
            }
          >
            {canOpenWebTerminal(role) && (
              <ActionMenu.Item onClick={openWebTerminal}>Open in Web Terminal</ActionMenu.Item>
            )}
            {canHoopCli(role) && !native && (
              <ActionMenu.Item onClick={() => onOpenCli(role.name)}>Open with Hoop CLI</ActionMenu.Item>
            )}
            {native && (
              <ActionMenu.Item onClick={() => useNativeAccessStore.getState().openAndConnect(role.name)}>
                Open in Native Client
              </ActionMenu.Item>
            )}
            {canTestConnection(role) && (
              <ActionMenu.Item disabled={testing} onClick={() => onTest(role.name)}>
                Test Connection
              </ActionMenu.Item>
            )}
          </ActionMenu>
        )}

        {isAdmin && (
          <ActionMenu
            target={
              <ActionIcon variant="subtle" color="gray" size="sm" aria-label={'More options for ' + role.name}>
                <EllipsisVertical size={16} />
              </ActionIcon>
            }
          >
            {role.managed_by !== 'hoopagent' && (
              <ActionMenu.Item
                onClick={() =>
                  navigate('/roles/' + encodeURIComponent(role.name) + '/configure?from_page=roles-list')
                }
              >
                Configure
              </ActionMenu.Item>
            )}
            {!isSidecarMirror(role) && (
              <ActionMenu.Item danger onClick={() => onDelete(role.name)}>
                Delete
              </ActionMenu.Item>
            )}
          </ActionMenu>
        )}
      </Group>
    </li>
  )
}
