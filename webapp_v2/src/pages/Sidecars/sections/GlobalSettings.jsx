import { useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import { Check, SquarePen } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { formatRelativeTime } from '@/utils/datetime'
import { showSnackbar } from '@/utils/snackbar'
import { auditEnabled, hasConfiguration } from '../config'
import { opaSummary, resolveOPA } from '../features'
import { configState } from '../status'
import { saveErrorMessage } from '../useListenerEditor'
import AuditModal from './AuditModal'

// Wide enough for "Configuration", the longest label.
const LABEL_WIDTH = 96
const LOG_LEVELS = ['debug', 'info', 'warn', 'error']

// `heading` sets the label like the card's section titles; the Audit row is one.
function Row({ label, heading = false, align = 'center', children }) {
  const labelProps = heading ? { fw: 600 } : { size: 'sm', c: 'dimmed' }
  return (
    <Group gap="sm" align={align} wrap="nowrap">
      <Text {...labelProps} w={LABEL_WIDTH} flex="0 0 auto">
        {label}
      </Text>
      {children}
    </Group>
  )
}

function EditButton({ onClick }) {
  return (
    <Button variant="subtle" size="compact-sm" leftSection={<SquarePen size={14} />} onClick={onClick}>
      Edit
    </Button>
  )
}

// A row in edit mode: the field, Save and Cancel, and the gateway's refusal
// under it. The refusal stays until the value changes or the row is left.
// Compact controls keep the row at its view height, so Edit does not shift the card.
function Editing({ children, onSave, onCancel, saving, error }) {
  return (
    <Stack gap={4}>
      <Group gap="xs" wrap="nowrap" align="center">
        {children}
        <Button size="compact-sm" onClick={onSave} loading={saving}>
          Save
        </Button>
        <Button size="compact-sm" variant="subtle" color="gray" onClick={onCancel} disabled={saving}>
          Cancel
        </Button>
      </Group>
      {error && (
        <Text size="xs" c="red">
          {error}
        </Text>
      )}
    </Stack>
  )
}

/**
 * The Global settings rows of the details card. With `editable`, Admin and
 * Log level edit in place and Audit opens a dialog; each save is a PATCH of
 * that one key, so the rest of the stored document stays as it is.
 */
export default function GlobalSettings({ sidecar, editable }) {
  const patchSidecar = useSidecarStore((s) => s.patchSidecar)
  const config = sidecar.configuration
  const configured = hasConfiguration(config)
  const opa = configured ? resolveOPA(null, config) : null
  const configuration = configState(sidecar)
  const [editing, setEditing] = useState(null)
  const [draft, setDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const [auditOpen, setAuditOpen] = useState(false)

  const edit = (key, value) => {
    setEditing(key)
    setDraft(value)
    setError(null)
  }
  const cancel = () => {
    setEditing(null)
    setError(null)
  }
  const change = (value) => {
    setDraft(value)
    setError(null)
  }

  const save = async (configuration, done) => {
    setSaving(true)
    const { ok, error: refusal } = await patchSidecar(sidecar.id, configuration)
    setSaving(false)
    if (!ok) {
      setError(saveErrorMessage(refusal))
      return
    }
    setEditing(null)
    showSnackbar({ level: 'success', text: done })
  }

  // One write at a time from this card: a second response could land first
  // and install the older snapshot.
  const canEdit = editable && !saving

  const listen = config?.admin?.listen ?? ''
  const logLevel = config?.log_level || 'info'

  return (
    <>
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
        {configuration && (
          <Row label="Configuration" align={configuration.detail ? 'flex-start' : 'center'}>
            <Stack gap={2}>
              <Text size="sm">{configuration.label}</Text>
              {configuration.detail && (
                <Text size="xs" c="dimmed" ff="monospace">
                  {configuration.detail}
                </Text>
              )}
            </Stack>
          </Row>
        )}
        {configured && (
          <>
            <Row label="Admin">
              {editing === 'admin' ? (
                // An empty address is the off switch: the daemon opens no endpoint for it.
                <Editing
                  onSave={() => save({ admin: { listen: draft.trim() } }, 'Admin endpoint updated.')}
                  onCancel={cancel}
                  saving={saving}
                  error={error}
                >
                  <TextInput
                    aria-label="Admin endpoint"
                    placeholder="127.0.0.1:19000"
                    size="xs"
                    ff="monospace"
                    w={280}
                    value={draft}
                    onChange={(e) => change(e.currentTarget.value)}
                    autoFocus
                  />
                </Editing>
              ) : (
                <>
                  <Text size="sm" ff={listen ? 'monospace' : undefined}>
                    {listen || 'Off'}
                  </Text>
                  {canEdit && <EditButton onClick={() => edit('admin', listen)} />}
                </>
              )}
            </Row>
            <Row label="Log level">
              {editing === 'log_level' ? (
                <Editing
                  onSave={() => save({ log_level: draft }, 'Log level updated.')}
                  onCancel={cancel}
                  saving={saving}
                  error={error}
                >
                  <Select
                    aria-label="Log level"
                    data={LOG_LEVELS}
                    value={draft}
                    onChange={(v) => change(v ?? 'info')}
                    allowDeselect={false}
                    size="xs"
                    w={160}
                  />
                </Editing>
              ) : (
                <>
                  <Badge tag chip variant="light" color="gray">
                    {logLevel}
                  </Badge>
                  {canEdit && <EditButton onClick={() => edit('log_level', logLevel)} />}
                </>
              )}
            </Row>
            <Row label="OPA">
              <Text size="sm">{opa?.url ? opaSummary(opa) : 'Off'}</Text>
            </Row>
            <Row label="Audit" heading>
              {auditEnabled(config) ? (
                <Badge tag chip variant="light" color="green" c="green.8" icon={<Check size={12} aria-hidden="true" />}>
                  Active
                </Badge>
              ) : (
                <Badge tag chip variant="light" color="gray">
                  Off
                </Badge>
              )}
              {canEdit && <EditButton onClick={() => setAuditOpen(true)} />}
            </Row>
          </>
        )}
      </Stack>

      {editable && configured && (
        <AuditModal opened={auditOpen} sidecar={sidecar} onClose={() => setAuditOpen(false)} />
      )}
    </>
  )
}
