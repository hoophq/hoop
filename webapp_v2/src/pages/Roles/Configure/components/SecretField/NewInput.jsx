import { Group, Stack } from '@mantine/core'
import { Trash2 } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import SourcedInput from '@/components/SourcedInput'
import FieldLabel from './FieldLabel'
import { sourceOptionsFor } from './util'

// The "new" state of SecretField: no existing value (the key isn't on the
// connection), so we render a plain editable input — same as the rest of
// the form. onRemove is optional: custom-type rows expose it, predefined
// fields don't.
export default function NewInput({
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
        <FieldLabel label={label} required={required} />
        {onRemove && (
          <ActionIcon
            variant="subtle"
            color="gray"
            onClick={onRemove}
            aria-label="Remove field"
          >
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
