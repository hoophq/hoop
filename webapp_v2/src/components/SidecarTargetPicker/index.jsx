import { useEffect } from 'react'
import { Anchor, Stack, Text } from '@mantine/core'
import MultiSelect from '@/components/MultiSelect'
import { useSidecarStore } from '@/stores/useSidecarStore'

// A target is a sidecar and, optionally, one of its listeners. MultiSelect
// carries flat strings, so the pair is encoded into one.
//
// The separator is positional, not a delimiter: a sidecar id is a UUID and
// therefore exactly 36 characters, while a listener name is free text an
// operator wrote in a YAML file and may contain anything, colons included.
// Splitting on the first colon would mangle `pg:primary`; splitting at a fixed
// offset cannot.
const ID_LENGTH = 36

function encodeTarget({ sidecar_id: sidecarID, listener_name: listener }) {
  return `${sidecarID}:${listener ?? ''}`
}

function decodeTarget(value) {
  return {
    sidecar_id: value.slice(0, ID_LENGTH),
    listener_name: value.slice(ID_LENGTH + 1),
  }
}

/**
 * Picks the sidecar listeners a rule is distributed to.
 *
 * One rule, many places: this is what makes a rule authored once reach a fleet,
 * and it is the control plane's whole reason for holding these rules. A rule
 * with no target is stored and distributed to nobody, which is a valid state —
 * a gateway-only rule.
 *
 * Selecting a sidecar with no listener named targets the WHOLE sidecar. That is
 * not "unset": it writes the configuration's top-level block, which every lane
 * on that sidecar inherits. Picking one listener writes that lane alone.
 *
 * Rendered only in the control plane, where the page is given the prop that
 * asks for it. In the gateway the fleet endpoint is not something a page should
 * be calling, so the component never mounts and never fetches.
 */
export default function SidecarTargetPicker({ value = [], onChange, label, description }) {
  const sidecars = useSidecarStore((s) => s.sidecars)
  const loading = useSidecarStore((s) => s.loading)
  const fetchSidecars = useSidecarStore((s) => s.fetchSidecars)

  useEffect(() => {
    fetchSidecars()
  }, [fetchSidecars])

  const data = sidecars.map((sc) => ({
    group: sc.name,
    items: [
      { value: encodeTarget({ sidecar_id: sc.id, listener_name: '' }), label: 'All listeners' },
      // A listener with no name cannot be bound: the name is the only handle
      // the control plane has on a lane, and the gateway refuses a document
      // whose listeners lack one. Leaving it out of the list is how an admin
      // finds out before a save is refused.
      ...(sc.configuration?.listeners ?? [])
        .filter((l) => l?.name)
        .map((l) => ({
          value: encodeTarget({ sidecar_id: sc.id, listener_name: l.name }),
          label: l.protocol ? `${l.name} (${l.protocol})` : l.name,
        })),
    ],
  }))

  const empty = !loading && sidecars.length === 0

  return (
    <Stack gap="xs">
      <MultiSelect
        label={label ?? 'Sidecars and listeners'}
        description={description}
        placeholder={empty ? 'No sidecars yet' : 'Select sidecars and listeners...'}
        data={data}
        value={value.map(encodeTarget)}
        onChange={(values) => onChange(values.map(decodeTarget))}
        disabled={empty}
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
    </Stack>
  )
}
