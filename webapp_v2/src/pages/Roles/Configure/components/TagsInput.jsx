import { useMemo, useState } from 'react'
import { Stack, Group, Button, ActionIcon, Text, Grid, Autocomplete } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import { useConfigureRoleStore } from '../store'

// Key/value editor for connection_tags. Mirrors the CLJS pattern in
// webapp/.../setup/tags_inputs.cljs: each pair (existing or pending)
// is two creatable single-selects — Key autocompletes from every tag
// key the org has ever used, Value autocompletes from values seen
// under the picked key. Users can also type a brand-new key/value and
// commit it with the Add button.
//
// Mantine's <Autocomplete> is the closest stock match for CLJS's
// single-creatable-grouped — it shows suggestions but allows free
// typing, so "creating" a new option is just typing it and hitting
// Save (the value is the input).

const KEY_PATTERN = /^[a-zA-Z0-9-]+$/

export default function TagsInput() {
  const tags = useConfigureRoleStore((s) => s.drafts.connection_tags)
  const setTag = useConfigureRoleStore((s) => s.setTag)
  const removeTag = useConfigureRoleStore((s) => s.removeTag)
  const pool = useConfigureRoleStore((s) => s.connectionTagsPool)

  const [draftKey, setDraftKey] = useState('')
  const [draftValue, setDraftValue] = useState('')
  const [keyError, setKeyError] = useState(null)

  const keyOptions = useMemo(() => {
    const set = new Set(pool.map((t) => t.key).filter(Boolean))
    return Array.from(set).sort()
  }, [pool])

  const valuesForKey = useMemo(() => {
    const map = new Map()
    for (const t of pool) {
      if (!t.key) continue
      const list = map.get(t.key) || []
      if (t.value && !list.includes(t.value)) list.push(t.value)
      map.set(t.key, list)
    }
    return map
  }, [pool])

  const entries = Object.entries(tags || {})

  const commitDraft = () => {
    const k = draftKey.trim()
    if (!k) return
    if (!KEY_PATTERN.test(k)) {
      setKeyError('Only letters, numbers and hyphens are allowed')
      setTimeout(() => setKeyError(null), 3000)
      return
    }
    setTag(k, draftValue)
    setDraftKey('')
    setDraftValue('')
  }

  return (
    <Stack gap="md">
      {entries.length > 0 && (
        <Grid gutter="md">
          {entries.map(([key, value]) => (
            <RowFragment
              key={key}
              tagKey={key}
              tagValue={value}
              keyOptions={keyOptions}
              valueOptions={valuesForKey.get(key) || []}
              onKeyChange={(newKey) => {
                if (newKey === key) return
                if (!KEY_PATTERN.test(newKey)) return
                removeTag(key)
                setTag(newKey, value)
              }}
              onValueChange={(newValue) => setTag(key, newValue)}
              onRemove={() => removeTag(key)}
            />
          ))}
        </Grid>
      )}

      <Grid gutter="md" align="flex-end">
        <Grid.Col span={5}>
          <Autocomplete
            label="Key"
            placeholder="Select or create a key..."
            data={keyOptions}
            value={draftKey}
            onChange={(value) => {
              setDraftKey(value)
              if (keyError) setKeyError(null)
            }}
            error={keyError}
          />
        </Grid.Col>
        <Grid.Col span={5}>
          <Autocomplete
            label="Value"
            placeholder={draftKey ? 'Select or create a value...' : 'First select a key...'}
            data={draftKey ? valuesForKey.get(draftKey) || [] : []}
            value={draftValue}
            onChange={setDraftValue}
            disabled={!draftKey.trim()}
          />
        </Grid.Col>
        <Grid.Col span={2}>
          <Button
            variant="light"
            leftSection={<Plus size={14} />}
            onClick={commitDraft}
            disabled={!draftKey.trim()}
            fullWidth
          >
            Add
          </Button>
        </Grid.Col>
      </Grid>
    </Stack>
  )
}

function RowFragment({
  tagKey,
  tagValue,
  keyOptions,
  valueOptions,
  onKeyChange,
  onValueChange,
  onRemove,
}) {
  return (
    <>
      <Grid.Col span={5}>
        <Autocomplete
          label="Key"
          data={keyOptions}
          value={tagKey}
          onChange={onKeyChange}
        />
      </Grid.Col>
      <Grid.Col span={5}>
        <Autocomplete
          label="Value"
          data={valueOptions}
          value={tagValue}
          onChange={onValueChange}
        />
      </Grid.Col>
      <Grid.Col span={2}>
        <Group justify="center">
          <ActionIcon
            variant="subtle"
            color="red"
            size="lg"
            onClick={onRemove}
            aria-label={'Remove tag ' + tagKey}
          >
            <Trash2 size={16} />
          </ActionIcon>
        </Group>
      </Grid.Col>
    </>
  )
}
