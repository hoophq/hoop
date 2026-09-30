import { useState } from 'react'
import { Divider, Group, Paper, Stack, Text, Title } from '@mantine/core'
import { Info, Lock } from 'lucide-react'
import ActionMenu from '@/components/ActionMenu'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Tooltip from '@/components/Tooltip'
import EmptyState from '@/layout/EmptyState'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { formatRelativeTime } from '@/utils/datetime'
import { showSnackbar } from '@/utils/snackbar'
import { auditEnabled, hasConfiguration, usesConfigFile } from '../config'
import { opaSummary, resolveOPA } from '../features'
import ListenersTable from '../sections/ListenersTable'
import { sidecarStatus } from '../status'
import SidecarSourceModal from '../sections/SidecarSourceModal'
import Callout from './Callout'
import FeatureAccordions from './FeatureAccordions'

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
      <Badge variant={status.badge} dot flex="0 0 auto">
        {status.label}
      </Badge>
    </Tooltip>
  )
}

// What an owner switch deleted and unbound, for the snackbar. Empty when it
// touched no rule.
function detachedSummary(detached) {
  if (!detached) return ''
  const names = (group) => [...(group?.guardrails ?? []), ...(group?.data_masking ?? []), ...(group?.analyzers ?? [])]
  const deleted = names(detached.deleted)
  const unbound = names(detached.unbound)
  return [
    deleted.length > 0 && `Deleted: ${deleted.join(', ')}.`,
    unbound.length > 0 && `Removed from this sidecar: ${unbound.join(', ')}.`,
  ]
    .filter(Boolean)
    .join(' ')
}

function SourceCallout({ fromFile, configured, editable, onSwitch }) {
  if (fromFile) {
    return (
      <Callout
        icon={Lock}
        color="indigo.0"
        action={
          editable && (
            <Button variant="light" size="xs" onClick={() => onSwitch(false)} flex="0 0 auto">
              Use the control plane
            </Button>
          )
        }
      >
        <Text size="sm">
          {
            'This sidecar loads its configuration from its own config file, and the control plane sends only its license. A running sidecar picks this up on its next check-in, within a minute.'
          }
        </Text>
        {configured && (
          <Text size="sm">The configuration below is stored in the control plane and is not applied.</Text>
        )}
      </Callout>
    )
  }
  if (configured) {
    return (
      <Callout
        icon={Lock}
        color="indigo.0"
        action={
          editable && (
            <Button variant="light" size="xs" onClick={() => onSwitch(true)} flex="0 0 auto">
              Change config
            </Button>
          )
        }
      >
        <Text size="sm">
          {
            "The control plane owns this sidecar's configuration and serves it on every check-in. Edit it here, not in the sidecar's own file."
          }
        </Text>
      </Callout>
    )
  }
  return (
    <Callout icon={Info} color="gray.0">
      <Text size="sm">
        The control plane stores no listeners for this sidecar yet. The sidecar imports its own config file on its
        first handshake, and the control plane owns it from then on.
      </Text>
    </Callout>
  )
}

/**
 * The "Sidecar Details" card, shared by the wizard Overview and the details page.
 *
 * The control plane answers the sidecar's check-in with the configuration it
 * holds for it (gateway/api/sidecar). This card reads that document and writes
 * one thing, only when the caller hands it somewhere to put the result: which
 * side owns the document, under `editable`. Listeners are authored through
 * `listenerActions`, the sidecar is deleted through `onDelete`. The wizard's
 * Overview step passes none of them — it holds its own copy of a sidecar that
 * is still waiting for the first handshake.
 */
export default function SidecarDetails({ sidecar, editable, listenerActions, onDelete }) {
  const setUsesConfigFile = useSidecarStore((s) => s.setUsesConfigFile)
  const refreshSidecar = useSidecarStore((s) => s.refreshSidecar)
  const config = sidecar.configuration
  const configured = hasConfiguration(config)
  const fromFile = usesConfigFile(sidecar)
  const opa = configured ? resolveOPA(null, config) : null
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
    // The counts must be what the gateway holds now, not what this page
    // loaded. A failed refresh leaves the loaded record on screen.
    refreshSidecar(sidecar.id).catch(() => {})
  }

  const confirmSource = async () => {
    if (!asking) return
    setSaving(true)
    try {
      const updated = await setUsesConfigFile(sidecar.id, target)
      setAsking(false)
      const summary = detachedSummary(updated.detached_rules)
      if (summary) showSnackbar({ level: 'success', text: 'Configuration source changed.', description: summary })
    } catch (error) {
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
    <>
      <Paper withBorder radius="md" p="lg">
        <Stack gap="lg">
          <SourceCallout
            fromFile={fromFile}
            configured={configured}
            editable={editable && !saving && !asking}
            onSwitch={ask}
          />

          <Group justify="space-between" align="center">
            <Title order={3}>Sidecar Details</Title>
            <Group gap="lg" align="center">
              <SidecarStatusBadge sidecar={sidecar} />
              {onDelete && (
                <ActionMenu width={200}>
                  <ActionMenu.Item danger onClick={onDelete}>
                    Delete sidecar
                  </ActionMenu.Item>
                </ActionMenu>
              )}
            </Group>
          </Group>

          <Stack gap="sm">
            <Text fw={600}>Global settings</Text>
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
            {configured && (
              <>
                <Row label="Admin">
                  <Text size="sm" ff={config.admin?.listen ? 'monospace' : undefined}>
                    {config.admin?.listen || 'Off'}
                  </Text>
                </Row>
                <Row label="Log level">
                  <Text size="sm">{config.log_level || 'info'}</Text>
                </Row>
                <Row label="OPA">
                  <Text size="sm">{opa ? opaSummary(opa) : 'Off'}</Text>
                </Row>
                <Row label="Audit">
                  <Badge variant={auditEnabled(config) ? 'active' : 'inactive'}>
                    {auditEnabled(config) ? 'Active' : 'Off'}
                  </Badge>
                </Row>
              </>
            )}
          </Stack>

          {configured ? (
            <>
              <Stack gap="sm">
                <Text fw={600}>Features</Text>
                <FeatureAccordions config={config} boundRules={sidecar.bound_rules} />
              </Stack>

              <Divider />

              {/* The file owns the listeners, so an edit here would change nothing it runs. */}
              <ListenersTable sidecar={sidecar} {...(fromFile ? {} : listenerActions)} />
            </>
          ) : fromFile ? (
            // Nothing stored and nothing to store into: the plane sends this
            // sidecar only its license, so an empty listener table with an Add
            // button would offer an edit that changes nothing it runs.
            <>
              <Divider />
              <EmptyState
                compact
                title="This sidecar runs the configuration in its own config file"
                description="The control plane stores no listeners for it and sends only its license."
              />
            </>
          ) : (
            <>
              <Divider />
              <ListenersTable sidecar={sidecar} {...listenerActions} />
            </>
          )}
        </Stack>
      </Paper>

      <SidecarSourceModal
        opened={asking}
        toConfigFile={target}
        boundRules={sidecar.bound_rules ?? []}
        onClose={() => setAsking(false)}
        onConfirm={confirmSource}
        loading={saving}
      />
    </>
  )
}
