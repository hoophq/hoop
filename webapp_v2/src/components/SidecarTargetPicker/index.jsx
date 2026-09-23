import { useEffect, useMemo, useState } from 'react'
import {
  Anchor,
  Checkbox,
  Combobox,
  Group,
  Pill,
  PillsInput,
  Stack,
  Text,
  useCombobox,
} from '@mantine/core'
import Tooltip from '@/components/Tooltip'
import { usesConfigFile } from '@/pages/Sidecars/config'
import { useSidecarStore } from '@/stores/useSidecarStore'
import classes from './SidecarTargetPicker.module.css'

// A target is a sidecar and one of its listeners. MultiSelect carries flat
// strings, so the pair is encoded into one.
//
// The separator is positional, not a delimiter: a sidecar id is a UUID and
// therefore exactly 36 characters, while a listener name is free text an
// operator wrote in a YAML file and may contain anything, colons included.
// Splitting on the first colon would mangle `pg:primary`; splitting at a fixed
// offset cannot.
const ID_LENGTH = 36

// Why a sidecar that runs its config file cannot be picked. The control plane
// does not manage its rules, and the gateway refuses the binding.
const CONFIG_FILE_REASON = 'Uses config file'

function encodeTarget({ sidecar_id: sidecarID, listener_name: listener }) {
  return `${sidecarID}:${listener ?? ''}`
}

function decodeTarget(value) {
  return {
    sidecar_id: value.slice(0, ID_LENGTH),
    listener_name: value.slice(ID_LENGTH + 1),
  }
}

function listenerItems(sc, locked) {
  // Listeners only. The sidecar row in the tree picks each of these, never
  // the sidecar itself: a sidecar-wide rule writes the document's top-level
  // block, which a lane carrying its own mask block silently replaces, so the
  // same rule would apply on some lanes and be ignored on others.
  //
  // A listener with no name is left out. The name is the only handle the
  // control plane has on a lane, and a binding to a nameless one is
  // refused on save.
  return (sc.configuration?.listeners ?? [])
    .filter((l) => l?.name)
    .map((l) => ({
      value: encodeTarget({ sidecar_id: sc.id, listener_name: l.name }),
      name: l.name,
      protocol: l.protocol ?? '',
      disabled: locked,
    }))
}

// Option values carry which row was clicked, so one submit handler can route
// a sidecar row and a listener row.
const SIDECAR_PREFIX = 'sc:'
const LISTENER_PREFIX = 'ln:'

// One node per sidecar, its listeners under it.
function buildTree(sidecars) {
  return sidecars.map((sc) => {
    const locked = usesConfigFile(sc)
    return {
      id: sc.id,
      name: sc.name,
      locked,
      listeners: listenerItems(sc, locked),
    }
  })
}

// Search matches the SIDECAR too, not just the listener. A sidecar whose name
// matches keeps all its listeners: the operator asked for that sidecar.
// Otherwise only matching listeners stay, under their sidecar row.
function filterTree(tree, search) {
  const q = search.trim().toLowerCase()
  return tree
    .map((node) => {
      if (q === '' || node.name.toLowerCase().includes(q)) {
        return { ...node, visibleListeners: node.listeners }
      }
      const visibleListeners = node.listeners.filter(
        (l) => l.name.toLowerCase().includes(q) || l.protocol.toLowerCase().includes(q),
      )
      return visibleListeners.length > 0 ? { ...node, visibleListeners } : null
    })
    .filter(Boolean)
}

// One pill per sidecar with a target, in the order the targets were picked.
// It names the picked listeners, so the field says what is bound without the
// dropdown. A target the fleet no longer lists keeps its stored name, so the
// pill never hides a binding the save would send.
// The pill text stops once it passes this many characters, so a long
// listener name never pushes the "+N more" out of sight.
const PILL_CHARS = 32
// The tooltip lists this many names; the dropdown holds the rest.
const HINT_NAMES = 10

function pillsFor(tree, selected) {
  const byId = new Map(tree.map((n) => [n.id, n]))
  const picked = new Map()
  for (const v of selected) {
    const { sidecar_id: id, listener_name: name } = decodeTarget(v)
    if (!picked.has(id)) picked.set(id, [])
    picked.get(id).push(name)
  }
  return [...picked].map(([id, names]) => {
    const node = byId.get(id)
    const total = node ? node.listeners.filter((l) => !l.disabled).length : 0
    const all = total > 0 && names.length === total
    let shown = 0
    let length = 0
    while (shown < names.length && (shown === 0 || length + names[shown].length <= PILL_CHARS)) {
      length += names[shown].length + 2
      shown++
    }
    const extra = names.length - shown
    // One name longer than the budget is cut, so the count after it stays in view.
    const listed = names.slice(0, shown).map((n) => (n.length > PILL_CHARS ? `${n.slice(0, PILL_CHARS - 1)}…` : n))
    const scope = all ? 'all listeners' : listed.join(', ') + (extra > 0 ? ` +${extra} more` : '')
    return {
      id,
      name: node?.name ?? 'Unknown sidecar',
      scope,
      names,
      // Only a pill that hides a name gets the list on hover.
      hinted: all || extra > 0,
    }
  })
}

