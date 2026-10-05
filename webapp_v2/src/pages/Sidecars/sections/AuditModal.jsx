import { useState } from 'react'
import { Divider, Group, ScrollArea, SimpleGrid, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import NumberInput from '@/components/NumberInput'
import Select from '@/components/Select'
import Switch from '@/components/Switch'
import TextInput from '@/components/TextInput'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { auditEnabled } from '../config'
import { saveErrorMessage } from '../useListenerEditor'

// audit.file: "-" or empty is stdout, a null device is off, anything else a
// path (buildAudit, sidecar/daemon/daemon.go). A stored NUL keeps its
// spelling: a Windows sidecar cannot open /dev/null.
const STDOUT = '-'
const OFF = '/dev/null'
const DEFAULT_MAX_STATEMENT_BYTES = 8192

const DESTINATIONS = [
  { value: 'stdout', label: 'Stdout (JSON lines)' },
  { value: 'file', label: 'A file on the sidecar' },
  { value: 'off', label: 'Off' },
]

function destinationOf(file) {
  if (!auditEnabled({ audit: { file } })) return 'off'
  return !file || file === STDOUT ? 'stdout' : 'file'
}

function toForm(audit = {}) {
  const destination = destinationOf(audit.file)
  return {
    destination,
    path: destination === 'file' ? audit.file : '',
    nullDevice: destination === 'off' ? audit.file : OFF,
    async_queue_size: audit.async_queue_size ?? 0,
    memory_buffer: audit.memory_buffer ?? 0,
    query_sessions: audit.query_sessions ?? 0,
    redact_statements: audit.redact_statements === true,
    max_statement_bytes: audit.max_statement_bytes || DEFAULT_MAX_STATEMENT_BYTES,
    // The daemon reads the deprecated fail_closed inverted when fail_open is absent.
    fail_open: audit.fail_open ?? (audit.fail_closed != null ? !audit.fail_closed : false),
  }
}

const count = (v) => Math.max(0, Math.trunc(Number(v) || 0))

// Every key: the patch replaces `audit` whole. fail_closed is dropped on purpose.
function toAudit(f) {
  return {
    file: f.destination === 'stdout' ? STDOUT : f.destination === 'off' ? f.nullDevice : f.path.trim(),
    async_queue_size: count(f.async_queue_size),
    memory_buffer: count(f.memory_buffer),
    query_sessions: count(f.query_sessions),
    redact_statements: f.redact_statements,
    max_statement_bytes: count(f.max_statement_bytes) || DEFAULT_MAX_STATEMENT_BYTES,
    fail_open: f.fail_open,
  }
}

function Caption({ children }) {
  return (
    <Text size="sm" fw={500} c="dimmed" tt="uppercase">
      {children}
    </Text>
  )
}

function Toggle({ label, hint, checked, onChange }) {
  return (
    <Stack gap={4}>
      <Group justify="space-between" wrap="nowrap">
        <Text size="sm" fw={500}>
          {label}
        </Text>
        <Switch checked={checked} onChange={(e) => onChange(e.currentTarget.checked)} aria-label={label} />
      </Group>
      <Text size="sm" c="dimmed">
        {hint}
      </Text>
    </Stack>
  )
}

// Remounts per opening, so the form starts from the stored block.
function Form({ sidecar, onClose }) {
  const patchSidecar = useSidecarStore((s) => s.patchSidecar)
  const [form, setForm] = useState(() => toForm(sidecar.configuration?.audit))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const set = (patch) => {
    setForm((f) => ({ ...f, ...patch }))
    setError(null)
  }
  const hinted = { inputWrapperOrder: ['label', 'input', 'description', 'error'] }

  const save = async () => {
    if (form.destination === 'file' && form.path.trim() === '') {
      setError('A file path is required, or pick another destination.')
      return
    }
    setSaving(true)
    const { ok, error: refusal } = await patchSidecar(sidecar.id, { audit: toAudit(form) })
    setSaving(false)
    if (!ok) {
      setError(saveErrorMessage(refusal))
      return
    }
    showSnackbar({ level: 'success', text: 'Audit settings updated.' })
    onClose()
  }

  return (
    <Stack gap="lg">
      <Text size="sm" c="dimmed">
        Set where the sidecar writes audit events and what a failed write does.
      </Text>
      <Divider />

      <Stack gap="md">
        <Caption>Output</Caption>
        <Select
          label="Destination"
          data={DESTINATIONS}
          value={form.destination}
          onChange={(v) => set({ destination: v ?? 'stdout' })}
          allowDeselect={false}
        />
        {form.destination === 'file' && (
          <TextInput
            {...hinted}
            label="File path"
            description="On the sidecar's filesystem. JSON lines, appended."
            placeholder="/var/log/hoop-sidecar/audit.jsonl"
            ff="monospace"
            value={form.path}
            onChange={(e) => set({ path: e.currentTarget.value })}
          />
        )}
      </Stack>
      <Divider />

      <Stack gap="md">
        <Caption>Buffers and query API</Caption>
        <NumberInput
          {...hinted}
          label="Async queue size"
          description="Events waiting for a slow sink. 0 writes synchronously."
          min={0}
          value={form.async_queue_size}
          onChange={(v) => set({ async_queue_size: v })}
        />
        <SimpleGrid cols={2} spacing="md">
          <NumberInput
            {...hinted}
            label="Memory buffer"
            description="Last N events at GET /events. 0 turns it off."
            min={0}
            value={form.memory_buffer}
            onChange={(v) => set({ memory_buffer: v })}
          />
          <NumberInput
            {...hinted}
            label="Query sessions"
            description="Sessions kept for /api/*. 0 turns off the query API."
            min={0}
            value={form.query_sessions}
            onChange={(v) => set({ query_sessions: v })}
          />
        </SimpleGrid>
      </Stack>
      <Divider />

      <Stack gap="md">
        <Caption>Statements</Caption>
        <Toggle
          label="Redact statements"
          hint="Replace statement text with a stable fingerprint."
          checked={form.redact_statements}
          onChange={(v) => set({ redact_statements: v })}
        />
        <NumberInput
          {...hinted}
          label="Max statement size (bytes)"
          description="Truncates statements in the audit record. The query itself does not change."
          min={0}
          value={form.max_statement_bytes}
          onChange={(v) => set({ max_statement_bytes: v })}
        />
      </Stack>
      <Divider />

      <Stack gap="md">
        <Caption>Failure</Caption>
        <Toggle
          label="Fail open"
          hint="Allow a statement when its audit record is not written."
          checked={form.fail_open}
          onChange={(v) => set({ fail_open: v })}
        />
      </Stack>
      <Divider />

      {error && (
        <Text size="sm" c="red">
          {error}
        </Text>
      )}
      <Group justify="flex-end" gap="sm">
        <Button variant="default" onClick={onClose} disabled={saving}>
          Cancel
        </Button>
        <Button onClick={save} loading={saving}>
          Save
        </Button>
      </Group>
    </Stack>
  )
}

export default function AuditModal({ opened, sidecar, onClose }) {
  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title="Audit"
      size={892}
      closeOnClickOutside={false}
      scrollAreaComponent={ScrollArea.Autosize}
    >
      <Form sidecar={sidecar} onClose={onClose} />
    </Modal>
  )
}
