import { useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import { Eye, EyeOff } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Textarea from '@/components/Textarea'
import Select from '@/components/Select'
import { SOURCE_LABELS } from '@/pages/Roles/Configure/utils/secretsCodec'
import SourceMenu from './SourceMenu'
import classes from './SourcedInput.module.css'

// Variant A — single-outline composite. The picker, the input, and any
// right-section all live inside one outlined cell painted by .composite,
// which uses :focus-within so any click inside lights up the ring.
//
// Why we don't use Mantine's `leftSection`: the rejected commit ecd2268
// took that path and the label kept overlapping the placeholder. Here
// we compose the outline ourselves around a plain <input> so Mantine's
// leftSection measurement / placeholder offset never runs. Plus, a plain
// <input> gives us full control over padding (Mantine's variant=unstyled
// keeps some inline padding that fights the composite's gap).
//
// Textareas fall back to the legacy sibling-Select pattern — embedding
// a picker on the left of a tall textarea looks broken.
export default function SingleOutlineSourcedInput({
  label,
  required,
  placeholder,
  type = 'text',
  value,
  onChange,
  disabled,
  autoFocus,
  source,
  sources,
  onSourceChange,
  rightSection,
  descriptionSlot,
}) {
  const showSourceMenu = Array.isArray(sources) && sources.length > 1

  if (type === 'textarea') {
    return (
      <Stack gap={4}>
        {label && <FieldLabel label={label} required={required} />}
        {descriptionSlot}
        {showSourceMenu && (
          <Select
            data={normalizeForSelect(sources)}
            value={source}
            onChange={(v) => v && onSourceChange?.(v)}
            allowDeselect={false}
            w={220}
            size="sm"
            aria-label="Credential source"
          />
        )}
        <Textarea
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange?.(e.currentTarget.value)}
          disabled={disabled}
          autoFocus={autoFocus}
          required={required}
        />
      </Stack>
    )
  }

  return (
    <Stack gap={4}>
      {label && <FieldLabel label={label} required={required} />}
      {descriptionSlot}
      <CompositeRow disabled={disabled}>
        {showSourceMenu && (
          <SourceMenu
            source={source}
            sources={sources}
            onSourceChange={onSourceChange}
            targetClassName={classes.picker}
            disabled={disabled}
          />
        )}
        <BareInput
          type={type}
          placeholder={placeholder}
          value={value}
          onChange={onChange}
          disabled={disabled}
          autoFocus={autoFocus}
          required={required}
        />
        {rightSection}
      </CompositeRow>
    </Stack>
  )
}

function CompositeRow({ disabled, children }) {
  return (
    <div
      className={classes.composite}
      data-disabled={disabled ? 'true' : undefined}
    >
      {children}
    </div>
  )
}

function FieldLabel({ label, required }) {
  return (
    <Group gap={4}>
      <Text size="sm" fw={500}>
        {label}
      </Text>
      {required && (
        <Text size="sm" c="red">
          *
        </Text>
      )}
    </Group>
  )
}

// Native <input> + show/hide toggle. Keeps things minimal — Mantine's
// own Input/PasswordInput wrappers ship inline padding-right reservations
// for a rightSection that fight the composite's flex layout, so we side-
// step them entirely.
function BareInput({
  type,
  placeholder,
  value,
  onChange,
  disabled,
  autoFocus,
  required,
}) {
  const [revealed, setRevealed] = useState(false)
  const isPassword = type === 'password'
  const inputType = !isPassword ? 'text' : revealed ? 'text' : 'password'
  return (
    <>
      <input
        type={inputType}
        className={classes.bareInput}
        placeholder={placeholder}
        value={value || ''}
        onChange={(e) => onChange?.(e.currentTarget.value)}
        disabled={disabled}
        autoFocus={autoFocus}
        required={required}
      />
      {isPassword && (
        <ActionIcon
          variant="subtle"
          color="gray"
          size="sm"
          onClick={() => setRevealed((r) => !r)}
          aria-label={revealed ? 'Hide password' : 'Show password'}
        >
          {revealed ? <EyeOff size={16} /> : <Eye size={16} />}
        </ActionIcon>
      )}
    </>
  )
}

function normalizeForSelect(sources) {
  return (sources || []).map((s) =>
    typeof s === 'string'
      ? { value: s, label: SOURCE_LABELS[s] || s }
      : { value: s.value, label: s.label || SOURCE_LABELS[s.value] || s.value },
  )
}