// The tooltip body: a count, one name per line, and where the rest are.
function PillHint({ names }) {
  const rest = names.length - HINT_NAMES
  return (
    <Stack gap={2} maw={420}>
      <Text size="xs" fw={600} mb={4}>
        {`${names.length} listener${names.length === 1 ? '' : 's'} selected`}
      </Text>
      {names.slice(0, HINT_NAMES).map((n) => (
        <Text key={n} size="xs" truncate="end">
          {n}
        </Text>
      ))}
      <Text size="xs" c="gray.5" mt={4}>
        {rest > 0 ? `+${rest} more — click to see all` : 'Click to edit'}
      </Text>
    </Stack>
  )
}

/**
 * Picks the sidecar listeners a rule is distributed to.
 *
 * One rule, many places: this is what makes a rule authored once reach a fleet,
 * and it is the control plane's whole reason for holding these rules. A rule
 * with no target is stored and distributed to nobody, which is a valid state —
 * a gateway-only rule.
 *
 * A rule binds to LISTENERS, one at a time. There is no "whole sidecar"
 * option: that would write the document's top-level block, which a lane
 * carrying its own mask block replaces, so one rule would apply on some lanes
 * and be ignored on others with nothing here saying which. The listener is
 * also what carries the protocol, and the protocol decides which rules and
 * which masking strategies are legal at all.
 *
 * The dropdown is a tree: a sidecar row selects or clears all its listeners,
 * and the listener rows under it pick one at a time. The field shows one pill
 * per sidecar, so a sidecar bound on twelve lanes is one chip, not twelve.
 *
 * Rendered only in the control plane, where the page is given the prop that
 * asks for it. In the gateway the fleet endpoint is not something a page should
 * be calling, so the component never mounts and never fetches.
 */

