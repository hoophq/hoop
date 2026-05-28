import { Card, Flex, Stack, Text } from '@mantine/core'
import { Search } from 'lucide-react'
import Select from '@/components/Select'
import TextInput from '@/components/TextInput'
import { useEventRoutingStore, filterSubscriptions } from '../store'
import SubscriptionCard from './SubscriptionCard'

export default function SubscriptionsList() {
  const {
    subscriptions,
    search,
    setSearch,
    statusFilter,
    setStatusFilter,
  } = useEventRoutingStore()

  const filtered = filterSubscriptions(subscriptions.data, search, statusFilter)

  return (
    <Stack gap="md">
      <Flex gap="sm" align="center">
        <TextInput
          placeholder="Search subscriptions or events"
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
          leftSection={<Search size={16} />}
          style={{ flex: 1 }}
        />
        <Select
          value={statusFilter}
          onChange={(v) => setStatusFilter(v || 'all')}
          data={[
            { value: 'all', label: 'All status' },
            { value: 'active', label: 'Active' },
            { value: 'paused', label: 'Paused' },
            { value: 'archived', label: 'Archived' },
          ]}
          w={160}
        />
      </Flex>

      {filtered.length === 0 ? (
        <Card padding="md" withBorder>
          <Stack gap={4}>
            <Text size="sm" fw={500}>No matches</Text>
            <Text size="xs" c="dimmed">Try a different search or status filter.</Text>
          </Stack>
        </Card>
      ) : (
        <Stack gap="sm">
          {filtered.map((sub) => (
            <SubscriptionCard key={sub.id} sub={sub} />
          ))}
        </Stack>
      )}
    </Stack>
  )
}
