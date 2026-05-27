import { Stack, Group, Text } from '@mantine/core'
import TextInput from '@/components/TextInput'
import PasswordInput from '@/components/PasswordInput'
import Textarea from '@/components/Textarea'
import Select from '@/components/Select'

const INPUT_BY_TYPE = {
  password: PasswordInput,
  textarea: Textarea,
  text: TextInput,
}

// Input field with an optional source picker rendered as a sibling on
// the left, instead of being embedded inside the input. Used when a
// credential field can be sourced from different providers (manual
// entry vs Vault vs AWS Secrets Manager) and the user needs to switch
// per-field. When `sources` is empty or has a single option, only the
// input renders.
//
// `description` renders as helper text between the label and the input
// row. The label sits outside the wrapped input so the source picker
// can be a sibling — that's why we paint the description manually here
// instead of letting the underlying Mantine input do it.
export default function SourcedInput({
  label,
  description,
  required = false,
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
}) {
  const Input = INPUT_BY_TYPE[type] || TextInput
  const showSourceSelect = sources && sources.length > 1
  return (
    <Stack gap={4}>
      {label && (
        <Group gap={4}>
          <Text size="sm" fw={500}>{label}</Text>
          {required && <Text size="sm" c="red">*</Text>}
        </Group>
      )}
      {description && (
        <Text size="xs" c="dimmed">
          {description}
        </Text>
      )}
      <Group gap="xs" wrap="nowrap" align="stretch">
        {showSourceSelect && (
          <Select
            data={sources}
            value={source}
            onChange={(v) => v && onSourceChange?.(v)}
            allowDeselect={false}
            w={180}
            aria-label="Source"
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
          {...(type === 'textarea' ? { autosize: true, minRows: 4 } : {})}
        />
      </Group>
    </Stack>
  )
}
