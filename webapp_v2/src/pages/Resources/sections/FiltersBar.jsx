import { useEffect, useRef } from 'react'
import { Group } from '@mantine/core'
import { ListVideo, Search, Shapes } from 'lucide-react'
import Button from '@/components/Button'
import FilterPopover from '@/components/FilterPopover'
import TextInput from '@/components/TextInput'
import { hasFilters, MIN_SEARCH_LENGTH, TABS, useResourcesStore } from '@/pages/Resources/store'
import { RESOURCE_TYPES } from '@/pages/Resources/utils'
import TagsFilter from '@/pages/Resources/sections/TagsFilter'

const SEARCH_DEBOUNCE_MS = 500

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

  const typeLabel = RESOURCE_TYPES.find((t) => t.value === filters.type)?.label ?? null

  return (
    <Group gap="xs" wrap="nowrap">
      {hasFilters(filters) && (
        <Button variant="light" color="gray" onClick={onClear}>
          Clear Filters
        </Button>
      )}
      <TextInput
        placeholder={placeholder}
        aria-label={placeholder}
        value={searchInput}
        onChange={(e) => onSearchChange(e.currentTarget.value)}
        leftSection={<Search size={16} />}
        w={200}
      />
      {tab === TABS.ROLES && (
        <TagsFilter tags={tags} selected={filters.tags} onChange={(next) => setFilters({ tags: next })} />
      )}
      {tab === TABS.ROLES && (
        <FilterPopover
          icon={ListVideo}
          label="Attribute"
          values={attributes.map((a) => a.name)}
          selected={filters.attribute}
          onSelect={(name) => setFilters({ attribute: name })}
          onClear={() => setFilters({ attribute: null })}
        />
      )}
      <FilterPopover
        icon={Shapes}
        label="Resource"
        values={RESOURCE_TYPES.map((t) => t.label)}
        selected={typeLabel}
        onSelect={(label) => setFilters({ type: RESOURCE_TYPES.find((t) => t.label === label)?.value ?? null })}
        onClear={() => setFilters({ type: null })}
      />
    </Group>
  )
}
