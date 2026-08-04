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
    set({ lookup: { ...EMPTY_LOOKUP, status: 'loading', key } })
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
    set({ batch: { ...EMPTY_BATCH, status: 'loading', batchId } })
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
