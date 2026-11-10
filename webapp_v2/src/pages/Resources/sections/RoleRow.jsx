import { useNavigate } from 'react-router-dom'
import { Group } from '@mantine/core'
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
import RoleSummary from '@/pages/Resources/components/RoleSummary'
import classes from '@/pages/Resources/List.module.css'

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

  const openWebTerminal = () => {
    useBridgeStore.getState().clearDatabaseSchema()
    navigate('/client?role=' + encodeURIComponent(role.name))
    useBridgeStore.getState().syncPrimaryConnectionFromUrl()
  }

  return (
    <li className={classes.row}>
      <RoleSummary role={role} iconSrc={iconSrc} iconAlt={iconAlt} />

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
