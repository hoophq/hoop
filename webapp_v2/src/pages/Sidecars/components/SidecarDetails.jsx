import { Box, Divider, Group, Image, Paper, Pill, Stack, Text, Title } from '@mantine/core'
import { Info, Lock } from 'lucide-react'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { auditEnabled, configFeatures, hasConfiguration, listenerFeatures, protocolInfo } from '../config'
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

// One lane of the stored configuration (Figma "Listeners": Name, Protocol,
// Listen, Upstream, Features).
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

/**
 * The "Sidecar Details" card (Figma: wizard Overview and the details page).
 *
 * The control plane answers the sidecar's check-in with the configuration it
 * holds for it (gateway/api/sidecar). This card reads that document and never
 * writes it: authoring a configuration from the control plane is not built.
 */
export default function SidecarDetails({ sidecar }) {
  const getIcon = useConnectionIconGetter()
  const config = sidecar.configuration
  const configured = hasConfiguration(config)
  const listeners = config?.listeners ?? []

  return (
    <Stack gap="md">
      {configured ? (
        <Alert color="blue" variant="light" radius="md" icon={<Lock size={16} />}>
          <Text size="sm">
            {
              "The control plane delivers guardrails, masking and analyzer settings to this sidecar. Listeners come from the sidecar's own config file. Editing any of it from here is not built yet."
            }
          </Text>
        </Alert>
      ) : (
        <Alert color="gray" variant="light" radius="md" icon={<Info size={16} />}>
          <Text size="sm">
            The control plane holds no configuration for this sidecar yet. Until it does, the sidecar runs whatever its
            own config file says.
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
            {configured ? (
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
                Nothing delivered by the control plane yet.
              </Text>
            )}
          </Stack>

          <Divider />

          <Stack gap="md">
            <Group gap="sm" align="baseline">
              <Text fw={600}>Listeners</Text>
              <Text size="sm" c="dimmed">
                {`${listeners.length} ${listeners.length === 1 ? 'listener' : 'listeners'}`}
              </Text>
            </Group>

            {configured ? (
              listeners.map((listener, index) => (
                <Box key={listener.name ?? index}>
                  {index > 0 && <Divider color="gray.1" mb="md" />}
                  <Listener listener={listener} config={config} getIcon={getIcon} />
                </Box>
              ))
            ) : (
              <Text size="sm" c="dimmed">
                No listeners here. A connected sidecar reads them from its own config file.
              </Text>
            )}
          </Stack>
        </Stack>
      </Paper>
    </Stack>
  )
}
