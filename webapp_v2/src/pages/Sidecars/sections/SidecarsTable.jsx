import { useNavigate } from 'react-router-dom'
import { Group, Image, Pill, Text } from '@mantine/core'
import ActionMenu from '@/components/ActionMenu'
import Table from '@/components/Table'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { configFeatures, protocolInfo } from '../config'
import FeaturePills from '../components/FeaturePills'
import { SidecarStatusBadge } from '../components/SidecarDetails'
import { formatRelativeTime } from '../status'

// Listeners come from the reported config once the sidecar connected; before
// that, from the connections an admin assigned by name.
function Listeners({ sidecar, connectionsByName, getIcon }) {
  const listeners = sidecar.config?.listeners
  const items = listeners
    ? listeners.map((l) => ({ name: l.name, icon: getIcon({ subtype: protocolInfo(l.protocol).subtype }) }))
    : (sidecar.connections ?? []).map((name) => {
        const connection = connectionsByName.get(name)
        return { name, icon: connection ? getIcon(connection) : null }
      })

  if (items.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        No listeners
      </Text>
    )
  }
  return (
    <Group gap="xs">
      {items.map(({ name, icon }) => (
        <Pill key={name}>
          <Group gap={6} wrap="nowrap">
            {icon && <Image src={icon} alt="" w={14} h={14} fit="contain" />}
            <span>{name}</span>
          </Group>
        </Pill>
      ))}
    </Group>
  )
}

// Figma: "License has sidecards" table. No Edit: the rules live in the
// sidecar's config file, so a row opens its read-only details.
export default function SidecarsTable({ sidecars, connectionsByName, onDelete }) {
  const navigate = useNavigate()
  const getIcon = useConnectionIconGetter()

  return (
    <Table>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>Sidecar</Table.Th>
          <Table.Th>Status</Table.Th>
          <Table.Th>Listeners</Table.Th>
          <Table.Th>Features</Table.Th>
          <Table.Th aria-label="Actions" w={56} />
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {sidecars.map((sidecar) => (
          <Table.Tr key={sidecar.id}>
            <Table.Td miw={160}>
              <Text size="sm" fw={600}>
                {sidecar.name}
              </Text>
            </Table.Td>
            <Table.Td miw={200}>
              <Group gap="xs" wrap="nowrap">
                <SidecarStatusBadge sidecar={sidecar} />
                {sidecar.last_seen_at && (
                  <Text size="xs" c="dimmed">
                    {formatRelativeTime(sidecar.last_seen_at)}
                  </Text>
                )}
              </Group>
            </Table.Td>
            <Table.Td>
              <Listeners sidecar={sidecar} connectionsByName={connectionsByName} getIcon={getIcon} />
            </Table.Td>
            <Table.Td>
              <FeaturePills features={configFeatures(sidecar.config)} />
            </Table.Td>
            <Table.Td>
              <ActionMenu>
                <ActionMenu.Item onClick={() => navigate(`/sidecars/${encodeURIComponent(sidecar.id)}`)}>
                  Details
                </ActionMenu.Item>
                <ActionMenu.Divider />
                <ActionMenu.Item danger onClick={() => onDelete(sidecar)}>
                  Delete
                </ActionMenu.Item>
              </ActionMenu>
            </Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  )
}
