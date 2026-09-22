import { Divider, Group, Paper, Stack, Text, Title } from '@mantine/core'
import { Info, Lock } from 'lucide-react'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import EmptyState from '@/layout/EmptyState'
import { formatRelativeTime } from '@/utils/datetime'
import { auditEnabled, configFeatures, hasConfiguration, loadsFromConfigFile } from '../config'
import ListenersTable from '../sections/ListenersTable'
import { sidecarStatus } from '../status'
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
 * holds for it (gateway/api/sidecar). This card READS that document. It writes
 * one thing, and only when the caller hands it somewhere to put the result:
 * the listeners inside it, under `listenerActions`. The wizard's Overview step
 * passes none — it holds its own copy of a sidecar still waiting for the first
 * handshake.
 *
 * Which side OWNS the document is no longer here. It used to be a switch in
 * this header, one click from the status badge, and it retires every rule the
 * control plane distributes to this sidecar at once. A control with that reach
 * does not belong beside a name and a timestamp: it lives in
 * ../sections/SidecarSourceSection, at the foot of the details page, which is
 * where the page also keeps Delete.
 */
export default function SidecarDetails({ sidecar, listenerActions }) {
  const config = sidecar.configuration
  const configured = hasConfiguration(config)
  const fromConfigFile = loadsFromConfigFile(sidecar)

  return (
    <Stack gap="md">
      {fromConfigFile ? (
        <Alert color="blue" variant="light" radius="md" icon={<Lock size={16} />}>
          <Stack gap={4}>
            <Text size="sm">
              {
                "This sidecar loads its configuration from its own config file, and the control plane sends only its license. A running sidecar picks this up on its next check-in, within a minute."
              }
            </Text>
            {configured && (
              <Text size="sm">The configuration below is stored in the control plane and is not applied.</Text>
            )}
          </Stack>
        </Alert>
      ) : configured ? (
        <Alert color="blue" variant="light" radius="md" icon={<Lock size={16} />}>
          <Text size="sm">
            {"The control plane delivers this sidecar's whole configuration, listeners included."}
          </Text>
        </Alert>
      ) : (
        <Alert color="gray" variant="light" radius="md" icon={<Info size={16} />}>
          <Text size="sm">
            The control plane stores no listeners for this sidecar yet. The sidecar imports its own config file on its
            first handshake, and the control plane owns it from then on.
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

          {configured ? (
            <>
              <Stack gap="sm">
                <Text fw={600}>Global settings</Text>
                <Row label="Features">
                  <FeaturePills features={configFeatures(config, sidecar.bound_rules)} />
                </Row>
                <Row label="Audit">
                  <Badge variant={auditEnabled(config) ? 'active' : 'inactive'}>
                    {auditEnabled(config) ? 'Active' : 'Off'}
                  </Badge>
                </Row>
              </Stack>

              <Divider />

              {/* Still authorable while the sidecar runs from its config file:
                  the stored document is what the source section hands back, so
                  it is worth getting right before the flip, not after. */}
              <ListenersTable sidecar={sidecar} {...listenerActions} />
            </>
          ) : fromConfigFile ? (
            // Nothing stored and nothing to store into: the plane sends this
            // sidecar only its license, so an empty listener table with an Add
            // button would offer an edit that changes nothing it runs.
            <EmptyState
              compact
              title="This sidecar runs the configuration in its own config file"
              description="The control plane stores no listeners for it and sends only its license."
            />
          ) : (
            <ListenersTable sidecar={sidecar} {...listenerActions} />
          )}
        </Stack>
      </Paper>
    </Stack>
  )
}
