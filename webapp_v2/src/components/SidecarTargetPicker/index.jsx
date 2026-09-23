import { useEffect, useMemo } from 'react'
import { Anchor, Group, Stack, Text } from '@mantine/core'
import MultiSelect from '@/components/MultiSelect'
import { usesConfigFile } from '@/pages/Sidecars/config'
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
  // Listeners only. There is no "whole sidecar" option, and that is the
  // point: a sidecar-wide rule writes the document's top-level block,
  // which a lane carrying its own mask block silently replaces, so the
  // same rule would apply on some lanes and be ignored on others with
  // nothing here saying which.
  //
  // A listener with no name is left out. The name is the only handle the
  // control plane has on a lane, and a binding to a nameless one is
  // refused on save.
  return (sc.configuration?.listeners ?? [])
    .filter((l) => l?.name)
    .map((l) => ({
      value: encodeTarget({ sidecar_id: sc.id, listener_name: l.name }),
      // The label is the listener name ALONE, because it is also the
      // chip. "appdb (postgres)" doubles a chip's width to repeat what
      // the row below already shows, and a rule bound to six listeners
      // then wraps the field to three lines.
      label: l.name,
      protocol: l.protocol ?? '',
      sidecar: sc.name,
      disabled: locked,
      reason: locked ? CONFIG_FILE_REASON : '',
    }))
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

  const data = useMemo(
    () =>
      sidecars.map((sc) => {
        const locked = usesConfigFile(sc)
        const items = listenerItems(sc, locked)
        // Shown even with no listener to offer, so the admin sees the sidecar
        // and why it is not available.
        if (locked && items.length === 0) {
          items.push({
            value: encodeTarget({ sidecar_id: sc.id, listener_name: '' }),
            label: sc.name,
            protocol: '',
            sidecar: sc.name,
            disabled: true,
            reason: CONFIG_FILE_REASON,
          })
        }
        return { group: sc.name, items }
      }),
    [sidecars],
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
            o.label.toLowerCase().includes(q) || (o.protocol ?? '').toLowerCase().includes(q),
        )
        return items.length > 0 ? { ...group, items } : null
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
        onChange={(values) => onChange(values.map(decodeTarget))}
        // The protocol moves here, where it is read once while choosing,
        // instead of riding in the chip forever. It is what decides which rule
        // types and masking strategies the lane can run, so it earns a place
        // in the row and not in the summary.
        renderOption={({ option }) => (
          <Group justify="space-between" gap="sm" wrap="nowrap" w="100%">
            <Text size="sm" c={option.disabled ? 'dimmed' : undefined}>
              {option.label}
            </Text>
            {option.reason ? (
              <Text size="xs" c="dimmed">
                {option.reason}
              </Text>
            ) : (
              option.protocol && (
                <Text size="xs" c="dimmed" tt="lowercase">
                  {option.protocol}
                </Text>
              )
            )}
          </Group>
        )}
        filter={filter}
        error={failed ? error : undefined}
        disabled={empty || failed}
        searchable
        clearable
      />
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
