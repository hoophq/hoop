import { Fragment, useEffect, useMemo, useState } from 'react'
import { Divider, Group, Stack, Text } from '@mantine/core'
import { Search, Server } from 'lucide-react'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import TextInput from '@/components/TextInput'
import { useMinDelay } from '@/hooks/useMinDelay'
import { useSidecarStore } from '@/stores/useSidecarStore'
import SidecarSlackChannelsModal from './sections/SidecarSlackChannelsModal'

function namedListeners(sidecar) {
  return (sidecar.configuration?.listeners ?? []).filter((l) => l.name)
}

/**
 * The control plane's counterpart of the gateway's Connections tab: reviews a
 * listener holds go to the channels set here for that listener.
 */
function SidecarSlackChannelsTab() {
  const sidecars = useSidecarStore((s) => s.sidecars)
  const loading = useSidecarStore((s) => s.loading)
  const error = useSidecarStore((s) => s.error)
  const fetchSidecars = useSidecarStore((s) => s.fetchSidecars)
  const [search, setSearch] = useState('')
  const [editing, setEditing] = useState(null)

  useEffect(() => {
    fetchSidecars()
  }, [fetchSidecars])

  const filtered = useMemo(() => {
    const term = search.trim().toLowerCase()
    if (!term) return sidecars
    return sidecars.filter((s) => s.name?.toLowerCase().includes(term))
  }, [sidecars, search])

  const showLoader = useMinDelay(loading)
  if (showLoader) return <PageLoader h={200} />
  if (error) return <PageLoader error h={200} message="Failed to load sidecars." />

  return (
    <Stack gap="md">
      <TextInput
        placeholder="Search sidecars"
        leftSection={<Search size={16} />}
        value={search}
        onChange={(e) => setSearch(e.currentTarget.value)}
      />
      {filtered.length === 0 ? (
        <Text size="sm" c="dimmed" fs="italic">
          {search.trim() ? 'No sidecars match your search.' : `You don't have any sidecars`}
        </Text>
      ) : (
        <Stack gap={0}>
          {filtered.map((sidecar, index) => {
            const listeners = namedListeners(sidecar)
            return (
              <Fragment key={sidecar.id}>
                <Group justify="space-between" py="sm">
                  <Group gap="sm">
                    <Server size={16} />
                    <Text size="sm" fw={600}>
                      {sidecar.name}
                    </Text>
                    <Text size="xs" c="dimmed">
                      {listeners.length === 1 ? '1 listener' : `${listeners.length} listeners`}
                    </Text>
                  </Group>
                  <Button variant="outline" size="xs" onClick={() => setEditing(sidecar)}>
                    Configure
                  </Button>
                </Group>
                {index < filtered.length - 1 && <Divider />}
              </Fragment>
            )
          })}
        </Stack>
      )}

      {editing && (
        <SidecarSlackChannelsModal
          sidecar={editing}
          listeners={namedListeners(editing)}
          onClose={() => setEditing(null)}
        />
      )}
    </Stack>
  )
}

export default SidecarSlackChannelsTab
