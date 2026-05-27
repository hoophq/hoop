import { Group, Stack, Text } from '@mantine/core'
import { X } from 'lucide-react'
import Badge from '@/components/Badge'
import ActionIcon from '@/components/ActionIcon'
import SourcedInput from '@/components/SourcedInput'
import { sourceOptionsFor } from './util'

// The "editing" state of SecretField: user clicked Replace on a set
// secret, or is mid-typing a new value. The Pending Change badge tells
// the user the new value will only persist on Save. Cancel unstages.
export default function ReplacingInput({
  label,
  description,
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
      {description && (
        <Text size="xs" c="dimmed">
          {description}
        </Text>
      )}
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
