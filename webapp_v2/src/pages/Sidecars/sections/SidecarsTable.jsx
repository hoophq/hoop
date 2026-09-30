import { Link } from 'react-router-dom'
import { Image, Text } from '@mantine/core'
import Avatar from '@/components/Avatar'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Table from '@/components/Table'
import Tooltip from '@/components/Tooltip'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { configFeatures, usesConfigFile, protocolInfo } from '../config'
import { listenerLabel } from '../listeners'
import FeaturePills from '../components/FeaturePills'
import { SidecarStatusBadge } from '../components/SidecarDetails'

// A fleet row is one line per sidecar, so the lane list cannot grow with the
// sidecar: a relay in front of fifty databases would push every other row off
// the screen. Four protocol icons say what kind of sidecar this is, which is
// what the fleet view answers; the names are one click away in its own table.
const LANES_SHOWN = 4

const AVATAR_SIZE = 24
const ICON_SIZE = 14

// `others` carries the ref and hover handlers Tooltip clones onto its child;
// dropping them is a tooltip that never opens.
function ProtocolAvatar({ protocol, getIcon, ...others }) {
  const info = protocolInfo(protocol)
  // grpc and spanner are not hoop connection types and have no icon of their
  // own; the fallback would show something unrelated, so they show letters.
  return (
    <Avatar size={AVATAR_SIZE} bg="gray.1" color="gray" variant="light" aria-label={info.label} {...others}>
      {info.subtype ? (
        <Image src={getIcon({ subtype: info.subtype })} alt="" w={ICON_SIZE} h={ICON_SIZE} fit="contain" />
      ) : (
        info.label.slice(0, 2)
      )}
    </Avatar>
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
      <Text size="xs" fw={500} c="gray.5">
        In its config file
      </Text>
    )
  }
  if (listeners.length === 0) {
    return (
      <Text size="xs" fw={500} c="gray.5">
        Not configured
      </Text>
    )
  }
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
    <Avatar.Group>
      {listeners.slice(0, LANES_SHOWN).map((listener, index) => (
        <Tooltip
          key={`${listenerLabel(listener, index)}-${index}`}
          label={`${listenerLabel(listener, index)} · ${protocolInfo(listener.protocol).label}`}
        >
          <ProtocolAvatar protocol={listener.protocol} getIcon={getIcon} />
        </Tooltip>
      ))}
      {hidden > 0 && (
        <Tooltip label={listeners.slice(LANES_SHOWN).map(listenerLabel).join(', ')}>
          <Avatar size={AVATAR_SIZE} bg="gray.0" color="gray" variant="light" fw={700}>
            {`+${hidden}`}
          </Avatar>
        </Tooltip>
      )}
    </Avatar.Group>
  )
}

export default function SidecarsTable({ sidecars }) {
  const getIcon = useConnectionIconGetter()

  return (
    <Table verticalSpacing="md">
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
                <Text size="sm" fw={500}>
                  {sidecar.name}
                </Text>
              </Table.Td>
              <Table.Td miw={200}>
                <SidecarStatusBadge sidecar={sidecar} />
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
                  <Text size="xs" fw={500} c="gray.5">
                    Not delivered
                  </Text>
                ) : features.length > 0 ? (
                  <FeaturePills compact features={features} />
                ) : (
                  <Text size="xs" fw={500} c="gray.5">
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
                  Edit
                </Button>
              </Table.Td>
            </Table.Tr>
          )
        })}
      </Table.Tbody>
    </Table>
  )
}
