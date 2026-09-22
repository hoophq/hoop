import { useNavigate } from 'react-router-dom'
import { Group, Image, Text } from '@mantine/core'
import ActionMenu from '@/components/ActionMenu'
import Badge from '@/components/Badge'
import Table from '@/components/Table'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { formatRelativeTime } from '@/utils/datetime'
import { configFeatures, loadsFromConfigFile, protocolInfo } from '../config'
import FeaturePills from '../components/FeaturePills'
import { SidecarStatusBadge } from '../components/SidecarDetails'

// A fleet row is one line per sidecar, so the lane list cannot grow with the
// sidecar: a relay in front of fifty databases would push every other row off
// the screen. Four names say what kind of sidecar this is, which is what the
// fleet view answers; the rest are one click away in its own table.
const LANES_SHOWN = 4

// The lanes of the configuration the control plane stores for this sidecar. A
// sidecar with none has nothing to serve and refuses to start, so the empty
// cell says that rather than "No listeners".
//
// A released sidecar runs the lanes in its own config file, which the plane
// never sees, so the stored ones are not what it is serving and are not shown
// here as if they were.
function Listeners({ sidecar, getIcon }) {
  const listeners = sidecar.configuration?.listeners ?? []

  if (loadsFromConfigFile(sidecar)) {
    return (
      <Text size="sm" c="dimmed">
        In its config file
      </Text>
    )
  }
  if (listeners.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        Not configured
      </Text>
    )
  }
  const hidden = listeners.length - LANES_SHOWN
  return (
    <Group gap="xs">
      {listeners.slice(0, LANES_SHOWN).map((listener) => (
        <Badge
          key={listener.name}
          tag
          chip
          variant="light"
          color="gray"
          icon={
            <Image
              src={getIcon({ subtype: protocolInfo(listener.protocol).subtype })}
              alt=""
              w={14}
              h={14}
              fit="contain"
            />
          }
        >
          {listener.name}
        </Badge>
      ))}
      {hidden > 0 && (
        <Text size="xs" c="dimmed">
          {`+${hidden} more`}
        </Text>
      )}
    </Group>
  )
}

// Figma: "License has sidecards" table. No Edit on the row: a listener is
// authored on the sidecar's own page, so a row opens its details and the fleet
// stays one line per sidecar.
export default function SidecarsTable({ sidecars, onDelete }) {
  const navigate = useNavigate()
  const getIcon = useConnectionIconGetter()

  return (
    <Table>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>Sidecar</Table.Th>
          <Table.Th>Status</Table.Th>
          <Table.Th>Source</Table.Th>
          <Table.Th>Listeners</Table.Th>
          <Table.Th>Features</Table.Th>
          <Table.Th aria-label="Actions" w={56} />
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {sidecars.map((sidecar) => (
          <Table.Tr key={sidecar.id}>
            {/* A hyphenated name is a legal break point, so a narrower cell
                splits "payments-sidecar" across two lines. */}
            <Table.Td miw={190}>
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
            <Table.Td miw={140}>
              <Badge variant="light" color={loadsFromConfigFile(sidecar) ? 'gray' : 'blue'} fullLabel>
                {loadsFromConfigFile(sidecar) ? 'Config file' : 'Control plane'}
              </Badge>
            </Table.Td>
            <Table.Td>
              <Listeners sidecar={sidecar} getIcon={getIcon} />
            </Table.Td>
            <Table.Td>
              {loadsFromConfigFile(sidecar) ? (
                <Text size="sm" c="dimmed">
                  Not delivered
                </Text>
              ) : (
                /* Icons, like the listener table: three labelled chips push a
                   fleet row to three lines the moment the listener names beside
                   them get long. */
                <FeaturePills compact features={configFeatures(sidecar.configuration, sidecar.bound_rules)} />
              )}
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
