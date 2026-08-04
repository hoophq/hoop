import { useEffect } from 'react'
import { Group, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import { useMinDelay } from '@/hooks/useMinDelay'
import { showSnackbar } from '@/utils/snackbar'
import SessionsList from './components/SessionsList'
import SessionsFilterBar from './sections/SessionsFilterBar'
import { useSessionsStore } from './store'
import { useSessionFilters } from './useSessionFilters'
import { SESSIONS_DOCS_URL } from './constants'

export default function Sessions() {
  const { filters, queryKey, setFilters } = useSessionFilters()
  const list = useSessionsStore((s) => s.list)
  const lookup = useSessionsStore((s) => s.lookup)
  const fetchList = useSessionsStore((s) => s.fetchList)
  const loadMoreList = useSessionsStore((s) => s.loadMoreList)

  // `queryKey` is a primitive projection of the filter params and `fetchList` is
  // a stable zustand action, so this fires exactly once per filter change.
  useEffect(() => {
    fetchList(filters, queryKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queryKey, fetchList])

  // Surface IDs that came back 404 — v1 collected them only to know when it was
  // finished, so a typo silently produced no row.
  useEffect(() => {
    if (lookup.status !== 'ready' || !lookup.failedIds.length) return
    showSnackbar({
      level: 'error',
      text: `${lookup.failedIds.length} session ID(s) could not be loaded.`,
      description: lookup.failedIds.join(', '),
    })
  }, [lookup.status, lookup.failedIds])

  // The ID search hijacks the list while it has results, and pagination is
  // disabled for its duration (main.cljs:45-52).
  const idSearchActive = lookup.status !== 'idle'
  const sessions = idSearchActive ? lookup.items : list.items
  const status = idSearchActive ? lookup.status : list.status
  const error = idSearchActive ? lookup.error : list.error

  // The loader only ever covers the FIRST load, when there is nothing to show.
  // Once rows exist they stay mounted through every refetch — v1 did the same
  // (main.cljs:57-64 only renders its spinner when the list is empty), and
  // swapping the content area in and out on each request is what makes the page
  // flash. 'idle' counts as loading because the fetch effect has not run yet on
  // the very first render; treating it as "no results" would flash the empty state.
  const isFirstLoad = (status === 'loading' || status === 'idle') && sessions.length === 0
  const showLoader = useMinDelay(isFirstLoad, 500)

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <Stack gap="xs">
          <Title order={1}>Sessions</Title>
          {!idSearchActive && list.status === 'ready' && (
            <Text c="dimmed" size="lg">
              {`Showing ${list.items.length} of ${list.total} sessions`}
            </Text>
          )}
        </Stack>
      </Group>

      <SessionsFilterBar filters={filters} setFilters={setFilters} />

      {showLoader && <PageLoader h={400} />}

      {!showLoader && status === 'error' && (
        <Stack align="center" gap="md">
          <PageLoader h={240} error={error ?? 'Failed to load sessions.'} />
          <Button variant="default" onClick={() => fetchList(filters, queryKey)}>
            Try again
          </Button>
        </Stack>
      )}

      {!showLoader && status !== 'error' && sessions.length === 0 && !isFirstLoad && (
        <EmptyState
          title="Nothing here yet with these filters."
          description="Try changing them to explore more sessions."
          docsUrl={SESSIONS_DOCS_URL}
          docsLabel="Sessions documentation"
        />
      )}

      {!showLoader && status !== 'error' && sessions.length > 0 && (
        <>
          <SessionsList sessions={sessions} />
          {!idSearchActive && list.hasMore && (
            <Group justify="center">
              <Button
                variant="subtle"
                loading={list.loadingMore}
                onClick={() => loadMoreList(filters)}
              >
                Load more sessions
              </Button>
            </Group>
          )}
        </>
      )}
    </Stack>
  )
}
