import { useEffect, useMemo, useState } from 'react'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { Activity } from 'lucide-react'
import PageLoader from '@/components/PageLoader'
import ValueFilter from '@/components/ValueFilter'
import { useMinDelay } from '@/hooks/useMinDelay'
import EmptyState from '@/layout/EmptyState'
import { useSidecarsStore } from './store'
import { SIDECAR_STATES, STATE_FILTER_VALUES } from './constants'
import SidecarRow from './components/SidecarRow'
// MOCK: delete this import and the <MockBanner /> below. See mock/index.js.
import MockBanner from './mock/Banner'

/**
 * The fleet.
 *
 * One page, keyed on the sidecar, with its resources under an expansion. That
 * follows what the Control Plane is for: "which Sidecars are running, what each
 * one resolved, and when it last checked in" — one view, three answers.
 *
 * It is the right shape while a sidecar runs a handful of listeners, which is
 * what the docs describe ("Most deployments run a single listener"). The axis
 * that grows is the number of sidecars, not the number of listeners inside one.
 *
 * What it cannot answer is the inverted question — "which sidecars serve appdb?"
 * — because one `connection` is served by many sidecars. That needs a
 * fleet-wide resource index keyed on `connection`, and it is an ADDITIVE page,
 * not a rewrite of this one. Which is why there is no resource detail route here.
 */
export default function Sidecars() {
  const list = useSidecarsStore((s) => s.list)
  const listStatus = useSidecarsStore((s) => s.listStatus)
  const fetchList = useSidecarsStore((s) => s.fetchList)

  const [selectedState, setSelectedState] = useState(null)

  useEffect(() => {
    fetchList()
  }, [fetchList])

  const filtered = useMemo(() => {
    if (!selectedState) return list
    return list.filter((s) => SIDECAR_STATES[s.state]?.label === selectedState)
  }, [list, selectedState])

  const loading = listStatus === 'loading'
  const showLoader = useMinDelay(loading && list.length === 0, 500)

  if (showLoader) {
    return <PageLoader h={300} />
  }

  // A failed load leaves the list empty, which would otherwise fall through to
  // the empty state and tell an admin their fleet is gone.
  if (listStatus === 'error') {
    return <PageLoader error h={300} message="Failed to load the fleet." />
  }

  return (
    <Stack gap="xl">
      <Stack gap="sm">
        <Title order={1}>Sidecars</Title>
        <Text size="md" c="dimmed">
          Every sidecar registered with this control plane, what each one resolved, and
          when it last checked in
        </Text>
      </Stack>

      <MockBanner />

      {list.length === 0 ? (
        // Never "you have no sidecars". Inventory is held in memory, so a
        // control plane restart empties this view until sidecars reconnect —
        // roughly one backoff window. An empty list that reads as an outage is
        // the failure this wording exists to prevent.
        <EmptyState
          compact
          title="No sidecars have checked in"
          description="A sidecar appears here on its first check-in. After a control plane restart the list rebuilds as each one reconnects, which takes about one backoff window."
        />
      ) : (
        <>
          <Group gap="sm">
            <ValueFilter
              icon={Activity}
              label="State"
              values={STATE_FILTER_VALUES}
              selected={selectedState}
              onSelect={setSelectedState}
              onClear={() => setSelectedState(null)}
            />
          </Group>

          {filtered.length === 0 ? (
            <EmptyState
              compact
              title="No sidecars match your filter"
              description="Try clearing the filter above."
            />
          ) : (
            <Box>
              {filtered.map((sidecar, idx) => (
                <SidecarRow
                  key={sidecar.name}
                  sidecar={sidecar}
                  isFirst={idx === 0}
                  isLast={idx === filtered.length - 1}
                />
              ))}
            </Box>
          )}
        </>
      )}
    </Stack>
  )
}
