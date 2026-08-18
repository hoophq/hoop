import { useMemo } from 'react'
import PaginatedMultiSelect from '@/components/PaginatedMultiSelect'
import { isEligibleForAccessType } from '../helpers'

/**
 * Resource-role picker for an access request rule.
 *
 * Keyed by connection *name*, not id: `connection_names` is what the rule
 * stores, and there is no bulk name -> id lookup to reconcile the two, so
 * ConnectionsMultiSelect (which is id-keyed) can't back this field. Names are
 * unique per organization and are their own label, so no resolution step is
 * needed for selections that fall outside the loaded pages.
 *
 * Options are narrowed to the roles the chosen access type can actually
 * govern. Only the pages loaded so far can be judged — same limitation as the
 * CLJS form, which filters the paginated list client-side.
 */
export default function ResourceRolesSelect({
  connections,
  accessType,
  value,
  onChange,
  required = false,
  disabled = false,
}) {
  const { items, loading, hasMore, searchValue, setSearch, loadMore, ensureLoaded } =
    connections

  const options = useMemo(
    () =>
      items
        .filter((item) => isEligibleForAccessType(accessType, item))
        .map((item) => ({ value: item.name, label: item.name })),
    [items, accessType],
  )

  // Already-selected roles keep their chip even when the current page or the
  // eligibility filter excludes them.
  const selectedOptions = useMemo(
    () => value.map((name) => ({ value: name, label: name })),
    [value],
  )

  return (
    <PaginatedMultiSelect
      label="Resource Roles"
      placeholder="Select resource roles..."
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
