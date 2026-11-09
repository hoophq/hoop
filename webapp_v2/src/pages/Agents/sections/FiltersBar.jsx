import { Group } from '@mantine/core'
import { ArrowDownUp, CircleDashed, Search, Tag } from 'lucide-react'
import Button from '@/components/Button'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import {
  DEFAULT_FILTERS,
  SORT_OPTIONS,
  STATUS_OFFLINE,
  STATUS_ONLINE,
  hasFilters,
} from '@/pages/Agents/filters'

const ALL = 'all'

const STATUS_OPTIONS = [
  { value: ALL, label: 'All statuses' },
  { value: STATUS_ONLINE, label: 'Online' },
  { value: STATUS_OFFLINE, label: 'Offline' },
]

// Search, Status, Version and Sort controls of the Agents list. The page
// owns the filter state; this bar only reads and sets it.
export default function FiltersBar({ filters, versions, onChange }) {
  const set = (patch) => onChange({ ...filters, ...patch })
  const versionOptions = [
    { value: ALL, label: 'All versions' },
    ...versions.map((v) => ({ value: v, label: v })),
  ]

  return (
    <Group gap="xs" wrap="wrap">
      {hasFilters(filters) && (
        <Button
          variant="light"
          color="gray"
          size="sm"
          onClick={() => onChange({ ...DEFAULT_FILTERS, sort: filters.sort })}
        >
          Clear Filters
        </Button>
      )}
      <TextInput
        placeholder="Search agents"
        aria-label="Search agents"
        value={filters.search}
        onChange={(e) => set({ search: e.currentTarget.value })}
        leftSection={<Search size={16} />}
        size="sm"
        w={200}
      />
      <Select
        aria-label="Filter by status"
        leftSection={<CircleDashed size={16} aria-hidden="true" />}
        data={STATUS_OPTIONS}
        value={filters.status ?? ALL}
        onChange={(value) => set({ status: value === ALL ? null : value })}
        allowDeselect={false}
        size="sm"
        w={150}
      />
      <Select
        aria-label="Filter by version"
        leftSection={<Tag size={16} aria-hidden="true" />}
        data={versionOptions}
        value={filters.version ?? ALL}
        onChange={(value) => set({ version: value === ALL ? null : value })}
        allowDeselect={false}
        searchable
        maxDropdownHeight={300}
        size="sm"
        w={160}
      />
      <Select
        aria-label="Sort agents"
        leftSection={<ArrowDownUp size={16} aria-hidden="true" />}
        data={SORT_OPTIONS}
        value={filters.sort}
        onChange={(value) => set({ sort: value })}
        allowDeselect={false}
        size="sm"
        w={170}
      />
    </Group>
  )
}
