import { create } from 'zustand'
import { sessionsService } from '@/services/sessions'
import { PAGE_SIZE } from './constants'

/**
 * Sessions state for /sessions, /sessions/filtered and (Wave 6) /sessions/:id.
 *
 * Page-local rather than a global store: nothing outside the /sessions* tree
 * reads it. It lives at the page root because CLAUDE.md puts files shared by a
 * page and its sub-pages there.
 *
 * Being a module-scope store also matters behaviourally: a row click navigates
 * to the CLJS /sessions/:id page, which unmounts this whole React tree. v1
 * opened a modal and never unmounted, so keeping the rows here is what makes
 * Back feel instant instead of refetching from scratch.
 */

const EMPTY_LIST = {
  items: [],
  total: 0,
  hasMore: false,
  status: 'idle', // idle | loading | ready | error
  error: null,
  loadingMore: false,
  queryKey: null,
}

const EMPTY_LOOKUP = {
  items: [],
  failedIds: [],
  status: 'idle',
  error: null,
  key: null,
}

/**
 * `size-threshold` in webapp events/audit.cljs:222. Above this the gateway is
 * asked NOT to expand the payload, and exec sessions fall back to the separate
 * /result/stream endpoint. Note the download menu uses a *different* 2 MB
 * threshold for a different decision — don't unify them.
 */
const SIZE_THRESHOLD = 4 * 1024 * 1024

const EMPTY_DETAIL = {
  session: null, // the partial row while phase 1 is in flight, then the full session
  status: 'idle', // idle | loading | ready | error
  error: null,
  hasLargePayload: false,
  hasLargeInput: false,
}

const EMPTY_BATCH = {
  items: [],
  total: 0,
  hasMore: false,
  status: 'idle',
  error: null,
  loadingMore: false,
  batchId: null,
}

/**
 * Three slices, not one, because their lifecycles genuinely differ: `list` is
 * URL-driven and must survive navigation; `lookup` must fall back to 'idle' when
 * the ID box empties (that exact status is what re-reveals the main list, per
 * main.cljs:45-52); `batch` belongs to another route. v1 collapsed the last two
 * into one `:audit->filtered-session-by-id` key and they fought over it.
 */

const appendUnique = (prev, next) => {
  const seen = new Set(prev.map((s) => s.id))
  return prev.concat(next.filter((s) => !seen.has(s.id)))
}

/**
 * `has_next_page` from the gateway is `len(items) == limit`
 * (gateway/models/session.go), so it is wrongly true on every exact multiple of
 * the page size. Derive it from `total` instead — and require that the last
 * append actually added something, because with ORDER BY created_at DESC and
 * sessions arriving mid-paging the offset window shifts and `total` grows, which
 * would keep `items.length < total` true forever on a busy org.
 */
const deriveHasMore = (items, total, addedCount) =>
  items.length < total && addedCount > 0

const patchIn = (slice, id, patch) => {
  const index = slice.items.findIndex((s) => s.id === id)
  if (index === -1) return slice // identity preserved → no re-render
  const items = slice.items.slice()
  items[index] = { ...items[index], ...patch }
  return { ...slice, items }
}

