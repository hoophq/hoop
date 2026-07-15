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

// Credential input with an optional source picker (Manual / Vault KV /
// AWS Secrets Manager / AWS IAM Role) glued to its left edge. Textareas
// stack the picker above instead — gluing a Select to a tall textarea
// looks broken.
export default function SourcedInput({
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
  rightSectionWidth,
  rightSectionPointerEvents,
  size = 'sm',
  minRows,
}) {
  const Input = INPUT_BY_TYPE[type] || TextInput
  const showSourceMenu = Array.isArray(sources) && sources.length > 1
  const isTextarea = type === 'textarea'
  // A fixed row count for textareas that must not collapse (e.g. a
  // pasted service-account JSON). The wrapper's autosize default caps
  // at 6 rows, so the max is raised alongside the min.
  const textareaSizing = minRows ? { minRows, maxRows: minRows } : {}

  const labelNode = label ? <FieldLabel label={label} required={required} /> : null

  if (isTextarea) {
    return (
      <Stack gap={4}>
        {labelNode}
        {showSourceMenu && (
          <Select
            data={normalizeForSelect(sources)}
            value={source}
            onChange={(v) => v && onSourceChange?.(v)}
            allowDeselect={false}
            w={220}
            size={size}
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
          size={size}
          {...textareaSizing}
        />
      </Stack>
    )
  }

  return (
    <Stack gap={4}>
      {labelNode}
      <Group gap={0} wrap="nowrap" align="stretch">
        {showSourceMenu && (
          <SourceMenu
            source={source}
            sources={sources}
            onSourceChange={onSourceChange}
            targetClassName={classes.pickerSibling}
            targetSize={size}
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
          rightSectionWidth={rightSectionWidth}
          rightSectionPointerEvents={rightSectionPointerEvents}
          size={size}
          classNames={showSourceMenu ? { input: classes.inputSibling } : undefined}
        />
      </Group>
    </Stack>
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

function normalizeForSelect(sources) {
  return (sources || []).map((s) =>
    typeof s === 'string'
      ? { value: s, label: SOURCE_LABELS[s] || s }
      : { value: s.value, label: s.label || SOURCE_LABELS[s.value] || s.value },
  )
}
