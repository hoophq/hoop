import { Group, Stack, ThemeIcon } from '@mantine/core'
import { PencilLine } from 'lucide-react'
import TextInput from '@/components/TextInput'
import Textarea from '@/components/Textarea'
import SourcedInput from '@/components/SourcedInput'
import FieldLabel from './FieldLabel'
import InlineAction from './InlineAction'
import { sourceOptionsFor } from './util'

const RIGHT_SECTION_WIDTH = 112

// The "editing" state of SecretField: the user clicked Replace on a set
// secret, or is staging a new value. The new value shows in plaintext so it
// can be verified before saving; Restore drops the staged change and
// returns to the stored value. In Secrets Manager mode the glued source
// picker stays; otherwise a pencil marks the field as being edited.
export default function ReplacingInput({
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
  const restore = <InlineAction kind="restore" onClick={onCancel} />
  const showPicker = Array.isArray(availableSources) && availableSources.length > 1

  if (type === 'textarea') {
    return (
      <Stack gap={4}>
        <Group justify="space-between" align="center">
          <FieldLabel label={label} required={required} />
          {restore}
        </Group>
        <Textarea
          autoFocus
          autosize
          minRows={4}
          required={required}
          placeholder={placeholder || 'Enter new value'}
          value={value}
          onChange={(e) => onChange(e.currentTarget.value)}
        />
      </Stack>
    )
  }

  if (showPicker) {
    return (
      <SourcedInput
        label={label}
        required={required}
        type="text"
        autoFocus
        placeholder={placeholder || 'Enter new value'}
        value={value}
        onChange={onChange}
        source={source}
        sources={sourceOptionsFor(availableSources)}
        onSourceChange={onSourceChange}
        rightSection={restore}
        rightSectionWidth={RIGHT_SECTION_WIDTH}
        rightSectionPointerEvents="auto"
      />
    )
  }

  return (
    <TextInput
      label={label}
      withAsterisk={required}
      required={required}
      autoFocus
      placeholder={placeholder || 'Enter new value'}
      value={value}
      onChange={(e) => onChange(e.currentTarget.value)}
      leftSection={
        <ThemeIcon size="sm" radius="xl" variant="light" color="gray">
          <PencilLine size={12} />
        </ThemeIcon>
      }
      rightSection={restore}
      rightSectionWidth={RIGHT_SECTION_WIDTH}
      rightSectionPointerEvents="auto"
    />
  )
}
