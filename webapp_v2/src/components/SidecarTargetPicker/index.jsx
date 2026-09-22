import { useEffect, useMemo } from 'react'
import { Anchor, Group, Stack, Text } from '@mantine/core'
import { Check } from 'lucide-react'
import MultiSelect from '@/components/MultiSelect'
import { useSidecarStore } from '@/stores/useSidecarStore'

// A target is a sidecar and one of its listeners. MultiSelect carries flat
// strings, so the pair is encoded into one.
//
// The separator is positional, not a delimiter: a sidecar id is a UUID and
// therefore exactly 36 characters, while a listener name is free text an
// operator wrote in a YAML file and may contain anything, colons included.
// Splitting on the first colon would mangle `pg:primary`; splitting at a fixed
// offset cannot.
const ID_LENGTH = 36

// The whole-sidecar shortcut. It is NOT a target and never reaches the API, and
// it is never in `value` either: it is a COMMAND row that selects or clears the
// sidecar's listeners, and the listeners themselves stay the selection. The
// prefix cannot collide with a real value because a real one starts with a
// 36-character UUID.
const ALL_PREFIX = 'all:'
const allValue = (sidecarID) => `${ALL_PREFIX}${sidecarID}`
const isAll = (value) => value.startsWith(ALL_PREFIX)

function encodeTarget({ sidecar_id: sidecarID, listener_name: listener }) {
  return `${sidecarID}:${listener ?? ''}`
}

function decodeTarget(value) {
  return {
    sidecar_id: value.slice(0, ID_LENGTH),
    listener_name: value.slice(ID_LENGTH + 1),
  }
}

// The listeners of a sidecar a rule can actually bind to. A listener with no
// name is left out: the name is the only handle the control plane has on a
// lane, and a binding to a nameless one is refused on save.
const bindableListeners = (sidecar) => (sidecar?.configuration?.listeners ?? []).filter((l) => l?.name)

