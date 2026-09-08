import { Divider, Group, Image, Paper, Stack, Text, Title } from '@mantine/core'
import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { formatRelativeTime, sidecarStatus } from '../status'

function Row({ label, children }) {
  return (
    <Group gap="sm" align="center" wrap="nowrap">
      <Text size="sm" c="dimmed" w={96}>
        {label}
      </Text>
      {children}
    </Group>
  )
}

export function SidecarStatusBadge({ sidecar }) {
  const status = sidecarStatus(sidecar)
  return (
    <Tooltip label={status.hint} multiline w={260}>
      <Badge variant={status.badge}>{status.label}</Badge>
    </Tooltip>
  )
}

/**
 * The "Sidecar Details" card (Figma: wizard Overview). Shows what the gateway
 * knows: identity, the last handshake and the connections it fronts. Listener
 * addresses, features and global settings live in the sidecar's own file today
 * and are not here.
 */
export default function SidecarDetails({ sidecar, connectionsByName }) {
  const getIcon = useConnectionIconGetter()
  const names = sidecar.connections ?? []

  return (
    <Paper withBorder radius="md" p="lg">
      <Stack gap="lg">
        <Group justify="space-between" align="center">
          <Title order={3}>Sidecar Details</Title>
          <SidecarStatusBadge sidecar={sidecar} />
        </Group>

        <Stack gap="sm">
          <Row label="Name">
            <Text size="sm" fw={600}>
              {sidecar.name}
            </Text>
          </Row>
          <Row label="Created">
            <Text size="sm">
              {`${new Date(sidecar.created_at).toLocaleString()} by ${sidecar.created_by}`}
            </Text>
          </Row>
          <Row label="Last seen">
            <Text size="sm">
              {sidecar.last_seen_at ? formatRelativeTime(sidecar.last_seen_at) : 'Never'}
            </Text>
          </Row>
          {sidecar.version && (
            <Row label="Version">
              <Text size="sm">{sidecar.version}</Text>
            </Row>
          )}
        </Stack>

        <Divider />

        <Stack gap="sm">
          <Group gap="sm" align="baseline">
            <Text fw={600}>Listeners</Text>
            <Text size="sm" c="dimmed">
              {`${names.length} ${names.length === 1 ? 'connection' : 'connections'}`}
            </Text>
          </Group>
          {names.length === 0 ? (
            <Text size="sm" c="dimmed">
              No connections assigned yet. Assign a connection to this sidecar and it becomes a listener.
            </Text>
          ) : (
            <Stack gap="xs">
              {names.map((name) => {
                const connection = connectionsByName?.get(name)
                return (
                  <Group key={name} gap="sm" wrap="nowrap">
                    {connection && <Image src={getIcon(connection)} alt="" w={16} h={16} fit="contain" />}
                    <Text size="sm" fw={600}>
                      {name}
                    </Text>
                    {connection?.subtype && (
                      <Text size="xs" c="dimmed">
                        {connection.subtype}
                      </Text>
                    )}
                  </Group>
                )
              })}
            </Stack>
          )}
        </Stack>
      </Stack>
    </Paper>
  )
}
