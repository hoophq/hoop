import { Box, Card, Flex, Group, Stack, Text, Title } from '@mantine/core'
import { ChevronRight, Filter, Search } from 'lucide-react'
import Button from '@/components/Button'
import Code from '@/components/Code'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import { useEventRoutingStore, filterCatalog } from '../store'
import CategoryBadge from './CategoryBadge'

export default function EventCatalogTab() {
  const {
    catalog,
    catalogSearch,
    setCatalogSearch,
    catalogFilter,
    setCatalogFilter,
  } = useEventRoutingStore()
  const setEventDetail = useEventRoutingStore((s) => s._setEventDetailTarget)

  const filtered = filterCatalog(catalog.data, catalogSearch, catalogFilter)

  return (
    <Stack gap="md">
      <Flex gap="sm" align="center">
        <TextInput
          placeholder="Search event types"
          value={catalogSearch}
          onChange={(e) => setCatalogSearch(e.currentTarget.value)}
          leftSection={<Search size={16} />}
          style={{ flex: 1 }}
        />
        <Select
          value={catalogFilter}
          onChange={(v) => setCatalogFilter(v || 'all')}
          data={[
            { value: 'all', label: 'All categories' },
            { value: 'ai', label: 'AI' },
            { value: 'alert', label: 'Alert' },
            { value: 'access', label: 'Access' },
            { value: 'session', label: 'Session' },
            { value: 'connection', label: 'Connection' },
          ]}
          w={160}
        />
      </Flex>

      {filtered.length === 0 ? (
        <Stack align="center" gap="sm" py="xxl">
          <Filter size={32} strokeWidth={1.5} color="var(--mantine-color-gray-6)" />
          <Title order={4}>No events match</Title>
          <Text size="sm" c="dimmed">Try a different search term or change the category filter.</Text>
        </Stack>
      ) : (
        <Card padding={0} withBorder>
          <Stack gap={0}>
            <Group px="sm" py="xs" wrap="nowrap" style={{ borderBottom: '1px solid var(--mantine-color-default-border)' }}>
              <Text size="xs" fw={600} c="dimmed" style={{ flex: 1 }}>Event type</Text>
              <Text size="xs" fw={600} c="dimmed" w={120}>Category</Text>
              <Text size="xs" fw={600} c="dimmed" w={140} ta="right">Actions</Text>
            </Group>
            {filtered.map((e) => (
              <Group
                key={e.name}
                px="sm"
                py="sm"
                wrap="nowrap"
                style={{ borderBottom: '1px solid var(--mantine-color-default-border)' }}
              >
                <Stack gap={2} style={{ flex: 1, minWidth: 0 }}>
                  <Code bg="indigo.1" c="indigo.9">{e.name}</Code>
                  <Text size="xs" c="dimmed">{e.description}</Text>
                </Stack>
                <Box w={120}>
                  <CategoryBadge category={e.category} />
                </Box>
                <Group w={140} justify="flex-end">
                  <Button
                    variant="subtle"
                    color="gray"
                    size="xs"
                    rightSection={<ChevronRight size={12} />}
                    onClick={() => setEventDetail(e)}
                  >
                    View schema
                  </Button>
                </Group>
              </Group>
            ))}
          </Stack>
        </Card>
      )}
    </Stack>
  )
}
