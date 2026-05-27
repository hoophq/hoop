import { useMemo, useState } from 'react'
import { Stack, Group, Grid } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import ActionIcon from '@/components/ActionIcon'
import Autocomplete from '@/components/Autocomplete'
import { useConfigureRoleStore } from '../store'

// Key/value editor for connection_tags. Mirrors the CLJS pattern in
// webapp/.../setup/tags_inputs.cljs: each pair (existing or pending)
// is two creatable single-selects — Key autocompletes from every tag
// key the org has ever used, Value autocompletes from values seen
// under the picked key. Users can also type a brand-new key/value and
// commit it with the Add button.

const KEY_PATTERN = /^[a-zA-Z0-9-]+$/

// CLJS tags_utils/extract-label: strips the `hoop.dev/<category>.`
// system prefix from a tag key so the user sees `environment` instead
// of `hoop.dev/infrastructure.environment`. Keys that don't match the
// system pattern (free-form user tags) pass through untouched.
const HOOP_LABEL_RE = /^hoop\.dev\/[^.]+\.([^.]+)$/
function labelForTag(key) {
  if (!key) return ''
  const m = key.match(HOOP_LABEL_RE)
  return m ? m[1] : key
}

export default function ConnectionTagsEditor() {
  const tags = useConfigureRoleStore((s) => s.drafts.connection_tags)
  const setTag = useConfigureRoleStore((s) => s.setTag)
  const removeTag = useConfigureRoleStore((s) => s.removeTag)
  const pool = useConfigureRoleStore((s) => s.connectionTagsPool)

  const [draftKey, setDraftKey] = useState('')
  const [draftValue, setDraftValue] = useState('')
  const [keyError, setKeyError] = useState(null)

  // label → full key map drawn from the org-wide pool. When the user
  // picks a label from the dropdown we resolve it back to the full
  // key (with `hoop.dev/...` prefix) before staging the change.
  // Last-wins on label collisions, which is fine — duplicate labels
  // across different system prefixes are practically rare.
  const labelToFullKey = useMemo(() => {
    const m = new Map()
    for (const t of pool) {
      if (!t.key) continue
      m.set(labelForTag(t.key), t.key)
    }
    return m
  }, [pool])

  const keyOptions = useMemo(
    () => Array.from(labelToFullKey.keys()).sort(),
    [labelToFullKey],
  )

  const valuesForLabel = useMemo(() => {
    const map = new Map()
    for (const t of pool) {
      if (!t.key) continue
      const lbl = labelForTag(t.key)
      const list = map.get(lbl) || []
      if (t.value && !list.includes(t.value)) list.push(t.value)
      map.set(lbl, list)
    }
    return map
  }, [pool])

  const entries = Object.entries(tags || {})

  // Resolve a user-facing label back to the org's canonical full key.
  // If the label matches a pool entry we use that entry's full key
  // (keeps `hoop.dev/...` prefix intact for system tags). Otherwise
  // the typed label becomes the key as-is (free-form user tag).
  const resolveLabelToKey = (label) => labelToFullKey.get(label) ?? label

  const commitDraft = () => {
    const k = draftKey.trim()
    const v = draftValue.trim()
    // Both must be non-empty — matches CLJS Add-disabled rule at
    // tags_inputs.cljs:69-73.
    if (!k || !v) return
    if (!KEY_PATTERN.test(k)) {
      setKeyError('Only letters, numbers and hyphens are allowed')
      setTimeout(() => setKeyError(null), 3000)
      return
    }
    setTag(resolveLabelToKey(k), v)
    setDraftKey('')
    setDraftValue('')
  }

  const canCommit = draftKey.trim().length > 0 && draftValue.trim().length > 0

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
              valueOptions={valuesForLabel.get(labelForTag(key)) || []}
              onKeyChange={(newLabel) => {
                const newKey = resolveLabelToKey(newLabel)
                if (newKey === key) return
                // KEY_PATTERN runs against the user-typed label (free-form
                // tags), not the resolved full key — system tags like
                // `hoop.dev/...` come from the pool and always pass.
                if (
                  newKey === newLabel /* free-form */ &&
                  !KEY_PATTERN.test(newLabel)
                ) return
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
            data={draftKey ? valuesForLabel.get(draftKey) || [] : []}
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
            disabled={!canCommit}
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
  // Display the stripped label so users never see the
  // `hoop.dev/<category>.` system prefix in the input.
  const keyLabel = labelForTag(tagKey)
  return (
    <>
      <Grid.Col span={5}>
        <Autocomplete
          label="Key"
          data={keyOptions}
          value={keyLabel}
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
            aria-label={'Remove tag ' + keyLabel}
          >
            <Trash2 size={16} />
          </ActionIcon>
        </Group>
      </Grid.Col>
    </>
  )
}
