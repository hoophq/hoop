import { useEffect, useRef } from 'react'
import { Group } from '@mantine/core'
import { ListVideo, Search, Shapes } from 'lucide-react'
import Button from '@/components/Button'
import FilterPopover from '@/components/FilterPopover'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import { hasFilters, MIN_SEARCH_LENGTH, TABS, useResourcesStore } from '@/pages/Resources/store'
import { RESOURCE_TYPES } from '@/pages/Resources/utils'
import TagsFilter from '@/pages/Resources/sections/TagsFilter'

const SEARCH_DEBOUNCE_MS = 500
const ALL_TYPES = 'all'

export default function FiltersBar() {
  const tab = useResourcesStore((s) => s.tab)
  const filters = useResourcesStore((s) => s.filters)
  const searchInput = useResourcesStore((s) => s.searchInput)
  const tags = useResourcesStore((s) => s.tags)
  const attributes = useResourcesStore((s) => s.attributes)
  const setSearchInput = useResourcesStore((s) => s.setSearchInput)
  const setFilters = useResourcesStore((s) => s.setFilters)
  const clearFilters = useResourcesStore((s) => s.clearFilters)
  const timer = useRef(null)

  // A pending search belongs to the tab it was typed in.
  useEffect(() => () => clearTimeout(timer.current), [tab])

  const placeholder = tab === TABS.RESOURCES ? 'Search resources' : 'Search roles'

  // Searches fire for an empty box or 3+ characters, 500 ms after the last key.
  const onSearchChange = (value) => {
    setSearchInput(value)
    clearTimeout(timer.current)
    const trimmed = value.trim()
    if (trimmed !== '' && trimmed.length < MIN_SEARCH_LENGTH) return
    timer.current = setTimeout(() => setFilters({ search: trimmed }), SEARCH_DEBOUNCE_MS)
  }

  const onClear = () => {
    clearTimeout(timer.current)
    clearFilters()
  }

  return (
    <Group gap="xs" wrap="nowrap">
      {hasFilters(filters) && (
        <Button variant="light" color="gray" size="sm" onClick={onClear}>
          Clear Filters
        </Button>
      )}
      <TextInput
        placeholder={placeholder}
        aria-label={placeholder}
        value={searchInput}
        onChange={(e) => onSearchChange(e.currentTarget.value)}
        leftSection={<Search size={16} />}
        size="sm"
        w={200}
      />
      {tab === TABS.ROLES && (
        <TagsFilter tags={tags} selected={filters.tags} onChange={(next) => setFilters({ tags: next })} size="sm" />
      )}
      {tab === TABS.ROLES && (
        <FilterPopover
          icon={ListVideo}
          label="Attribute"
          values={attributes.map((a) => a.name)}
          selected={filters.attribute}
          onSelect={(name) => setFilters({ attribute: name })}
          onClear={() => setFilters({ attribute: null })}
          size="sm"
        />
      )}
      <Select
        aria-label="Filter by resource type"
        placeholder="Resource"
        leftSection={<Shapes size={16} aria-hidden="true" />}
        rightSection={null}
        data={[{ value: ALL_TYPES, label: 'All resources' }, ...RESOURCE_TYPES]}
        value={filters.type}
        onChange={(value) => setFilters({ type: value === ALL_TYPES ? null : value })}
        allowDeselect={false}
        maxDropdownHeight={400}
        size="sm"
        w={170}
        comboboxProps={{ width: 200, position: 'bottom-end' }}
      />
    </Group>
  )
}
