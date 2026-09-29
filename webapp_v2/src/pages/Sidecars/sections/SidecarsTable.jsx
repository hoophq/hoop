import { Link } from 'react-router-dom'
import { Group, Image, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Table from '@/components/Table'
import Tooltip from '@/components/Tooltip'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { formatRelativeTime } from '@/utils/datetime'
import { configFeatures, usesConfigFile, protocolInfo } from '../config'
import { listenerLabel } from '../listeners'
import FeaturePills from '../components/FeaturePills'
import { SidecarStatusBadge } from '../components/SidecarDetails'

// A fleet row is one line per sidecar, so the lane list cannot grow with the
// sidecar: a relay in front of fifty databases would push every other row off
// the screen. Four protocol icons say what kind of sidecar this is, which is
// what the fleet view answers; the names are one click away in its own table.
const LANES_SHOWN = 4

// `others` carries the ref and hover handlers Tooltip clones onto its child;
// dropping them is a tooltip that never opens.
function ProtocolIcon({ protocol, getIcon, ...others }) {
  const info = protocolInfo(protocol)
  // grpc and spanner are not hoop connection types and have no icon of their
  // own; the fallback would show something unrelated, so they keep their label.
  if (!info.subtype) {
    return (
      <Badge tag chip variant="light" color="gray" {...others}>
        {info.label}
      </Badge>
    )
  }
  return (
    <Badge
      tag
      chip
      variant="light"
      color="gray"
      icon={<Image src={getIcon({ subtype: info.subtype })} alt="" w={14} h={14} fit="contain" />}
      aria-label={info.label}
      {...others}
    />
  )
}

// The lanes of the configuration the control plane stores for this sidecar. A
// sidecar with none has nothing to serve and refuses to start, so the empty
// cell says that rather than "No listeners".
//
// A released sidecar runs the lanes in its own config file, which the plane
// never sees, so the stored ones are not what it is serving and are not shown
// here as if they were.
function Listeners({ sidecar, getIcon }) {
  const listeners = sidecar.configuration?.listeners ?? []

  if (usesConfigFile(sidecar)) {
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
  // One lane reads as its protocol (Figma: "PostgreSQL"); more read as icons.
  if (listeners.length === 1) {
    const [listener] = listeners
    const info = protocolInfo(listener.protocol)
    return (
      <Tooltip label={listenerLabel(listener, 0)}>
        <Badge
          tag
          chip
          variant="light"
          color="gray"
          icon={
            info.subtype ? (
              <Image src={getIcon({ subtype: info.subtype })} alt="" w={14} h={14} fit="contain" />
            ) : undefined
          }
        >
          {info.label}
        </Badge>
      </Tooltip>
    )
  }
  const hidden = listeners.length - LANES_SHOWN
  return (
    <Group gap={4} wrap="nowrap">
      {listeners.slice(0, LANES_SHOWN).map((listener, index) => (
        <Tooltip
          key={`${listenerLabel(listener, index)}-${index}`}
          label={`${listenerLabel(listener, index)} · ${protocolInfo(listener.protocol).label}`}
        >
          <ProtocolIcon protocol={listener.protocol} getIcon={getIcon} />
        </Tooltip>
      ))}
      {hidden > 0 && (
        <Text size="xs" c="dimmed">
          {`+${hidden}`}
        </Text>
      )}
    </Group>
  )
}

// Figma: "Sidecars" table. A row opens its details; listeners and the sidecar
// itself are authored on that page, so the fleet stays one line per sidecar.
export default function SidecarsTable({ sidecars }) {
  const getIcon = useConnectionIconGetter()

  return (
    <Table>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>Sidecar</Table.Th>
          <Table.Th>Status</Table.Th>
          <Table.Th>Source</Table.Th>
          <Table.Th>Listeners</Table.Th>
          <Table.Th>Policies</Table.Th>
          <Table.Th aria-label="Actions" w={96} />
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {sidecars.map((sidecar) => {
          const features = usesConfigFile(sidecar) ? [] : configFeatures(sidecar.configuration, sidecar.bound_rules)
          return (
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
                <Badge variant="light" color={usesConfigFile(sidecar) ? 'gray' : 'blue'} fullLabel>
                  {usesConfigFile(sidecar) ? 'Config file' : 'Control plane'}
                </Badge>
              </Table.Td>
              <Table.Td>
                <Listeners sidecar={sidecar} getIcon={getIcon} />
              </Table.Td>
              <Table.Td miw={150}>
                {usesConfigFile(sidecar) ? (
                  <Text size="sm" c="dimmed">
                    Not delivered
                  </Text>
                ) : features.length > 0 ? (
                  <FeaturePills compact features={features} />
                ) : (
                  <Text size="sm" c="dimmed">
                    No policies configured
                  </Text>
                )}
              </Table.Td>
              <Table.Td>
                <Button
                  component={Link}
                  to={`/sidecars/${encodeURIComponent(sidecar.id)}`}
                  variant="subtle"
                  size="compact-sm"
                >
                  Details
                </Button>
              </Table.Td>
            </Table.Tr>
          )
        })}
      </Table.Tbody>
    </Table>
  )
}
