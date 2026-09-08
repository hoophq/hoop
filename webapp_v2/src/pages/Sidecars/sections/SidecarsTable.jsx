import { useNavigate } from 'react-router-dom'
import { Group, Image, Pill, Text } from '@mantine/core'
import ActionMenu from '@/components/ActionMenu'
import Table from '@/components/Table'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { SidecarStatusBadge } from '../components/SidecarDetails'
import { formatRelativeTime } from '../status'

function Listeners({ names, connectionsByName, getIcon }) {
  if (names.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        No listeners
      </Text>
    )
  }
  return (
    <Group gap="xs">
      {names.map((name) => {
        const connection = connectionsByName.get(name)
        return (
          <Pill key={name}>
            <Group gap={6} wrap="nowrap">
              {connection && <Image src={getIcon(connection)} alt="" w={14} h={14} fit="contain" />}
              <span>{name}</span>
            </Group>
          </Pill>
        )
      })}
    </Group>
  )
}

// Figma: "License has sidecards" table. Features is not a column: the API has
// no per-sidecar features to show.
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
          <Table.Th>Created</Table.Th>
          <Table.Th aria-label="Actions" w={56} />
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {sidecars.map((sidecar) => (
          <Table.Tr key={sidecar.id}>
            <Table.Td>
              <Text size="sm" fw={600}>
                {sidecar.name}
              </Text>
            </Table.Td>
            <Table.Td>
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
              <Listeners names={sidecar.connections ?? []} connectionsByName={connectionsByName} getIcon={getIcon} />
            </Table.Td>
            <Table.Td>
              <Text size="sm" c="dimmed">
                {new Date(sidecar.created_at).toLocaleDateString()}
              </Text>
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
