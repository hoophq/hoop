import { useState } from 'react'
import { Group, Input, Stack, Text } from '@mantine/core'
import { Eye, EyeOff } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Textarea from '@/components/Textarea'
import Select from '@/components/Select'
import { SOURCE_LABELS } from '@/pages/Roles/Configure/utils/secretsCodec'
import SourceMenu from './SourceMenu'
import classes from './SourcedInput.module.css'

// Variant A — single-outline composite. The picker, the input, and any
// right-section all live inside one outlined box. Uses :focus-within
// on the composite so clicks anywhere inside light up the same ring.
//
// Why we don't use Mantine's `leftSection`: the rejected commit ecd2268
// used that path and the label kept overlapping the placeholder. Here
// we compose the outline ourselves around an Input.Input core so
// Mantine's leftSection measurement / placeholder offset never runs.
//
// Textareas fall back to the legacy sibling-Select pattern — embedding
// a picker on the left of a tall textarea looks broken. This matches
// the original SourcedInput's behaviour for multi-line inputs.
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
  const showSourceMenu = sources && sources.length > 1

  if (type === 'textarea') {
    // No embedded picker for textareas — render Select above + Textarea
    // below so neither component needs to fit a horizontal slot.
    return (
      <Stack gap={4}>
        {label && (
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
        )}
        {descriptionSlot}
        {showSourceMenu && (
          <Select
            data={sources.map((s) => ({ value: s, label: SOURCE_LABELS[s] || s }))}
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
      {label && (
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
      )}
      {descriptionSlot}
      <div
        className={classes.composite}
        data-disabled={disabled ? 'true' : undefined}
      >
        {showSourceMenu && (
          <SourceMenu
            source={source}
            sources={sources}
            onSourceChange={onSourceChange}
            targetClassName={classes.picker}
            disabled={disabled}
          />
        )}
        <InputCore
          type={type}
          placeholder={placeholder}
          value={value}
          onChange={onChange}
          disabled={disabled}
          autoFocus={autoFocus}
          required={required}
          rightSection={rightSection}
        />
      </div>
    </Stack>
  )
}

// Renders the actual value input inside the composite. Strips Mantine's
// own border + radius via `classes.bareInput` so the composite outline
// is the only one visible. For passwords we layer a small show/hide
// adornment in `rightSection` because Mantine's PasswordInput always
// ships its own outline and can't be cleanly stripped.
function InputCore({
  type,
  placeholder,
  value,
  onChange,
  disabled,
  autoFocus,
  required,
  rightSection,
}) {
  const [revealed, setRevealed] = useState(false)
  const isPassword = type === 'password'
  const inputType = !isPassword ? 'text' : revealed ? 'text' : 'password'
  const effectiveRightSection = rightSection || (isPassword ? (
    <PasswordToggle revealed={revealed} onToggle={() => setRevealed((r) => !r)} />
  ) : undefined)

  return (
    <Input
      flex={1}
      type={inputType}
      placeholder={placeholder}
      value={value || ''}
      onChange={(e) => onChange?.(e.currentTarget.value)}
      disabled={disabled}
      autoFocus={autoFocus}
      required={required}
      classNames={{ input: classes.bareInput }}
      rightSection={effectiveRightSection}
      // Strip Mantine's variant background so it doesn't repaint over
      // our composite cell background.
      variant="unstyled"
    />
  )
}

function PasswordToggle({ revealed, onToggle }) {
  return (
    <ActionIcon
      variant="subtle"
      color="gray"
      size="sm"
      onClick={onToggle}
      aria-label={revealed ? 'Hide password' : 'Show password'}
    >
      {revealed ? <EyeOff size={16} /> : <Eye size={16} />}
    </ActionIcon>
  )
}