export default function SidecarTargetPicker({ value = [], onChange, label, description }) {
  const sidecars = useSidecarStore((s) => s.sidecars)
  const loading = useSidecarStore((s) => s.loading)
  const error = useSidecarStore((s) => s.error)
  const fetchSidecars = useSidecarStore((s) => s.fetchSidecars)
  const [search, setSearch] = useState('')
  const combobox = useCombobox({ onDropdownClose: () => combobox.resetSelectedOption() })

  useEffect(() => {
    fetchSidecars()
  }, [fetchSidecars])

  const tree = useMemo(() => buildTree(sidecars), [sidecars])
  const selected = useMemo(() => value.map(encodeTarget), [value])
  const selectedSet = useMemo(() => new Set(selected), [selected])
  const visible = useMemo(() => filterTree(tree, search), [tree, search])

  const emit = (values) => onChange(values.map(decodeTarget))

  // A sidecar row adds every listener it can offer, or clears them all once
  // they are all in. It reads the WHOLE sidecar, not the filtered rows, so a
  // search never makes a partial pick look like "all listeners".
  const toggleSidecar = (node) => {
    const values = node.listeners.filter((l) => !l.disabled).map((l) => l.value)
    if (values.length === 0) return
    const all = values.every((v) => selectedSet.has(v))
    if (all) {
      const drop = new Set(values)
      emit(selected.filter((v) => !drop.has(v)))
      return
    }
    emit([...selected, ...values.filter((v) => !selectedSet.has(v))])
  }

  const toggleListener = (val) =>
    emit(selectedSet.has(val) ? selected.filter((v) => v !== val) : [...selected, val])

  // Every target of one sidecar, known or not: a listener deleted since the
  // rule was saved still leaves with its sidecar's pill.
  const removeSidecar = (id) => emit(selected.filter((v) => v.slice(0, ID_LENGTH) !== id))

  const onOptionSubmit = (option) => {
    if (option.startsWith(SIDECAR_PREFIX)) {
      const node = tree.find((n) => n.id === option.slice(SIDECAR_PREFIX.length))
      if (node) toggleSidecar(node)
    } else {
      toggleListener(option.slice(LISTENER_PREFIX.length))
    }
    setSearch('')
  }

  const pills = pillsFor(tree, selected)

  // A pill click opens the list at that sidecar, where every listener it
  // binds is shown and editable.
  const showSidecar = (id) => {
    setSearch('')
    combobox.openDropdown()
    requestAnimationFrame(() =>
      document.querySelector(`[data-sidecar-row="${id}"]`)?.scrollIntoView({ block: 'start' }),
    )
  }

  // A fleet that did not load is not an empty fleet. Rendering the failure as
  // "no sidecars yet" tells an admin their fleet is gone and hides the reason,
  // and the disabled input then reads as a state of the product rather than as
  // a request that failed.
  const failed = !loading && !!error && sidecars.length === 0
  const empty = !loading && !error && sidecars.length === 0
  const disabled = empty || failed
  const placeholder = failed
    ? 'Sidecars could not be loaded'
    : empty
      ? 'No sidecars yet'
      : 'Search a sidecar or a listener...'

  const options = visible.flatMap((node) => {
    const offered = node.listeners.filter((l) => !l.disabled)
    const picked = offered.filter((l) => selectedSet.has(l.value)).length
    const all = offered.length > 0 && picked === offered.length
    return [
      <Combobox.Option
        key={`sc:${node.id}`}
        value={SIDECAR_PREFIX + node.id}
        data-sidecar-row={node.id}
        disabled={offered.length === 0}
      >
        <Group justify="space-between" gap="sm" wrap="nowrap">
          <Group gap="sm" wrap="nowrap">
            <Checkbox
              size="xs"
              readOnly
              tabIndex={-1}
              checked={all}
              indeterminate={picked > 0 && !all}
              disabled={offered.length === 0}
              aria-hidden
              className={classes.check}
            />
            <Text size="sm" fw={600} c={node.locked ? 'dimmed' : undefined}>
              {node.name}
            </Text>
          </Group>
          <Text size="xs" c="dimmed">
            {node.locked ? CONFIG_FILE_REASON : 'Sidecar'}
          </Text>
        </Group>
      </Combobox.Option>,
      ...node.visibleListeners.map((l) => (
        <Combobox.Option
          key={`ln:${l.value}`}
          value={LISTENER_PREFIX + l.value}
          disabled={l.disabled}
          pl="xl"
        >
          <Group justify="space-between" gap="sm" wrap="nowrap">
            <Group gap="sm" wrap="nowrap" miw={0}>
              <Checkbox
                size="xs"
                readOnly
                tabIndex={-1}
                checked={selectedSet.has(l.value)}
                disabled={l.disabled}
                aria-hidden
                className={classes.check}
              />
              <Text size="sm" c={l.disabled ? 'dimmed' : undefined} truncate="end" title={l.name}>
                {l.name}
              </Text>
            </Group>
            {l.protocol && (
              <Text size="xs" c="dimmed" tt="lowercase" flex="0 0 auto">
                {l.protocol}
              </Text>
            )}
          </Group>
        </Combobox.Option>
      )),
    ]
  })

  return (
    <Stack gap="xs">
      <Combobox store={combobox} onOptionSubmit={onOptionSubmit} disabled={disabled}>
        <Combobox.DropdownTarget>
          <PillsInput
            label={label ?? 'Listeners'}
            description={description}
            error={failed ? error : undefined}
            disabled={disabled}
            onClick={() => combobox.openDropdown()}
          >
            <Pill.Group>
              {pills.map((p) => {
                const pill = (
                  <Pill
                    key={p.id}
                    withRemoveButton
                    disabled={disabled}
                    onRemove={() => removeSidecar(p.id)}
                    onClick={(event) => {
                      if (event.target.closest('button')) return
                      showSidecar(p.id)
                    }}
                    className={classes.pill}
                  >
                    <Text span inherit fw={600}>{`${p.name}:`}</Text>
                    {` ${p.scope}`}
                  </Pill>
                )
                return p.hinted ? (
                  <Tooltip key={p.id} label={<PillHint names={p.names} />} position="bottom-start">
                    {pill}
                  </Tooltip>
                ) : (
                  pill
                )
              })}
              <Combobox.EventsTarget>
                <PillsInput.Field
                  value={search}
                  placeholder={pills.length === 0 ? placeholder : ''}
                  disabled={disabled}
                  onFocus={() => combobox.openDropdown()}
                  onBlur={() => combobox.closeDropdown()}
                  onChange={(event) => {
                    combobox.openDropdown()
                    combobox.updateSelectedOptionIndex()
                    setSearch(event.currentTarget.value)
                  }}
                  onKeyDown={(event) => {
                    if (event.key === 'Backspace' && search.length === 0 && pills.length > 0) {
                      event.preventDefault()
                      removeSidecar(pills[pills.length - 1].id)
                    }
                  }}
                />
              </Combobox.EventsTarget>
            </Pill.Group>
          </PillsInput>
        </Combobox.DropdownTarget>

        <Combobox.Dropdown>
          <Combobox.Options mah={280} className={classes.options}>
            {options.length > 0 ? options : <Combobox.Empty>Nothing found</Combobox.Empty>}
          </Combobox.Options>
        </Combobox.Dropdown>
      </Combobox>
      {empty && (
        <Text size="sm" c="dimmed">
          {'Rules reach a sidecar once one is connected. '}
          <Anchor size="sm" href="/sidecars">
            Add a sidecar
          </Anchor>
        </Text>
      )}
      {failed && (
        <Anchor component="button" type="button" size="sm" onClick={() => fetchSidecars()}>
          Try again
        </Anchor>
      )}
    </Stack>
  )
}
