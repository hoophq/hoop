import { useCallback, useMemo, useRef, useState } from 'react'
import { connectionsService } from '@/services/connections'

const SEARCH_DEBOUNCE_MS = 300
const MIN_SEARCH_LENGTH = 2

// Paginated connection option source with server-side search and infinite
// scroll. Each call site gets its own independent state.
export function usePaginatedConnections({ pageSize = 50 } = {}) {
  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(0) // 0 = not loaded yet
  const [activeSearch, setActiveSearch] = useState('')
  const [searchValue, setSearchValue] = useState('')
  const [loading, setLoading] = useState(false)

  // Newer requests invalidate older in-flight ones so out-of-order responses
  // can't clobber fresher state.
  const reqIdRef = useRef(0)
  const debounceRef = useRef(null)

  const fetchPage = useCallback(
    async (pageNum, search, { append }) => {
      const reqId = reqIdRef.current + 1
      reqIdRef.current = reqId
      setLoading(true)
      try {
        const { pages, data } = await connectionsService.getConnectionsPaginated({
          page: pageNum,
          pageSize,
          search: search || undefined,
        })
        if (reqId !== reqIdRef.current) return
        const rows = data ?? []
        setItems((prev) => (append ? [...prev, ...rows] : rows))
        setTotal(pages?.total ?? 0)
        setPage(pageNum)
        setActiveSearch(search)
      } catch {
        // keep what's already loaded
      } finally {
        if (reqId === reqIdRef.current) setLoading(false)
      }
    },
    [pageSize],
  )

  const ensureLoaded = useCallback(() => {
    if (page === 0 && !loading) fetchPage(1, '', { append: false })
  }, [page, loading, fetchPage])

  const setSearch = useCallback(
    (term) => {
      setSearchValue(term)
      if (debounceRef.current) clearTimeout(debounceRef.current)
      const trimmed = term.trim()
      const shouldSearch = trimmed === '' || trimmed.length > MIN_SEARCH_LENGTH
      if (!shouldSearch) return
      debounceRef.current = setTimeout(() => {
        fetchPage(1, trimmed, { append: false })
      }, SEARCH_DEBOUNCE_MS)
    },
    [fetchPage],
  )

  const hasMore = items.length < total

  const loadMore = useCallback(() => {
    if (!loading && items.length < total) {
      fetchPage(page + 1, activeSearch, { append: true })
    }
  }, [loading, items.length, total, page, activeSearch, fetchPage])

  const reset = useCallback(() => {
    reqIdRef.current += 1
    if (debounceRef.current) clearTimeout(debounceRef.current)
    setItems([])
    setTotal(0)
    setPage(0)
    setActiveSearch('')
    setSearchValue('')
    setLoading(false)
  }, [])

  const options = useMemo(
    () => items.map((c) => ({ value: c.id, label: c.name })),
    [items],
  )

  return {
    options,
    loading,
    hasMore,
    searchValue,
    setSearch,
    loadMore,
    ensureLoaded,
    reset,
  }
}
