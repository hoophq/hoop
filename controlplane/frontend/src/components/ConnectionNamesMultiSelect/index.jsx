import { useMemo } from 'react'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import PaginatedMultiSelect from '@/components/PaginatedMultiSelect'

/**
 * Resource-role (connection) multi-select keyed by **name**, for APIs whose
 * payload carries `connection_names`. The id-keyed twin is
 * `@/components/ConnectionsMultiSelect`.
 *
 * No label resolution is needed here — the value is the label — so selected
 * chips render correctly even for connections outside the loaded pages.
 *
 * Usage:
 *   <ConnectionNamesMultiSelect value={form.connectionNames} onChange={setNames} />
 */
export default function ConnectionNamesMultiSelect({
  value = [],
  onChange,
  label = 'Resource Roles',
  placeholder = 'Select resource roles...',
  required = false,
  disabled = false,
}) {
  const { items, loading, hasMore, searchValue, setSearch, loadMore, ensureLoaded } =
    usePaginatedConnections({ pageSize: 50 })

  const options = useMemo(
    () => items.map((c) => ({ value: c.name, label: c.name })),
    [items],
  )

  const selectedOptions = useMemo(
    () => value.map((name) => ({ value: name, label: name })),
    [value],
  )

  return (
    <PaginatedMultiSelect
      label={label}
      placeholder={placeholder}
      required={required}
      disabled={disabled}
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
