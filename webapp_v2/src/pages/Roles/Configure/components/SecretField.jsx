import { useState } from 'react'
import {
  Group,
  Stack,
  Text,
  Button,
  Paper,
  ThemeIcon,
  Badge,
  TextInput,
  Textarea,
  ActionIcon,
} from '@mantine/core'
import { Check, RotateCcw, Trash2, X } from 'lucide-react'
import Select from '@/components/Select'
import { SOURCE_LABELS } from '../utils/secretsCodec'

// Renders a compact source picker as the input's leftSection adornment.
// Used when the form is in Secrets Manager mode; `availableSources` is
// the subset offered to the user (current provider + manual-input).
// Auto-sized so the trigger label drives width; the popover stays wider
// for legible options.
function SourceSelectorAdornment({ source, availableSources, onSourceChange }) {
  if (!availableSources || availableSources.length < 2) return null
  return (
    <Select
      data={availableSources.map((s) => ({ value: s, label: SOURCE_LABELS[s] || s }))}
      value={source}
      onChange={(v) => v && onSourceChange(v)}
      size="xs"
      variant="unstyled"
      allowDeselect={false}
      withCheckIcon={false}
      comboboxProps={{ width: 220 }}
    />
  )
}

// SecretField — write-only credential editor.
//
// Renders one of four mutually-exclusive states based on the props:
//
//   * "set"        — an existing inline secret. Value is never shown.
//                    Offers Replace (and Delete if allowDelete is true).
//   * "replacing"  — user clicked Replace; staged input awaiting Save.
//                    Cancel removes the staged change.
//   * "deleted"    — user clicked Delete; staged removal awaiting Save.
//                    Undo restores the previous "set" state. Custom-type only.
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

function ReadOnlyStatus({ label, required, secretsUpdatedAt, isReference, referenceText, allowDelete, onReplace, onDelete }) {
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
          {allowDelete && (
            <Button
              size="xs"
              variant="subtle"
              color="red"
              leftSection={<Trash2 size={14} />}
              onClick={onDelete}
            >
              Delete
            </Button>
          )}
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
  const isTextarea = type === 'textarea'
  const InputComponent = isTextarea ? Textarea : TextInput
  const textareaProps = isTextarea ? { autosize: true, minRows: 4 } : {}
  const adornment = !isTextarea ? (
    <SourceSelectorAdornment
      source={source}
      availableSources={availableSources}
      onSourceChange={onSourceChange}
    />
  ) : null
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
      <InputComponent
        autoFocus
        required={required}
        placeholder={placeholder || 'Enter new value'}
        value={value}
        onChange={(e) => onChange(e.currentTarget.value)}
        leftSection={adornment}
        leftSectionWidth={adornment ? 'auto' : undefined}
        {...textareaProps}
      />
    </Stack>
  )
}

function StagedDeleted({ label, onUndo }) {
  return (
    <Stack gap="xs">
      <Group justify="space-between" align="center">
        <Text size="sm" fw={500} c="dimmed" td="line-through">{label}</Text>
        <Group gap="xs">
          <Badge size="sm" color="red" variant="light">
            Will be deleted on Save
          </Badge>
          <Button
            size="xs"
            variant="subtle"
            leftSection={<RotateCcw size={14} />}
            onClick={onUndo}
          >
            Undo
          </Button>
        </Group>
      </Group>
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
  const isTextarea = type === 'textarea'
  const InputComponent = isTextarea ? Textarea : TextInput
  const textareaProps = isTextarea ? { autosize: true, minRows: 4 } : {}
  const adornment = !isTextarea ? (
    <SourceSelectorAdornment
      source={source}
      availableSources={availableSources}
      onSourceChange={onSourceChange}
    />
  ) : null
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
      <InputComponent
        required={required}
        placeholder={placeholder || 'Enter value'}
        value={value}
        onChange={(e) => onChange(e.currentTarget.value)}
        leftSection={adornment}
        leftSectionWidth={adornment ? 'auto' : undefined}
        {...textareaProps}
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
  allowDelete,
  stagedAction,
  stagedValue = '',
  secretsUpdatedAt,
  source,
  availableSources,
  onSourceChange,
  onReplace,
  onChangeStaged,
  onCancel,
  onDelete,
  onRemove,
}) {
  const [editing, setEditing] = useState(false)

  // Compute effective state
  const state = (() => {
    if (stagedAction === 'delete') return 'deleted'
    if (stagedAction === 'replace' || stagedAction === 'new' || editing) return 'editing'
    if (!isExisting) return 'new'
    return 'set'
  })()

  if (state === 'deleted') {
    return <StagedDeleted label={label} onUndo={onCancel} />
  }

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
      allowDelete={allowDelete}
      onReplace={() => {
        setEditing(true)
        // Start staged with empty input so HTML5 required validation works.
        onReplace('')
      }}
      onDelete={onDelete}
    />
  )
}
