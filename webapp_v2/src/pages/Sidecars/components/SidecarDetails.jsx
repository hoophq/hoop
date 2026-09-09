import { Box, Divider, Group, Image, Paper, Pill, Stack, Text, Title } from '@mantine/core'
import { Lock } from 'lucide-react'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { auditEnabled, configFeatures, listenerFeatures, protocolInfo } from '../config'
import { formatRelativeTime, sidecarStatus } from '../status'
import FeaturePills from './FeaturePills'

const LABEL_WIDTH = 88

function Row({ label, children }) {
  return (
    <Group gap="sm" align="center" wrap="nowrap">
      <Text size="sm" c="dimmed" w={LABEL_WIDTH} flex="0 0 auto">
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
      <Badge variant={status.badge} flex="0 0 auto">
        {status.label}
      </Badge>
    </Tooltip>
  )
}

function ProtocolPill({ protocol, getIcon }) {
  const info = protocolInfo(protocol)
  return (
    <Pill>
      <Group gap={6} wrap="nowrap">
        <Image src={getIcon({ subtype: info.subtype })} alt="" w={14} h={14} fit="contain" />
        <span>{info.label}</span>
      </Group>
    </Pill>
  )
}

// One lane of the reported config (Figma "Listeners": Name, Protocol, Listen,
// Upstream, Features). Read-only: the file in the sidecar is the source.
function Listener({ listener, config, getIcon }) {
  return (
    <Stack gap="sm">
      <Row label="Name">
        <Text size="sm" fw={600}>
          {listener.name}
        </Text>
      </Row>
      <Row label="Protocol">
        <ProtocolPill protocol={listener.protocol} getIcon={getIcon} />
      </Row>
      <Row label="Listen">
        <Group gap="sm" wrap="nowrap">
          <Text size="sm" ff="monospace">
            {listener.listen}
          </Text>
          <Text size="xs" c="dimmed">
            Sidecar address where outside clients connect to
          </Text>
        </Group>
      </Row>
      <Row label="Upstream">
        <Group gap="sm" wrap="nowrap">
          <Text size="sm" ff="monospace">
            {listener.upstream}
          </Text>
          <Text size="xs" c="dimmed">
            Internal address of your real resource
          </Text>
        </Group>
      </Row>
      <Row label="Features">
        <FeaturePills features={listenerFeatures(listener, config)} />
      </Row>
    </Stack>
  )
}

// Before the handshake there is no config: the lanes are the connections an
// admin assigned in the control plane, by name.
function AssignedConnections({ names, connectionsByName, getIcon }) {
  if (names.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        No listeners yet. They appear here when the sidecar connects and reports its configuration.
      </Text>
    )
  }
  return (
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
  )
}

/**
 * The "Sidecar Details" card (Figma: wizard Overview and the details page).
 * Everything here is what the sidecar reports; the rules live in its config
 * file and are not edited from the control plane.
 */
export default function SidecarDetails({ sidecar, connectionsByName }) {
  const getIcon = useConnectionIconGetter()
  const config = sidecar.config
  const listeners = config?.listeners ?? []

  return (
    <Stack gap="md">
      {config && (
        <Alert color="blue" variant="light" radius="md" icon={<Lock size={16} />}>
          <Text size="sm">
            The rules below are managed in the sidecar and cannot be edited here. The control plane shows what the
            sidecar reports.
          </Text>
        </Alert>
      )}

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
              <Text size="sm">{`${new Date(sidecar.created_at).toLocaleString()} by ${sidecar.created_by}`}</Text>
            </Row>
            <Row label="Last seen">
              <Text size="sm">{sidecar.last_seen_at ? formatRelativeTime(sidecar.last_seen_at) : 'Never'}</Text>
            </Row>
            {sidecar.version && (
              <Row label="Version">
                <Text size="sm">{sidecar.version}</Text>
              </Row>
            )}
          </Stack>

          <Divider />

          <Stack gap="sm">
            <Text fw={600}>Global settings</Text>
            {config ? (
              <>
                <Row label="Features">
                  <FeaturePills features={configFeatures(config)} />
                </Row>
                <Row label="Audit">
                  <Badge variant={auditEnabled(config) ? 'active' : 'inactive'}>
                    {auditEnabled(config) ? 'Active' : 'Off'}
                  </Badge>
                </Row>
              </>
            ) : (
              <Text size="sm" c="dimmed">
                Reported by the sidecar on its first handshake.
              </Text>
            )}
          </Stack>

          <Divider />

          <Stack gap="md">
            <Group gap="sm" align="baseline">
              <Text fw={600}>Listeners</Text>
              <Text size="sm" c="dimmed">
                {config
                  ? `${listeners.length} ${listeners.length === 1 ? 'listener' : 'listeners'}`
                  : `${(sidecar.connections ?? []).length} assigned`}
              </Text>
            </Group>

            {config ? (
              listeners.map((listener, index) => (
                <Box key={listener.name ?? index}>
                  {index > 0 && <Divider color="gray.1" mb="md" />}
                  <Listener listener={listener} config={config} getIcon={getIcon} />
                </Box>
              ))
            ) : (
              <AssignedConnections
                names={sidecar.connections ?? []}
                connectionsByName={connectionsByName}
                getIcon={getIcon}
              />
            )}
          </Stack>
        </Stack>
      </Paper>
    </Stack>
  )
}
