import { useEffect, useMemo, useState } from 'react'
import {
  Box,
  Button,
  Card,
  Checkbox,
  Flex,
  Group,
  Popover,
  Stack,
  Text,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  Check,
  ListVideo,
  Rotate3d,
  Search,
  Shapes,
  Tag,
  X,
} from 'lucide-react'
import PageLoader from '@/components/PageLoader'
import Table from '@/components/Table'
import TextInput from '@/components/TextInput'
import { useRulepackStore } from '../store'

function distinctNonBlank(items, extract) {
  const seen = new Set()
  for (const item of items) {
    const value = extract(item)
    const values = Array.isArray(value) ? value : value == null ? [] : [value]
    for (const v of values) {
      if (v == null) continue
      const str = String(v).trim()
      if (str) seen.add(str)
    }
  }
  return Array.from(seen).sort()
}

function connectionTagValues(connection) {
  const tags = connection?.connection_tags
  if (!tags) return []
  return Object.values(tags).filter((v) => typeof v === 'string' && v.trim() !== '')
}

function connectionMatchesFilters(connection, filters) {
  const { resource, type, attribute, tag } = filters
  if (resource && connection.resource_name !== resource) return false
  const connType = connection.subtype || connection.type
  if (type && connType !== type) return false
  if (attribute) {
    const attrs = connection.attributes ?? []
    if (!attrs.includes(attribute)) return false
  }
  if (tag) {
    if (!connectionTagValues(connection).includes(tag)) return false
  }
  return true
}

function ValueFilter({ icon, label, values, selected, onSelect, onClear }) {
  const [open, setOpen] = useState(false)
  const [searchTerm, setSearchTerm] = useState('')
  const Icon = icon

  const hasSelected = typeof selected === 'string' && selected.trim() !== ''
  const filtered = useMemo(() => {
    const q = searchTerm.trim().toLowerCase()
    if (!q) return values
    return values.filter((v) => v.toLowerCase().includes(q))
  }, [values, searchTerm])

  const close = () => {
    setOpen(false)
    setSearchTerm('')
  }

  return (
    <Popover
      opened={open}
      onChange={setOpen}
      position="bottom-start"
      width={320}
      withinPortal
    >
      <Popover.Target>
        <Button
          variant={hasSelected ? 'light' : 'default'}
          color="gray"
          onClick={() => setOpen((value) => !value)}
          leftSection={<Icon size={16} />}
          rightSection={
            hasSelected ? (
              <X
                size={14}
                onClick={(event) => {
                  event.stopPropagation()
                  onClear()
                  close()
                }}
              />
            ) : null
          }
        >
          {hasSelected ? selected : label}
        </Button>
      </Popover.Target>
      <Popover.Dropdown p="xs">
        <Stack gap="xs">
          {hasSelected && (
            <Box
              px="sm"
              py="xs"
              onClick={() => {
                onClear()
                close()
              }}
              style={{ cursor: 'pointer', borderRadius: 4 }}
            >
              <Text size="sm" c="dimmed">
                Clear filter
              </Text>
            </Box>
          )}
          <TextInput
            placeholder={`Search ${label.toLowerCase()}`}
            value={searchTerm}
            onChange={(event) => setSearchTerm(event.currentTarget.value)}
            leftSection={<Search size={14} />}
            size="xs"
          />
          {filtered.length > 0 ? (
            <Stack gap={0} mah={288} style={{ overflowY: 'auto' }}>
              {filtered.map((value) => (
                <Flex
                  key={value}
                  align="center"
                  justify="space-between"
                  px="sm"
                  py="xs"
                  onClick={() => {
                    onSelect(value)
                    close()
                  }}
                  style={{ cursor: 'pointer', borderRadius: 4 }}
                >
                  <Text size="sm" lineClamp={1}>
                    {value}
                  </Text>
                  {value === selected && <Check size={14} />}
                </Flex>
              ))}
            </Stack>
          ) : (
            <Box px="sm" py="md">
              <Text size="xs" c="dimmed" fs="italic">
                {searchTerm
                  ? `No ${label.toLowerCase()} found`
                  : `No ${label.toLowerCase()} available`}
              </Text>
            </Box>
          )}
        </Stack>
      </Popover.Dropdown>
    </Popover>
  )
}