export const useSessionsStore = create((set, get) => ({
  list: { ...EMPTY_LIST },
  lookup: { ...EMPTY_LOOKUP },
  batch: { ...EMPTY_BATCH },
  detail: { ...EMPTY_DETAIL },

  // --- session details modal -------------------------------------------------

  /**
   * Two-phase fetch, ported verbatim from :audit->get-session-by-id +
   * :audit->check-session-size (events/audit.cljs:206-273).
   *
   * Phase 1 is a probe: it fetches the session only to read `event_size` and
   * `script_size`. Phase 2 then re-fetches with an `expand` built from those
   * sizes. It is two round-trips for one modal — wasteful, but it is what v1
   * does, and the sizes are not on the list payload.
   */
  openDetail: async (row) => {
    set({ detail: { ...EMPTY_DETAIL, session: row, status: 'loading' } })
    const id = row?.id
    if (!id) return

    const isExec = row?.verb === 'exec'
    try {
      // Phase 1 — probe. v1 asks for base64 here only for exec.
      const probe = await sessionsService.get(id, isExec ? { event_stream: 'base64' } : undefined)
      if (get().detail.session?.id !== id) return // a newer session was opened

      const hasLargePayload = Boolean(probe.event_size && probe.event_size > SIZE_THRESHOLD)
      const hasLargeInput = Boolean(probe.script_size && probe.script_size > SIZE_THRESHOLD)

      // Both oversized: v1 skips the second request entirely.
      if (hasLargePayload && hasLargeInput) {
        set({
          detail: {
            session: probe,
            status: 'ready',
            error: null,
            hasLargePayload,
            hasLargeInput,
          },
        })
        return
      }

      // Phase 2 — the real fetch. `expand` drops whichever half is oversized.
      const expand = [
        !hasLargePayload && 'event_stream',
        !hasLargeInput && 'session_input',
      ].filter(Boolean)

      const params = {}
      if (expand.length) params.expand = expand.join(',')
      if (!hasLargePayload) {
        const liveConnect = probe.verb === 'connect' && probe.status === 'open'
        // Live connect keeps the raw wire frames so the client-side decoder can
        // render historical and streamed events the same way.
        if (isExec) params.event_stream = 'base64'
        else if (!liveConnect && probe.connection_subtype === 'postgres') {
          params.event_stream = 'raw-queries'
        }
      }

      const full = await sessionsService.get(id, params)
      if (get().detail.session?.id !== id) return
      set({
        detail: {
          session: full,
          status: 'ready',
          error: null,
          hasLargePayload,
          hasLargeInput,
        },
      })
    } catch (error) {
      if (get().detail.session?.id !== id) return
      set({
        detail: {
          ...get().detail,
          status: 'error',
          error: error?.message ?? 'Failed to load the session.',
        },
      })
    }
  },

  closeDetail: () => set({ detail: { ...EMPTY_DETAIL } }),

  /** Re-read the open session after a review, kill or re-run. */
  refreshDetail: async () => {
    const current = get().detail.session
    if (current?.id) await get().openDetail(current)
  },

  // --- main list (/sessions) ------------------------------------------------

  fetchList: async (filters, queryKey) => {
    const cached = get().list
    // Stale-while-revalidate: returning to the page with the same filters shows
    // the rows immediately and refreshes underneath, no loader flash.
    const isRevalidate = cached.queryKey === queryKey && cached.status === 'ready'
    set({
      list: {
        ...cached,
        status: isRevalidate ? 'ready' : 'loading',
        error: null,
        queryKey,
      },
    })
    try {
      const { data } = await sessionsService.list({
        ...filters,
        limit: PAGE_SIZE,
        offset: 0,
      })
      // A newer query started while this one was in flight — drop the response.
      if (get().list.queryKey !== queryKey) return
      const items = data?.data ?? []
      const total = data?.total ?? 0
      set({
        list: {
          items,
          total,
          hasMore: items.length < total,
          status: 'ready',
          error: null,
          loadingMore: false,
          queryKey,
        },
      })
    } catch (error) {
      if (get().list.queryKey !== queryKey) return
      set({
        list: {
          ...get().list,
          status: 'error',
          error: error?.message ?? 'Failed to load sessions.',
          loadingMore: false,
        },
      })
    }
  },

  loadMoreList: async (filters) => {
    const current = get().list
    if (current.loadingMore || !current.hasMore) return
    const { queryKey } = current
    set({ list: { ...current, loadingMore: true } })
    try {
      const { data } = await sessionsService.list({
        ...filters,
        limit: PAGE_SIZE,
        offset: current.items.length,
      })
      if (get().list.queryKey !== queryKey) return
      const previous = get().list
      const items = appendUnique(previous.items, data?.data ?? [])
      const total = data?.total ?? previous.total
      set({
        list: {
          ...previous,
          items,
          total,
          hasMore: deriveHasMore(items, total, items.length - previous.items.length),
          loadingMore: false,
        },
      })
    } catch (error) {
      if (get().list.queryKey !== queryKey) return
      set({
        list: {
          ...get().list,
          loadingMore: false,
          error: error?.message ?? 'Failed to load more sessions.',
        },
      })
    }
  },

  // --- ID lookup (search box on /sessions, and /sessions/filtered?id=) -------

  lookupByIds: async (ids) => {
    const key = ids.join(',')
    const current = get().lookup
    // Idempotent for the same input: covers StrictMode's double-effect, remounts,
    // and Enter racing the debounce (v1 fired both, costing 2N requests).
    if (current.key === key && current.status !== 'idle') return
    // Keep the previous rows on screen while the new ones load. Clearing them
    // here empties the list, which flips the page back to its loading state and
    // makes the whole content area flash on every search.
    set({ lookup: { ...current, status: 'loading', error: null, key } })
    try {
      const { sessions, failedIds } = await sessionsService.getFilteredByIds(ids)
      if (get().lookup.key !== key) return
      set({ lookup: { items: sessions, failedIds, status: 'ready', error: null, key } })
    } catch (error) {
      if (get().lookup.key !== key) return
      set({
        lookup: {
          ...EMPTY_LOOKUP,
          status: 'error',
          error: error?.message ?? 'Failed to load sessions.',
          key,
        },
      })
    }
  },

  clearLookup: () => set({ lookup: { ...EMPTY_LOOKUP } }),

  // --- batch (/sessions/filtered?batch_id=) ---------------------------------

  fetchBatch: async (batchId) => {
    // Same reason as `lookupByIds`: keep the current rows mounted while loading.
    const previous = get().batch
    set({
      batch: {
        ...(previous.batchId === batchId ? previous : EMPTY_BATCH),
        status: 'loading',
        error: null,
        batchId,
      },
    })
    try {
      const data = await sessionsService.getByBatchId(batchId, {
        limit: PAGE_SIZE,
        offset: 0,
      })
      if (get().batch.batchId !== batchId) return
      const items = data?.data ?? []
      const total = data?.total ?? 0
      set({
        batch: {
          items,
          total,
          hasMore: items.length < total,
          status: 'ready',
          error: null,
          loadingMore: false,
          batchId,
        },
      })
    } catch (error) {
      if (get().batch.batchId !== batchId) return
      set({
        batch: {
          ...EMPTY_BATCH,
          status: 'error',
          error: error?.message ?? 'Failed to load sessions.',
          batchId,
        },
      })
    }
  },

  loadMoreBatch: async () => {
    const current = get().batch
    if (current.loadingMore || !current.hasMore || !current.batchId) return
    const { batchId } = current
    set({ batch: { ...current, loadingMore: true } })
    try {
      const data = await sessionsService.getByBatchId(batchId, {
        limit: PAGE_SIZE,
        offset: current.items.length,
      })
      if (get().batch.batchId !== batchId) return
      const previous = get().batch
      const items = appendUnique(previous.items, data?.data ?? [])
      const total = data?.total ?? previous.total
      set({
        batch: {
          ...previous,
          items,
          total,
          hasMore: deriveHasMore(items, total, items.length - previous.items.length),
          loadingMore: false,
        },
      })
    } catch (error) {
      if (get().batch.batchId !== batchId) return
      set({
        batch: {
          ...get().batch,
          loadingMore: false,
          error: error?.message ?? 'Failed to load more sessions.',
        },
      })
    }
  },

  /**
   * EVL-132 seam. The SSE live tail calls this on every event; a session updates
   * wherever it happens to be rendered, and no surface needs to know the reader
   * exists. Slices that don't hold the id keep their identity, so a tail running
   * on /sessions/:id never re-renders an unrelated list.
   *
   * The reader itself (a ReadableStream + its AbortController) must live in a
   * module-level variable, NEVER in store state — same idiom as
   * `useConfigStatusStore`'s `inFlight`. Nothing here expects non-plain values.
   */
  patchSession: (id, patch) =>
    set((state) => ({
      list: patchIn(state.list, id, patch),
      lookup: patchIn(state.lookup, id, patch),
      batch: patchIn(state.batch, id, patch),
    })),
}))
