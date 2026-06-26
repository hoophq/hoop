import { useState } from 'react'
import { Group, Stack, Text, ThemeIcon } from '@mantine/core'
import { Check, PencilLine, Trash2 } from 'lucide-react'
import TextInput from '@/components/TextInput'
import Textarea from '@/components/Textarea'
import SourcedInput from '@/components/SourcedInput'
import ActionIcon from '@/components/ActionIcon'
import InlineAction from './InlineAction'
import { sourceOptionsFor, SECRET_MASK } from './util'
import classes from './SecretField.module.css'

const RIGHT_SECTION_WIDTH = 112

// Leading status icon inside the field: a check for a stored value, a
// pencil while editing.
function LeadingIcon({ color, children }) {
  return (
    <ThemeIcon size="sm" radius="xl" variant="light" color={color}>
      {children}
    </ThemeIcon>
  )
}

// SecretField — write-only credential editor. One component, three
// variations selected from its props:
//   set      → a stored secret; the value is never shown (masked input +
//              Replace). Provider references show their pointer text.
//   editing  → replacing or staging a value; plaintext + Restore. The
//              Secrets Manager source picker stays; otherwise a pencil
//              marks the field as edited.
//   new      → no stored value yet; a plain editable input (+ optional
//              remove for free-form rows).
// The Replace/Restore control is likewise one component with variants —
// see InlineAction.
export default function SecretField({
  label,
  required = false,
  placeholder,
  type,
  isExisting,
  isReference,
  referenceText,
  stagedAction,
  stagedValue = '',
  source,
  availableSources,
  onSourceChange,
  onReplace,
  onChangeStaged,
  onCancel,
  onRemove,
}) {
  const [editing, setEditing] = useState(false)
  const isTextarea = type === 'textarea'
  const showPicker = Array.isArray(availableSources) && availableSources.length > 1

  const state =
    stagedAction === 'replace' || stagedAction === 'new' || editing
      ? 'editing'
      : isExisting
        ? 'set'
        : 'new'

  if (state === 'set') {
    const Input = isTextarea ? Textarea : TextInput
    const maskedValue = isTextarea
      ? [SECRET_MASK, SECRET_MASK, SECRET_MASK].join('\n')
      : SECRET_MASK
    return (
      <Input
        label={label}
        withAsterisk={required}
        readOnly
        value={isReference ? referenceText : maskedValue}
        leftSection={
          <LeadingIcon color={isReference ? 'indigo' : 'green'}>
            <Check size={12} />
          </LeadingIcon>
        }
        rightSection={
          <InlineAction
            kind="replace"
            onClick={() => {
              setEditing(true)
              onReplace('') // stage empty so HTML5 required validation works
            }}
          />
        }
        rightSectionWidth={RIGHT_SECTION_WIDTH}
        rightSectionPointerEvents="auto"
        classNames={isTextarea ? { section: classes.topSection } : undefined}
      />
    )
  }

  if (state === 'editing') {
    const handleChange = (plain) =>
      stagedAction ? onChangeStaged(plain) : onReplace(plain)
    const restore = (
      <InlineAction
        kind="restore"
        onClick={() => {
          setEditing(false)
          onCancel()
        }}
      />
    )
    const placeholderText = placeholder || 'Enter new value'

    if (isTextarea) {
      return (
        <Textarea
          label={label}
          withAsterisk={required}
          required={required}
          autoFocus
          autosize
          minRows={4}
          placeholder={placeholderText}
          value={stagedValue}
          onChange={(e) => handleChange(e.currentTarget.value)}
          leftSection={
          <LeadingIcon color="gray">
            <PencilLine size={12} />
          </LeadingIcon>
        }
          rightSection={restore}
          rightSectionWidth={RIGHT_SECTION_WIDTH}
          rightSectionPointerEvents="auto"
          classNames={{ section: classes.topSection }}
        />
      )
    }

    if (showPicker) {
      return (
        <SourcedInput
          label={label}
          required={required}
          type="text"
          autoFocus
          placeholder={placeholderText}
          value={stagedValue}
          onChange={handleChange}
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
        placeholder={placeholderText}
        value={stagedValue}
        onChange={(e) => handleChange(e.currentTarget.value)}
        leftSection={
          <LeadingIcon color="gray">
            <PencilLine size={12} />
          </LeadingIcon>
        }
        rightSection={restore}
        rightSectionWidth={RIGHT_SECTION_WIDTH}
        rightSectionPointerEvents="auto"
      />
    )
  }

  // state === 'new'
  return (
    <Stack gap="xs">
      <Group justify="space-between" align="center">
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
        value={stagedValue}
        onChange={(plain) => onReplace(plain)}
        source={source}
        sources={sourceOptionsFor(availableSources)}
        onSourceChange={onSourceChange}
      />
    </Stack>
  )
}