const INITIAL_FILTERS = { resource: null, type: null, attribute: null }

export default function RolesTab() {
  const {
    connections,
    connectionsStatus,
    selectedConnections,
    applying,
    fetchConnections,
    toggleConnection,
    resetSelectedConnections,
    applyConnections,
    hasPendingChanges,
  } = useRulepackStore()

  const [search, setSearch] = useState('')
  const [filters, setFilters] = useState(INITIAL_FILTERS)

  useEffect(() => {
    fetchConnections()
  }, [fetchConnections])

  const loading = connectionsStatus === 'loading'

  const resourceOptions = useMemo(
    () => distinctNonBlank(connections, (c) => c.resource_name),
    [connections],
  )
  const typeOptions = useMemo(
    () => distinctNonBlank(connections, (c) => c.subtype || c.type),
    [connections],
  )
  const attributeOptions = useMemo(
    () => distinctNonBlank(connections, (c) => c.attributes ?? []),
    [connections],
  )

  const anyFilter =
    search.trim().length > 0 || Object.values(filters).some((v) => v != null)

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase()
    return connections
      .filter((c) => connectionMatchesFilters(c, filters))
      .filter((c) => !q || (c.name ?? '').toLowerCase().includes(q))
  }, [connections, filters, search])

  const pending = hasPendingChanges()

  const handleApply = async () => {
    const result = await applyConnections()
    if (result.ok) {
      notifications.show({
        color: 'green',
        message: 'Rulepack applied successfully',
      })
      return
    }
    const missing = result.missing
    notifications.show({
      color: 'red',
      message:
        missing && missing.length > 0
          ? `Unknown connections: ${missing.join(', ')}`
          : 'Failed to apply rulepack',
    })
  }

  const setFilter = (key) => (value) =>
    setFilters((current) => ({ ...current, [key]: value }))

  return (
    <Stack gap="md">
      <Group gap="xs" align="flex-start" wrap="wrap">
        <Box style={{ flex: 1, minWidth: 240 }}>
          <TextInput
            placeholder="Search roles"
            value={search}
            onChange={(event) => setSearch(event.currentTarget.value)}
            leftSection={<Search size={16} />}
          />
        </Box>
        <ValueFilter
          icon={Rotate3d}
          label="Resource"
          values={resourceOptions}
          selected={filters.resource}
          onSelect={setFilter('resource')}
          onClear={() => setFilter('resource')(null)}
        />
        <ValueFilter
          icon={Shapes}
          label="Type"
          values={typeOptions}
          selected={filters.type}
          onSelect={setFilter('type')}
          onClear={() => setFilter('type')(null)}
        />
        <ValueFilter
          icon={ListVideo}
          label="Attribute"
          values={attributeOptions}
          selected={filters.attribute}
          onSelect={setFilter('attribute')}
          onClear={() => setFilter('attribute')(null)}
        />
      </Group>

      <Card padding={0} withBorder>
        {loading ? (
          <PageLoader h={240} />
        ) : visible.length === 0 ? (
          <Box p="xl" ta="center">
            <Text size="sm" c="dimmed">
              {anyFilter
                ? 'No connections match your filters.'
                : 'No connections available.'}
            </Text>
          </Box>
        ) : (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Role name</Table.Th>
                <Table.Th>Type</Table.Th>
                <Table.Th>Resource</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {visible.map((connection) => (
                <Table.Tr key={connection.id ?? connection.name}>
                  <Table.Td>
                    <Group gap="xs" align="center">
                      <Checkbox
                        checked={selectedConnections.has(connection.name)}
                        onChange={() => toggleConnection(connection.name)}
                      />
                      <Text size="sm">{connection.name}</Text>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">
                      {connection.subtype || connection.type || ''}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{connection.resource_name || ''}</Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
      </Card>

      {pending && (
        <Group justify="flex-end" gap="xs">
          <Button
            variant="default"
            disabled={applying}
            onClick={resetSelectedConnections}
          >
            Discard
          </Button>
          <Button disabled={applying} loading={applying} onClick={handleApply}>
            {applying ? 'Applying...' : 'Apply changes'}
          </Button>
        </Group>
      )}
    </Stack>
  )
}
