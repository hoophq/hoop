import { Divider, Group, Paper, Stack, Text, Title } from '@mantine/core'
import { Info, Lock } from 'lucide-react'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import { auditEnabled, configFeatures, hasConfiguration } from '../config'
import ListenersTable from '../sections/ListenersTable'
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

/**
 * The "Sidecar Details" card (Figma: wizard Overview and the details page).
 *
 * The control plane answers the sidecar's check-in with the configuration it
 * holds for it (gateway/api/sidecar). This card reads that document; the
 * listener controls are passed in, so the card stays read-only where there is
 * nowhere to put the result — the wizard's Overview step holds its own copy of
 * the sidecar and is still waiting for the first handshake.
 */
export default function SidecarDetails({ sidecar, listenerActions }) {
  const config = sidecar.configuration
  const configured = hasConfiguration(config)

  return (
    <Stack gap="md">
      {configured ? (
        <Alert color="blue" variant="light" radius="md" icon={<Lock size={16} />}>
          <Text size="sm">
            {
              "The control plane holds this sidecar's configuration and serves it on every check-in. Changes to a listener apply when the sidecar restarts."
            }
          </Text>
        </Alert>
      ) : (
        <Alert color="gray" variant="light" radius="md" icon={<Info size={16} />}>
          <Text size="sm">
            {
              'The control plane holds no configuration for this sidecar yet. Until it does, the sidecar runs whatever its own config file says. Adding a listener here takes that over, and the sidecar stops seeding from its file.'
            }
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

          <ListenersTable sidecar={sidecar} {...listenerActions} />
        </Stack>
      </Paper>
    </Stack>
  )
}
