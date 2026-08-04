import { useEffect } from 'react'
import { Group, Stack } from '@mantine/core'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import { useMinDelay } from '@/hooks/useMinDelay'
import { showSnackbar } from '@/utils/snackbar'
import SessionsList from '../components/SessionsList'
import { useSessionsStore } from '../store'
import { SESSIONS_DOCS_URL } from '../constants'

/**
 * Owns everything that reacts to fetched data.
 *
 * This is deliberately a separate component from the page: it is the only thing
 * subscribed to the `list`/`lookup` slices, so a response re-renders the results
 * region alone. When the page itself held the subscription, every request
 * re-rendered the title and the whole filter bar too — which is what made the
 * screen flash on each change.
 */
export default function SessionsResults({ filters, queryKey }) {
  const list = useSessionsStore((s) => s.list)
  const lookup = useSessionsStore((s) => s.lookup)
  const fetchList = useSessionsStore((s) => s.fetchList)
  const loadMoreList = useSessionsStore((s) => s.loadMoreList)

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
  // (main.cljs:57-64 only renders its spinner when the list is empty).
  const isFirstLoad = (status === 'loading' || status === 'idle') && sessions.length === 0
  const showLoader = useMinDelay(isFirstLoad, 500)

  if (showLoader) return <PageLoader h={400} />

  if (status === 'error') {
    return (
      <Stack align="center" gap="md">
        <PageLoader h={240} error={error ?? 'Failed to load sessions.'} />
        <Button variant="default" onClick={() => fetchList(filters, queryKey)}>
          Try again
        </Button>
      </Stack>
    )
  }

  if (sessions.length === 0) {
    return (
      <EmptyState
        title="Nothing here yet with these filters."
        description="Try changing them to explore more sessions."
        docsUrl={SESSIONS_DOCS_URL}
        docsLabel="Sessions documentation"
      />
    )
  }

  return (
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
  )
}
