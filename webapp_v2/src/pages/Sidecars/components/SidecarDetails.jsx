import { useState } from 'react'
import { Box, Divider, Group, Image, Paper, Pill, Stack, Text, Title } from '@mantine/core'
import { Info, Lock } from 'lucide-react'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Switch from '@/components/Switch'
import Tooltip from '@/components/Tooltip'
import EmptyState from '@/layout/EmptyState'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { showSnackbar } from '@/utils/snackbar'
import {
  auditEnabled,
  configFeatures,
  hasConfiguration,
  listenerFeatures,
  loadsFromDisk,
  protocolInfo,
} from '../config'
import { formatRelativeTime, sidecarStatus } from '../status'
import SidecarSourceModal from '../sections/SidecarSourceModal'
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
 * holds for it (gateway/api/sidecar). This card reads that document; the one
 * thing it writes is which side owns it, and only when onSourceChange is
 * given — the wizard renders the same card for a sidecar whose source is
 * chosen afterwards. onSourceChange receives the updated record.
 */
export default function SidecarDetails({ sidecar, onSourceChange }) {
  const getIcon = useConnectionIconGetter()
  const setLoadFromDisk = useSidecarStore((s) => s.setLoadFromDisk)
  const config = sidecar.configuration
  const configured = hasConfiguration(config)
  const fromDisk = loadsFromDisk(sidecar)
  const listeners = config?.listeners ?? []
  // The value awaiting confirmation, and whether the dialog is up. Two states
  // rather than one: Mantine keeps the modal mounted through its exit
  // transition, and a target cleared on close would rewrite the copy of the
  // dialog the user is watching leave.
  const [target, setTarget] = useState(false)
  const [asking, setAsking] = useState(false)
  const [saving, setSaving] = useState(false)

  const ask = (next) => {
    setTarget(next)
    setAsking(true)
  }

  const confirmSource = async () => {
    if (!asking) return
    setSaving(true)
    try {
      onSourceChange(await setLoadFromDisk(sidecar.id, target))
      setAsking(false)
    } catch (error) {
      // The switch renders the stored value, so it is already back where it
      // was once the dialog closes.
      setAsking(false)
      showSnackbar({
        level: 'error',
        text: 'Could not change the configuration source.',
        description: error.response?.data?.message ?? error.message,
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Stack gap="md">
      {fromDisk ? (
        <Alert color="blue" variant="light" radius="md" icon={<Lock size={16} />}>
          <Stack gap={4}>
            <Text size="sm">
              {
                'This sidecar loads its configuration from its own config file, and the control plane sends only its license. A running sidecar picks this up on its next check-in, within a minute.'
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
            <Group gap="lg" align="center">
              {onSourceChange && (
                <Switch
                  label="Load configuration from disk"
                  labelPosition="left"
                  checked={fromDisk}
                  disabled={saving || asking}
                  onChange={(event) => ask(event.currentTarget.checked)}
                />
              )}
              <SidecarStatusBadge sidecar={sidecar} />
            </Group>
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
                  <FeaturePills features={configFeatures(config)} />
                </Row>
                <Row label="Audit">
                  <Badge variant={auditEnabled(config) ? 'active' : 'inactive'}>
                    {auditEnabled(config) ? 'Active' : 'Off'}
                  </Badge>
                </Row>
              </Stack>

              <Divider />

              <Stack gap="md">
                <Group gap="sm" align="baseline">
                  <Text fw={600}>Listeners</Text>
                  <Text size="sm" c="dimmed">
                    {`${listeners.length} ${listeners.length === 1 ? 'listener' : 'listeners'}`}
                  </Text>
                </Group>

                {listeners.map((listener, index) => (
                  <Box key={listener.name ?? index}>
                    {index > 0 && <Divider color="gray.1" mb="md" />}
                    <Listener listener={listener} config={config} getIcon={getIcon} />
                  </Box>
                ))}
              </Stack>
            </>
          ) : (
            <EmptyState
              compact
              title={
                fromDisk
                  ? 'This sidecar runs the configuration in its own config file'
                  : 'The control plane stores no listeners yet'
              }
              description={
                fromDisk
                  ? 'The control plane stores no listeners for it and sends only its license.'
                  : "Nothing is delivered to this sidecar yet. A connected sidecar reads its listeners from its own config file and imports them here."
              }
            />
          )}
        </Stack>
      </Paper>

      <SidecarSourceModal
        opened={asking}
        toDisk={target}
        storedListeners={listeners.length}
        onClose={() => setAsking(false)}
        onConfirm={confirmSource}
        loading={saving}
      />
    </Stack>
  )
}
