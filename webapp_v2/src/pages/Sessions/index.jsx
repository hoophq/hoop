import { useEffect } from 'react'
import { Group, Stack, Title } from '@mantine/core'
import SessionsFilterBar from './sections/SessionsFilterBar'
import SessionsResults from './sections/SessionsResults'
import SessionsCount from './sections/SessionsCount'
import { useSessionsStore } from './store'
import { useSessionFilters } from './useSessionFilters'
import SessionDetailsModal from './Detail'

/**
 * Deliberately subscribes to NO data. `fetchList` is a stable zustand action, so
 * this component only re-renders when the URL params change — which means a
 * response repaints the results region and the count, never the title or the
 * filter bar. That split is what keeps the page from flashing on every request;
 * v1 got the same effect for free because re-frame re-rendered only the
 * subscribed subtree.
 */
export default function Sessions() {
  const { filters, queryKey, setFilters } = useSessionFilters()
  const fetchList = useSessionsStore((s) => s.fetchList)

  // `queryKey` is a primitive projection of the filter params and `fetchList` is
  // a stable action, so this fires exactly once per filter change.
  useEffect(() => {
    fetchList(filters, queryKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queryKey, fetchList])

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <Stack gap="xs">
          <Title order={1}>Sessions</Title>
          <SessionsCount />
        </Stack>
      </Group>

      <SessionsFilterBar filters={filters} setFilters={setFilters} />

      <SessionsResults filters={filters} queryKey={queryKey} />

      <SessionDetailsModal />
    </Stack>
  )
}
