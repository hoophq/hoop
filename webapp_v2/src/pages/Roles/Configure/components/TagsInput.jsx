import { useState } from 'react'
import { Stack, Group, Button, ActionIcon } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import TextInput from '@/components/TextInput'
import { useConfigureRoleStore } from '../store'

// Key/value editor for connection_tags. Each row is one tag; the trailing
// row is a draft pair the user fills in and commits via the Add button.
//
// Tags are stored as a flat { key: value } map on the connection, so
// updating any field that exists immediately reflects in the store —
// only the "draft pair" (the empty row at the bottom) needs to be
// committed explicitly to prevent accidental empty-key entries.
export default function TagsInput() {
  const tags = useConfigureRoleStore((s) => s.drafts.connection_tags)
  const setTag = useConfigureRoleStore((s) => s.setTag)
  const removeTag = useConfigureRoleStore((s) => s.removeTag)

  const [draftKey, setDraftKey] = useState('')
  const [draftValue, setDraftValue] = useState('')

  const entries = Object.entries(tags || {})

  const commitDraft = () => {
    const k = draftKey.trim()
    if (!k) return
    setTag(k, draftValue)
    setDraftKey('')
    setDraftValue('')
  }

  // Editing an existing key isn't supported through the inline inputs —
  // the user removes and re-adds. Editing the value in place works
  // because we set the same key.
  return (
    <Stack gap="sm">
      {entries.map(([key, value]) => (
        <Group key={key} gap="sm" align="flex-end" wrap="nowrap">
          <TextInput label="Key" value={key} disabled flex={1} />
          <TextInput
            label="Value"
            value={value}
            onChange={(e) => setTag(key, e.currentTarget.value)}
            flex={1}
          />
          <ActionIcon
            variant="subtle"
            color="red"
            size="lg"
            onClick={() => removeTag(key)}
            aria-label={'Remove tag ' + key}
          >
            <Trash2 size={16} />
          </ActionIcon>
        </Group>
      ))}
      <Group gap="sm" align="flex-end" wrap="nowrap">
        <TextInput
          label="Key"
          placeholder="Select or create a key..."
          value={draftKey}
          onChange={(e) => setDraftKey(e.currentTarget.value)}
          flex={1}
        />
        <TextInput
          label="Value"
          placeholder="First select a key..."
          value={draftValue}
          onChange={(e) => setDraftValue(e.currentTarget.value)}
          disabled={!draftKey.trim()}
          flex={1}
        />
        <Button
          variant="light"
          leftSection={<Plus size={14} />}
          onClick={commitDraft}
          disabled={!draftKey.trim()}
        >
          Add
        </Button>
      </Group>
    </Stack>
  )
}
