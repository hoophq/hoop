import { useMemo, useState } from 'react'
import { Stack, Group, Grid, Text } from '@mantine/core'
import { Plus, Trash2 } from 'lucide-react'
import Button from '@/components/Button'
import ActionIcon from '@/components/ActionIcon'
import Autocomplete from '@/components/Autocomplete'
import { labelForTag } from '@/utils/tags'

// Key/value editor for resource tags (connection_tags, agent tags). Mirrors
// the CLJS pattern in webapp/.../setup/tags_inputs.cljs: each pair (existing
// or pending) is two creatable single-selects — Key autocompletes from every
// tag key in `pool`, Value autocompletes from values seen under the picked
// key. Users can also type a brand-new key/value and commit it with the Add
// button.

// The Roles → Configure rules: only the key is checked, against the typed
// label.
const DEFAULT_RULES = {
  keyPattern: /^[a-zA-Z0-9-]+$/,
  keyError: 'Only letters, numbers and hyphens are allowed',
}

/**
 * Controlled key/value tags editor.
 *
 * Usage:
 *   <TagsEditor
 *     value={{ 'hoop.dev/infrastructure.environment': 'prod' }}
 *     onChange={setTags}
 *     pool={[{ key: 'hoop.dev/infrastructure.environment', value: 'prod' }]}
 *   />
 *
 * `pool` is the suggestion list, `[{ key, value }]`. `onChange` receives the
 * whole next tags object. `rules` sets what the editor accepts:
 * `{ keyPattern, keyError, valuePattern?, valueError?, maxTags?,
 * warnPendingDraft? }`. The default is the Roles → Configure rule set.
 */
export default function TagsEditor({
  value,
  onChange,
  pool = [],
  disabled = false,
  rules = DEFAULT_RULES,
}) {
  const tags = value || {}
  const { keyPattern, keyError: keyRuleError, valuePattern, valueError: valueRuleError, maxTags } = rules
  const valueErrorOf = (val) => (valuePattern && !valuePattern.test(val) ? valueRuleError : null)

  const [draftKey, setDraftKey] = useState('')
  const [draftValue, setDraftValue] = useState('')
  const [keyError, setKeyError] = useState(null)
  const [valueError, setValueError] = useState(null)

  // label → full key map drawn from the pool. When the user picks a label
  // from the dropdown we resolve it back to the full key (with
  // `hoop.dev/...` prefix) before staging the change. Last-wins on label
  // collisions, which is fine — duplicate labels across different system
  // prefixes are practically rare.
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

  const entries = Object.entries(tags)

  const setTag = (key, val) => onChange({ ...tags, [key]: val })

  const removeTag = (key) => {
    const next = { ...tags }
    delete next[key]
    onChange(next)
  }

  const renameTag = (oldKey, newKey) => {
    const next = { ...tags }
    const val = next[oldKey]
    delete next[oldKey]
    next[newKey] = val
    onChange(next)
  }

  // Resolve a user-facing label back to the pool's canonical full key.
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
    if (!keyPattern.test(k)) {
      setKeyError(keyRuleError)
      setTimeout(() => setKeyError(null), 3000)
      return
    }
    const vError = valueErrorOf(v)
    if (vError) {
      setValueError(vError)
      return
    }
    setTag(resolveLabelToKey(k), v)
    setDraftKey('')
    setDraftValue('')
  }

  const atMaxTags = maxTags != null && entries.length >= maxTags
  const hasDraft = draftKey.trim().length > 0 && draftValue.trim().length > 0
  const canCommit = hasDraft && !atMaxTags

  return (
    <Stack gap="md">
      {entries.length > 0 && (
        <Grid gutter="md" align="flex-end">
          {entries.map(([key, val]) => (
            <RowFragment
              key={key}
              tagKey={key}
              tagValue={val}
              keyOptions={keyOptions}
              valueOptions={valuesForLabel.get(labelForTag(key)) || []}
              valueError={valueErrorOf(val)}
              disabled={disabled}
              onKeyChange={(newLabel) => {
                const newKey = resolveLabelToKey(newLabel)
                if (newKey === key) return
                // keyPattern runs against the user-typed label (free-form
                // tags), not the resolved full key — system tags like
                // `hoop.dev/...` come from the pool and always pass.
                if (
                  newKey === newLabel /* free-form */ &&
                  !keyPattern.test(newLabel)
                ) return
                renameTag(key, newKey)
              }}
              onValueChange={(newValue) => setTag(key, newValue)}
              onRemove={() => removeTag(key)}
            />
          ))}
        </Grid>
      )}

      {!disabled && (
        <Grid gutter="md" align="flex-end">
          <Grid.Col span={5}>
            <Autocomplete
              label="Key"
              placeholder="Select or create a key..."
              data={keyOptions}
              value={draftKey}
              onChange={(val) => {
                setDraftKey(val)
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
              onChange={(val) => {
                setDraftValue(val)
                if (valueError) setValueError(null)
              }}
              error={valueError}
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
      )}

      {!disabled && atMaxTags && (
        <Text size="xs" c="dimmed">{`Maximum of ${maxTags} tags reached.`}</Text>
      )}
      {!disabled && rules.warnPendingDraft && hasDraft && !atMaxTags && (
        <Text size="xs" c="dimmed">Click Add to keep this tag. A tag that is not added is not saved.</Text>
      )}
    </Stack>
  )
}

function RowFragment({
  tagKey,
  tagValue,
  keyOptions,
  valueOptions,
  valueError,
  disabled,
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
          disabled={disabled}
        />
      </Grid.Col>
      <Grid.Col span={5}>
        <Autocomplete
          label="Value"
          data={valueOptions}
          value={tagValue}
          onChange={onValueChange}
          error={valueError}
          disabled={disabled}
        />
      </Grid.Col>
      <Grid.Col span={2}>
        {!disabled && (
          <Group justify="center">
            <ActionIcon
              variant="subtle"
              color="red"
              onClick={onRemove}
              aria-label={'Remove tag ' + keyLabel}
            >
              <Trash2 size={16} />
            </ActionIcon>
          </Group>
        )}
      </Grid.Col>
    </>
  )
}
