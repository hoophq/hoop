import { useState } from 'react'
import { Divider, Group, Paper, Stack, Text, Title } from '@mantine/core'
import { Info, Lock, TriangleAlert } from 'lucide-react'
import ActionMenu from '@/components/ActionMenu'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Tooltip from '@/components/Tooltip'
import EmptyState from '@/layout/EmptyState'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { hasConfiguration, usesConfigFile } from '../config'
import GlobalSettings from '../sections/GlobalSettings'
import ListenersTable from '../sections/ListenersTable'
import { sidecarStatus } from '../status'
import SidecarSourceModal from '../sections/SidecarSourceModal'
import Callout from './Callout'
import FeatureRules from './FeatureRules'

// The reason a sidecar gave is its own words, so it renders as code under the
// sentence rather than inside it.
function StatusHint({ status }) {
  if (!status.detail) return status.hint
  return (
    <Stack gap={4}>
      <Text size="sm">{status.hint}</Text>
      <Text size="xs" ff="monospace">
        {status.detail}
      </Text>
    </Stack>
  )
}

export function SidecarStatusBadge({ sidecar }) {
  const status = sidecarStatus(sidecar)
  return (
    <Tooltip label={<StatusHint status={status} />} multiline w={status.detail ? 340 : 260}>
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
 * to it only when the caller hands it somewhere to put the result, under
 * `editable`: which side owns the document, and the Global settings rows.
 * Listeners are authored through `listenerActions`, the sidecar is deleted
 * through `onDelete`. The wizard's Overview step passes none of them — it
 * holds its own copy of a sidecar that is still waiting for the first
 * handshake.
 */
export default function SidecarDetails({ sidecar, editable, listenerActions, onDelete }) {
  const setUsesConfigFile = useSidecarStore((s) => s.setUsesConfigFile)
  const refreshSidecar = useSidecarStore((s) => s.refreshSidecar)
  const config = sidecar.configuration
  const configured = hasConfiguration(config)
  const fromFile = usesConfigFile(sidecar)
  const deprecations = sidecar.deprecations ?? []
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

          {/* Read-only on the file's config. The key drops an open editor when that flips. */}
          <GlobalSettings key={String(editable && !fromFile)} sidecar={sidecar} editable={editable && !fromFile} />

          {deprecations.length > 0 && (
            <Callout icon={TriangleAlert} color="amber.0">
              <Text size="sm">
                This configuration uses deprecated keys. The sidecar still accepts them; rewrite them before they
                are removed.
              </Text>
              {deprecations.map((line) => (
                <Text key={line} size="sm">
                  {line}
                </Text>
              ))}
            </Callout>
          )}

          {configured ? (
            <>
              <Stack gap="sm">
                <Text fw={600}>Features</Text>
                <FeatureRules sidecar={sidecar} editable={editable && !fromFile} />
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
