import { useNavigate } from 'react-router-dom'
import { Group, Image, Pill, Text } from '@mantine/core'
import ActionMenu from '@/components/ActionMenu'
import Table from '@/components/Table'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { configFeatures, protocolInfo } from '../config'
import FeaturePills from '../components/FeaturePills'
import { SidecarStatusBadge } from '../components/SidecarDetails'
import { formatRelativeTime } from '../status'

// The lanes of the configuration the control plane stores for this sidecar. A
// sidecar with none has nothing to serve and refuses to start, so the empty
// cell says that rather than "No listeners".
function Listeners({ sidecar, getIcon }) {
  const listeners = sidecar.configuration?.listeners ?? []

  if (listeners.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        Not configured
      </Text>
    )
  }
  return (
    <Group gap="xs">
      {listeners.map((listener) => (
        <Pill key={listener.name}>
          <Group gap={6} wrap="nowrap">
            <Image
              src={getIcon({ subtype: protocolInfo(listener.protocol).subtype })}
              alt=""
              w={14}
              h={14}
              fit="contain"
            />
            <span>{listener.name}</span>
          </Group>
        </Pill>
      ))}
    </Group>
  )
}

// Figma: "License has sidecards" table. No Edit: writing the configuration
// from the control plane is not built yet, so a row opens its read-only
// details.
export default function SidecarsTable({ sidecars, onDelete }) {
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
              <Listeners sidecar={sidecar} getIcon={getIcon} />
            </Table.Td>
            <Table.Td>
              <FeaturePills features={configFeatures(sidecar.configuration)} />
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
