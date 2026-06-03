import { Group, Stack, Text } from '@mantine/core'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import Textarea from '@/components/Textarea'
import Select from '@/components/Select'
import { SOURCE_LABELS } from '@/pages/Roles/Configure/utils/secretsCodec'
import SourceMenu from './SourceMenu'
import classes from './SourcedInput.module.css'

const INPUT_BY_TYPE = {
  password: PasswordInput,
  textarea: Textarea,
  text: TextInput,
}

// Variant B — two touching components. The picker carries the seam
// border so we don't double up at the joint; the input drops its left
// radius. Each component keeps its own focus ring, so the user always
// sees which side is active.
//
// Reuses the existing Mantine input wrappers verbatim — only their
// classNames are overridden via the module CSS. PasswordInput keeps its
// built-in eye toggle; no special handling needed.
export default function GluedSiblingsSourcedInput({
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
  const Input = INPUT_BY_TYPE[type] || TextInput
  const showSourceMenu = sources && sources.length > 1
  const isTextarea = type === 'textarea'

  // Textareas don't glue cleanly — keep the picker stacked above so the
  // multi-line input gets the full row width and visual breathing room.
  if (isTextarea) {
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
      <Group gap={0} wrap="nowrap" align="stretch">
        {showSourceMenu && (
          <SourceMenu
            source={source}
            sources={sources}
            onSourceChange={onSourceChange}
            targetClassName={classes.pickerSibling}
            disabled={disabled}
          />
        )}
        <Input
          flex={1}
          placeholder={placeholder}
          value={value}
          onChange={(e) => onChange?.(e.currentTarget.value)}
          disabled={disabled}
          autoFocus={autoFocus}
          required={required}
          rightSection={rightSection}
          classNames={showSourceMenu ? { input: classes.inputSibling } : undefined}
        />
      </Group>
    </Stack>
  )
}

function normalizeForSelect(sources) {
  return (sources || []).map((s) =>
    typeof s === 'string'
      ? { value: s, label: SOURCE_LABELS[s] || s }
      : { value: s.value, label: s.label || SOURCE_LABELS[s.value] || s.value },
  )
}