/**
 * Picks the sidecar listeners a rule is distributed to.
 *
 * One rule, many places: this is what makes a rule authored once reach a fleet,
 * and it is the control plane's whole reason for holding these rules. A rule
 * with no target is stored and distributed to nobody, which is a valid state —
 * a gateway-only rule.
 *
 * A rule binds to LISTENERS, one at a time, and that has not changed. What a
 * sidecar row offers is a SHORTCUT: picking it selects every listener that
 * sidecar has right now, and the value that leaves this component is still the
 * flat list of pairs. There is deliberately no stored "whole sidecar" target —
 * that would have to write the document's top-level block, which a lane
 * carrying its own mask block replaces, so one rule would apply on some lanes
 * and be ignored on others with nothing here saying which.
 *
 * The cost of expanding instead is real and is stated on screen: a listener
 * added after the fact is not covered.
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

  useEffect(() => {
    fetchSidecars()
  }, [fetchSidecars])

  // Which sidecars have every one of their listeners selected. A sidecar with
  // no bindable listener is never "all of them": an empty set would otherwise
  // satisfy `every` and claim a rule reaches a sidecar it reaches on no lane.
  const covered = useMemo(() => {
    const selected = new Set(value.map(encodeTarget))
    const whole = new Set()
    for (const sc of sidecars) {
      const listeners = bindableListeners(sc)
      if (listeners.length === 0) continue
      if (listeners.every((l) => selected.has(encodeTarget({ sidecar_id: sc.id, listener_name: l.name })))) {
        whole.add(sc.id)
      }
    }
    return whole
  }, [value, sidecars])

  const data = useMemo(
    () =>
      sidecars.map((sc) => {
        const listeners = bindableListeners(sc)
        return {
          group: sc.name,
          items: [
            // First in its group, so the shortcut reads as a header rather
            // than as a listener that happens to be called "All listeners".
            // Offered only where it would do something: a sidecar with one
            // listener already has that row, and a sidecar with none has
            // nothing to expand to.
            ...(listeners.length > 1 ? [{ value: allValue(sc.id), label: 'All listeners', sidecar: sc.name }] : []),
            ...listeners.map((l) => ({
              value: encodeTarget({ sidecar_id: sc.id, listener_name: l.name }),
              // The label is the listener name ALONE, because it is also the
              // chip. "appdb (postgres)" doubles a chip's width to repeat what
              // the row below already shows, and a rule bound to six listeners
              // then wraps the field to three lines.
              label: l.name,
              protocol: l.protocol ?? '',
              sidecar: sc.name,
            })),
          ],
        }
      }),
    [sidecars]
  )

  /**
   * Turn what the control hands back into targets.
   *
   * The shortcut never survives this function. Mantine reports it like any
   * other option, so its presence in `values` means it was just clicked:
   * selecting it adds that sidecar's listeners, and clicking it again — while
   * the sidecar is already covered — removes them. Either way what leaves here
   * is the flat list of pairs the API has always taken, and the listeners
   * themselves stay the selection, so every tick in the dropdown is true.
   */
  const handleChange = (values) => {
    const byId = new Map(sidecars.map((sc) => [sc.id, sc]))
    const out = []
    const seen = new Set()
    const drop = new Set()
    const add = (target) => {
      const key = encodeTarget(target)
      if (seen.has(key)) return
      seen.add(key)
      out.push(target)
    }
    for (const v of values) {
      if (!isAll(v)) {
        add(decodeTarget(v))
        continue
      }
      const sidecarID = v.slice(ALL_PREFIX.length)
      if (covered.has(sidecarID)) {
        drop.add(sidecarID)
        continue
      }
      for (const l of bindableListeners(byId.get(sidecarID))) {
        add({ sidecar_id: sidecarID, listener_name: l.name })
      }
    }
    onChange(out.filter((t) => !drop.has(t.sidecar_id)))
  }

  // The sidecars a reader can name in one phrase instead of reading twelve
  // chips for. This is the whole visual difference between a whole-sidecar
  // selection and a listener one: Mantine renders every pill the same and
  // exposes no pill renderer, and painting one with `styles` on the instance
  // is what this app's rules rule out — so the distinction is carried in words,
  // under the field, where it can also say what the shortcut does not do.
  const wholeSidecars = useMemo(
    () => sidecars.filter((sc) => covered.has(sc.id)).map((sc) => sc.name),
    [sidecars, covered]
  )

  // Search matches the SIDECAR too, not just the listener. "payments" is how an
  // operator thinks about a fleet, and Mantine's default filter reads the
  // option label only — so typing a sidecar's name matched nothing, while its
  // group heading sat right there on screen.
  const filter = ({ options, search }) => {
    const q = search.trim().toLowerCase()
    if (q === '') return options
    return options
      .map((group) => {
        // A group whose sidecar matches keeps ALL its listeners: the operator
        // asked for that sidecar, and hiding lanes whose names happen not to
        // contain the query would answer a question they did not ask.
        if (group.group?.toLowerCase().includes(q)) return group
        const items = (group.items ?? []).filter(
          (o) =>
            // The shortcut is never filtered out from under a group that still
            // has a match: it is how the operator takes the rest of them.
            isAll(o.value) ||
            o.label.toLowerCase().includes(q) ||
            (o.protocol ?? '').toLowerCase().includes(q)
        )
        return items.some((o) => !isAll(o.value)) ? { ...group, items } : null
      })
      .filter(Boolean)
  }

  // A fleet that did not load is not an empty fleet. Rendering the failure as
  // "no sidecars yet" tells an admin their fleet is gone and hides the reason,
  // and the disabled input then reads as a state of the product rather than as
  // a request that failed.
  const failed = !loading && !!error && sidecars.length === 0
  const empty = !loading && !error && sidecars.length === 0

  return (
    <Stack gap="xs">
      <MultiSelect
        label={label ?? 'Listeners'}
        description={description}
        placeholder={
          failed
            ? 'Sidecars could not be loaded'
            : empty
              ? 'No sidecars yet'
              : 'Search a sidecar or a listener...'
        }
        data={data}
        value={value.map(encodeTarget)}
        onChange={handleChange}
        // The protocol moves here, where it is read once while choosing,
        // instead of riding in the chip forever. It is what decides which rule
        // types and masking strategies the lane can run, so it earns a place
        // in the row and not in the summary. On the shortcut the same slot
        // carries the count, which is what that row is choosing.
        renderOption={({ option }) => {
          // The shortcut is never in `value`, so Mantine has no tick to draw
          // for it. It says its own state instead — without that, a row that
          // both selects and clears would look identical in either direction.
          if (isAll(option.value)) {
            const on = covered.has(option.value.slice(ALL_PREFIX.length))
            return (
              <Group justify="space-between" gap="sm" wrap="nowrap" w="100%">
                <Text size="sm" fw={600}>
                  {on ? 'Clear all listeners' : 'Select all listeners'}
                </Text>
                {on && <Check size={14} aria-hidden="true" />}
              </Group>
            )
          }
          return (
            <Group justify="space-between" gap="sm" wrap="nowrap" w="100%">
              <Text size="sm">{option.label}</Text>
              {option.protocol && (
                <Text size="xs" c="dimmed" tt="lowercase">
                  {option.protocol}
                </Text>
              )}
            </Group>
          )
        }}
        filter={filter}
        error={failed ? error : undefined}
        disabled={empty || failed}
        searchable
        clearable
      />
      {wholeSidecars.length > 0 && (
        <Text size="xs" c="dimmed">
          {`Every listener of ${wholeSidecars.join(', ')} is selected. That is the listeners it has today: one added later does not join this rule on its own.`}
        </Text>
      )}
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
