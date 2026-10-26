import { useEffect, useMemo, useRef, useState } from 'react'
import { connectionOption, usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import { connectionsService } from '@/services/connections'
import PaginatedMultiSelect from '@/components/PaginatedMultiSelect'

const RESOLVE_CHUNK = 100 // /connections page_size cap

function chunk(arr, size) {
  const out = []
  for (let i = 0; i < arr.length; i += size) out.push(arr.slice(i, i + size))
  return out
}

/**
 * Resource-role (connection) multi-select with paginated options + server search.
 * Labels for already-selected ids are resolved via `?connection_ids=`, so chips
 * show names without loading the full connection list. `includeSidecarMirrors`
 * offers the sidecar listener mirrors too, tagged "Sidecar".
 *
 * Usage:
 *   <ConnectionsMultiSelect value={form.connectionIds} onChange={setIds} />
 */
export default function ConnectionsMultiSelect({
  value = [],
  onChange,
  label = 'Resource Roles',
  placeholder = 'Select resource roles...',
  required = false,
  includeSidecarMirrors = false,
}) {
  const { options, loading, hasMore, searchValue, setSearch, loadMore, ensureLoaded } =
    usePaginatedConnections({ pageSize: 50, includeSidecarMirrors })

  // id -> option cache. Unresolvable ids store the id as label so they aren't retried.
  const [resolved, setResolved] = useState({})
  const inFlightRef = useRef(new Set())

  const optionByValue = useMemo(
    () => new Map(options.map((o) => [o.value, o])),
    [options],
  )

  useEffect(() => {
    const missing = value.filter(
      (id) =>
        !optionByValue.has(id) &&
        !(id in resolved) &&
        !inFlightRef.current.has(id),
    )
    if (missing.length === 0) return

    missing.forEach((id) => inFlightRef.current.add(id))

    // No cancel-on-cleanup guard: a cancel flag would let StrictMode's
    // mount→cleanup→mount discard the only fetch (the re-mount is deduped by
    // inFlightRef), leaving labels unresolved. setResolved merges idempotently.
    ;(async () => {
      const found = new Map()
      try {
        const batches = await Promise.all(
          chunk(missing, RESOLVE_CHUNK).map((ids) =>
            connectionsService.getConnectionsByIds(ids, { includeSidecarMirrors }),
          ),
        )
        batches.forEach((rows) =>
          (rows ?? []).forEach((c) => found.set(c.id, connectionOption(c))),
        )
      } catch {
        // fall back to the id below
      } finally {
        missing.forEach((id) => inFlightRef.current.delete(id))
      }
      const next = {}
      missing.forEach((id) => {
        next[id] = found.get(id) ?? { value: id, label: id }
      })
      setResolved((prev) => ({ ...prev, ...next }))
    })()
  }, [value, optionByValue, resolved, includeSidecarMirrors])

  // label: null = still resolving → the pill shows a skeleton instead of the id.
  const selectedOptions = useMemo(
    () =>
      value.map((id) => optionByValue.get(id) ?? resolved[id] ?? { value: id, label: null }),
    [value, optionByValue, resolved],
  )

  return (
    <PaginatedMultiSelect
      label={label}
      placeholder={placeholder}
      required={required}
      value={value}
      onChange={onChange}
      options={options}
      selectedOptions={selectedOptions}
      loading={loading}
      hasMore={hasMore}
      onLoadMore={loadMore}
      searchValue={searchValue}
      onSearchChange={setSearch}
      onDropdownOpen={ensureLoaded}
    />
  )
}
