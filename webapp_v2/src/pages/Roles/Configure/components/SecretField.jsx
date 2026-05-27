import { useState } from 'react'
import {
  Group,
  Stack,
  Text,
  Button,
  Paper,
  ThemeIcon,
  Badge,
  ActionIcon,
} from '@mantine/core'
import { Check, RotateCcw, Trash2, X } from 'lucide-react'
import SourcedInput from '@/components/SourcedInput'
import { SOURCE_LABELS } from '../utils/secretsCodec'

function sourceOptionsFor(availableSources) {
  return (availableSources || []).map((s) => ({ value: s, label: SOURCE_LABELS[s] || s }))
}

// SecretField — write-only credential editor.
//
// Renders one of four mutually-exclusive states based on the props:
//
//   * "set"        — an existing inline secret. Value is never shown.
//                    Offers Replace (and Delete if allowDelete is true).
//   * "replacing"  — user clicked Replace; staged input awaiting Save.
//                    Cancel removes the staged change.
//   * "reference"  — value points at an external provider; shown verbatim
//                    in a read-only field. Replace still works (admin can
//                    point it at a different ARN); Delete only if allowed.
//   * "new"        — there is no existing value (key absent in connection);
//                    a plain editable input. Useful for custom-type adds.
//
// stagedAction === 'replace' | 'delete' | 'new' takes priority over the
// underlying connection state.

function formatTimestamp(iso) {
  if (!iso) return null
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return null
  }
}

function ReadOnlyStatus({ label, required, secretsUpdatedAt, isReference, referenceText, onReplace }) {
  const updated = formatTimestamp(secretsUpdatedAt)
  return (
    <Stack gap="xs">
      <Group justify="space-between" align="center">
        <Group gap={4}>
          <Text size="sm" fw={500}>{label}</Text>
          {required && <Text size="sm" c="red">*</Text>}
        </Group>
        <Group gap="xs">
          <Button size="xs" variant="default" onClick={onReplace}>
            Replace
          </Button>
        </Group>
      </Group>
      <Paper p="sm" radius="sm" bg="gray.0" withBorder>
        {isReference ? (
          <Group gap="xs" wrap="nowrap">
            <ThemeIcon size="sm" color="indigo" variant="light">
              <Check size={12} />
            </ThemeIcon>
            <Text size="sm" ff="monospace" lh={1.2}>
              {referenceText}
            </Text>
          </Group>
        ) : (
          <Group gap="xs">
            <ThemeIcon size="sm" color="green" variant="light">
              <Check size={12} />
            </ThemeIcon>
            <Text size="sm" c="dimmed">Set</Text>
            {updated && (
              <Text size="xs" c="dimmed">
                {'· Last updated ' + updated}
              </Text>
            )}
          </Group>
        )}
      </Paper>
    </Stack>
  )
}

function ReplacingInput({
  label,
  required,
  placeholder,
  type,
  value,
  onChange,
  onCancel,
  source,
  availableSources,
  onSourceChange,
}) {
  return (
    <Stack gap="xs">
      <Group justify="space-between" align="center">
        <Group gap={4}>
          <Text size="sm" fw={500}>{label}</Text>
          {required && <Text size="sm" c="red">*</Text>}
        </Group>
        <Group gap="xs">
          <Badge size="sm" variant="dot" color="indigo">
            Pending change
          </Badge>
          <ActionIcon variant="subtle" color="gray" onClick={onCancel} aria-label="Cancel replace">
            <X size={16} />
          </ActionIcon>
        </Group>
      </Group>
      <SourcedInput
        type={type}
        autoFocus
        required={required}
        placeholder={placeholder || 'Enter new value'}
        value={value}
        onChange={onChange}
        source={source}
        sources={sourceOptionsFor(availableSources)}
        onSourceChange={onSourceChange}
      />
    </Stack>
  )
}

function NewInput({
  label,
  required,
  placeholder,
  type,
  value,
  onChange,
  onRemove,
  source,
  availableSources,
  onSourceChange,
}) {
  return (
    <Stack gap="xs">
      <Group justify="space-between" align="center">
        <Group gap={4}>
          <Text size="sm" fw={500}>{label}</Text>
          {required && <Text size="sm" c="red">*</Text>}
        </Group>
        {onRemove && (
          <ActionIcon variant="subtle" color="gray" onClick={onRemove} aria-label="Remove field">
            <Trash2 size={16} />
          </ActionIcon>
        )}
      </Group>
      <SourcedInput
        type={type}
        required={required}
        placeholder={placeholder || 'Enter value'}
        value={value}
        onChange={onChange}
        source={source}
        sources={sourceOptionsFor(availableSources)}
        onSourceChange={onSourceChange}
      />
    </Stack>
  )
}

export default function SecretField({
  label,
  required = false,
  placeholder,
  type,
  isExisting,
  isReference,
  referenceText,
  stagedAction,
  stagedValue = '',
  secretsUpdatedAt,
  source,
  availableSources,
  onSourceChange,
  onReplace,
  onChangeStaged,
  onCancel,
  onRemove,
}) {
  const [editing, setEditing] = useState(false)

  // Compute effective state
  const state = (() => {
    if (stagedAction === 'replace' || stagedAction === 'new' || editing) return 'editing'
    if (!isExisting) return 'new'
    return 'set'
  })()

  if (state === 'editing') {
    return (
      <ReplacingInput
        label={label}
        required={required}
        placeholder={placeholder}
        type={type}
        value={stagedValue}
        onChange={(plain) => {
          if (!stagedAction) {
            onReplace(plain)
          } else {
            onChangeStaged(plain)
          }
        }}
        onCancel={() => {
          setEditing(false)
          onCancel()
        }}
        source={source}
        availableSources={availableSources}
        onSourceChange={onSourceChange}
      />
    )
  }

  if (state === 'new') {
    return (
      <NewInput
        label={label}
        required={required}
        placeholder={placeholder}
        type={type}
        value={stagedValue}
        onChange={(plain) => onReplace(plain)}
        onRemove={onRemove}
        source={source}
        availableSources={availableSources}
        onSourceChange={onSourceChange}
      />
    )
  }

  // state === 'set'
  return (
    <ReadOnlyStatus
      label={label}
      required={required}
      secretsUpdatedAt={secretsUpdatedAt}
      isReference={isReference}
      referenceText={referenceText}
      onReplace={() => {
        setEditing(true)
        // Start staged with empty input so HTML5 required validation works.
        onReplace('')
      }}
    />
  )
}
