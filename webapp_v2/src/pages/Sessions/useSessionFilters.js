import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'

/**
 * The query string IS the filter state — same contract as v1, where
 * `:audit->get-sessions` (events/audit.cljs:46) read `window.location.search`
 * at fetch time and `:audit->filter-sessions` (:183) wrote it back.
 *
 * Keys are written verbatim as gateway query params; this list IS the contract
 * (openapi.AvailableSessionOptions). Unlike v1 — which forwarded *every* param
 * present in the URL, relying on the gateway silently ignoring unknown ones —
 * this is an allow-list, so a stray `?foo=bar` never reaches the API.
 * `batch_id` and `id` are deliberately absent: they belong to /sessions/filtered.
 */
export const FILTER_KEYS = [
  'user', // user UUID (matched against s.user_id); the dropdown shows emails
  'connection', // connection NAME, not id
  'type', // custom | database | application
  'review.status', // PENDING | APPROVED | REJECTED
  'start_date', // RFC3339
  'end_date', // RFC3339
  'jira_issue_key', // free text, comma-separated server-side
]

export function useSessionFilters() {
  const [searchParams, setSearchParams] = useSearchParams()

  const filters = useMemo(() => {
    const out = {}
    for (const key of FILTER_KEYS) {
      const value = searchParams.get(key)
      if (value != null && value !== '') out[key] = value
    }
    return out
  }, [searchParams])

  /**
   * Primitive projection of the filter params, used as the fetch effect's only
   * dependency. Deriving a string (rather than depending on `filters` or
   * `searchParams`, both fresh objects every render) is what keeps the effect
   * from looping, and scoping it to FILTER_KEYS means an unrelated future param
   * never triggers a refetch.
   */
  const queryKey = useMemo(
    () => FILTER_KEYS.map((key) => `${key}=${searchParams.get(key) ?? ''}`).join('&'),
    [searchParams]
  )

  /**
   * Merge a patch into the query string. A null/empty value DELETES the key —
   * writing `?connection=` would read server-side as "filter by the empty name".
   * Pass `{ replace: true }` for debounced text inputs so a dozen keystrokes
   * don't bury the previous page under a dozen history entries.
   */
  const setFilters = useCallback(
    (patch, { replace = false } = {}) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          for (const [key, value] of Object.entries(patch)) {
            if (value == null || value === '') next.delete(key)
            else next.set(key, value)
          }
          return next
        },
        { replace }
      )
    },
    [setSearchParams]
  )

  const clearAll = useCallback(() => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      FILTER_KEYS.forEach((key) => next.delete(key))
      return next
    })
  }, [setSearchParams])

  return {
    filters,
    queryKey,
    setFilters,
    clearAll,
    activeCount: Object.keys(filters).length,
  }
}
