import { Group, Stack, Text } from '@mantine/core'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import Textarea from '@/components/Textarea'
import Select from '@/components/Select'
import MarkdownText from '@/components/MarkdownText'
import { SOURCE_LABELS } from '@/pages/Roles/Configure/utils/secretsCodec'
import SourceMenu from './SourceMenu'
import classes from './SourcedInput.module.css'

const INPUT_BY_TYPE = {
  password: PasswordInput,
  textarea: Textarea,
  text: TextInput,
}

// Input field with an optional credential source picker (Manual /
// Vault KV / AWS Secrets Manager / AWS IAM Role) glued to its left.
// The picker is a button styled to share the input's outline at the
// seam — same border color, matching radii, and a single shared edge
// between them — so the two read as one control.
//
// Sizes match Mantine's input scale (xs/sm/md/lg/xl); default `sm`,
// matching every other input wrapper in the app. Heights come from
// the Mantine --input-height-* variables so SourcedInput aligns with
// a sibling TextInput at the same size.
//
// Textareas don't glue cleanly — the picker stacks above as a plain
// Select instead. PasswordInput's built-in eye toggle keeps working
// since we don't touch its right section.
export default function SourcedInput({
  label,
  required,
  placeholder,
  description,
  type = 'text',
  value,
  onChange,
  disabled,
  autoFocus,
  source,
  sources,
  onSourceChange,
  rightSection,
  size = 'sm',
}) {
  const Input = INPUT_BY_TYPE[type] || TextInput
  const showSourceMenu = Array.isArray(sources) && sources.length > 1
  const isTextarea = type === 'textarea'

  const labelNode = label ? <FieldLabel label={label} required={required} /> : null
  const descriptionNode = description ? <MarkdownText>{description}</MarkdownText> : null

  if (isTextarea) {
    return (
      <Stack gap={4}>
        {labelNode}
        {descriptionNode}
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
        />
      </Stack>
    )
  }

  return (
    <Stack gap={4}>
      {labelNode}
      {descriptionNode}
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
