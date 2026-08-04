import { useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Box, Group, Loader, Stack, Text, Title } from '@mantine/core'
import { useIntersection } from '@mantine/hooks'
import { Search } from 'lucide-react'
import CopyButton from '@/components/CopyButton'
import PageLoader from '@/components/PageLoader'
import TextInput from '@/components/TextInput'
import EmptyState from '@/layout/EmptyState'
import { useMinDelay } from '@/hooks/useMinDelay'
import SessionsTable from '../components/SessionsTable'
import { useSessionsStore } from '../store'
import { SESSIONS_DOCS_URL } from '../constants'

const matches = (session, term) =>
  [session.connection, session.type, session.id, session.user_name]
    .filter(Boolean)
    .some((field) => String(field).toLowerCase().includes(term))

/**
 * Port of `sessions_filtered_by_id.cljs` — the Execution Summary surface.
 *
 * `batch_id` wins over `id` when both are present, matching v1 (:50-57). v1
 * fetched twice for `?id=` because both the panel (app.cljs:543-551) and the
 * component dispatched the same event; here a single effect plus the store's
 * idempotency guard means N requests, not 2N.
 */
export default function SessionsFiltered() {
  const [searchParams] = useSearchParams()
  const batchId = searchParams.get('batch_id')?.trim() || null
  const idParam = searchParams.get('id')?.trim() || null

  const batch = useSessionsStore((s) => s.batch)
  const lookup = useSessionsStore((s) => s.lookup)
  const fetchBatch = useSessionsStore((s) => s.fetchBatch)
  const loadMoreBatch = useSessionsStore((s) => s.loadMoreBatch)
  const lookupByIds = useSessionsStore((s) => s.lookupByIds)
  const clearLookup = useSessionsStore((s) => s.clearLookup)

  const [search, setSearch] = useState('')

  useEffect(() => {
    if (batchId) {
      fetchBatch(batchId)
      return
    }
    if (idParam) {
      lookupByIds(idParam.split(',').map((id) => id.trim()).filter(Boolean))
    }
  }, [batchId, idParam, fetchBatch, lookupByIds])

  useEffect(() => () => clearLookup(), [clearLookup])

  const hasTarget = Boolean(batchId || idParam)
  const slice = batchId ? batch : lookup
  const sessions = slice.items
  // With no batch_id and no id there is nothing to fetch, so 'idle' is terminal
  // and must fall through to the empty state — v1 rendered a blank page here.
  // Otherwise 'idle' means "the effect has not run yet" and counts as loading.
  const isFirstLoad =
    hasTarget && (slice.status === 'loading' || slice.status === 'idle') && sessions.length === 0
  const showLoader = useMinDelay(isFirstLoad, 500)

  const visible = useMemo(() => {
    const term = search.trim().toLowerCase()
    if (!term) return sessions
    return sessions.filter((session) => matches(session, term))
  }, [sessions, search])

  // Infinite scroll only exists in batch mode — the id-list mode has no server
  // pagination to page through (v1 had the same guard). `loadMoreBatch` is a
  // zustand action, so its identity is stable across renders.
  const { ref: sentinelRef, entry } = useIntersection({ threshold: 1 })
  useEffect(() => {
    if (entry?.isIntersecting && batchId && batch.hasMore && !batch.loadingMore) {
      loadMoreBatch()
    }
  }, [entry?.isIntersecting, batchId, batch.hasMore, batch.loadingMore, loadMoreBatch])

  const shareUrl = batchId
    ? `${window.location.origin}/sessions/filtered?${new URLSearchParams({ batch_id: batchId })}`
    : null

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start" wrap="wrap">
        <Title order={1}>Execution Summary</Title>
        <Group gap="sm" wrap="wrap">
          <TextInput
            w={256}
            placeholder="Search by resource role or type"
            value={search}
            onChange={(event) => setSearch(event.currentTarget.value)}
            leftSection={<Search size={16} />}
          />
          {/* CopyButton renders nothing when disable_clipboard_copy_cut is on,
              which is exactly the gate v1 put on its Share button. */}
          {shareUrl && <CopyButton value={shareUrl} label="Copy share link" />}
        </Group>
      </Group>

      {showLoader && <PageLoader h={400} />}

      {!showLoader && slice.status === 'error' && (
        <PageLoader h={240} error={slice.error ?? 'Failed to load sessions.'} />
      )}

      {!showLoader && slice.status !== 'error' && sessions.length === 0 && !isFirstLoad && (
        <EmptyState
          title="Beep boop, no sessions to look"
          description={
            hasTarget
              ? "There's nothing with this criteria."
              : 'This page needs a batch_id or a comma-separated id list in the URL.'
          }
          docsUrl={SESSIONS_DOCS_URL}
          docsLabel="Sessions documentation"
        />
      )}

      {!showLoader && slice.status !== 'error' && sessions.length > 0 && (
        <>
          {visible.length > 0 ? (
            <SessionsTable sessions={visible} />
          ) : (
            <Text ta="center" c="dimmed" py="lg">
              No sessions found matching your search
            </Text>
          )}
          {batchId && batch.hasMore && (
            <Box ref={sentinelRef} h={1}>
              {batch.loadingMore && (
                <Group justify="center" py="md">
                  <Loader size="sm" />
                </Group>
              )}
            </Box>
          )}
        </>
      )}
    </Stack>
  )
}
