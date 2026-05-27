import { Group, Stack, Text } from '@mantine/core'
import { Trash2 } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import SourcedInput from '@/components/SourcedInput'
import { sourceOptionsFor } from './util'

// The "new" state of SecretField: there is no existing value (the key
// isn't on the connection), so we render a plain editable input.
// onRemove is optional — custom-type rows expose it; predefined fields
// don't.
export default function NewInput({
  label,
  description,
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
      {description && (
        <Text size="xs" c="dimmed">
          {description}
        </Text>
      )}
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
