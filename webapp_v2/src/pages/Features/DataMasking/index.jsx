import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Card, Group, Stack, Text, Title } from '@mantine/core'
import { Shapes, Tag } from 'lucide-react'
import { useUserStore } from '@/stores/useUserStore'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import FullBleed from '@/layout/FullBleed'
import PageLoader from '@/components/PageLoader'
import Button from '@/components/Button'
import ValueFilter from '@/components/ValueFilter'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import { useDataMaskingStore } from './store'
import RuleListItem from './components/RuleListItem'
import DataMaskingPromotion from './components/DataMaskingPromotion'

const FREE_LICENSE_LIMIT_MESSAGE =
  'Your organization has reached Live Data Masking free usage limits. Upgrade to Enterprise to keep your sensitive data protected.'

function uniqueSorted(values) {
  return [...new Set(values)].sort((a, b) => a.localeCompare(b))
}

export default function DataMasking() {
  const navigate = useNavigate()

  const list = useDataMaskingStore((s) => s.list)
  const listStatus = useDataMaskingStore((s) => s.listStatus)
  const connections = useDataMaskingStore((s) => s.connections)
  const attributes = useDataMaskingStore((s) => s.attributes)
  const fetchList = useDataMaskingStore((s) => s.fetchList)
  const fetchConnections = useDataMaskingStore((s) => s.fetchConnections)
  const fetchAttributes = useDataMaskingStore((s) => s.fetchAttributes)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const redactProvider = useUserStore((s) => s.redactProvider)

  const [selectedRole, setSelectedRole] = useState(null)
  const [selectedAttribute, setSelectedAttribute] = useState(null)

  // Paginated source for the filter dropdown. The full `connections` load above
  // is still used for rule matching + name resolution.
  const roleFilter = usePaginatedConnections({ pageSize: 50 })

  useEffect(() => {
    fetchList()
    fetchConnections()
    fetchAttributes()
  }, [fetchList, fetchConnections, fetchAttributes])

  const connectionsById = useMemo(() => {
    const map = new Map()
    for (const c of connections) map.set(c.id, c)
    return map
  }, [connections])

  const resolveConnections = useMemo(
    () => (rule) =>
      (rule.connection_ids ?? [])
        .map((id) => connectionsById.get(id))
        .filter(Boolean),
    [connectionsById],
  )

  const attributeFilterValues = useMemo(
    () => uniqueSorted(attributes.map((a) => a.name)),
    [attributes],
  )

  const filteredRules = useMemo(() => {
    let rules = list
    if (selectedRole) {
      rules = rules.filter((rule) =>
        resolveConnections(rule).some((c) => c.name === selectedRole),
      )
    }
    if (selectedAttribute) {
      rules = rules.filter((rule) =>
        (rule.attributes ?? []).includes(selectedAttribute),
      )
    }
    return rules
  }, [list, selectedRole, selectedAttribute, resolveConnections])

  const atFreeLimit = isFreeLicense && list.length >= 1
  const loading = listStatus === 'loading'
  const showLoader = useMinDelay(loading && list.length === 0, 500)
  const hasFilters = Boolean(selectedRole || selectedAttribute)

  const goCreate = () => navigate('/features/data-masking/new')

  if (showLoader) {
    return <PageLoader h={300} />
  }

  if (list.length === 0) {
    return (
      <FullBleed>
        <DataMaskingPromotion
          redactProvider={redactProvider}
          onConfigure={goCreate}
        />
      </FullBleed>
    )
  }

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="sm">
          <Title order={1}>Live Data Masking</Title>
          <Text size="md" c="dimmed">
            Automatically mask sensitive data in real-time at the protocol layer
          </Text>
        </Stack>
        <Button onClick={goCreate} disabled={atFreeLimit}>
          Create new
        </Button>
      </Group>

      {atFreeLimit && (
        <FreeLicenseCallout message={FREE_LICENSE_LIMIT_MESSAGE} variant="limit" />
      )}

      <Group gap="sm">
        <AsyncValueFilter
          icon={Shapes}
          label="Resource Role"
          placeholder="Search resource roles"
          selected={selectedRole}
          onSelect={setSelectedRole}
          onClear={() => setSelectedRole(null)}
          options={roleFilter.options}
          loading={roleFilter.loading}
          hasMore={roleFilter.hasMore}
          onLoadMore={roleFilter.loadMore}
          searchValue={roleFilter.searchValue}
          onSearchChange={roleFilter.setSearch}
          onOpen={roleFilter.ensureLoaded}
        />
        <ValueFilter
          icon={Tag}
          label="Attribute"
          values={attributeFilterValues}
          selected={selectedAttribute}
          onSelect={setSelectedAttribute}
          onClear={() => setSelectedAttribute(null)}
        />
      </Group>

      {filteredRules.length === 0 ? (
        <Card padding="xl" withBorder>
          <Stack gap={4} align="center" ta="center">
            <Text fw={600}>No Live Data Masking rules match your filters</Text>
            <Text size="sm" c="dimmed">
              Try clearing the {hasFilters ? 'filters' : 'filter'} above.
            </Text>
          </Stack>
        </Card>
      ) : (
        <Box>
          {filteredRules.map((rule, idx) => (
            <RuleListItem
              key={rule.id}
              rule={rule}
              connections={resolveConnections(rule)}
              isFirst={idx === 0}
              isLast={idx === filteredRules.length - 1}
              onConfigure={(id) =>
                navigate(`/features/data-masking/edit/${id}`)
              }
              onConfigureConnection={(name) =>
                navigate(`/resources/configure/${encodeURIComponent(name)}`)
              }
            />
          ))}
        </Box>
      )}
    </Stack>
  )
}
